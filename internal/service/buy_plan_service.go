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
// DTO
// ═══════════════════════════════════════════════════════════════

type BuyPlanDTO struct {
	model.BuyPlan
	CurrentPrice *float64 `json:"current_price,omitempty"`
	DistToBuy    *float64 `json:"dist_to_buy_pct,omitempty"`
	DistToTarget *float64 `json:"dist_to_target_pct,omitempty"`
	TriggerHit   bool     `json:"trigger_hit"`
	RRCalc       *float64 `json:"rr_calc,omitempty"`
}

type CreateBuyPlanRequest struct {
	StockCode         string                  `json:"stock_code"     binding:"required"`
	BuyPrice          *float64                `json:"buy_price"`
	BuyPriceHigh      *float64                `json:"buy_price_high"`
	TargetPrice       *float64                `json:"target_price"`
	StopLossPrice     *float64                `json:"stop_loss_price"`
	PlannedVolume     int                     `json:"planned_volume"`
	PlannedAmount     *float64                `json:"planned_amount"`
	PositionRatio     *float64                `json:"position_ratio"`
	BuyBatches        int                     `json:"buy_batches"`
	Reason            string                  `json:"reason"`
	Catalyst          string                  `json:"catalyst"`
	TriggerConditions model.TriggerConditions `json:"trigger_conditions"`
	ValidUntil        *string                 `json:"valid_until"`
}

type UpdateBuyPlanRequest struct {
	BuyPrice          *float64                 `json:"buy_price"`
	BuyPriceHigh      *float64                 `json:"buy_price_high"`
	TargetPrice       *float64                 `json:"target_price"`
	StopLossPrice     *float64                 `json:"stop_loss_price"`
	PlannedVolume     *int                     `json:"planned_volume"`
	PlannedAmount     *float64                 `json:"planned_amount"`
	PositionRatio     *float64                 `json:"position_ratio"`
	BuyBatches        *int                     `json:"buy_batches"`
	Reason            *string                  `json:"reason"`
	Catalyst          *string                  `json:"catalyst"`
	TriggerConditions *model.TriggerConditions `json:"trigger_conditions"`
	Status            *string                  `json:"status"`
	ValidUntil        *string                  `json:"valid_until"`
}

// ═══════════════════════════════════════════════════════════════
// BuyPlanService
// ═══════════════════════════════════════════════════════════════

type BuyPlanService struct {
	repo        repo.BuyPlanRepo
	stockSvc    *StockService
	market      *MarketProvider
	guardianSvc *PositionGuardianService // 执行计划时自动关联持仓（延迟注入避免循环依赖）
	log         *zap.Logger
}

func NewBuyPlanService(r repo.BuyPlanRepo, stockSvc *StockService, log *zap.Logger) *BuyPlanService {
	return &BuyPlanService{repo: r, stockSvc: stockSvc, market: stockSvc.market, log: log}
}

type SmartPlanBacktestRequest struct {
	StockCode     string  `json:"stock_code" binding:"required"`
	BuyPrice      float64 `json:"buy_price" binding:"required"`
	BuyPriceHigh  float64 `json:"buy_price_high" binding:"required"`
	StopLoss      float64 `json:"stop_loss" binding:"required"`
	TargetPrice   float64 `json:"target_price" binding:"required"`
	LookaheadDays int     `json:"lookahead_days"`
	SampleDays    int     `json:"sample_days"`
}

type SmartPlanBacktestResult struct {
	StockCode        string  `json:"stock_code"`
	SampleDays       int     `json:"sample_days"`
	LookaheadDays    int     `json:"lookahead_days"`
	TotalSamples     int     `json:"total_samples"`
	TriggeredSamples int     `json:"triggered_samples"`
	WinSamples       int     `json:"win_samples"`
	LossSamples      int     `json:"loss_samples"`
	TimeoutSamples   int     `json:"timeout_samples"`
	TriggerRatePct   float64 `json:"trigger_rate_pct"`
	WinRatePct       float64 `json:"win_rate_pct"`
	AvgReturnPct     float64 `json:"avg_return_pct"`
	MedianReturnPct  float64 `json:"median_return_pct"`
	AvgHoldDays      float64 `json:"avg_hold_days"`
	ProfitFactor     float64 `json:"profit_factor"`
}

// SetGuardianSvc 延迟注入持仓守护服务（在 router 里 new 完两个服务后调用）
func (s *BuyPlanService) SetGuardianSvc(gs *PositionGuardianService) {
	s.guardianSvc = gs
}

// Create — 创建买入计划
func (s *BuyPlanService) Create(ctx context.Context, userID int64, req *CreateBuyPlanRequest) (*BuyPlanDTO, error) {
	code := strings.ToUpper(strings.TrimSpace(req.StockCode))
	if len(code) != 6 {
		return nil, fmt.Errorf("stock_code 格式错误（应为 6 位数字）")
	}
	if err := validatePrices(req.BuyPrice, req.TargetPrice, req.StopLossPrice); err != nil {
		return nil, err
	}

	stockName := code
	if q, err := s.market.FetchRealtimeQuote(code); err == nil {
		stockName = q.Name
	}

	plan := &model.BuyPlan{
		UserID:            userID,
		StockCode:         code,
		StockName:         stockName,
		BuyPrice:          req.BuyPrice,
		BuyPriceHigh:      req.BuyPriceHigh,
		TargetPrice:       req.TargetPrice,
		StopLossPrice:     req.StopLossPrice,
		PlannedVolume:     req.PlannedVolume,
		PlannedAmount:     req.PlannedAmount,
		PositionRatio:     req.PositionRatio,
		BuyBatches:        maxInt(1, req.BuyBatches),
		Reason:            strings.TrimSpace(req.Reason),
		Catalyst:          strings.TrimSpace(req.Catalyst),
		TriggerConditions: req.TriggerConditions,
		Status:            model.BuyPlanStatusWatching,
	}

	if rr := calcRR(req.BuyPrice, req.TargetPrice, req.StopLossPrice); rr != nil {
		plan.RiskRewardRatio = rr
		if req.BuyPrice != nil && req.TargetPrice != nil {
			pct := (*req.TargetPrice - *req.BuyPrice) / *req.BuyPrice * 100
			plan.ExpectedReturnPct = &pct
		}
	}

	if req.ValidUntil != nil {
		t, err := time.Parse("2006-01-02", *req.ValidUntil)
		if err != nil {
			return nil, fmt.Errorf("valid_until 格式错误（应为 YYYY-MM-DD）: %w", err)
		}
		plan.ValidUntil = &t
	}

	if err := s.repo.Create(ctx, plan); err != nil {
		return nil, fmt.Errorf("创建买入计划失败: %w", err)
	}
	s.log.Info("buy plan created", zap.String("code", code), zap.Int64("id", plan.ID))
	return s.enrichDTO(plan, nil), nil
}

// List — 列表
func (s *BuyPlanService) List(ctx context.Context, userID int64, statusFilter string) ([]*BuyPlanDTO, error) {
	var statuses []model.BuyPlanStatus
	switch statusFilter {
	case "active":
		statuses = []model.BuyPlanStatus{model.BuyPlanStatusWatching, model.BuyPlanStatusReady}
	case "done":
		statuses = []model.BuyPlanStatus{model.BuyPlanStatusExecuted, model.BuyPlanStatusAbandoned, model.BuyPlanStatusExpired}
	case "watching":
		statuses = []model.BuyPlanStatus{model.BuyPlanStatusWatching}
	case "ready":
		statuses = []model.BuyPlanStatus{model.BuyPlanStatusReady}
	case "executed":
		statuses = []model.BuyPlanStatus{model.BuyPlanStatusExecuted}
	}

	plans, err := s.repo.ListByUser(ctx, userID, statuses)
	if err != nil {
		return nil, fmt.Errorf("查询买入计划失败: %w", err)
	}
	if len(plans) == 0 {
		return []*BuyPlanDTO{}, nil
	}

	activeCodes := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.Status == model.BuyPlanStatusWatching || p.Status == model.BuyPlanStatusReady {
			activeCodes = append(activeCodes, p.StockCode)
		}
	}
	quotes, _ := s.market.FetchMultipleQuotes(activeCodes)

	dtos := make([]*BuyPlanDTO, 0, len(plans))
	for _, p := range plans {
		dtos = append(dtos, s.enrichDTO(p, quotes[p.StockCode]))
	}
	return dtos, nil
}

// ListByCode — 按股票查询
func (s *BuyPlanService) ListByCode(ctx context.Context, userID int64, code string) ([]*BuyPlanDTO, error) {
	plans, err := s.repo.ListByCode(ctx, userID, strings.ToUpper(code))
	if err != nil {
		return nil, fmt.Errorf("查询买入计划失败: %w", err)
	}
	if len(plans) == 0 {
		return []*BuyPlanDTO{}, nil
	}

	var q *Quote
	if qq, err := s.market.FetchRealtimeQuote(strings.ToUpper(code)); err == nil {
		q = qq
	}

	dtos := make([]*BuyPlanDTO, 0, len(plans))
	for _, p := range plans {
		dtos = append(dtos, s.enrichDTO(p, q))
	}
	return dtos, nil
}

// Update — 更新计划字段
func (s *BuyPlanService) Update(ctx context.Context, userID int64, id int64, req *UpdateBuyPlanRequest) (*BuyPlanDTO, error) {
	plan, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("计划不存在: %w", err)
	}
	if plan.UserID != userID {
		return nil, fmt.Errorf("无权限操作此计划")
	}

	if req.BuyPrice != nil {
		plan.BuyPrice = req.BuyPrice
	}
	if req.BuyPriceHigh != nil {
		plan.BuyPriceHigh = req.BuyPriceHigh
	}
	if req.TargetPrice != nil {
		plan.TargetPrice = req.TargetPrice
	}
	if req.StopLossPrice != nil {
		plan.StopLossPrice = req.StopLossPrice
	}
	if req.PlannedVolume != nil {
		plan.PlannedVolume = *req.PlannedVolume
	}
	if req.PlannedAmount != nil {
		plan.PlannedAmount = req.PlannedAmount
	}
	if req.PositionRatio != nil {
		plan.PositionRatio = req.PositionRatio
	}
	if req.BuyBatches != nil {
		plan.BuyBatches = maxInt(1, *req.BuyBatches)
	}
	if req.Reason != nil {
		plan.Reason = strings.TrimSpace(*req.Reason)
	}
	if req.Catalyst != nil {
		plan.Catalyst = strings.TrimSpace(*req.Catalyst)
	}
	if req.TriggerConditions != nil {
		plan.TriggerConditions = *req.TriggerConditions
	}
	if req.Status != nil {
		plan.Status = model.BuyPlanStatus(*req.Status)
		if plan.Status == model.BuyPlanStatusExecuted {
			now := time.Now()
			plan.ExecutedAt = &now
		}
	}
	if req.ValidUntil != nil {
		t, err := time.Parse("2006-01-02", *req.ValidUntil)
		if err != nil {
			return nil, fmt.Errorf("valid_until 格式错误: %w", err)
		}
		plan.ValidUntil = &t
	}

	if rr := calcRR(plan.BuyPrice, plan.TargetPrice, plan.StopLossPrice); rr != nil {
		plan.RiskRewardRatio = rr
		if plan.BuyPrice != nil && plan.TargetPrice != nil {
			pct := (*plan.TargetPrice - *plan.BuyPrice) / *plan.BuyPrice * 100
			plan.ExpectedReturnPct = &pct
		}
	}

	if err := s.repo.Update(ctx, plan); err != nil {
		return nil, fmt.Errorf("更新买入计划失败: %w", err)
	}
	return s.enrichDTO(plan, nil), nil
}

// NOTE: UpdateStatus 定义在 buy_plan_update_status.go

// Delete — 删除计划
func (s *BuyPlanService) Delete(ctx context.Context, userID, id int64) error {
	plan, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("计划不存在")
	}
	if plan.UserID != userID {
		return fmt.Errorf("无权限操作此计划")
	}
	return s.repo.Delete(ctx, id)
}

// CheckTriggers — 扫描 WATCHING 计划，价格到达时升级为 READY
func (s *BuyPlanService) CheckTriggers(ctx context.Context, userID int64) ([]*BuyPlanDTO, error) {
	plans, err := s.repo.ListByUser(ctx, userID, []model.BuyPlanStatus{model.BuyPlanStatusWatching})
	if err != nil {
		return nil, err
	}
	if len(plans) == 0 {
		return []*BuyPlanDTO{}, nil
	}

	codes := make([]string, len(plans))
	for i, p := range plans {
		codes[i] = p.StockCode
	}
	quotes, _ := s.market.FetchMultipleQuotes(codes)

	triggered := make([]*BuyPlanDTO, 0)
	for _, p := range plans {
		q := quotes[p.StockCode]
		if q == nil || !isPriceTriggered(p, q.Price) {
			continue
		}
		_ = s.repo.UpdateStatus(ctx, p.ID, model.BuyPlanStatusReady)
		p.Status = model.BuyPlanStatusReady
		dto := s.enrichDTO(p, q)
		dto.TriggerHit = true
		triggered = append(triggered, dto)
		s.log.Info("buy plan triggered",
			zap.String("code", p.StockCode),
			zap.Int64("plan_id", p.ID),
			zap.Float64("price", q.Price),
		)
	}
	return triggered, nil
}

// BacktestSmartPlan 使用腾讯日K回测给定建仓参数的历史表现。
func (s *BuyPlanService) BacktestSmartPlan(ctx context.Context, req *SmartPlanBacktestRequest) (*SmartPlanBacktestResult, error) {
	code := strings.ToUpper(strings.TrimSpace(req.StockCode))
	if len(code) != 6 {
		return nil, fmt.Errorf("stock_code 格式错误（应为 6 位数字）")
	}
	if req.BuyPrice <= 0 || req.BuyPriceHigh <= 0 || req.StopLoss <= 0 || req.TargetPrice <= 0 {
		return nil, fmt.Errorf("回测参数必须为正数")
	}
	if req.BuyPriceHigh < req.BuyPrice {
		return nil, fmt.Errorf("buy_price_high 必须大于等于 buy_price")
	}
	if req.StopLoss >= req.BuyPrice {
		return nil, fmt.Errorf("stop_loss 必须低于 buy_price")
	}
	if req.TargetPrice <= req.BuyPrice {
		return nil, fmt.Errorf("target_price 必须高于 buy_price")
	}

	lookahead := req.LookaheadDays
	if lookahead <= 0 || lookahead > 60 {
		lookahead = 20
	}
	sampleDays := req.SampleDays
	if sampleDays <= 0 || sampleDays > 360 {
		sampleDays = 120
	}

	needBars := sampleDays + lookahead + 30
	klineResp, err := s.stockSvc.GetKLine(code, needBars)
	if err != nil {
		return nil, fmt.Errorf("获取腾讯K线失败: %w", err)
	}
	klines := klineResp.KLines
	if len(klines) < lookahead+30 {
		return nil, fmt.Errorf("K线样本不足，至少需要 %d 根，当前 %d", lookahead+30, len(klines))
	}

	start := 30
	end := len(klines) - lookahead
	if end <= start {
		return nil, fmt.Errorf("有效回测区间不足")
	}
	if max := start + sampleDays; max < end {
		start = end - sampleDays
	}

	total := 0
	triggered := 0
	win := 0
	loss := 0
	timeout := 0
	retSum := 0.0
	holdSum := 0.0
	profitSum := 0.0
	lossSum := 0.0
	returns := make([]float64, 0, end-start)

	for i := start; i < end; i++ {
		total++
		bar := klines[i]
		if !isPlanTriggeredInBar(bar, req.BuyPrice, req.BuyPriceHigh) {
			continue
		}
		triggered++

		outcomeRet := 0.0
		outcomeHold := float64(lookahead)
		outcomeType := "timeout"

		for j := i + 1; j <= i+lookahead && j < len(klines); j++ {
			next := klines[j]
			if next.Low <= req.StopLoss {
				outcomeRet = (req.StopLoss - req.BuyPrice) / req.BuyPrice * 100
				outcomeHold = float64(j - i)
				outcomeType = "loss"
				break
			}
			if next.High >= req.TargetPrice {
				outcomeRet = (req.TargetPrice - req.BuyPrice) / req.BuyPrice * 100
				outcomeHold = float64(j - i)
				outcomeType = "win"
				break
			}
		}

		if outcomeType == "timeout" {
			last := klines[minInt(i+lookahead, len(klines)-1)]
			outcomeRet = (last.Close - req.BuyPrice) / req.BuyPrice * 100
		}

		retSum += outcomeRet
		holdSum += outcomeHold
		returns = append(returns, outcomeRet)
		if outcomeRet > 0 {
			profitSum += outcomeRet
		} else if outcomeRet < 0 {
			lossSum += -outcomeRet
		}

		switch outcomeType {
		case "win":
			win++
		case "loss":
			loss++
		default:
			timeout++
		}
	}

	res := &SmartPlanBacktestResult{
		StockCode:        code,
		SampleDays:       sampleDays,
		LookaheadDays:    lookahead,
		TotalSamples:     total,
		TriggeredSamples: triggered,
		WinSamples:       win,
		LossSamples:      loss,
		TimeoutSamples:   timeout,
	}

	if total > 0 {
		res.TriggerRatePct = round2(float64(triggered) / float64(total) * 100)
	}
	if triggered > 0 {
		res.WinRatePct = round2(float64(win) / float64(triggered) * 100)
		res.AvgReturnPct = round2(retSum / float64(triggered))
		res.AvgHoldDays = round2(holdSum / float64(triggered))
		res.MedianReturnPct = round2(calcMedian(returns))
		if lossSum > 0 {
			res.ProfitFactor = round2(profitSum / lossSum)
		}
	}
	return res, nil
}

// ═══════════════════════════════════════════════════════════════
// 内部工具
// ═══════════════════════════════════════════════════════════════

func (s *BuyPlanService) enrichDTO(p *model.BuyPlan, q *Quote) *BuyPlanDTO {
	dto := &BuyPlanDTO{BuyPlan: *p}
	if q == nil {
		return dto
	}
	dto.CurrentPrice = &q.Price
	if p.BuyPrice != nil {
		dist := (q.Price - *p.BuyPrice) / *p.BuyPrice * 100
		dto.DistToBuy = &dist
		dto.TriggerHit = isPriceTriggered(p, q.Price)
	}
	if p.TargetPrice != nil {
		dist := (*p.TargetPrice - q.Price) / q.Price * 100
		dto.DistToTarget = &dist
	}
	if p.TargetPrice != nil && p.StopLossPrice != nil {
		dto.RRCalc = calcRRFromPrice(q.Price, *p.TargetPrice, *p.StopLossPrice)
	}
	return dto
}

func isPriceTriggered(p *model.BuyPlan, currentPrice float64) bool {
	if p.BuyPrice == nil {
		return false
	}
	if p.BuyPriceHigh != nil {
		return currentPrice >= *p.BuyPrice && currentPrice <= *p.BuyPriceHigh
	}
	return currentPrice <= *p.BuyPrice*1.005
}

func calcRR(buyPrice, targetPrice, stopPrice *float64) *float64 {
	if buyPrice == nil || targetPrice == nil || stopPrice == nil {
		return nil
	}
	return calcRRFromPrice(*buyPrice, *targetPrice, *stopPrice)
}

func calcRRFromPrice(entry, target, stop float64) *float64 {
	gain := target - entry
	loss := entry - stop
	if loss <= 0 {
		return nil
	}
	rr := math.Round(gain/loss*100) / 100
	return &rr
}

func validatePrices(buy, target, stop *float64) error {
	if buy != nil && *buy <= 0 {
		return fmt.Errorf("buy_price 必须 > 0")
	}
	if target != nil && *target <= 0 {
		return fmt.Errorf("target_price 必须 > 0")
	}
	if stop != nil && *stop <= 0 {
		return fmt.Errorf("stop_loss_price 必须 > 0")
	}
	if buy != nil && stop != nil && *stop >= *buy {
		return fmt.Errorf("stop_loss_price（%.2f）必须低于 buy_price（%.2f）", *stop, *buy)
	}
	if buy != nil && target != nil && *target <= *buy {
		return fmt.Errorf("target_price（%.2f）必须高于 buy_price（%.2f）", *target, *buy)
	}
	return nil
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isPlanTriggeredInBar(k KLine, buy, buyHigh float64) bool {
	return k.Low <= buyHigh && k.High >= buy
}

func calcMedian(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	mid := len(cp) / 2
	if len(cp)%2 == 1 {
		return cp[mid]
	}
	return (cp[mid-1] + cp[mid]) / 2
}
