package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// NewsSentimentAnalyzer
//
// 1. 抓取财联社电报（解析 HTML 中的 __NEXT_DATA__ JSON）
// 2. 用 LLM 对每条新闻打情绪分（+1 利好 / 0 中性 / -1 利空）
// 3. 与持仓/买入计划做关联匹配，生成操作提示
// ═══════════════════════════════════════════════════════════════

// NewsItem 财联社一条电报
type NewsItem struct {
	ID        string      `json:"id"`
	Content   string      `json:"content"`
	Level     string      `json:"level"`   // A/B/C（A=重大）
	Timestamp int64       `json:"timestamp"`
	Time      time.Time   `json:"time"`
	Tags      []string    `json:"tags"`    // 行业/主题标签
	Stocks    []NewsStock `json:"stocks"`  // 关联股票（财联社标注）
}

type NewsStock struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// NewsSentiment 一条新闻的情绪分析结果
type NewsSentiment struct {
	NewsItem
	Score         int      `json:"score"`          // +1 利好 / 0 中性 / -1 利空
	ScoreLabel    string   `json:"score_label"`    // "利好" / "中性" / "利空"
	Scope         string   `json:"scope"`          // "大盘" / "板块:光伏" / "个股:中国稀土"
	ScopeType     string   `json:"scope_type"`     // "market" / "sector" / "stock"
	AffectedCodes []string `json:"affected_codes"` // 关联到持仓/买入计划的股票代码
	ActionHints   []string `json:"action_hints"`   // 操作提示
	Reason        string   `json:"reason"`         // LLM 简短分析
}

// NewsAnalysisResult 整体分析结果
type NewsAnalysisResult struct {
	FetchedAt       time.Time        `json:"fetched_at"`
	TotalNews       int              `json:"total_news"`
	BullishCount    int              `json:"bullish_count"`
	BearishCount    int              `json:"bearish_count"`
	NeutralCount    int              `json:"neutral_count"`
	MarketSentiment string           `json:"market_sentiment"` // "偏多" / "偏空" / "中性"
	Items           []*NewsSentiment `json:"items"`
	LinkedItems     []*NewsSentiment `json:"linked_items"` // 与持仓/计划有关联的条目
}

// ─────────────────────────────────────────────────────────────────
// 服务结构
// ─────────────────────────────────────────────────────────────────

type NewsSentimentAnalyzer struct {
	aiSvc        *AIAnalysisService
	buyPlanRepo  repo.BuyPlanRepo
	positionRepo repo.PositionGuardianRepo
	log          *zap.Logger

	mu           sync.RWMutex
	cachedAt     time.Time
	cachedResult *NewsAnalysisResult
}

const newsCacheTTL = 5 * time.Minute

func NewNewsSentimentAnalyzer(
	aiSvc *AIAnalysisService,
	buyPlanRepo repo.BuyPlanRepo,
	positionRepo repo.PositionGuardianRepo,
	log *zap.Logger,
) *NewsSentimentAnalyzer {
	return &NewsSentimentAnalyzer{
		aiSvc:        aiSvc,
		buyPlanRepo:  buyPlanRepo,
		positionRepo: positionRepo,
		log:          log,
	}
}

// ─────────────────────────────────────────────────────────────────
// Analyze — 主入口
// ─────────────────────────────────────────────────────────────────

func (a *NewsSentimentAnalyzer) Analyze(ctx context.Context, userID int64) (*NewsAnalysisResult, error) {
	a.mu.RLock()
	if a.cachedResult != nil && time.Since(a.cachedAt) < newsCacheTTL {
		cp := *a.cachedResult
		a.mu.RUnlock()
		return &cp, nil
	}
	a.mu.RUnlock()

	type newsResult struct {
		items []*NewsItem
		err   error
	}
	type ctxResult struct {
		positions []string
		plans     map[string]string
		err       error
	}

	newsCh := make(chan newsResult, 1)
	ctxCh  := make(chan ctxResult, 1)

	go func() {
		items, err := a.fetchTelegraph(ctx)
		newsCh <- newsResult{items, err}
	}()
	go func() {
		pos, plans, err := a.fetchUserContext(ctx, userID)
		ctxCh <- ctxResult{pos, plans, err}
	}()

	nr := <-newsCh
	cr := <-ctxCh

	if nr.err != nil {
		return nil, fmt.Errorf("fetch telegraph: %w", nr.err)
	}

	importantNews := filterImportantNews(nr.items)
	sentiments := a.scoreNewsBatch(ctx, importantNews)

	if cr.err == nil {
		a.linkToPortfolio(sentiments, cr.positions, cr.plans)
	}

	result := newsAnalysisBuildResult(sentiments, nr.items)
	a.log.Info("news sentiment analyzed",
		zap.Int("total", result.TotalNews),
		zap.Int("bullish", result.BullishCount),
		zap.Int("bearish", result.BearishCount),
		zap.Int("linked", len(result.LinkedItems)),
	)

	a.mu.Lock()
	a.cachedResult = result
	a.cachedAt = time.Now()
	a.mu.Unlock()

	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// 财联社电报抓取（解析 __NEXT_DATA__）
// ─────────────────────────────────────────────────────────────────

const clsTelegraphURL = "https://www.cls.cn/telegraph"

type clsNextData struct {
	Props struct {
		InitialState struct {
			Telegraph struct {
				TelegraphList []clsTelegraphItem `json:"telegraphList"`
			} `json:"telegraph"`
		} `json:"initialState"`
	} `json:"props"`
}

type clsTelegraphItem struct {
	ID       interface{} `json:"id"`
	Content  string      `json:"content"`
	Level    string      `json:"level"`
	Ctime    int64       `json:"ctime"`
	Subjects []struct {
		SubjectName string `json:"subject_name"`
	} `json:"subjects"`
	Stocks []struct {
		StockCode string `json:"stock_code"`
		StockName string `json:"stock_name"`
	} `json:"stocks"`
}

func (a *NewsSentimentAnalyzer) fetchTelegraph(ctx context.Context) ([]*NewsItem, error) {
	body, err := fetchQQHTTP(ctx, clsTelegraphURL)
	if err != nil {
		return nil, fmt.Errorf("http fetch: %w", err)
	}

	raw := string(body)
	marker := `id="__NEXT_DATA__" type="application/json">`
	start := strings.Index(raw, marker)
	if start < 0 {
		return nil, fmt.Errorf("__NEXT_DATA__ not found in page")
	}
	start += len(marker)
	end := strings.Index(raw[start:], `</script>`)
	if end < 0 {
		return nil, fmt.Errorf("__NEXT_DATA__ script end not found")
	}
	jsonStr := raw[start : start+end]

	var nextData clsNextData
	if err := json.Unmarshal([]byte(jsonStr), &nextData); err != nil {
		return nil, fmt.Errorf("parse __NEXT_DATA__: %w", err)
	}

	list := nextData.Props.InitialState.Telegraph.TelegraphList
	if len(list) == 0 {
		return nil, fmt.Errorf("telegraph list empty")
	}

	items := make([]*NewsItem, 0, len(list))
	for _, item := range list {
		news := &NewsItem{
			Content:   newsCleanContent(item.Content),
			Level:     item.Level,
			Timestamp: item.Ctime,
			Time:      time.Unix(item.Ctime, 0),
		}
		switch v := item.ID.(type) {
		case float64:
			news.ID = fmt.Sprintf("%.0f", v)
		case string:
			news.ID = v
		}
		for _, s := range item.Subjects {
			if s.SubjectName != "" {
				news.Tags = append(news.Tags, s.SubjectName)
			}
		}
		for _, s := range item.Stocks {
			if s.StockCode != "" {
				news.Stocks = append(news.Stocks, NewsStock{Code: s.StockCode, Name: s.StockName})
			}
		}
		items = append(items, news)
	}

	a.log.Info("telegraph fetched", zap.Int("count", len(items)))
	return items, nil
}

// newsCleanContent 去掉财联社来源后缀（避免与 market_sentinel_service.go 中的 cleanContent 重名）
func newsCleanContent(s string) string {
	s = strings.TrimSpace(s)
	if idx := strings.LastIndex(s, " ("); idx > len(s)/2 {
		s = strings.TrimSpace(s[:idx])
	}
	return s
}

func filterImportantNews(items []*NewsItem) []*NewsItem {
	noiseKeywords := []string{"涨跌不一", "截至收盘", "今日成交", "期货开盘", "美股三大"}
	result := make([]*NewsItem, 0, len(items))
	for _, item := range items {
		if item.Level == "C" {
			continue
		}
		isNoise := false
		for _, kw := range noiseKeywords {
			if strings.Contains(item.Content, kw) {
				isNoise = true
				break
			}
		}
		if !isNoise {
			result = append(result, item)
		}
	}
	return result
}

// ─────────────────────────────────────────────────────────────────
// LLM 情绪打分（批量）
// ─────────────────────────────────────────────────────────────────

const newsSentimentPrompt = `你是一位专业的A股量化分析师，请分析以下财联社电报，对每条新闻进行情绪打分和影响范围判断。

新闻列表（格式：[序号] [级别] 内容）：
%s

请对每条新闻输出 JSON 数组，格式如下（不要输出任何其他文字）：
[
  {
    "index": 1,
    "score": 1,
    "scope_type": "sector",
    "scope": "板块:光伏",
    "reason": "特斯拉采购国内光伏设备，直接利好光伏板块"
  }
]

评分规则：
- score: +1=利好，0=中性，-1=利空
- scope_type: "market"(影响大盘) | "sector"(影响特定板块) | "stock"(影响特定个股)
- scope: 如 "大盘"、"板块:稀土"、"板块:光伏"、"个股:中国稀土"
- reason: 15字以内的简短理由

只输出 JSON 数组，不要 markdown 代码块。`

type llmSentimentItem struct {
	Index     int    `json:"index"`
	Score     int    `json:"score"`
	ScopeType string `json:"scope_type"`
	Scope     string `json:"scope"`
	Reason    string `json:"reason"`
}

func (a *NewsSentimentAnalyzer) scoreNewsBatch(ctx context.Context, items []*NewsItem) []*NewsSentiment {
	results := make([]*NewsSentiment, len(items))
	for i, item := range items {
		results[i] = &NewsSentiment{
			NewsItem:   *item,
			Score:      0,
			ScoreLabel: "中性",
			Scope:      "大盘",
			ScopeType:  "market",
		}
	}

	if a.aiSvc.apiKey == "" || len(items) == 0 {
		return results
	}

	var sb strings.Builder
	for i, item := range items {
		sb.WriteString(fmt.Sprintf("[%d] [%s] %s\n", i+1, item.Level, item.Content))
	}
	prompt := fmt.Sprintf(newsSentimentPrompt, sb.String())

	raw, err := a.aiSvc.callEino(ctx, prompt)
	if err != nil {
		a.log.Warn("news sentiment LLM failed, using defaults", zap.Error(err))
		return results
	}

	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	// 截取用于日志的安全长度
	logLen := len(raw)
	if logLen > 200 {
		logLen = 200
	}

	var llmResults []llmSentimentItem
	if err := json.Unmarshal([]byte(raw), &llmResults); err != nil {
		a.log.Warn("news sentiment LLM parse failed", zap.Error(err), zap.String("raw", raw[:logLen]))
		return results
	}

	for _, lr := range llmResults {
		idx := lr.Index - 1
		if idx < 0 || idx >= len(results) {
			continue
		}
		results[idx].Score     = newsClampScore(lr.Score)
		results[idx].ScopeType = lr.ScopeType
		results[idx].Scope     = lr.Scope
		results[idx].Reason    = lr.Reason
		switch results[idx].Score {
		case 1:
			results[idx].ScoreLabel = "利好"
		case -1:
			results[idx].ScoreLabel = "利空"
		default:
			results[idx].ScoreLabel = "中性"
		}
	}

	return results
}

// ─────────────────────────────────────────────────────────────────
// 关联持仓/买入计划
// ─────────────────────────────────────────────────────────────────

func (a *NewsSentimentAnalyzer) fetchUserContext(ctx context.Context, userID int64) (
	positions []string, plans map[string]string, err error,
) {
	plans = make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var posErr, planErr error

	wg.Add(2)
	go func() {
		defer wg.Done()
		posList, e := a.positionRepo.ListAll(ctx)
		if e != nil {
			posErr = e
			return
		}
		mu.Lock()
		for _, p := range posList {
			positions = append(positions, p.StockCode)
		}
		mu.Unlock()
	}()
	go func() {
		defer wg.Done()
		planList, e := a.buyPlanRepo.ListByUser(ctx, userID,
			[]model.BuyPlanStatus{model.BuyPlanStatusWatching, model.BuyPlanStatusReady})
		if e != nil {
			planErr = e
			return
		}
		mu.Lock()
		for _, p := range planList {
			plans[p.StockCode] = p.StockName
		}
		mu.Unlock()
	}()
	wg.Wait()

	if posErr != nil {
		err = posErr
	} else if planErr != nil {
		err = planErr
	}
	return
}

func (a *NewsSentimentAnalyzer) linkToPortfolio(
	sentiments []*NewsSentiment,
	positions []string,
	plans map[string]string,
) {
	posSet := make(map[string]bool, len(positions))
	for _, code := range positions {
		posSet[code] = true
	}

	for _, s := range sentiments {
		if s.Score == 0 {
			continue
		}

		var linked []string
		var hints []string

		// 财联社已标注的关联股票
		for _, ns := range s.Stocks {
			if posSet[ns.Code] {
				linked = append(linked, ns.Code)
				if s.Score > 0 {
					hints = append(hints, fmt.Sprintf("📈 %s(%s)持仓利好：%s", ns.Name, ns.Code, s.Reason))
				} else {
					hints = append(hints, fmt.Sprintf("📉 %s(%s)持仓利空：%s，考虑减仓", ns.Name, ns.Code, s.Reason))
				}
			}
			if name, inPlan := plans[ns.Code]; inPlan {
				linked = append(linked, ns.Code)
				if s.Score > 0 {
					hints = append(hints, fmt.Sprintf("🎯 买入计划 %s(%s) 有利好催化：%s，建议关注入场时机", name, ns.Code, s.Reason))
				} else {
					hints = append(hints, fmt.Sprintf("⚠️ 买入计划 %s(%s) 面临利空：%s，暂缓建仓", name, ns.Code, s.Reason))
				}
			}
		}

		// 关键词匹配
		linked, hints = a.keywordMatch(s, posSet, plans, linked, hints)

		s.AffectedCodes = newsUniqueStrings(linked)
		s.ActionHints   = hints
	}
}

func (a *NewsSentimentAnalyzer) keywordMatch(
	s *NewsSentiment,
	posSet map[string]bool,
	plans map[string]string,
	linked []string,
	hints []string,
) ([]string, []string) {
	content    := s.Content
	scopeLower := strings.ToLower(s.Scope)

	sectorKeywords := map[string][]string{
		"稀土":   {"稀土", "磁材", "钕铁硼"},
		"光伏":   {"光伏", "太阳能", "硅片", "组件"},
		"新能源": {"新能源", "锂电", "储能", "电池"},
		"半导体": {"芯片", "半导体", "集成电路"},
		"军工":   {"军工", "航天", "国防"},
		"医药":   {"医药", "创新药", "CXO"},
	}

	for sector, keywords := range sectorKeywords {
		matched := false
		for _, kw := range keywords {
			if strings.Contains(content, kw) || strings.Contains(scopeLower, strings.ToLower(kw)) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		for code, name := range plans {
			alreadyLinked := false
			for _, c := range linked {
				if c == code {
					alreadyLinked = true
					break
				}
			}
			if alreadyLinked {
				continue
			}
			for _, kw := range keywords {
				if strings.Contains(name, kw) || strings.Contains(name, sector) {
					linked = append(linked, code)
					if s.Score > 0 {
						hints = append(hints, fmt.Sprintf(
							"🎯 买入计划 %s(%s) 关联 [%s] 利好新闻：%s，建议调高优先级",
							name, code, sector, s.Reason))
					} else {
						hints = append(hints, fmt.Sprintf(
							"⚠️ 买入计划 %s(%s) 关联 [%s] 利空新闻：%s，暂缓建仓",
							name, code, sector, s.Reason))
					}
					break
				}
			}
		}
	}

	return linked, hints
}

// ─────────────────────────────────────────────────────────────────
// 汇总结果
// ─────────────────────────────────────────────────────────────────

func newsAnalysisBuildResult(sentiments []*NewsSentiment, allItems []*NewsItem) *NewsAnalysisResult {
	result := &NewsAnalysisResult{
		FetchedAt: time.Now(),
		TotalNews: len(allItems),
		Items:     sentiments,
	}

	bullish, bearish, neutral := 0, 0, 0
	linked := make([]*NewsSentiment, 0)
	for _, s := range sentiments {
		switch s.Score {
		case 1:
			bullish++
		case -1:
			bearish++
		default:
			neutral++
		}
		if len(s.AffectedCodes) > 0 {
			linked = append(linked, s)
		}
	}
	result.BullishCount = bullish
	result.BearishCount = bearish
	result.NeutralCount = neutral
	result.LinkedItems  = linked

	netScore := bullish - bearish
	switch {
	case netScore >= 3:
		result.MarketSentiment = "偏多"
	case netScore <= -3:
		result.MarketSentiment = "偏空"
	case netScore > 0:
		result.MarketSentiment = "略偏多"
	case netScore < 0:
		result.MarketSentiment = "略偏空"
	default:
		result.MarketSentiment = "中性"
	}

	return result
}

// ─────────────────────────────────────────────────────────────────
// 包私有工具函数（带 news 前缀避免与其他文件冲突）
// ─────────────────────────────────────────────────────────────────

// newsClampScore 将 score 限定在 [-1, 1]
func newsClampScore(v int) int {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}

// newsUniqueStrings 去重字符串切片
func newsUniqueStrings(ss []string) []string {
	seen := make(map[string]bool, len(ss))
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
