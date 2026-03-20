package service

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 费率常量（万一免五）
// ═══════════════════════════════════════════════════════════════

const (
	feeBuy  = 0.0001
	feeSell = 0.0006

	minProfitPct = 0.001

	atrPeriod    = 20
	maPeriod     = 20
	klineHistory = 30

	// stopLossPct：-8% 硬止损，优先级最低（最后一道防线）
	stopLossPct       = -0.08
	atrStopMultiplier = 2.0
	tAmplitudeMin     = 0.015
	supportTolerance  = 0.005
	sellTAboveDayAvg  = 0.01

	rsWeakThreshold     = 3.0
	rsCriticalThreshold = 5.0

	nearStopThreshold   = 0.02
	nearTargetThreshold = 0.03

	// 补录虚假买入日期的截止点（此日期前的 bought_at 视为不可信）
	buyDatePlaceholderYear  = 2026
	buyDatePlaceholderMonth = 1
	buyDatePlaceholderDay   = 15
)

// ═══════════════════════════════════════════════════════════════
// 对外响应结构
// ═══════════════════════════════════════════════════════════════

type PositionDiagnosisResult struct {
	StockCode       string                   `json:"stock_code"`
	StockName       string                   `json:"stock_name"`
	Signal          model.SignalType         `json:"signal"`
	HealthScore     int                      `json:"health_score"`
	HealthLevel     string                   `json:"health_level"`
	ActionDirective string                   `json:"action_directive"`
	Snapshot        model.DiagnosticSnapshot `json:"snapshot"`
	SectorInfo      *SectorInfo              `json:"sector_info"`
	Position        *model.PositionDetail    `json:"position"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

type PositionAIResult struct {
	StockCode       string    `json:"stock_code"`
	StockName       string    `json:"stock_name"`
	ActionDirective string    `json:"action_directive"`
	GeneratedAt     time.Time `json:"generated_at"`
}

// ═══════════════════════════════════════════════════════════════
// PositionGuardianService
// ═══════════════════════════════════════════════════════════════

type PositionGuardianService struct {
	posRepo        repo.PositionGuardianRepo
	stockSvc       *StockService
	aiSvc          *AIAnalysisService
	sectorProvider *SectorProvider
	log            *zap.Logger
}

func NewPositionGuardianService(
	posRepo repo.PositionGuardianRepo,
	sectorRepo repo.SectorRepo,
	stockSvc *StockService,
	aiSvc *AIAnalysisService,
	log *zap.Logger,
) *PositionGuardianService {
	return &PositionGuardianService{
		posRepo:        posRepo,
		stockSvc:       stockSvc,
		aiSvc:          aiSvc,
		sectorProvider: NewSectorProvider(sectorRepo, log),
		log:            log,
	}
}

// ─────────────────────────────────────────────────────────────────
// DiagnoseAll — 并发量化指标刷新，不调用 AI
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) DiagnoseAll(ctx context.Context) ([]*PositionDiagnosisResult, error) {
	positions, err := s.posRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	if len(positions) == 0 {
		return []*PositionDiagnosisResult{}, nil
	}

	type quoteResult struct {
		code  string
		quote *Quote
		err   error
	}
	quoteCh := make(chan quoteResult, len(positions))
	for _, pos := range positions {
		go func(code string) {
			q, e := s.stockSvc.GetRealtimeQuote(code)
			quoteCh <- quoteResult{code: code, quote: q, err: e}
		}(pos.StockCode)
	}
	quotes := make(map[string]*Quote, len(positions))
	for range positions {
		r := <-quoteCh
		if r.err != nil {
			s.log.Warn("DiagnoseAll: get quote failed", zap.String("code", r.code), zap.Error(r.err))
			continue
		}
		quotes[r.code] = r.quote
	}

	type sectorInput struct {
		Code        string
		ChangeToday float64
	}
	sectorBatch := make([]sectorInput, 0, len(positions))
	for _, pos := range positions {
		if q, ok := quotes[pos.StockCode]; ok {
			sectorBatch = append(sectorBatch, sectorInput{pos.StockCode, q.ChangeRate})
		}
	}
	sectorInputs := make([]struct {
		Code        string
		ChangeToday float64
	}, len(sectorBatch))
	for i, b := range sectorBatch {
		sectorInputs[i].Code = b.Code
		sectorInputs[i].ChangeToday = b.ChangeToday
	}
	sectorInfoMap := s.sectorProvider.FetchSectorInfoBatch(ctx, sectorInputs)

	results := make([]*PositionDiagnosisResult, 0, len(positions))
	for _, pos := range positions {
		quote, ok := quotes[pos.StockCode]
		if !ok {
			continue
		}
		sectorInfo := sectorInfoMap[pos.StockCode]
		res, diagErr := s.diagnoseOneNoAI(ctx, pos, quote, sectorInfo)
		if diagErr != nil {
			s.log.Warn("diagnose failed, skip", zap.String("code", pos.StockCode), zap.Error(diagErr))
			continue
		}
		if saveErr := s.posRepo.SaveDiagnostic(ctx, &model.PositionDiagnostic{
			StockCode:       res.StockCode,
			SignalType:      res.Signal,
			ActionDirective: res.Snapshot.ActionSummary,
			DataSnapshot:    res.Snapshot,
		}); saveErr != nil {
			s.log.Warn("DiagnoseAll: save diagnostic failed", zap.String("code", res.StockCode), zap.Error(saveErr))
		}
		results = append(results, res)
	}
	return results, nil
}

// ─────────────────────────────────────────────────────────────────
// AnalyzeOne — 对单只持仓触发 AI 深度分析
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) AnalyzeOne(ctx context.Context, stockCode string) (*PositionAIResult, error) {
	pos, err := s.posRepo.GetByCode(ctx, stockCode)
	if err != nil {
		return nil, fmt.Errorf("position not found: %s", stockCode)
	}

	var (
		quote    *Quote
		quoteErr error
		wg       sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		quote, quoteErr = s.stockSvc.GetRealtimeQuote(stockCode)
	}()
	wg.Wait()
	if quoteErr != nil {
		return nil, fmt.Errorf("get quote: %w", quoteErr)
	}

	var sectorInfo *SectorInfo
	if rs, rsErr := s.sectorProvider.GetRelativeStrength(ctx, stockCode, quote.ChangeRate); rsErr == nil && rs != nil {
		sectorInfo = BuildSectorInfo(rs)
	}

	klineResp, err := s.stockSvc.GetKLine(stockCode, klineHistory)
	if err != nil {
		return nil, fmt.Errorf("get kline: %w", err)
	}
	klines := klineResp.KLines
	if len(klines) < maPeriod {
		return nil, fmt.Errorf("insufficient kline data")
	}

	cost := pos.AvgCost.InexactFloat64()
	price := quote.Price
	atr := calcATRFromKLines(klines, atrPeriod)
	ma20, ma20Slope := calcMA20WithSlope(klines)
	support, resistance := calcSupportResistance(klines, maPeriod)
	amplitude := calcAmplitude(klines)
	atrStop := calcDynamicATRStop(price, cost, atr)
	pnlPct := (price*(1-feeSell) - cost) / cost

	snapshot := buildSnapshot(pos, price, cost, pnlPct, atr, ma20, ma20Slope,
		support, resistance, atrStop, amplitude, sectorInfo)

	signal, reasons := s.runDecisionMatrix(pos, quote, snapshot, sectorInfo)
	snapshot.Reasons = reasons
	snapshot.CanDoT = (signal == model.SignalBuyT || signal == model.SignalSellT)
	enrichSnapshot(&snapshot, signal, pos)

	directive := s.buildAIDirectiveWithQty(ctx, quote, snapshot, signal, pos.AvailableQty)

	_ = s.posRepo.SaveDiagnostic(ctx, &model.PositionDiagnostic{
		StockCode:       stockCode,
		SignalType:      signal,
		ActionDirective: directive,
		DataSnapshot:    snapshot,
	})

	return &PositionAIResult{
		StockCode:       stockCode,
		StockName:       quote.Name,
		ActionDirective: directive,
		GeneratedAt:     time.Now(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// SyncPosition — 同步手动录入的持仓成本
// ─────────────────────────────────────────────────────────────────

type SyncPositionRequest struct {
	StockCode    string  `json:"stock_code"    binding:"required"`
	AvgCost      float64 `json:"avg_cost"      binding:"required,gt=0"`
	Quantity     int     `json:"quantity"      binding:"required,gt=0"`
	AvailableQty int     `json:"available_qty"`
	BoughtAt     string  `json:"bought_at"`
	BuyReason    string  `json:"buy_reason"`
}

func (s *PositionGuardianService) SyncPosition(ctx context.Context, req *SyncPositionRequest) (*model.PositionDetail, error) {
	if req.StockCode == "" {
		return nil, fmt.Errorf("stock_code is required")
	}
	if req.Quantity <= 0 {
		return nil, fmt.Errorf("quantity must be > 0")
	}
	costDec := decimal.NewFromFloat(req.AvgCost)
	if costDec.IsZero() || costDec.IsNegative() {
		return nil, fmt.Errorf("avg_cost must be > 0")
	}

	pos := &model.PositionDetail{
		StockCode:    req.StockCode,
		AvgCost:      costDec,
		Quantity:     req.Quantity,
		AvailableQty: req.AvailableQty,
		BuyReason:    req.BuyReason,
	}

	if req.BoughtAt != "" {
		cst := time.FixedZone("CST", 8*3600)
		if t, parseErr := time.ParseInLocation("2006-01-02", req.BoughtAt, cst); parseErr == nil {
			pos.BoughtAt = &t
		}
	} else {
		now := time.Now()
		pos.BoughtAt = &now
	}

	// ATR 初始止损（用当前价动态计算）
	if q, qErr := s.stockSvc.GetRealtimeQuote(req.StockCode); qErr == nil {
		if atr, atrErr := s.calcATR(req.StockCode); atrErr == nil && atr > 0 {
			stop := calcDynamicATRStop(q.Price, req.AvgCost, atr)
			stopDec := decimal.NewFromFloat(stop)
			pos.HardStopLoss = &stopDec
		}
	} else {
		// 降级：用成本价计算
		if atr, atrErr := s.calcATR(req.StockCode); atrErr == nil && atr > 0 {
			stop := req.AvgCost - atrStopMultiplier*atr
			stopDec := decimal.NewFromFloat(stop)
			pos.HardStopLoss = &stopDec
		}
	}

	if err := s.posRepo.Upsert(ctx, pos); err != nil {
		return nil, fmt.Errorf("upsert position: %w", err)
	}

	go func() {
		if _, sectorErr := s.sectorProvider.SyncSectorMapping(context.Background(), req.StockCode); sectorErr != nil {
			s.log.Warn("SyncPosition: sector mapping async failed",
				zap.String("code", req.StockCode), zap.Error(sectorErr))
		}
	}()

	return pos, nil
}

// ─────────────────────────────────────────────────────────────────
// LinkPlanToPosition
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) LinkPlanToPosition(
	ctx context.Context,
	stockCode string,
	planID int64,
	planStopLoss *float64,
	planTargetPrice *float64,
	planBuyReason string,
) error {
	pos, err := s.posRepo.GetByCode(ctx, stockCode)
	if err != nil {
		s.log.Debug("LinkPlanToPosition: position not found, skip",
			zap.String("code", stockCode), zap.Int64("plan_id", planID))
		return nil
	}
	pos.LinkedPlanID = &planID
	pos.PlanStopLoss = planStopLoss
	pos.PlanTargetPrice = planTargetPrice
	pos.PlanBuyReason = planBuyReason
	return s.posRepo.Upsert(ctx, pos)
}

// ─────────────────────────────────────────────────────────────────
// diagnoseOneNoAI — 纯量化诊断（内部）
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) diagnoseOneNoAI(
	ctx context.Context,
	pos *model.PositionDetail,
	quote *Quote,
	sectorInfo *SectorInfo,
) (*PositionDiagnosisResult, error) {
	code := pos.StockCode

	klineResp, err := s.stockSvc.GetKLine(code, klineHistory)
	if err != nil {
		return nil, fmt.Errorf("get kline: %w", err)
	}
	klines := klineResp.KLines
	if len(klines) < maPeriod {
		return nil, fmt.Errorf("insufficient kline data: got %d, need %d", len(klines), maPeriod)
	}

	cost := pos.AvgCost.InexactFloat64()
	price := quote.Price
	atr := calcATRFromKLines(klines, atrPeriod)
	ma20, ma20Slope := calcMA20WithSlope(klines)
	support, resistance := calcSupportResistance(klines, maPeriod)
	amplitude := calcAmplitude(klines)
	// ★ 修复1：ATR止损动态跟随当前价，不再锚定历史成本
	atrStop := calcDynamicATRStop(price, cost, atr)
	pnlPct := (price*(1-feeSell) - cost) / cost

	snapshot := buildSnapshot(pos, price, cost, pnlPct, atr, ma20, ma20Slope,
		support, resistance, atrStop, amplitude, sectorInfo)

	signal, reasons := s.runDecisionMatrix(pos, quote, snapshot, sectorInfo)
	snapshot.Reasons = reasons
	snapshot.CanDoT = (signal == model.SignalBuyT || signal == model.SignalSellT)
	enrichSnapshot(&snapshot, signal, pos)
	healthScore, healthLevel := calcHealthScore(signal, &snapshot)

	stopDec := decimal.NewFromFloat(atrStop)
	pos.HardStopLoss = &stopDec
	_ = s.posRepo.Upsert(ctx, pos)

	return &PositionDiagnosisResult{
		StockCode:   code,
		StockName:   quote.Name,
		Signal:      signal,
		HealthScore: healthScore,
		HealthLevel: healthLevel,
		Snapshot:    snapshot,
		SectorInfo:  sectorInfo,
		Position:    pos,
		UpdatedAt:   time.Now(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// calcDynamicATRStop — 动态ATR止损位（修复2：跟随当前价）
//
// 原逻辑：cost - 2×ATR（锚定买入成本，股价下跌后止损位反而比较远）
// 新逻辑：currentPrice - 2×ATR（跟随现价，下跌后止损更快触发）
//   - 盈利时额外保护：止损不低于成本×99.5%（防止盈利变亏损）
// ─────────────────────────────────────────────────────────────────

func calcDynamicATRStop(currentPrice, cost, atr float64) float64 {
	if atr <= 0 {
		return cost * 0.95
	}
	atrBasedStop := currentPrice - atrStopMultiplier*atr
	// 盈利时：止损不低于成本线附近（保护已有利润）
	if currentPrice > cost {
		costLine := cost * 0.995
		if atrBasedStop < costLine {
			atrBasedStop = costLine
		}
	}
	return math.Round(atrBasedStop*100) / 100
}

// ─────────────────────────────────────────────────────────────────
// enrichSnapshot — 填充面向小白的辅助字段
// ─────────────────────────────────────────────────────────────────

func enrichSnapshot(snap *model.DiagnosticSnapshot, signal model.SignalType, pos *model.PositionDetail) {
	// ★ 修复3：持仓天数——补录虚假日期返回 -1
	snap.HoldDays = resolveHoldDays(pos)
	snap.BuyReason = pos.BuyReason
	snap.PlanBuyReason = pos.PlanBuyReason
	snap.PlanStopLoss = pos.PlanStopLoss
	snap.PlanTargetPrice = pos.PlanTargetPrice

	effectiveStop := snap.HardStopLoss
	if pos.PlanStopLoss != nil {
		effectiveStop = *pos.PlanStopLoss
	}

	if snap.Price > 0 && effectiveStop > 0 {
		snap.StopDistPct = (snap.Price - effectiveStop) / snap.Price * 100
		snap.NearStopWarning = snap.StopDistPct >= 0 && snap.StopDistPct < nearStopThreshold*100
	}

	if pos.PlanTargetPrice != nil && *pos.PlanTargetPrice > 0 && snap.Price > 0 {
		dist := (*pos.PlanTargetPrice - snap.Price) / snap.Price * 100
		snap.TargetDistPct = &dist
		snap.NearTargetNotice = dist >= 0 && dist < nearTargetThreshold*100
	}

	snap.SuggestQty = suggestQuantity(signal, pos.Quantity)
	snap.ActionSummary = buildActionSummary(signal, snap, pos)
}

// resolveHoldDays 返回可信持仓天数；补录虚假日期（2026-01-15 前）返回 -1
func resolveHoldDays(pos *model.PositionDetail) int {
	if pos.BoughtAt == nil {
		return 0
	}
	t := *pos.BoughtAt
	placeholder := time.Date(buyDatePlaceholderYear, buyDatePlaceholderMonth, buyDatePlaceholderDay,
		0, 0, 0, 0, t.Location())
	if t.Before(placeholder) {
		return -1 // 信号：日期不可信
	}
	days := int(time.Since(t).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

func suggestQuantity(signal model.SignalType, totalQty int) int {
	switch signal {
	case model.SignalStopLoss:
		return totalQty
	case model.SignalSell, model.SignalSellT, model.SignalBuyT:
		return totalQty / 3
	default:
		return 0
	}
}

func calcHealthScore(signal model.SignalType, snap *model.DiagnosticSnapshot) (int, string) {
	score := 80
	switch signal {
	case model.SignalStopLoss:
		score -= 50
	case model.SignalSell:
		score -= 30
	case model.SignalSellT:
		score -= 20
	case model.SignalBuyT:
		score += 5
	case model.SignalHold:
		score += 8
	}
	if snap != nil {
		if snap.NearStopWarning {
			score -= 20
		}
		if snap.StopDistPct < 2 {
			score -= 15
		} else if snap.StopDistPct >= 8 {
			score += 6
		}
		if snap.RelStrengthDiff < -5 {
			score -= 10
		} else if snap.RelStrengthDiff > 0 {
			score += 4
		}
		if snap.NearTargetNotice {
			score -= 4
		}
	}
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	level := "GOOD"
	if score < 45 {
		level = "DANGER"
	} else if score < 70 {
		level = "WARN"
	}
	return score, level
}

func buildActionSummary(signal model.SignalType, snap *model.DiagnosticSnapshot, pos *model.PositionDetail) string {
	if snap.NearTargetNotice && snap.TargetDistPct != nil {
		return fmt.Sprintf("🎯 距目标价仅剩 %.1f%%，考虑分批止盈（先卖 %d 股）", *snap.TargetDistPct, snap.SuggestQty)
	}
	switch signal {
	case model.SignalStopLoss:
		return fmt.Sprintf("🛑 已触发止损，今日必须卖出全部 %d 股，不要犹豫", pos.Quantity)
	case model.SignalSell:
		if snap.NearStopWarning {
			return fmt.Sprintf("⚠️ 接近止损位（距止损仅 %.1f%%），建议立即减仓 %d 股观察", snap.StopDistPct, snap.SuggestQty)
		}
		return fmt.Sprintf("⚠️ 建议先卖出 %d 股（约 1/3 仓位）观察，保留底仓", snap.SuggestQty)
	case model.SignalSellT:
		return fmt.Sprintf("📈 现价偏高，可以高抛 %d 股，等回落到 ¥%.2f 附近再买回", snap.SuggestQty, snap.Support)
	case model.SignalBuyT:
		stop := snap.HardStopLoss
		if snap.PlanStopLoss != nil {
			stop = *snap.PlanStopLoss
		}
		return fmt.Sprintf("📉 现价偏低靠近支撑位，可以低吸 %d 股做T，止损 ¥%.2f", snap.SuggestQty, stop)
	default:
		if snap.NearStopWarning {
			return fmt.Sprintf("⚠️ 接近止损位，距止损还有 %.1f%%，请密切关注", snap.StopDistPct)
		}
		if snap.HoldDays > 20 {
			return fmt.Sprintf("✅ 继续持有，今天不操作（已持仓 %d 天）", snap.HoldDays)
		}
		return "✅ 继续持有，今天不操作"
	}
}

// ─────────────────────────────────────────────────────────────────
// buildSnapshot
// ─────────────────────────────────────────────────────────────────

func buildSnapshot(
	pos *model.PositionDetail,
	price, cost, pnlPct float64,
	atr, ma20, ma20Slope float64,
	support, resistance, atrStop, amplitude float64,
	si *SectorInfo,
) model.DiagnosticSnapshot {
	// 有效止损：ATR止损 与 计划止损取较高者（更严格）
	hardStop := atrStop
	if pos.PlanStopLoss != nil && *pos.PlanStopLoss > hardStop {
		hardStop = *pos.PlanStopLoss
	}

	snap := model.DiagnosticSnapshot{
		Price:           price,
		AvgCost:         cost,
		PnLPct:          pnlPct,
		ATR:             atr,
		MA20:            ma20,
		MA20Slope:       ma20Slope,
		Support:         support,
		Resistance:      resistance,
		HardStopLoss:    hardStop,
		Amplitude:       amplitude,
		MA20DistPct:     calcMA20DistPct(price, ma20),
		MA20PressureTip: buildMA20PressureTip(price, ma20, ma20Slope),
	}

	if si != nil {
		snap.SectorName = si.SectorName
		snap.SectorSecID = si.SectorCode
		snap.RelStrengthDiff = si.RelativeStrength
		snap.SectorWarning = buildSectorWarningFromSectorInfo(si)
	}
	return snap
}

// ─────────────────────────────────────────────────────────────────
// runDecisionMatrix — 止损三道防线（修复优先级顺序）
//
// ★ 正确顺序（从"最直接"到"最兜底"）：
//  1. 跌破支撑位（技术面信号最直接，说明支撑已失效）
//  2. 跌破ATR/计划止损位（量化动态止损）
//  3. 浮亏 -8% 硬止损（最后兜底，此时应同时检讨止损参数设置）
//
// 原错误：-8% 排在防线1后面、防线2前面，导致：
//   - 明阳智能：pnl=-10.4% 触发防线1（-8%），但ATR止损¥18.47距现价¥19.51有5.3%
//   - 信号说"立刻止损"，止损位却显示"还有5%空间"，自相矛盾
// ─────────────────────────────────────────────────────────────────

func buildSectorWarningFromSectorInfo(si *SectorInfo) string {
	if si == nil {
		return ""
	}
	switch si.RSLevel {
	case "critical":
		return fmt.Sprintf("严重偏离！%s（板块%.1f%%，RS=%.1f%%）", si.SectorName, si.SectorChangePercent, si.RelativeStrength)
	case "weak":
		return fmt.Sprintf("偏弱于%s（板块%.1f%%，RS=%.1f%%）", si.SectorName, si.SectorChangePercent, si.RelativeStrength)
	case "strong":
		return fmt.Sprintf("强于%s（板块%.1f%%，RS=+%.1f%%）", si.SectorName, si.SectorChangePercent, si.RelativeStrength)
	default:
		return ""
	}
}

func (s *PositionGuardianService) runDecisionMatrix(
	pos *model.PositionDetail,
	quote *Quote,
	snap model.DiagnosticSnapshot,
	sectorInfo *SectorInfo,
) (model.SignalType, []string) {

	var reasons []string
	price := snap.Price
	cost := snap.AvgCost

	// 板块背离预警（加入 reasons，不直接改变信号）
	if sectorInfo != nil && sectorInfo.RelativeStrength < -rsWeakThreshold {
		label := sectorInfo.RSLabel
		if sectorInfo.RelativeStrength < -rsCriticalThreshold {
			label = fmt.Sprintf("个股显著弱于行业，主力主动流出（RS=%.1f%%）", sectorInfo.RelativeStrength)
		}
		reasons = append(reasons, fmt.Sprintf("板块背离：%s | %s", sectorInfo.SectorName, label))
	}

	// ══════════════════════════════════════════════════════════════
	// 止损三道防线（顺序修复，从直接到兜底）
	// ══════════════════════════════════════════════════════════════

	// 防线1：跌破支撑位（技术面最直接，说明支撑失效）
	if price < snap.Support*(1-supportTolerance) {
		reasons = append(reasons, fmt.Sprintf(
			"价格¥%.2f跌破支撑位¥%.2f（容差%.1f%%），支撑已失效",
			price, snap.Support, supportTolerance*100))
		return model.SignalStopLoss, reasons
	}

	// 防线2：跌破ATR动态止损/计划止损（量化止损）
	effectiveStop := snap.HardStopLoss
	if price < effectiveStop {
		stopLabel := fmt.Sprintf("ATR动态止损位¥%.2f", effectiveStop)
		if pos.PlanStopLoss != nil && *pos.PlanStopLoss >= snap.HardStopLoss {
			stopLabel = fmt.Sprintf("计划止损位¥%.2f", effectiveStop)
		}
		reasons = append(reasons, fmt.Sprintf("价格¥%.2f已跌破%s", price, stopLabel))
		return model.SignalStopLoss, reasons
	}

	// 防线3：-8% 硬止损（最后兜底）
	if snap.PnLPct < stopLossPct {
		reasons = append(reasons, fmt.Sprintf(
			"浮亏%.1f%%触发-8%%硬止损（支撑和ATR止损均未触发，建议事后检讨止损参数是否过宽）",
			snap.PnLPct*100))
		return model.SignalStopLoss, reasons
	}

	inLoss := snap.PnLPct < 0

	// 优先级 2：做T判定
	if snap.Amplitude >= tAmplitudeMin && pos.AvailableQty > 0 {
		nearSupport := price <= snap.Support*(1+supportTolerance)
		nearResistance := price >= snap.Resistance*(1-supportTolerance)
		aboveDayAvg := quote.Price > quote.Open*(1+sellTAboveDayAvg)

		if nearResistance || aboveDayAvg {
			reason := "靠近压力位"
			if aboveDayAvg {
				reason = fmt.Sprintf("高于开盘均价1%%（¥%.2f）", quote.Open)
			}
			reasons = append(reasons, fmt.Sprintf("振幅%.1f%%达标，%s，建议高抛", snap.Amplitude*100, reason))
			return model.SignalSellT, reasons
		}
		if nearSupport && !inLoss {
			reasons = append(reasons, fmt.Sprintf("振幅%.1f%%达标，靠近支撑位¥%.2f，可低吸做T", snap.Amplitude*100, snap.Support))
			return model.SignalBuyT, reasons
		}
		if inLoss {
			reasons = append(reasons, fmt.Sprintf("当前亏损%.1f%%，禁止加仓，仅允许高抛减仓", snap.PnLPct*100))
			if nearResistance {
				return model.SignalSellT, reasons
			}
		}
	}

	// 优先级 3：板块严重背离 → SELL
	if sectorInfo != nil && sectorInfo.RelativeStrength < -rsCriticalThreshold {
		reasons = append(reasons, fmt.Sprintf(
			"个股跑输%s %.1f%%（RS=%.1f%%），主力主动流出，信号升级为减仓",
			sectorInfo.SectorName, -sectorInfo.RelativeStrength, sectorInfo.RelativeStrength))
		return model.SignalSell, reasons
	}

	// 优先级 4：MA20 趋势
	if snap.MA20Slope < 0 && price < snap.MA20 {
		reasons = append(reasons, fmt.Sprintf(
			"MA20趋势向下(斜率%.4f)，价格低于MA20(¥%.2f)，建议减仓观望",
			snap.MA20Slope, snap.MA20))
		return model.SignalSell, reasons
	}

	// 买入逻辑自检
	if warning := checkBuyReasonValidity(pos.EffectiveBuyReason(), snap); warning != "" {
		reasons = append(reasons, warning)
	}

	// T+0 收益空间
	netSellPrice := price * (1 - feeSell)
	netBuyCost := cost * (1 + feeBuy)
	tProfit := (netSellPrice - netBuyCost) / netBuyCost
	if tProfit < minProfitPct {
		reasons = append(reasons, fmt.Sprintf("T+0收益空间仅%.3f%%，低于0.1%%平衡线，持有等待", tProfit*100))
	} else {
		reasons = append(reasons, fmt.Sprintf("持仓盈亏%.1f%%，T+0空间%.3f%%，继续持有", snap.PnLPct*100, tProfit*100))
	}

	return model.SignalHold, reasons
}

func checkBuyReasonValidity(reason string, snap model.DiagnosticSnapshot) string {
	if reason == "" {
		return ""
	}
	for _, kw := range []string{"均线", "MA20", "ma20", "MA5", "ma5", "金叉"} {
		if strContains(reason, kw) {
			if snap.MA20Slope < 0 && snap.Price < snap.MA20 {
				return fmt.Sprintf("⚠️ 买入理由含「%s」，但当前 MA20 已转跌，买入逻辑可能失效", kw)
			}
			break
		}
	}
	for _, kw := range []string{"支撑", "回踩", "底部"} {
		if strContains(reason, kw) {
			if snap.Price < snap.Support*(1-supportTolerance) {
				return fmt.Sprintf("⚠️ 买入理由含「%s」，但价格已跌破支撑位，买入逻辑可能失效", kw)
			}
			break
		}
	}
	return ""
}

func strContains(s, sub string) bool {
	if len(sub) == 0 || len(s) < len(sub) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ─────────────────────────────────────────────────────────────────
// AI 行动指令生成（修复：持仓天数显示）
// ─────────────────────────────────────────────────────────────────

const positionGuardianPrompt = `你是一位专业的A股短线交易员，擅长T+0操作和风控管理。

当前持仓信息：
- 股票：%s（%s）
- 持仓成本：%.2f 元，现价：%.2f 元
- 盈亏：%+.1f%%（含万一免五手续费）
- 持仓天数：%s
- 买入理由：%s
- 可用仓位：%d 股

技术面数据：
- MA20：%.2f（趋势：%s，斜率：%+.4f）
- ATR(20)：%.3f（今日振幅：%.1f%%）
- 支撑位：%.2f，压力位：%.2f
- 有效止损位：%.2f（%s）
- 距止损还有：%.2f%%

板块强度：%s
量化决策：%s
决策依据：%s

请以专业交易员身份，基于以上量化结果给出明确的行动指令。要求：
1. 用简单易懂的语言（避免过多专业术语）
2. 明确说明是否执行做T，以及具体价位和数量
3. 明确当前止损位，以及在什么情况下需要执行止损
4. 若当前盈亏为负，必须包含"绝对禁止亏损加仓"的警告
5. 若买入理由可能失效，必须点明
6. 禁止模棱两可，必须有具体价格或百分比数字
7. 可以用 Markdown 格式，使用加粗和列表增强可读性`

func (s *PositionGuardianService) buildAIDirectiveWithQty(
	ctx context.Context,
	quote *Quote,
	snap model.DiagnosticSnapshot,
	signal model.SignalType,
	availableQty int,
) string {
	if s.aiSvc.apiKey == "" {
		return s.buildRuleDirective(quote, snap, signal)
	}

	trend := "上行"
	if snap.MA20Slope < 0 {
		trend = "下行"
	}

	sectorStr := "暂无板块数据"
	if snap.SectorName != "" {
		sectorStr = fmt.Sprintf("所属板块：%s（今日%.1f%%），强度对比：%.1f%%（%s）",
			snap.SectorName, snap.Sector5DChange, snap.RelStrengthDiff, snap.SectorWarning)
	}

	stopLabel := "ATR动态计算（当前价-2×ATR）"
	if snap.PlanStopLoss != nil {
		stopLabel = "计划止损位（与ATR取较高者）"
	}

	buyReason := snap.PlanBuyReason
	if buyReason == "" {
		buyReason = snap.BuyReason
	}
	if buyReason == "" {
		buyReason = "未填写"
	}

	// ★ 修复：持仓天数显示
	holdDaysStr := fmt.Sprintf("%d 天", snap.HoldDays)
	if snap.HoldDays < 0 {
		holdDaysStr = "数据补录（实际今日建仓，历史日期为系统占位符）"
	}

	prompt := fmt.Sprintf(positionGuardianPrompt,
		quote.Name, quote.Code,
		snap.AvgCost, snap.Price,
		snap.PnLPct*100,
		holdDaysStr,
		buyReason,
		availableQty,
		snap.MA20, trend, snap.MA20Slope,
		snap.ATR, snap.Amplitude*100,
		snap.Support, snap.Resistance,
		snap.HardStopLoss, stopLabel,
		snap.StopDistPct,
		sectorStr,
		signal,
		formatReasons(snap.Reasons),
	)

	report, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		s.log.Warn("AI directive failed, use rule directive", zap.String("code", quote.Code), zap.Error(err))
		return s.buildRuleDirective(quote, snap, signal)
	}
	return report
}

func (s *PositionGuardianService) buildRuleDirective(quote *Quote, snap model.DiagnosticSnapshot, signal model.SignalType) string {
	lossWarning := ""
	if snap.PnLPct < 0 {
		lossWarning = fmt.Sprintf("【⚠️ 亏损%.1f%% — 绝对禁止加仓！】", snap.PnLPct*100)
	}
	sectorNote := ""
	if snap.SectorWarning != "" {
		sectorNote = fmt.Sprintf("【板块信号：%s】", snap.SectorWarning)
	}

	switch signal {
	case model.SignalStopLoss:
		// ★ 修复：显示具体的触发原因
		triggerNote := ""
		if len(snap.Reasons) > 0 {
			triggerNote = fmt.Sprintf(" 触发：%s", snap.Reasons[len(snap.Reasons)-1])
		}
		return fmt.Sprintf("%s%s【止损】现价¥%.2f已触发止损，立即卖出全部%d股。止损位¥%.2f。%s",
			lossWarning, sectorNote, snap.Price, snap.SuggestQty, snap.HardStopLoss, triggerNote)
	case model.SignalSellT:
		return fmt.Sprintf("%s%s【高抛T】现价¥%.2f靠近压力位¥%.2f，振幅%.1f%%，建议卖出%d股，等回落至支撑位¥%.2f再买回。",
			lossWarning, sectorNote, snap.Price, snap.Resistance, snap.Amplitude*100, snap.SuggestQty, snap.Support)
	case model.SignalBuyT:
		return fmt.Sprintf("%s【低吸T】现价¥%.2f靠近支撑¥%.2f，振幅%.1f%%，建议买入%d股做T，止损¥%.2f，目标¥%.2f。",
			sectorNote, snap.Price, snap.Support, snap.Amplitude*100, snap.SuggestQty, snap.HardStopLoss, snap.Resistance)
	case model.SignalSell:
		return fmt.Sprintf("%s%s【减仓】建议先卖出%d股（约1/3仓位），MA20趋势向下，严守止损¥%.2f。",
			lossWarning, sectorNote, snap.SuggestQty, snap.HardStopLoss)
	default:
		return fmt.Sprintf("%s%s%s 止损¥%.2f，压力位¥%.2f注意减仓。",
			lossWarning, sectorNote, snap.ActionSummary, snap.HardStopLoss, snap.Resistance)
	}
}

// ─────────────────────────────────────────────────────────────────
// 技术指标计算
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) calcATR(code string) (float64, error) {
	resp, err := s.stockSvc.GetKLine(code, klineHistory)
	if err != nil {
		return 0, err
	}
	if len(resp.KLines) < atrPeriod {
		return 0, fmt.Errorf("insufficient data")
	}
	return calcATRFromKLines(resp.KLines, atrPeriod), nil
}

func calcATRFromKLines(klines []KLine, n int) float64 {
	if len(klines) < n+1 {
		n = len(klines) - 1
	}
	recent := klines[len(klines)-n:]
	trSum := 0.0
	for i := 1; i < len(recent); i++ {
		high, low, prev := recent[i].High, recent[i].Low, recent[i-1].Close
		tr := math.Max(high-low, math.Max(math.Abs(high-prev), math.Abs(low-prev)))
		trSum += tr
	}
	if len(recent) <= 1 {
		return 0
	}
	return trSum / float64(len(recent)-1)
}

func calcMA20WithSlope(klines []KLine) (ma20, slope float64) {
	n := len(klines)
	if n < maPeriod {
		return 0, 0
	}
	sum := 0.0
	for _, k := range klines[n-maPeriod:] {
		sum += k.Close
	}
	ma20 = sum / float64(maPeriod)

	const slopeWindow = 5
	if n < maPeriod+slopeWindow-1 {
		return ma20, 0
	}
	maVals := make([]float64, slopeWindow)
	for i := 0; i < slopeWindow; i++ {
		start := n - maPeriod - (slopeWindow - 1 - i)
		sv := 0.0
		for _, k := range klines[start : start+maPeriod] {
			sv += k.Close
		}
		maVals[i] = sv / float64(maPeriod)
	}
	xMean := float64(slopeWindow-1) / 2.0
	yMean := 0.0
	for _, v := range maVals {
		yMean += v
	}
	yMean /= float64(slopeWindow)
	num, den := 0.0, 0.0
	for i, v := range maVals {
		dx := float64(i) - xMean
		num += dx * (v - yMean)
		den += dx * dx
	}
	if den == 0 {
		return ma20, 0
	}
	return ma20, num / den
}

func calcSupportResistance(klines []KLine, n int) (support, resistance float64) {
	end := len(klines)
	start := end - n
	if start < 0 {
		start = 0
	}
	recent := klines[start:end]
	support, resistance = recent[0].Low, recent[0].High
	for _, k := range recent[1:] {
		if k.Low < support {
			support = k.Low
		}
		if k.High > resistance {
			resistance = k.High
		}
	}
	return
}

func calcAmplitude(klines []KLine) float64 {
	if len(klines) == 0 {
		return 0
	}
	last := klines[len(klines)-1]
	if last.Close == 0 {
		return 0
	}
	return (last.High - last.Low) / last.Close
}

func calcMA20DistPct(price, ma20 float64) float64 {
	if ma20 == 0 {
		return 0
	}
	return (price - ma20) / ma20 * 100
}

func buildMA20PressureTip(price, ma20, ma20Slope float64) string {
	if ma20 == 0 {
		return ""
	}
	distPct := calcMA20DistPct(price, ma20)
	trend := "上行"
	if ma20Slope < 0 {
		trend = "下行"
	}
	if distPct > 0 {
		return fmt.Sprintf("现价高于MA20 %.1f%%（MA20=¥%.2f，趋势%s）", distPct, ma20, trend)
	}
	absDist := -distPct
	if absDist < 2 {
		return fmt.Sprintf("距MA20压力位仅 %.1f%%，反弹遇阻概率高（MA20=¥%.2f，趋势%s）", absDist, ma20, trend)
	}
	return fmt.Sprintf("预计反弹压力位 MA20=¥%.2f，距当前价 %.1f%%（趋势%s）", ma20, absDist, trend)
}

func formatReasons(reasons []string) string {
	result := ""
	for i, r := range reasons {
		result += fmt.Sprintf("%d. %s", i+1, r)
		if i < len(reasons)-1 {
			result += "；"
		}
	}
	return result
}
