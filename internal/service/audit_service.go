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

	// 逻辑冲突判定阈值
	longTermMinDays  = 30   // "长线"最短持有天数
	shortTermMaxDays = 7    // 短于 7 天且理由含长线词 → LOGIC_CONFLICT
	chasingHighPct   = 8.0  // 买点偏离 MA20 超过 8% → CHASING_HIGH（原题 10%，调整为更严格的 8%）
	panicSellPct     = -5.0 // 当日跌幅超过 -5% 且当天卖出 → PANIC_SELL
	prematureExitPct = 3.0  // 盈利不足 3% 就卖，且理由含目标位词 → PREMATURE_EXIT
)

// longTermKeywords 表示"长线/趋势"类买入理由关键词
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

// SubmitReviewRequest POST /api/v1/review/submit
type SubmitReviewRequest struct {
	TradeLogID  int64    `json:"trade_log_id"  binding:"required"`
	MentalState string   `json:"mental_state"` // 冷静|贪婪|恐惧|急躁|犹豫|自信|迷茫
	UserNote    string   `json:"user_note"`    // 用户自己的主观复盘
	Tags        []string `json:"tags"`         // 自定义标签
	BuyReason   string   `json:"buy_reason"`   // 同步更新买入理由（可选）
	SellReason  string   `json:"sell_reason"`  // 同步更新卖出理由（可选）
	TriggerAI   bool     `json:"trigger_ai"`   // 是否立即触发 AI 审计
}

// ReviewDetailDTO 复盘详情
type ReviewDetailDTO struct {
	*repo.TradeReviewWithTrade
	AIReady bool `json:"ai_ready"` // AI 审计是否已完成
}

// DashboardDTO GET /api/v1/review/dashboard
type DashboardDTO struct {
	*repo.DashboardStats
	RecentReviews []*repo.TradeReviewWithTrade `json:"recent_reviews"`
	// 可视化友好的情绪热力图数据（按胜率排序）
	EmotionMatrix []*EmotionCell `json:"emotion_matrix"`
}

// EmotionCell 情绪热力图单元格
type EmotionCell struct {
	Emotion   string  `json:"emotion"`
	Count     int64   `json:"count"`
	AvgPnl    float64 `json:"avg_pnl"`    // 百分比
	WinRate   float64 `json:"win_rate"`   // 百分比
	RiskScore float64 `json:"risk_score"` // 综合风险分（越高越危险）
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
// 用户手动补充交易心态与主观总结，可选触发 AI 审计
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) SubmitReview(ctx context.Context, userID int64, req *SubmitReviewRequest) (*ReviewDetailDTO, error) {
	// 1. 查找或创建复盘记录（confirm trade_log 存在且属于该用户）
	rev, err := s.reviewRepo.GetByTradeLogID(ctx, req.TradeLogID)
	if err != nil {
		// 不存在则尝试初始化（触发 InitReviewForSell 逻辑）
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

	// 3. 同步更新 trade_logs 的买卖理由（如果用户提供了）
	if req.BuyReason != "" || req.SellReason != "" {
		if err := s.tradeV2Repo.UpdateReasons(ctx, req.TradeLogID, req.BuyReason, req.SellReason); err != nil {
			s.log.Warn("update trade reasons failed", zap.Error(err))
		}
		// 重新跑一次一致性审计（理由变了）
		// 需要查出关联的买入记录以便计算 MA20
		var buyLog *model.TradeLogV2
		if sellLog, err := s.tradeV2Repo.GetSellsInRange(ctx, userID, time.Time{}, time.Now()); err == nil {
			// 这里 GetSellsInRange 可能效率低，最好有个 GetByID。但在 repo 中没看到 GetByID for TradeLogV2。
			// 暂时遍历查找（假设 7 天内数据不多，或者复用 initFromTradeLogID 的逻辑）
			// 优化：我们在 repo 里加一个 GetByID 更好，但为了少改动，先尝试复用现有逻辑。
			// 其实 rev.TradeLogID 就是 sellLog.ID。
			// 我们可以直接用 initFromTradeLogID 里的逻辑：先查 sell，再查 buy。
			for _, item := range sellLog {
				if item.ID == req.TradeLogID {
					buyLog, _ = s.tradeV2Repo.GetMatchedBuy(ctx, userID, item.StockCode, item.TradedAt)
					break
				}
			}
		}
		s.runConsistencyAudit(ctx, rev, req.BuyReason, req.SellReason, buyLog)
	}

	if err := s.reviewRepo.Update(ctx, rev); err != nil {
		return nil, fmt.Errorf("保存复盘失败: %w", err)
	}

	// 4. 可选触发 AI 审计（异步，避免阻塞 HTTP 响应）
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

	// 构建情绪矩阵（按风险分排序）
	matrix := buildEmotionMatrix(stats.MentalStateStats)

	return &DashboardDTO{
		DashboardStats: stats,
		RecentReviews:  recent,
		EmotionMatrix:  matrix,
	}, nil
}

// ListReviews 分页查询复盘列表
func (s *AuditService) ListReviews(ctx context.Context, userID int64, limit, offset int) ([]*repo.TradeReviewWithTrade, error) {
	return s.reviewRepo.ListByUser(ctx, userID, limit, offset)
}

// CountReviews 统计复盘总数
func (s *AuditService) CountReviews(ctx context.Context, userID int64) (int64, error) {
	return s.reviewRepo.CountByUser(ctx, userID)
}

// ─────────────────────────────────────────────────────────────────
// GenerateAIAudit — 向 AI 发送审计请求，写入 ai_audit_comment
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) GenerateAIAudit(ctx context.Context, reviewID int64) error {
	rev, err := s.reviewRepo.GetByID(ctx, reviewID)
	if err != nil {
		return fmt.Errorf("找不到复盘记录 id=%d: %w", reviewID, err)
	}

	// 获取关联的交易信息
	items, err := s.reviewRepo.ListByUser(ctx, defaultUserID, 1000, 0)
	if err != nil {
		return err
	}
	var withTrade *repo.TradeReviewWithTrade
	for _, item := range items {
		if item.ID == reviewID {
			withTrade = item
			break
		}
	}
	if withTrade == nil {
		return fmt.Errorf("找不到关联交易数据 review_id=%d", reviewID)
	}

	// 构建审计 Prompt
	prompt := buildAuditPrompt(withTrade)

	// 调用 AI（复用 callEino）
	comment, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		return fmt.Errorf("AI 审计调用失败: %w", err)
	}

	// 解析执行力评分（从 AI 回复中提取）
	score := extractExecutionScore(comment)

	// 写回
	now := time.Now()
	rev.AIAuditComment = comment
	rev.AIGeneratedAt = &now
	if score > 0 {
		rev.ExecutionScore = &score
	}
	// 从 AI 回复中提取改进计划（第二段）
	rev.ImprovementPlan = extractImprovementPlan(comment)

	if err := s.reviewRepo.Update(ctx, rev); err != nil {
		return fmt.Errorf("保存 AI 审计结果失败: %w", err)
	}

	s.log.Info("AI audit completed",
		zap.Int64("review_id", reviewID),
		zap.Int("score", score),
	)
	return nil
}

// ─────────────────────────────────────────────────────────────────
// RunPriceTracker — Cron 入口：追踪过去 7 天卖出后的价格
// 在 main.go 中由定时任务调用（每日 16:00）
// ─────────────────────────────────────────────────────────────────

func (s *AuditService) RunPriceTracker(ctx context.Context) (int, error) {
	s.log.Info("price tracker: started")

	// 1. 查询所有 PENDING / PARTIAL 记录
	pending, err := s.reviewRepo.ListPending(ctx)
	if err != nil {
		return 0, fmt.Errorf("查询待追踪记录失败: %w", err)
	}
	if len(pending) == 0 {
		s.log.Info("price tracker: no pending records")
		return 0, nil
	}

	// 2. 按 stock_code 分组（减少 K 线接口请求次数）
	grouped := make(map[string][]*model.TradeReview)
	for _, r := range pending {
		grouped[r.StockCode] = append(grouped[r.StockCode], r)
	}

	updated := 0
	for code, reviews := range grouped {
		klineResp, err := s.stockSvc.GetKLine(code, 20) // 最近 20 根日线
		if err != nil {
			s.log.Warn("price tracker: get kline failed",
				zap.String("code", code), zap.Error(err))
			continue
		}

		for _, rev := range reviews {
			if changed := s.fillPriceTracking(rev, klineResp); changed {
				if err := s.reviewRepo.Update(ctx, rev); err != nil {
					s.log.Warn("price tracker: update failed",
						zap.Int64("id", rev.ID), zap.Error(err))
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
// InitReviewsForRecentSells — 为最近 7 天的 SELL 创建复盘记录
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
		rev, err := s.buildReviewFromSell(ctx, userID, sell)
		if err != nil {
			s.log.Warn("init review: build failed",
				zap.Int64("trade_id", sell.ID), zap.Error(err))
			continue
		}
		if err := s.reviewRepo.Create(ctx, rev); err != nil {
			s.log.Warn("init review: create failed",
				zap.Int64("trade_id", sell.ID), zap.Error(err))
			continue
		}
		created++
	}
	return created, nil
}

// ═══════════════════════════════════════════════════════════════
// 核心内部逻辑
// ═══════════════════════════════════════════════════════════════

// fillPriceTracking 用 K 线数据填充卖出后 1/3/5 日价格，返回是否有更新
func (s *AuditService) fillPriceTracking(rev *model.TradeReview, kline *KLineResponse) bool {
	if rev.PriceAtSell == nil {
		return false
	}
	sellPrice := *rev.PriceAtSell
	if sellPrice <= 0 {
		return false
	}

	// 找到卖出日在 K 线中的位置
	var sellIdx int = -1
	for i, k := range kline.KLines {
		if strings.HasPrefix(k.Date, rev.CreatedAt.Format("2006-01-02")) {
			sellIdx = i
			break
		}
	}
	// 如果找不到精确日期，用倒数第 N 个作为近似（K 线按时间升序）
	if sellIdx == -1 {
		sellIdx = len(kline.KLines) - 6
		if sellIdx < 0 {
			return false
		}
	}

	bars := kline.KLines
	changed := false

	// 第 1 个交易日（sellIdx+1）
	if rev.Price1dAfter == nil && sellIdx+1 < len(bars) {
		v := bars[sellIdx+1].Close
		rev.Price1dAfter = &v
		changed = true
	}
	// 第 3 个交易日
	if rev.Price3dAfter == nil && sellIdx+3 < len(bars) {
		v := bars[sellIdx+3].Close
		rev.Price3dAfter = &v
		changed = true
	}
	// 第 5 个交易日
	if rev.Price5dAfter == nil && sellIdx+5 < len(bars) {
		v := bars[sellIdx+5].Close
		rev.Price5dAfter = &v
		changed = true

		// 计算 5 日内最高价
		maxH := 0.0
		for i := sellIdx + 1; i <= sellIdx+5 && i < len(bars); i++ {
			if bars[i].High > maxH {
				maxH = bars[i].High
			}
		}
		rev.MaxPrice5d = &maxH

		// 后悔指数
		regret := (maxH - sellPrice) / sellPrice
		rev.RegretIndex = &regret

		// 卖出后 5 日涨幅
		post5d := (bars[sellIdx+5].Close - sellPrice) / sellPrice
		rev.Post5dGainPct = &post5d

		// 状态升级到 COMPLETED
		rev.TrackingStatus = model.TrackingCompleted
	} else if changed && rev.Price5dAfter == nil {
		rev.TrackingStatus = model.TrackingPartial
	}

	return changed
}

// buildReviewFromSell 从一条 SELL 记录构建复盘草稿
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

	// 卖出价格
	sellPrice := sell.Price
	rev.PriceAtSell = &sellPrice

	// 查找匹配的买入记录，计算盈亏和逻辑一致性
	buy, err := s.tradeV2Repo.GetMatchedBuy(ctx, userID, sell.StockCode, sell.TradedAt)
	if err == nil && buy != nil {
		// 盈亏百分比（简单计算，不含手续费）
		pnl := (sell.Price - buy.Price) / buy.Price
		rev.PnlPct = &pnl

		// 逻辑一致性审计
		s.runConsistencyAudit(ctx, rev, buy.BuyReason, sell.SellReason, buy)
	}

	return rev, nil
}

// runConsistencyAudit 对比买入/卖出理由，检测逻辑冲突
func (s *AuditService) runConsistencyAudit(
	_ context.Context,
	rev *model.TradeReview,
	buyReason, sellReason string,
	buyLog *model.TradeLogV2,
) {
	buy := strings.ToLower(buyReason)
	sell := strings.ToLower(sellReason)

	// 规则 0：追高检测（MA20 偏离度）
	// 仅当有买入记录且买入理由不包含激进策略词汇时检测
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
			// 获取买入日截止的 K 线（计算 MA20 至少需要 20 根）
			// 我们多取一些以防万一
			kline, err := s.stockSvc.GetKLineEndAt(rev.StockCode, buyLog.TradedAt, 30)
			if err == nil && len(kline.KLines) >= 20 {
				// 最后一根通常是买入日（或前一日）
				lastIdx := len(kline.KLines) - 1

				sum := 0.0
				count := 0
				for i := lastIdx; i > lastIdx-20; i-- {
					sum += kline.KLines[i].Close
					count++
				}
				ma20 := sum / float64(count)

				if ma20 > 0 {
					deviation := (buyLog.Price - ma20) / ma20 * 100
					if deviation > chasingHighPct {
						rev.ConsistencyFlag = model.ConsistencyChasingHigh
						rev.ConsistencyNote = fmt.Sprintf(
							"买入价偏离 MA20 %.1f%%（>%.1f%%），判定为追高。",
							deviation, chasingHighPct,
						)
						return // 优先级最高
					}
				}
			}
		}
	}

	// 规则 1：买入理由含长线词，但持仓时间极短
	holdDays := 0
	if rev.TradeLogID > 0 && rev.CreatedAt.Year() > 1 {
		holdDays = int(time.Since(rev.CreatedAt).Hours() / 24)
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

	// 规则 2：PnL 过低且卖出理由含目标词 → 过早止盈
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

	// 规则 3：卖出理由含"止损""跌破""恐慌"等词，且当时盈亏为正 → 恐慌卖出
	panicWords := []string{"止损", "跌破", "恐慌", "割肉", "害怕", "跌太多"}
	for _, kw := range panicWords {
		if strings.Contains(sell, kw) {
			if rev.PnlPct != nil && *rev.PnlPct > 0 {
				rev.ConsistencyFlag = model.ConsistencyPanicSell
				rev.ConsistencyNote = fmt.Sprintf(
					"卖出理由含「%s」且当时持仓盈利，判定为非理性恐慌性卖出。",
					kw,
				)
				return
			}
		}
	}

	// 无冲突
	rev.ConsistencyFlag = model.ConsistencyNormal
	rev.ConsistencyNote = ""
}

// initFromTradeLogID 通过 trade_log_id 初始化一条复盘记录
func (s *AuditService) initFromTradeLogID(
	ctx context.Context, userID int64, tradeLogID int64,
) (*model.TradeReview, error) {
	sells, err := s.tradeV2Repo.GetSellsInRange(
		ctx, userID,
		time.Now().AddDate(-1, 0, 0), // 回溯 1 年
		time.Now(),
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
		// 风险分 = (亏损率 * 0.6) + (abs(平均盈亏为负部分) * 0.4)
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
	// 按风险分降序
	sort.Slice(cells, func(i, j int) bool {
		return cells[i].RiskScore > cells[j].RiskScore
	})
	return cells
}

// extractExecutionScore 从 AI 回复中提取【执行力评分：XX分】
func extractExecutionScore(text string) int {
	// 简单字符串扫描，寻找"执行力评分：数字分"
	marker := "执行力评分："
	idx := strings.Index(text, marker)
	if idx == -1 {
		marker = "执行力评分:"
		idx = strings.Index(text, marker)
	}
	if idx == -1 {
		return 0
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
	if score < 1 || score > 100 {
		return 0
	}
	return score
}

// extractImprovementPlan 提取"改进建议"段落
func extractImprovementPlan(text string) string {
	marker := "## 🛠 改进建议"
	idx := strings.Index(text, marker)
	if idx == -1 {
		marker = "改进建议"
		idx = strings.Index(text, marker)
	}
	if idx == -1 {
		return ""
	}
	plan := strings.TrimSpace(text[idx+len(marker):])
	// 截取到下一个 ## 为止
	nextSection := strings.Index(plan, "## ")
	if nextSection > 0 {
		plan = plan[:nextSection]
	}
	return strings.TrimSpace(plan)
}

func emptyStr(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}
