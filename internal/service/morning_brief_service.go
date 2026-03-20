package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// MorningBriefService — 每日开盘前报告
// ═══════════════════════════════════════════════════════════════

type MorningBriefSection struct {
	Title string   `json:"title"`
	Items []string `json:"items"`
	Level string   `json:"level"` // "normal" | "warning" | "danger" | "info"
}

type MorningBriefDTO struct {
	Date        string                `json:"date"`
	GeneratedAt time.Time             `json:"generated_at"`
	MarketMood  string                `json:"market_mood"`
	MoodScore   int                   `json:"mood_score"`
	MoodSummary string                `json:"mood_summary"`
	Sections    []MorningBriefSection `json:"sections"`
	AIComment   string                `json:"ai_comment"`
	AIPending   bool                  `json:"ai_pending"`
	FromCache   bool                  `json:"from_cache"`
}

type MorningBriefService struct {
	marketSvc     *MarketSentinelService
	guardianSvc   *PositionGuardianService
	reportSvc     *StockReportService
	valSvc        *ValuationService
	dividendSvc   *DividendCalendarService
	newsAnalyzer  *NewsSentimentAnalyzer // ★ 新增
	buyPlanRepo   repo.BuyPlanRepo
	watchlistRepo repo.WatchlistRepo
	aiSvc         *AIAnalysisService
	log           *zap.Logger

	mu         sync.RWMutex
	cachedDate string
	cached     *MorningBriefDTO
}

func NewMorningBriefService(
	marketSvc *MarketSentinelService,
	guardianSvc *PositionGuardianService,
	reportSvc *StockReportService,
	valSvc *ValuationService,
	buyPlanRepo repo.BuyPlanRepo,
	watchlistRepo repo.WatchlistRepo,
	aiSvc *AIAnalysisService,
	log *zap.Logger,
) *MorningBriefService {
	return &MorningBriefService{
		marketSvc:     marketSvc,
		guardianSvc:   guardianSvc,
		reportSvc:     reportSvc,
		valSvc:        valSvc,
		dividendSvc:   NewDividendCalendarService(log),
		newsAnalyzer:  NewNewsSentimentAnalyzer(aiSvc, buyPlanRepo, guardianSvc.posRepo, log),
		buyPlanRepo:   buyPlanRepo,
		watchlistRepo: watchlistRepo,
		aiSvc:         aiSvc,
		log:           log,
	}
}

// ─────────────────────────────────────────────────────────────────
// Generate — 立即返回主体数据，AI 点评异步生成
// ─────────────────────────────────────────────────────────────────

func (s *MorningBriefService) Generate(ctx context.Context, userID int64, force bool) (*MorningBriefDTO, error) {
	today := time.Now().Format("2006-01-02")

	if !force {
		s.mu.RLock()
		if s.cachedDate == today && s.cached != nil {
			cp := *s.cached
			cp.FromCache = true
			s.mu.RUnlock()
			return &cp, nil
		}
		s.mu.RUnlock()
	}

	brief, err := s.build(ctx, userID, today)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.cachedDate = today
	s.cached = brief
	s.mu.Unlock()

	if s.aiSvc.apiKey != "" {
		go s.generateAICommentAsync(brief)
	}

	return brief, nil
}

func (s *MorningBriefService) generateAICommentAsync(brief *MorningBriefDTO) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	comment := s.buildAIComment(ctx, brief)
	if comment == "" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cachedDate == brief.Date && s.cached != nil {
		s.cached.AIComment = comment
		s.cached.AIPending  = false
		s.log.Info("morning brief: AI comment updated in cache", zap.String("date", brief.Date))
	}
}

// ─────────────────────────────────────────────────────────────────
// build — 并发拉取各数据源（新增 Section 5：新闻情绪）
// ─────────────────────────────────────────────────────────────────

func (s *MorningBriefService) build(ctx context.Context, userID int64, today string) (*MorningBriefDTO, error) {
	brief := &MorningBriefDTO{
		Date:        today,
		GeneratedAt: time.Now(),
		Sections:    make([]MorningBriefSection, 6), // 6 个 section
		AIPending:   s.aiSvc.apiKey != "",
	}

	marketSection, mood, score, moodSummary := s.buildMarketSection(ctx)
	brief.MarketMood  = mood
	brief.MoodScore   = score
	brief.MoodSummary = moodSummary
	brief.Sections[0] = marketSection

	type result struct {
		section MorningBriefSection
		idx     int
	}
	ch := make(chan result, 5)

	go func() { ch <- result{s.buildPositionSection(ctx), 1} }()
	go func() { ch <- result{s.buildBuyPlanSection(ctx, userID), 2} }()
	go func() { ch <- result{s.buildReportSection(ctx), 3} }()
	go func() { ch <- result{s.buildValuationSection(ctx, userID), 4} }()
	go func() { ch <- result{s.buildNewsSection(ctx, userID), 5} }() // ★ 新闻情绪

	for range [5]struct{}{} {
		r := <-ch
		brief.Sections[r.idx] = r.section
	}

	return brief, nil
}

// ─────────────────────────────────────────────────────────────────
// buildNewsSection — 财联社新闻情绪分析
// ─────────────────────────────────────────────────────────────────

func (s *MorningBriefService) buildNewsSection(ctx context.Context, userID int64) MorningBriefSection {
	sec := MorningBriefSection{Title: "新闻情绪", Level: "normal"}

	// 给新闻分析一个单独的超时（最多 30s，不阻断其他 section）
	newsCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := s.newsAnalyzer.Analyze(newsCtx, userID)
	if err != nil {
		s.log.Warn("morning brief: news analysis failed", zap.Error(err))
		sec.Items = []string{"财联社电报暂时无法获取，请稍后刷新"}
		return sec
	}

	// 情绪概览行
	sentimentIcon := "🟡"
	switch result.MarketSentiment {
	case "偏多", "略偏多":
		sentimentIcon = "🟢"
		sec.Level = "info"
	case "偏空", "略偏空":
		sentimentIcon = "🔴"
		sec.Level = "warning"
	}

	sec.Items = append(sec.Items, fmt.Sprintf(
		"%s 财联社 %d 条电报 | 利好 %d 利空 %d 中性 %d | 综合情绪：%s",
		sentimentIcon,
		result.TotalNews,
		result.BullishCount,
		result.BearishCount,
		result.NeutralCount,
		result.MarketSentiment,
	))

	// 与持仓/买入计划有关联的新闻（最重要，放最前）
	if len(result.LinkedItems) > 0 {
		sec.Items = append(sec.Items, "── 关联你的持仓/计划 ──────────────")
		for _, item := range result.LinkedItems {
			for _, hint := range item.ActionHints {
				sec.Items = append(sec.Items, hint)
			}
		}
		// 有关联且有利空，升级警告级别
		for _, item := range result.LinkedItems {
			if item.Score < 0 {
				sec.Level = "warning"
				break
			}
		}
	}

	// 重要新闻摘要（A 级 + 有评分的 B 级，最多 5 条）
	importantShown := 0
	headerAdded := false
	for _, item := range result.Items {
		if importantShown >= 5 {
			break
		}
		if item.Level != "A" && (item.Level == "B" && item.Score == 0) {
			continue
		}
		if len(item.AffectedCodes) > 0 {
			continue // 已在上面的关联区显示
		}
		if !headerAdded {
			sec.Items = append(sec.Items, "── 重要电报 ──────────────────────")
			headerAdded = true
		}
		scoreIcon := "⬜"
		switch item.Score {
		case 1:
			scoreIcon = "🟢"
		case -1:
			scoreIcon = "🔴"
		}
		content := item.Content
		if len([]rune(content)) > 80 {
			content = string([]rune(content)[:80]) + "…"
		}
		line := fmt.Sprintf("%s [%s] %s", scoreIcon, item.Level, content)
		if item.Reason != "" {
			line += fmt.Sprintf("（%s）", item.Reason)
		}
		sec.Items = append(sec.Items, line)
		importantShown++
	}

	if len(sec.Items) == 1 {
		sec.Items = append(sec.Items, "暂无重要电报")
	}

	return sec
}

// ─────────────────────────────────────────────────────────────────
// 原有 Section 构建（保持不变）
// ─────────────────────────────────────────────────────────────────

func (s *MorningBriefService) buildMarketSection(ctx context.Context) (MorningBriefSection, string, int, string) {
	sec := MorningBriefSection{Title: "大盘情绪", Level: "normal"}

	summary, err := s.marketSvc.GetSummary(ctx)
	if err != nil || summary == nil {
		sec.Items = []string{"暂无今日大盘数据，市场可能尚未开盘"}
		return sec, "SAFE", 50, "大盘数据暂缺"
	}

	amtStr := fmt.Sprintf("%.0f 亿", summary.TotalAmount/1e8)
	sec.Items = []string{
		fmt.Sprintf("成交额 %s，热度指数 %d/100", amtStr, summary.SentimentScore),
		fmt.Sprintf("上涨 %d 家 / 下跌 %d 家", summary.UpCount, summary.DownCount),
		summary.DailySummary,
	}

	level := "normal"
	switch summary.AlertStatus {
	case "DANGER":
		level = "danger"
		sec.Items = append(sec.Items, "⚠️ 市场极寒，严控仓位，以防御为主")
	case "WARNING":
		level = "warning"
		sec.Items = append(sec.Items, "⚡ 市场偏弱，谨慎操作，注意止损")
	default:
		if summary.SentimentScore >= 70 {
			sec.Items = append(sec.Items, "🔥 市场火热，可适度进攻")
		}
	}
	sec.Level = level
	return sec, summary.AlertStatus, summary.SentimentScore, summary.DailySummary
}

func (s *MorningBriefService) buildPositionSection(ctx context.Context) MorningBriefSection {
	sec := MorningBriefSection{Title: "持仓预警", Level: "normal"}

	results, err := s.guardianSvc.DiagnoseAll(ctx)
	if err != nil || len(results) == 0 {
		sec.Items = []string{"暂无持仓记录"}
		return sec
	}

	holdCodes := make([]string, 0, len(results))
	for _, r := range results {
		holdCodes = append(holdCodes, r.StockCode)
	}
	divCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	dividends, _ := s.dividendSvc.GetUpcomingDividends(divCtx, holdCodes)
	divMap := make(map[string]*DividendEvent)
	for _, d := range dividends {
		if _, exists := divMap[d.StockCode]; !exists {
			divMap[d.StockCode] = d
		}
	}

	var stopItems, sellItems, tItems, holdItems []string
	for _, r := range results {
		pnlStr   := fmt.Sprintf("%+.1f%%", r.Snapshot.PnLPct*100)
		priceStr := fmt.Sprintf("¥%.2f", r.Snapshot.Price)
		base     := fmt.Sprintf("%s（%s）现价 %s，盈亏 %s", r.StockName, r.StockCode, priceStr, pnlStr)
		divSuffix := ""
		if div, ok := divMap[r.StockCode]; ok {
			switch div.DaysUntil {
			case 0:
				divSuffix = fmt.Sprintf(" ⚠️【今日除权：%s】注意成本线跳升", div.PlanDesc)
			case 1:
				divSuffix = fmt.Sprintf(" 📅【明日除权：%s】今日卖出可避免除权", div.PlanDesc)
			default:
				divSuffix = fmt.Sprintf(" 📅【%d天后除权：%s】", div.DaysUntil, div.PlanDesc)
			}
		}
		switch r.Signal {
		case model.SignalStopLoss:
			stopItems = append(stopItems, fmt.Sprintf("🛑 %s — 触发止损，建议立即执行%s", base, divSuffix))
		case model.SignalSell:
			sellItems = append(sellItems, fmt.Sprintf("📉 %s — 建议减仓%s", base, divSuffix))
		case model.SignalSellT, model.SignalBuyT:
			action := "高抛"
			if r.Signal == model.SignalBuyT {
				action = "低吸"
			}
			tItems = append(tItems, fmt.Sprintf("🔄 %s — T+0 %s机会，振幅 %.1f%%%s", base, action, r.Snapshot.Amplitude*100, divSuffix))
		default:
			holdItems = append(holdItems, fmt.Sprintf("✅ %s — 持有%s", base, divSuffix))
		}
	}

	if len(stopItems) > 0 {
		sec.Level = "danger"
	} else if len(sellItems) > 0 {
		sec.Level = "warning"
	}
	sec.Items = append(sec.Items, stopItems...)
	sec.Items = append(sec.Items, sellItems...)
	sec.Items = append(sec.Items, tItems...)
	sec.Items = append(sec.Items, holdItems...)
	if len(sec.Items) == 0 {
		sec.Items = []string{"持仓正常，无需特别操作"}
	}
	return sec
}

func (s *MorningBriefService) buildBuyPlanSection(ctx context.Context, userID int64) MorningBriefSection {
	sec := MorningBriefSection{Title: "买入计划", Level: "normal"}

	plans, err := s.buyPlanRepo.ListByUser(ctx, userID,
		[]model.BuyPlanStatus{model.BuyPlanStatusWatching, model.BuyPlanStatusReady})
	if err != nil || len(plans) == 0 {
		sec.Items = []string{"暂无活跃买入计划"}
		return sec
	}

	triggeredCount := 0
	for _, p := range plans {
		buyStr := "市价"
		if p.BuyPrice != nil {
			buyStr = fmt.Sprintf("¥%.2f", *p.BuyPrice)
		}
		tgtStr := "—"
		if p.TargetPrice != nil {
			tgtStr = fmt.Sprintf("¥%.2f", *p.TargetPrice)
		}
		if p.Status == model.BuyPlanStatusReady {
			triggeredCount++
			item := fmt.Sprintf("🎯 %s（%s）已到达买入价 %s → 目标 %s", p.StockName, p.StockCode, buyStr, tgtStr)
			if p.RiskRewardRatio != nil {
				item += fmt.Sprintf("，盈亏比 1:%.1f", *p.RiskRewardRatio)
			}
			sec.Items = append(sec.Items, item)
		} else {
			sec.Items = append(sec.Items,
				fmt.Sprintf("👀 %s（%s）观察中，买入价 %s，目标 %s", p.StockName, p.StockCode, buyStr, tgtStr))
		}
	}
	if triggeredCount > 0 {
		sec.Level = "info"
		sec.Items = append([]string{
			fmt.Sprintf("⚡ 有 %d 个计划已触发买点，请关注入场时机", triggeredCount),
		}, sec.Items...)
	}
	return sec
}

func (s *MorningBriefService) buildReportSection(ctx context.Context) MorningBriefSection {
	sec := MorningBriefSection{Title: "研报速递", Level: "normal"}
	page, err := s.reportSvc.GetReports(ctx, repo.StockReportQuery{Page: 1, Limit: 5})
	if err != nil || page == nil || len(page.Items) == 0 {
		sec.Items = []string{"暂无最新研报"}
		return sec
	}
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	todayStr  := time.Now().Format("2006-01-02")
	for _, r := range page.Items {
		dateStr := r.PublishDate.Format("2006-01-02")
		if dateStr != todayStr && dateStr != yesterday {
			continue
		}
		summary := r.AISummary
		if summary == "" {
			summary = r.Title
		}
		if len([]rune(summary)) > 60 {
			summary = string([]rune(summary)[:60]) + "…"
		}
		sec.Items = append(sec.Items,
			fmt.Sprintf("📋 %s（%s）%s — %s [%s]", r.StockName, r.StockCode, r.RatingName, summary, r.OrgSName))
	}
	if len(sec.Items) == 0 {
		sec.Items = []string{"今日暂无新研报"}
	}
	return sec
}

func (s *MorningBriefService) buildValuationSection(ctx context.Context, userID int64) MorningBriefSection {
	sec := MorningBriefSection{Title: "估值机会", Level: "normal"}
	summary, err := s.valSvc.GetWatchlistSummary(ctx, userID)
	if err != nil || summary == nil || summary.Total == 0 {
		sec.Items = []string{"自选股估值数据暂缺，可手动触发同步"}
		return sec
	}
	sec.Items = append(sec.Items, fmt.Sprintf(
		"自选股共 %d 只：低估 %d | 合理 %d | 高估 %d | 积累中 %d",
		summary.Total, summary.Undervalued, summary.Normal, summary.Overvalued, summary.Unknown,
	))
	undervaluedStocks := make([]string, 0, 3)
	for _, item := range summary.Items {
		if item.Status == StatusUndervalued && item.PEPercentile != nil {
			undervaluedStocks = append(undervaluedStocks,
				fmt.Sprintf("%s（%s）PE 分位 %.0f%%", item.StockName, item.StockCode, *item.PEPercentile))
			if len(undervaluedStocks) >= 3 {
				break
			}
		}
	}
	if len(undervaluedStocks) > 0 {
		sec.Level = "info"
		sec.Items = append(sec.Items, "低估标的："+strings.Join(undervaluedStocks, " | "))
	}
	overvaluedNames := make([]string, 0, 3)
	for _, item := range summary.Items {
		if item.Status == StatusOvervalued {
			overvaluedNames = append(overvaluedNames, item.StockName)
			if len(overvaluedNames) >= 3 {
				break
			}
		}
	}
	if len(overvaluedNames) > 0 {
		sec.Items = append(sec.Items, "高估注意："+strings.Join(overvaluedNames, "、")+"，酌情减仓")
	}
	return sec
}

func (s *MorningBriefService) buildAIComment(ctx context.Context, brief *MorningBriefDTO) string {
	if s.aiSvc.apiKey == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("今日（%s）A股开盘前简报：\n", brief.Date))
	sb.WriteString(fmt.Sprintf("大盘情绪：%s，热度 %d/100。%s\n", brief.MarketMood, brief.MoodScore, brief.MoodSummary))
	for _, sec := range brief.Sections {
		if len(sec.Items) == 0 {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n【%s】\n", sec.Title))
		for _, item := range sec.Items {
			sb.WriteString("- " + item + "\n")
		}
	}
	prompt := sb.String() + "\n请用 3-4 句话给出今日操作的核心建议，语气专业简练，不超过 150 字。"
	comment, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		s.log.Warn("morning brief: AI comment failed", zap.Error(err))
		return ""
	}
	return strings.TrimSpace(comment)
}
