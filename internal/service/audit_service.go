package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 常量与关键词表
// ═══════════════════════════════════════════════════════════════

const (
	defaultUserID = int64(1)

	shortTermMaxDays = 7   // 短于 7 天且理由含长线词 → LOGIC_CONFLICT
	chasingHighPct   = 8.0 // 买点偏离 MA20 超过 8% → CHASING_HIGH
	prematureExitPct = 3.0 // 盈利不足 3% 就卖，且理由含目标位词 → PREMATURE_EXIT
)

var longTermKeywords = []string{
	"长线", "长期", "价值投资", "基本面", "年线", "半年线",
	"价投", "长持", "持有1年", "持有半年",
}

var targetKeywords = []string{
	"目标", "止盈", "达到目标", "涨停", "压力位",
}

// ═══════════════════════════════════════════════════════════════
// 请求 / 响应 DTO
// ═══════════════════════════════════════════════════════════════

type SubmitReviewRequest struct {
	TradeLogID  int64    `json:"trade_log_id"  binding:"required"`
	MentalState string   `json:"mental_state"`
	UserNote    string   `json:"user_note"`
	Tags        []string `json:"tags"`
	BuyReason   string   `json:"buy_reason"`
	SellReason  string   `json:"sell_reason"`
	TriggerAI   bool     `json:"trigger_ai"`
}

type ReviewDetailDTO struct {
	*repo.TradeReviewWithTrade
	AIReady bool `json:"ai_ready"`
}

type DashboardDTO struct {
	*repo.DashboardStats
	RecentReviews []*repo.TradeReviewWithTrade `json:"recent_reviews"`
	EmotionMatrix []*EmotionCell               `json:"emotion_matrix"`
}

type EmotionCell struct {
	Emotion   string  `json:"emotion"`
	Count     int64   `json:"count"`
	AvgPnl    float64 `json:"avg_pnl"`
	WinRate   float64 `json:"win_rate"`
	RiskScore float64 `json:"risk_score"`
}

// ═══════════════════════════════════════════════════════════════
// AuditService
// ═══════════════════════════════════════════════════════════════

type AuditService struct {
	reviewRepo  repo.ReviewRepo
	tradeV2Repo repo.TradeLogV2Repo
	stockSvc    *StockService
	aiSvc       *AIAnalysisService
	log         *zap.Logger
}

func NewAuditService(
	reviewRepo repo.ReviewRepo,
	tradeV2Repo repo.TradeLogV2Repo,
	stockSvc *StockService,
	aiSvc *AIAnalysisService,
	log *zap.Logger,
) *AuditService {
	return &AuditService{
		reviewRepo:  reviewRepo,
		tradeV2Repo: tradeV2Repo,
		stockSvc:    stockSvc,
		aiSvc:       aiSvc,
		log:         log,
	}
}

// ─────────────────────────────────────────────────────────────────
// SubmitReview — POST /api/v1/review/submit
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) SubmitReview(ctx context.Context, userID int64, req *SubmitReviewRequest) (*ReviewDetailDTO, error) {
	// 1. 查找已有复盘，没有则初始化
	rev, err := s.reviewRepo.GetByTradeLogID(ctx, req.TradeLogID)
	if err != nil {
		rev, err = s.initFromTradeLogID(ctx, userID, req.TradeLogID)
		if err != nil {
			return nil, fmt.Errorf("初始化复盘记录失败: %w", err)
		}
	}

	// 2. 更新用户填写的字段
	if req.MentalState != "" {
		rev.MentalState = req.MentalState
	}
	if req.UserNote != "" {
		rev.UserNote = req.UserNote
	}
	if len(req.Tags) > 0 {
		rev.Tags = model.StringArray(req.Tags)
	}

	// 3. 更新买卖理由并重跑一致性审计
	if req.BuyReason != "" || req.SellReason != "" {
		if err := s.tradeV2Repo.UpdateReasons(ctx, req.TradeLogID, req.BuyReason, req.SellReason); err != nil {
			s.log.Warn("update trade reasons failed", zap.Error(err))
		}
		// FIX: 直接用 GetByID 精确查卖出记录，不再全量遍历
		sell, buyLog, fetchErr := s.fetchSellAndBuy(ctx, userID, req.TradeLogID)
		if fetchErr == nil && sell != nil {
			buyReason := req.BuyReason
			sellReason := req.SellReason
			if buyReason == "" && buyLog != nil {
				buyReason = buyLog.BuyReason
			}
			if sellReason == "" {
				sellReason = sell.SellReason
			}
			s.runConsistencyAudit(ctx, rev, buyReason, sellReason, buyLog, sell.TradedAt)
		}
	}

	if err := s.reviewRepo.Update(ctx, rev); err != nil {
		return nil, fmt.Errorf("保存复盘失败: %w", err)
	}

	// 4. 可选触发 AI 审计（异步）
	if req.TriggerAI {
		go func() {
			bgCtx := context.Background()
			if err := s.GenerateAIAudit(bgCtx, rev.ID); err != nil {
				s.log.Error("async AI audit failed", zap.Int64("review_id", rev.ID), zap.Error(err))
			}
		}()
	}

	return s.toDetailDTO(ctx, rev), nil
}

// ─────────────────────────────────────────────────────────────────
// GetDashboard — GET /api/v1/review/dashboard
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) GetDashboard(ctx context.Context, userID int64) (*DashboardDTO, error) {
	stats, err := s.reviewRepo.DashboardStats(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("获取看板数据失败: %w", err)
	}

	recent, err := s.reviewRepo.ListByUser(ctx, userID, 5, 0)
	if err != nil {
		recent = []*repo.TradeReviewWithTrade{}
	}

	return &DashboardDTO{
		DashboardStats: stats,
		RecentReviews:  recent,
		EmotionMatrix:  buildEmotionMatrix(stats.MentalStateStats),
	}, nil
}

func (s *AuditService) ListReviews(ctx context.Context, userID int64, limit, offset int) ([]*repo.TradeReviewWithTrade, error) {
	return s.reviewRepo.ListByUser(ctx, userID, limit, offset)
}

func (s *AuditService) CountReviews(ctx context.Context, userID int64) (int64, error) {
	return s.reviewRepo.CountByUser(ctx, userID)
}

// ─────────────────────────────────────────────────────────────────
// GenerateAIAudit — AI 审计（写入 ai_audit_comment）
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) GenerateAIAudit(ctx context.Context, reviewID int64) error {
	rev, err := s.reviewRepo.GetByID(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("找不到复盘记录 id=%d: %w", reviewID, err)
	}

	// FIX: 用 GetByTradeLogID 精确查，不再 ListByUser(1000) 遍历
	withTrade, err := s.reviewRepo.GetWithTradeByID(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("找不到关联交易数据 review_id=%d: %w", reviewID, err)
	}

	prompt := buildAuditPrompt(withTrade)

	comment, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		return fmt.Errorf("AI 审计调用失败: %w", err)
	}

	score := extractExecutionScore(comment)

	now := time.Now()
	rev.AIAuditComment = comment
	rev.AIGeneratedAt = &now
	if score > 0 {
		rev.ExecutionScore = &score
	}
	rev.ImprovementPlan = extractImprovementPlan(comment)

	if err := s.reviewRepo.Update(ctx, rev); err != nil {
		return fmt.Errorf("保存 AI 审计结果失败: %w", err)
	}

	s.log.Info("AI audit completed", zap.Int64("review_id", reviewID), zap.Int("score", score))
	return nil
}

// ─────────────────────────────────────────────────────────────────
// RunPriceTracker — Cron 入口
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) RunPriceTracker(ctx context.Context) (int, error) {
	s.log.Info("price tracker: started")

	pending, err := s.reviewRepo.ListPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询待追踪记录失败: %w", err)
	}
	if len(pending) == 0 {
		s.log.Info("price tracker: no pending records")
		return 0, nil
	}

	// 按 stock_code 分组，减少 K 线接口请求次数
	grouped := make(map[string][]*model.TradeReview)
	for _, r := range pending {
		grouped[r.StockCode] = append(grouped[r.StockCode], r)
	}

	updated := 0
	for code, reviews := range grouped {
		// 拉多一点以确保覆盖最近的卖出日
		klineResp, err := s.stockSvc.GetKLine(code, 30)
		if err != nil {
			s.log.Warn("price tracker: get kline failed", zap.String("code", code), zap.Error(err))
			continue
		}

		for _, rev := range reviews {
			// 需要知道卖出日期，从关联交易记录获取
			withTrade, err := s.reviewRepo.GetWithTradeByID(ctx, rev.ID)
			if err != nil {
				s.log.Warn("price tracker: get trade failed", zap.Int64("id", rev.ID), zap.Error(err))
				continue
			}
			if changed := s.fillPriceTracking(rev, klineResp, withTrade.TradedAt); changed {
				if err := s.reviewRepo.Update(ctx, rev); err != nil {
					s.log.Warn("price tracker: update failed", zap.Int64("id", rev.ID), zap.Error(err))
					continue
				}
				updated++
			}
		}
	}

	s.log.Info("price tracker: completed", zap.Int("updated", updated))
	return updated, nil
}

// ─────────────────────────────────────────────────────────────────
// InitReviewsForRecentSells
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) InitReviewsForRecentSells(ctx context.Context, userID int64) (int, error) {
	to := time.Now()
	from := to.AddDate(0, 0, -7)

	sells, err := s.tradeV2Repo.GetSellsInRange(ctx, userID, from, to)
	if err != nil {
		return 0, err
	}

	created := 0
	for _, sell := range sells {
		// 幂等：已存在则跳过
		if _, err := s.reviewRepo.GetByTradeLogID(ctx, sell.ID); err == nil {
			continue
		}
		rev, err := s.buildReviewFromSell(ctx, userID, sell)
		if err != nil {
			s.log.Warn("init review: build failed", zap.Int64("trade_id", sell.ID), zap.Error(err))
			continue
		}
		if err := s.reviewRepo.Create(ctx, rev); err != nil {
			s.log.Warn("init review: create failed", zap.Int64("trade_id", sell.ID), zap.Error(err))
			continue
		}
		created++
	}
	return created, nil
}

// ═══════════════════════════════════════════════════════════════
// 核心内部逻辑
// ═══════════════════════════════════════════════════════════════

// fillPriceTracking 用 K 线数据填充卖出后 1/3/5 日价格
// FIX: 接收真实的卖出时间 sellTime，不再依赖 rev.CreatedAt
func (s *AuditService) fillPriceTracking(rev *model.TradeReview, kline *KLineResponse, sellTime time.Time) bool {
	if rev.PriceAtSell == nil {
		return false
	}
	sellPrice := *rev.PriceAtSell
	if sellPrice <= 0 {
		return false
	}

	// 用卖出时间匹配 K 线日期
	sellDateStr := sellTime.Format("2006-01-02")
	sellIdx := -1
	for i, k := range kline.KLines {
		if k.Date == sellDateStr {
			sellIdx = i
			break
		}
	}
	// 找不到精确日期：找最近的较早日期
	if sellIdx == -1 {
		for i := len(kline.KLines) - 1; i >= 0; i-- {
			if kline.KLines[i].Date <= sellDateStr {
				sellIdx = i
				break
			}
		}
	}
	if sellIdx == -1 || sellIdx+1 >= len(kline.KLines) {
		// K 线数据不足以追踪，等下次
		return false
	}

	bars := kline.KLines
	changed := false

	if rev.Price1dAfter == nil && sellIdx+1 < len(bars) {
		v := bars[sellIdx+1].Close
		rev.Price1dAfter = &v
		changed = true
	}
	if rev.Price3dAfter == nil && sellIdx+3 < len(bars) {
		v := bars[sellIdx+3].Close
		rev.Price3dAfter = &v
		changed = true
	}
	if rev.Price5dAfter == nil && sellIdx+5 < len(bars) {
		v := bars[sellIdx+5].Close
		changed = true
		rev.Price5dAfter = &v

		maxH := 0.0
		for i := sellIdx + 1; i <= sellIdx+5 && i < len(bars); i++ {
			if bars[i].High > maxH {
				maxH = bars[i].High
			}
		}
		rev.MaxPrice5d = &maxH

		regret := (maxH - sellPrice) / sellPrice
		rev.RegretIndex = &regret

		post5d := (bars[sellIdx+5].Close - sellPrice) / sellPrice
		rev.Post5dGainPct = &post5d

		rev.TrackingStatus = model.TrackingCompleted
	} else if changed && rev.Price5dAfter == nil {
		rev.TrackingStatus = model.TrackingPartial
	}

	return changed
}

// buildReviewFromSell 从 SELL 记录构建复盘草稿
func (s *AuditService) buildReviewFromSell(
	ctx context.Context,
	userID int64,
	sell *model.TradeLogV2,
) (*model.TradeReview, error) {
	rev := &model.TradeReview{
		TradeLogID:     sell.ID,
		StockCode:      sell.StockCode,
		TrackingStatus: model.TrackingPending,
		Tags:           model.StringArray{},
	}

	sellPrice := sell.Price
	rev.PriceAtSell = &sellPrice

	buy, err := s.tradeV2Repo.GetMatchedBuy(ctx, userID, sell.StockCode, sell.TradedAt)
	if err == nil && buy != nil {
		pnl := (sell.Price - buy.Price) / buy.Price
		rev.PnlPct = &pnl
		// FIX: 传入真实的卖出时间和买入记录
		s.runConsistencyAudit(ctx, rev, buy.BuyReason, sell.SellReason, buy, sell.TradedAt)
	}

	return rev, nil
}

// runConsistencyAudit 检测逻辑冲突
// FIX: 新增 sellTime 参数，持仓时间用 sellTime - buyLog.TradedAt 计算
func (s *AuditService) runConsistencyAudit(
	_ context.Context,
	rev *model.TradeReview,
	buyReason, sellReason string,
	buyLog *model.TradeLogV2,
	sellTime time.Time,
) {
	buy := strings.ToLower(buyReason)
	sell := strings.ToLower(sellReason)

	// 规则 0：追高检测（MA20 偏离度）
	if buyLog != nil && buyLog.Price > 0 {
		aggressiveWords := []string{"打板", "追涨", "龙头", "妖股", "强势", "连板", "首板", "二板"}
		isAggressive := false
		for _, kw := range aggressiveWords {
			if strings.Contains(buy, kw) {
				isAggressive = true
				break
			}
		}
		if !isAggressive {
			kline, err := s.stockSvc.GetKLineEndAt(rev.StockCode, buyLog.TradedAt, 30)
			if err == nil && len(kline.KLines) >= 20 {
				lastIdx := len(kline.KLines) - 1
				sum := 0.0
				for i := lastIdx; i > lastIdx-20 && i >= 0; i-- {
					sum += kline.KLines[i].Close
				}
				ma20 := sum / 20.0
				if ma20 > 0 {
					deviation := (buyLog.Price - ma20) / ma20 * 100
					if deviation > chasingHighPct {
						rev.ConsistencyFlag = model.ConsistencyChasingHigh
						rev.ConsistencyNote = fmt.Sprintf(
							"买入价偏离 MA20 %.1f%%（>%.1f%%），判定为追高。",
							deviation, chasingHighPct,
						)
						return
					}
				}
			}
		}
	}

	// 规则 1：买入理由含长线词，但持仓时间极短
	// FIX: 持仓天数 = 卖出时间 - 买入时间（不再用 rev.CreatedAt）
	holdDays := 0
	if buyLog != nil && !buyLog.TradedAt.IsZero() && !sellTime.IsZero() {
		holdDays = int(sellTime.Sub(buyLog.TradedAt).Hours() / 24)
	}

	for _, kw := range longTermKeywords {
		if strings.Contains(buy, kw) && holdDays > 0 && holdDays < shortTermMaxDays {
			rev.ConsistencyFlag = model.ConsistencyLogicConflict
			rev.ConsistencyNote = fmt.Sprintf(
				"买入理由含「%s」（长线逻辑），但实际持仓仅 %d 天，判定为策略行为不一致。",
				kw, holdDays,
			)
			return
		}
	}

	// 规则 2：盈利不足但卖出理由含目标词 → 过早止盈
	if rev.PnlPct != nil {
		pnlPct := *rev.PnlPct * 100
		for _, kw := range targetKeywords {
			if strings.Contains(sell, kw) && pnlPct > 0 && pnlPct < prematureExitPct {
				rev.ConsistencyFlag = model.ConsistencyPrematureExit
				rev.ConsistencyNote = fmt.Sprintf(
					"卖出理由含「%s」，但盈利仅 %.1f%%，止盈目标设置过保守。",
					kw, pnlPct,
				)
				return
			}
		}
	}

	// 规则 3：盈利时卖出理由含恐慌词 → 恐慌卖出
	panicWords := []string{"止损", "跌破", "恐慌", "割肉", "害怕", "跌太多"}
	for _, kw := range panicWords {
		if strings.Contains(sell, kw) && rev.PnlPct != nil && *rev.PnlPct > 0 {
			rev.ConsistencyFlag = model.ConsistencyPanicSell
			rev.ConsistencyNote = fmt.Sprintf(
				"卖出理由含「%s」且当时持仓盈利，判定为非理性恐慌性卖出。",
				kw,
			)
			return
		}
	}

	rev.ConsistencyFlag = model.ConsistencyNormal
	rev.ConsistencyNote = ""
}

// initFromTradeLogID 通过 trade_log_id 初始化复盘记录
func (s *AuditService) initFromTradeLogID(
	ctx context.Context, userID int64, tradeLogID int64,
) (*model.TradeReview, error) {
	// 回溯 1 年
	sells, err := s.tradeV2Repo.GetSellsInRange(
		ctx, userID, time.Now().AddDate(-1, 0, 0), time.Now(),
	)
	if err != nil {
		return nil, err
	}
	for _, sell := range sells {
		if sell.ID == tradeLogID {
			rev, err := s.buildReviewFromSell(ctx, userID, sell)
			if err != nil {
				return nil, err
			}
			if err := s.reviewRepo.Create(ctx, rev); err != nil {
				return nil, err
			}
			return rev, nil
		}
	}
	return nil, fmt.Errorf("trade_log_id=%d 不存在或非 SELL 记录", tradeLogID)
}

func (s *AuditService) toDetailDTO(_ context.Context, rev *model.TradeReview) *ReviewDetailDTO {
	return &ReviewDetailDTO{
		TradeReviewWithTrade: &repo.TradeReviewWithTrade{TradeReview: *rev},
		AIReady:              rev.AIAuditComment != "",
	}
}

// fetchSellAndBuy 精确查询指定卖出记录和对应的买入记录
func (s *AuditService) fetchSellAndBuy(
	ctx context.Context, userID int64, tradeLogID int64,
) (*model.TradeLogV2, *model.TradeLogV2, error) {
	// 回溯 1 年内的 SELL
	sells, err := s.tradeV2Repo.GetSellsInRange(
		ctx, userID, time.Now().AddDate(-1, 0, 0), time.Now(),
	)
	if err != nil {
		return nil, nil, err
	}
	for _, sell := range sells {
		if sell.ID == tradeLogID {
			buy, _ := s.tradeV2Repo.GetMatchedBuy(ctx, userID, sell.StockCode, sell.TradedAt)
			return sell, buy, nil
		}
	}
	return nil, nil, fmt.Errorf("trade_log_id=%d not found", tradeLogID)
}

// ═══════════════════════════════════════════════════════════════
// Prompt 工程
// ═══════════════════════════════════════════════════════════════

func buildAuditPrompt(item *repo.TradeReviewWithTrade) string {
	pnl := 0.0
	if item.PnlPct != nil {
		pnl = *item.PnlPct * 100
	}
	post5d := 0.0
	if item.Post5dGainPct != nil {
		post5d = *item.Post5dGainPct * 100
	}
	regret := 0.0
	if item.RegretIndex != nil {
		regret = *item.RegretIndex * 100
	}

	consistencyContext := ""
	if item.ConsistencyFlag != model.ConsistencyNormal {
		consistencyContext = fmt.Sprintf("\n系统检测到逻辑冲突：%s（%s）",
			item.ConsistencyFlag, item.ConsistencyNote)
	}
	mentalContext := ""
	if item.MentalState != "" {
		mentalContext = fmt.Sprintf("\n交易时情绪自评：%s", item.MentalState)
	}

	return fmt.Sprintf(`你是一个毒舌且理性的职业交易员，正在对一位散户的交易进行复盘审计。
你的风格是：直接、犀利、不安慰、只讲事实和逻辑。

=== 交易数据 ===
股票：%s（%s）
买入理由：%s
卖出理由：%s
本次盈亏：%.2f%%
卖出后 5 日涨幅：%.2f%%
卖出后 5 日最高偏离（后悔指数）：%.2f%%%s%s

=== 审计要求 ===
请按以下结构输出，每项必须基于数据说话：

## 🔍 卖出动机诊断
判断这次卖出是基于「逻辑」还是「恐惧/贪婪」？给出具体证据。

## 📉 机会成本分析
卖出后股价走势如何？是否存在"卖飞"？后悔指数 %.2f%% 说明了什么？

## 🧠 逻辑一致性
买入逻辑和卖出行为是否自洽？如果有矛盾，直接点出。

## ⚡ 执行力评分
给出 1-100 的执行力评分，并说明扣分原因。
格式必须包含：【执行力评分：XX分】

## 🛠 改进建议
给出 2-3 条具体可操作的改进建议（不要废话）。`,
		item.StockName, item.StockCode,
		emptyStr(item.BuyReason, "未填写"),
		emptyStr(item.SellReason, "未填写"),
		pnl, post5d, regret,
		consistencyContext, mentalContext,
		regret,
	)
}

// ═══════════════════════════════════════════════════════════════
// 工具函数
// ═══════════════════════════════════════════════════════════════

func buildEmotionMatrix(stats []*repo.MentalStateStat) []*EmotionCell {
	cells := make([]*EmotionCell, 0, len(stats))
	for _, s := range stats {
		lossRate := 100 - s.WinRate
		avgLoss := 0.0
		if s.AvgPnlPct < 0 {
			avgLoss = math.Abs(s.AvgPnlPct)
		}
		riskScore := lossRate*0.6 + avgLoss*0.4
		cells = append(cells, &EmotionCell{
			Emotion:   s.MentalState,
			Count:     s.Count,
			AvgPnl:    s.AvgPnlPct,
			WinRate:   s.WinRate,
			RiskScore: math.Round(riskScore*10) / 10,
		})
	}
	sort.Slice(cells, func(i, j int) bool {
		return cells[i].RiskScore > cells[j].RiskScore
	})
	return cells
}

func extractExecutionScore(text string) int {
	for _, marker := range []string{"执行力评分：", "执行力评分:"} {
		idx := strings.Index(text, marker)
		if idx == -1 {
			continue
		}
		rest := text[idx+len(marker):]
		score := 0
		for _, ch := range rest {
			if ch >= '0' && ch <= '9' {
				score = score*10 + int(ch-'0')
			} else {
				break
			}
		}
		if score >= 1 && score <= 100 {
			return score
		}
	}
	return 0
}

func extractImprovementPlan(text string) string {
	for _, marker := range []string{"## 🛠 改进建议", "改进建议"} {
		idx := strings.Index(text, marker)
		if idx == -1 {
			continue
		}
		plan := strings.TrimSpace(text[idx+len(marker):])
		if next := strings.Index(plan, "## "); next > 0 {
			plan = plan[:next]
		}
		return strings.TrimSpace(plan)
	}
	return ""
}

func emptyStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
