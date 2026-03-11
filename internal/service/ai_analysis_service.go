package service

import (
	"context"
	"fmt"
	"os"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"

	"github.com/cloudwego/eino/schema"
	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
)

// ═══════════════════════════════════════════════════════════════
// AI 分析服务（eino + 硅基流动 SiliconFlow）
// ═══════════════════════════════════════════════════════════════

const (
	siliconflowBaseURL = "https://api.siliconflow.cn/v1"
	defaultSFModel     = "Qwen/Qwen2.5-72B-Instruct"

	aiCacheTTL = 30 * time.Minute

	// aiRequestTimeout 是发给硅基流动的单次 HTTP 超时。
	// 必须小于 http.Server.WriteTimeout（3min），留出足够缓冲。
	aiRequestTimeout = 150 * time.Second // 2.5 分钟

	aiMaxTokens = 1024
)

const aiAnalysisPrompt = `你是一位专业的 A 股量化分析师。请根据以下股票的实时行情数据，提供一份简洁的分析报告。

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

请从以下维度分析（使用 Markdown 格式，每个维度用 ## 标题，内容简洁精准）：

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

type AnalysisResult struct {
	Code      string    `json:"code"`
	Name      string    `json:"name"`
	Report    string    `json:"report"`
	Model     string    `json:"model"`
	FromCache bool      `json:"from_cache"`
	CreatedAt time.Time `json:"created_at"`
}

// ── 服务 ──────────────────────────────────────────────────────────

type AIAnalysisService struct {
	cache  *gocache.Cache
	apiKey string
	model  string
	log    *zap.Logger
}

func NewAIAnalysisService(log *zap.Logger) *AIAnalysisService {
	model := os.Getenv("SILICONFLOW_MODEL")
	if model == "" {
		model = defaultSFModel
	}
	return &AIAnalysisService{
		cache:  gocache.New(aiCacheTTL, 5*time.Minute),
		apiKey: os.Getenv("SILICONFLOW_API_KEY"),
		model:  model,
		log:    log,
	}
}

func (s *AIAnalysisService) Analyze(ctx context.Context, quote *Quote) (*AnalysisResult, error) {
	cacheKey := "ai:" + quote.Code

	// 1. 查内存缓存
	if cached, found := s.cache.Get(cacheKey); found {
		result := cached.(*AnalysisResult)
		cp := *result
		cp.FromCache = true
		s.log.Sugar().Debugw("AI cache hit", "code", quote.Code)
		return &cp, nil
	}

	// 2. 无 API Key → 降级为规则模板报告
	if s.apiKey == "" {
		s.log.Warn("SILICONFLOW_API_KEY not set, using mock report", zap.String("code", quote.Code))
		mock := s.buildMockReport(quote)
		s.cache.Set(cacheKey, mock, aiCacheTTL)
		return mock, nil
	}

	// 3. 构建 Prompt
	prompt := fmt.Sprintf(aiAnalysisPrompt,
		quote.Code, quote.Name,
		quote.Price, quote.ChangeRate,
		quote.High, quote.Low,
		quote.Volume, quote.Amount,
		quote.Turnover, quote.VolumeRatio,
	)

	// 4. 调用 eino + 硅基流动
	report, err := s.callEino(ctx, prompt)
	if err != nil {
		s.log.Error("eino AI call failed, fallback to mock",
			zap.String("code", quote.Code),
			zap.Error(err),
		)
		mock := s.buildMockReport(quote)
		return mock, nil
	}

	result := &AnalysisResult{
		Code:      quote.Code,
		Name:      quote.Name,
		Report:    report,
		Model:     s.model,
		FromCache: false,
		CreatedAt: time.Now(),
	}
	s.cache.Set(cacheKey, result, aiCacheTTL)
	s.log.Sugar().Infow("AI analysis done",
		"code", quote.Code,
		"model", s.model,
		"chars", len(report),
	)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// eino 调用
// ─────────────────────────────────────────────────────────────────

func (s *AIAnalysisService) callEino(ctx context.Context, prompt string) (string, error) {
	maxTok := aiMaxTokens
	timeout := aiRequestTimeout

	chatModel, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL:   siliconflowBaseURL,
		APIKey:    s.apiKey,
		Model:     s.model,
		MaxTokens: &maxTok,
		Timeout:   timeout,
	})
	if err != nil {
		return "", fmt.Errorf("eino: init ChatModel: %w", err)
	}

	messages := []*schema.Message{
		schema.SystemMessage("你是一位专业的 A 股分析师，擅长量化分析和基本面研究，用中文回答。"),
		schema.UserMessage(prompt),
	}

	resp, err := chatModel.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("eino: Generate: %w", err)
	}
	if resp == nil || resp.Content == "" {
		return "", fmt.Errorf("eino: empty response from model")
	}

	return resp.Content, nil
}

// ─────────────────────────────────────────────────────────────────
// Mock 报告
// ─────────────────────────────────────────────────────────────────

func (s *AIAnalysisService) buildMockReport(q *Quote) *AnalysisResult {
	trend := "震荡"
	switch {
	case q.ChangeRate > 2:
		trend = "强势上涨"
	case q.ChangeRate > 0:
		trend = "温和上涨"
	case q.ChangeRate < -2:
		trend = "明显下跌"
	case q.ChangeRate < 0:
		trend = "小幅调整"
	}

	amplitude := 0.0
	if q.Price > 0 {
		amplitude = (q.High - q.Low) / q.Price * 100
	}

	volSignal := "成交清淡"
	if q.VolumeRatio > 1.5 {
		volSignal = "成交活跃"
	}

	turnoverNote := "换手偏低，市场观望情绪较重"
	if q.Turnover > 3 {
		turnoverNote = "换手较高，资金关注度较强"
	}

	report := fmt.Sprintf(
		"## 📊 盘面解读\n\n**%s（%s）** 今日%s，当前报价 **%.2f 元**，涨跌幅 **%+.2f%%**。\n\n"+
			"今日价格区间 %.2f ~ %.2f 元，振幅 **%.2f%%**，换手率 %.2f%%，量比 %.2f。\n\n"+
			"## 📈 短期趋势\n\n> ⚠️ **提示**：AI 分析功能需配置 `SILICONFLOW_API_KEY` 环境变量后启用，当前为演示模式。\n\n"+
			"- 量比 %.2f，%s\n- %s\n\n"+
			"## ⚠️ 风险提示\n\n1. 当前为**演示模式**，AI 深度分析功能尚未启用\n"+
			"2. 行情数据存在缓存延迟\n3. **所有分析仅供参考，不构成投资建议**\n\n"+
			"## 💡 启用 AI 分析\n\n在 `.env` 文件中配置硅基流动 API Key 后重启后端：\n\n"+
			"```env\nSILICONFLOW_API_KEY=sk-xxxxxxxxxxxxxxxx\nSILICONFLOW_MODEL=Qwen/Qwen2.5-72B-Instruct\n```\n\n"+
			"---\n*演示数据 · %s*",
		q.Name, q.Code, trend, q.Price, q.ChangeRate,
		q.Low, q.High, amplitude, q.Turnover, q.VolumeRatio,
		q.VolumeRatio, volSignal, turnoverNote,
		time.Now().Format("2006-01-02 15:04:05"),
	)

	return &AnalysisResult{
		Code:      q.Code,
		Name:      q.Name,
		Report:    report,
		Model:     "mock(no-api-key)",
		FromCache: false,
		CreatedAt: time.Now(),
	}
}
