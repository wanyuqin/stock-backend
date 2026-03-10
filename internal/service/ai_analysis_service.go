package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// AI 分析服务（调用 Anthropic Claude API）
// ═══════════════════════════════════════════════════════════════

const (
	anthropicURL   = "https://api.anthropic.com/v1/messages"
	anthropicModel = "claude-haiku-4-5-20251001" // 速度快、成本低，适合行情分析
	aiCacheTTL     = 30 * time.Minute
)

const aiAnalysisPrompt = `你是一位专业的 A 股分析师。请根据以下股票的实时行情数据，提供一份简洁的分析报告。

股票信息：
- 代码：%s
- 名称：%s
- 当前价格：%.2f 元
- 涨跌幅：%+.2f%%
- 今日最高：%.2f / 最低：%.2f
- 成交量：%d 手
- 成交额：%.0f 元
- 换手率：%.2f%%
- 量比：%.2f

请从以下几个维度分析（使用 Markdown 格式，每个维度用 ## 标题，内容简洁）：

## 📊 盘面解读
简要分析当前价格位置、量价关系。

## 📈 短期趋势
基于今日数据判断短期走势信号（注意：仅供参考）。

## ⚠️ 风险提示
列出 2-3 个当前需要关注的风险点。

## 💡 关注要点
投资者近期应重点关注的 1-2 个事项。

---
*以上分析基于实时行情数据，仅供参考，不构成投资建议。投资有风险，入市需谨慎。*`

// ── 数据结构 ──────────────────────────────────────────────────────

// AnalysisResult AI 分析结果
type AnalysisResult struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Report    string    `json:"report"`     // Markdown 格式的分析报告
	Model     string    `json:"model"`      // 使用的 AI 模型
	FromCache bool      `json:"from_cache"` // 是否来自缓存
	CreatedAt time.Time `json:"created_at"`
}

// ── 服务 ──────────────────────────────────────────────────────────

// AIAnalysisService 封装 AI 分析逻辑，含 30 分钟内存缓存。
type AIAnalysisService struct {
	httpClient *http.Client
	cache      *gocache.Cache
	apiKey     string
	log        *zap.Logger
}

// NewAIAnalysisService 创建实例，API Key 从环境变量 ANTHROPIC_API_KEY 读取。
func NewAIAnalysisService(log *zap.Logger) *AIAnalysisService {
	return &AIAnalysisService{
		httpClient: &http.Client{Timeout: 60 * time.Second},
		cache:      gocache.New(aiCacheTTL, 5*time.Minute),
		apiKey:     os.Getenv("ANTHROPIC_API_KEY"),
		log:        log,
	}
}

// Analyze 对指定股票进行 AI 分析，30 分钟内重复请求走缓存。
func (s *AIAnalysisService) Analyze(ctx context.Context, quote *Quote) (*AnalysisResult, error) {
	cacheKey := "ai:" + quote.Code

	// ── 1. 查缓存 ─────────────────────────────────────────────────
	if cached, found := s.cache.Get(cacheKey); found {
		result := cached.(*AnalysisResult)
		cp := *result // 返回副本，避免修改缓存里的指针
		cp.FromCache = true
		s.log.Sugar().Debugw("AI cache hit", "code", quote.Code)
		return &cp, nil
	}

	// ── 2. 无 API Key → 降级为规则报告 ───────────────────────────
	if s.apiKey == "" {
		mock := s.buildMockReport(quote)
		s.cache.Set(cacheKey, mock, aiCacheTTL)
		return mock, nil
	}

	// ── 3. 构建 Prompt ────────────────────────────────────────────
	prompt := fmt.Sprintf(aiAnalysisPrompt,
		quote.Code, quote.Name,
		quote.Price, quote.ChangeRate,
		quote.High, quote.Low,
		quote.Volume, quote.Amount,
		quote.Turnover, quote.VolumeRatio,
	)

	// ── 4. 调用 Claude API ────────────────────────────────────────
	report, err := s.callClaude(ctx, prompt)
	if err != nil {
		s.log.Error("AI analysis failed, falling back to mock",
			zap.String("code", quote.Code), zap.Error(err))
		mock := s.buildMockReport(quote)
		return mock, nil
	}

	result := &AnalysisResult{
		Code:      quote.Code,
		Name:      quote.Name,
		Report:    report,
		Model:     anthropicModel,
		FromCache: false,
		CreatedAt: time.Now(),
	}
	s.cache.Set(cacheKey, result, aiCacheTTL)
	s.log.Sugar().Infow("AI analysis done", "code", quote.Code, "chars", len(report))
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// Anthropic Claude API 调用
// ─────────────────────────────────────────────────────────────────

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (s *AIAnalysisService) callClaude(ctx context.Context, prompt string) (string, error) {
	reqBody := claudeRequest{
		Model:     anthropicModel,
		MaxTokens: 1024,
		Messages:  []claudeMessage{{Role: "user", Content: prompt}},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("http post: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("anthropic API status %d: %s",
			resp.StatusCode, truncateBytes(respBody, 200))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return "", fmt.Errorf("unmarshal response: %w", err)
	}
	if claudeResp.Error != nil {
		return "", fmt.Errorf("claude error: %s", claudeResp.Error.Message)
	}
	if len(claudeResp.Content) == 0 {
		return "", fmt.Errorf("empty claude response")
	}
	return claudeResp.Content[0].Text, nil
}

// ─────────────────────────────────────────────────────────────────
// Mock 报告（未配置 API Key 时的降级方案）
// ─────────────────────────────────────────────────────────────────

func (s *AIAnalysisService) buildMockReport(q *Quote) *AnalysisResult {
	trend := "震荡"
	if q.ChangeRate > 2 {
		trend = "强势上涨"
	} else if q.ChangeRate > 0 {
		trend = "温和上涨"
	} else if q.ChangeRate < -2 {
		trend = "明显下跌"
	} else if q.ChangeRate < 0 {
		trend = "小幅调整"
	}

	amplitude := 0.0
	if q.Close > 0 {
		amplitude = (q.High - q.Low) / q.Close * 100
	}

	volSignal := "成交清淡"
	if q.VolumeRatio > 1.5 {
		volSignal = "成交活跃"
	}

	turnoverNote := "换手偏低，市场观望情绪较重"
	if q.Turnover > 3 {
		turnoverNote = "换手较高，资金关注度较强"
	}

	report := fmt.Sprintf(`## 📊 盘面解读

**%s（%s）** 今日%s，当前报价 **%.2f 元**，涨跌幅 **%+.2f%%**。

今日价格区间 %.2f ~ %.2f 元，振幅 **%.2f%%**，换手率 %.2f%%，量比 %.2f。

## 📈 短期趋势

> ⚠️ **提示**：AI 分析功能需配置 `+"`"+`ANTHROPIC_API_KEY`+"`"+` 环境变量后启用，当前为演示模式。

基于规则的简单判断：

- 量比 %.2f，%s
- %s

## ⚠️ 风险提示

1. 当前为**演示模式**，AI 深度分析功能尚未启用
2. 行情数据存在 5 秒缓存延迟
3. **所有分析仅供参考，不构成投资建议**

## 💡 启用 AI 分析

在 `+"`"+`.env`+"`"+` 文件中添加 API Key 后重启后端：

`+"```"+`env
ANTHROPIC_API_KEY=sk-ant-xxxxxxxxxxxx
`+"```"+`

---
*演示数据 · %s*`,
		q.Name, q.Code, trend,
		q.Price, q.ChangeRate,
		q.Low, q.High, amplitude,
		q.Turnover, q.VolumeRatio,
		q.VolumeRatio, volSignal,
		turnoverNote,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	return &AnalysisResult{
		Code:      q.Code,
		Name:      q.Name,
		Report:    report,
		Model:     "mock",
		FromCache: false,
		CreatedAt: time.Now(),
	}
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

// truncateBytes 截断字节切片，转换为字符串，用于错误日志。
func truncateBytes(b []byte, maxLen int) string {
	if len(b) <= maxLen {
		return string(b)
	}
	return string(b[:maxLen]) + "…"
}
