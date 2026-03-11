package service

import (
	"context"
	"fmt"
	"math"
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

	stopLossPct       = -0.08
	atrStopMultiplier = 2.0
	tAmplitudeMin     = 0.015
	supportTolerance  = 0.005
	sellTAboveDayAvg  = 0.01
)

// ═══════════════════════════════════════════════════════════════
// 对外响应结构
// ═══════════════════════════════════════════════════════════════

// PositionDiagnosisResult 单只持仓完整诊断结果
// ActionDirective 字段在纯指标刷新时为空字符串，
// 只在主动调用 AnalyzeOne 后才会被填充。
type PositionDiagnosisResult struct {
	StockCode       string                   `json:"stock_code"`
	StockName       string                   `json:"stock_name"`
	Signal          model.SignalType         `json:"signal"`
	ActionDirective string                   `json:"action_directive"` // 仅 AI 分析后有值
	Snapshot        model.DiagnosticSnapshot `json:"snapshot"`
	Position        *model.PositionDetail    `json:"position"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

// PositionAIResult AI 深度分析结果（单只）
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
	posRepo  repo.PositionGuardianRepo
	stockSvc *StockService
	aiSvc    *AIAnalysisService
	log      *zap.Logger
}

func NewPositionGuardianService(
	posRepo repo.PositionGuardianRepo,
	stockSvc *StockService,
	aiSvc *AIAnalysisService,
	log *zap.Logger,
) *PositionGuardianService {
	return &PositionGuardianService{
		posRepo:  posRepo,
		stockSvc: stockSvc,
		aiSvc:    aiSvc,
		log:      log,
	}
}

// ─────────────────────────────────────────────────────────────────
// DiagnoseAll — 纯量化指标刷新，不调用 AI
// 用于定时轮询，返回最新行情 + 技术指标 + 信号，ActionDirective 为空
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) DiagnoseAll(ctx context.Context) ([]*PositionDiagnosisResult, error) {
	positions, err := s.posRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	if len(positions) == 0 {
		return []*PositionDiagnosisResult{}, nil
	}

	results := make([]*PositionDiagnosisResult, 0, len(positions))
	for _, pos := range positions {
		res, err := s.diagnoseOneNoAI(ctx, pos)
		if err != nil {
			s.log.Warn("diagnose failed, skip",
				zap.String("code", pos.StockCode),
				zap.Error(err),
			)
			continue
		}
		results = append(results, res)
	}
	return results, nil
}

// ─────────────────────────────────────────────────────────────────
// AnalyzeOne — 对单只持仓触发 AI 深度分析，消耗 token
// 手动触发，不在定时轮询中调用
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) AnalyzeOne(ctx context.Context, stockCode string) (*PositionAIResult, error) {
	pos, err := s.posRepo.GetByCode(ctx, stockCode)
	if err != nil {
		return nil, fmt.Errorf("position not found: %s", stockCode)
	}

	quote, err := s.stockSvc.GetRealtimeQuote(stockCode)
	if err != nil {
		return nil, fmt.Errorf("get quote: %w", err)
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
	hardStop := cost - atrStopMultiplier*atr

	netSellPrice := price * (1 - feeSell)
	pnlPct := (netSellPrice - cost) / cost

	snapshot := model.DiagnosticSnapshot{
		Price:        price,
		AvgCost:      cost,
		PnLPct:       pnlPct,
		ATR:          atr,
		MA20:         ma20,
		MA20Slope:    ma20Slope,
		Support:      support,
		Resistance:   resistance,
		HardStopLoss: hardStop,
		Amplitude:    amplitude,
	}

	signal, reasons := s.runDecisionMatrix(pos, quote, snapshot)
	snapshot.Reasons = reasons
	snapshot.CanDoT = (signal == model.SignalBuyT || signal == model.SignalSellT)

	// 生成 AI 行动指令（耗时，手动触发）
	directive := s.buildAIDirectiveWithQty(ctx, quote, snapshot, signal, pos.AvailableQty)

	// 持久化
	diag := &model.PositionDiagnostic{
		StockCode:       stockCode,
		SignalType:      signal,
		ActionDirective: directive,
		DataSnapshot:    snapshot,
	}
	_ = s.posRepo.SaveDiagnostic(ctx, diag)

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
	}

	if atr, err := s.calcATR(req.StockCode); err == nil && atr > 0 {
		stop := req.AvgCost - atrStopMultiplier*atr
		stopDec := decimal.NewFromFloat(stop)
		pos.HardStopLoss = &stopDec
	}

	if err := s.posRepo.Upsert(ctx, pos); err != nil {
		return nil, fmt.Errorf("upsert position: %w", err)
	}
	return pos, nil
}

// SyncPositionRequest 录入持仓请求体
type SyncPositionRequest struct {
	StockCode    string  `json:"stock_code"    binding:"required"`
	AvgCost      float64 `json:"avg_cost"      binding:"required,gt=0"`
	Quantity     int     `json:"quantity"      binding:"required,gt=0"`
	AvailableQty int     `json:"available_qty"`
}

// ─────────────────────────────────────────────────────────────────
// diagnoseOneNoAI — 纯量化诊断，不调 AI（内部使用）
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) diagnoseOneNoAI(ctx context.Context, pos *model.PositionDetail) (*PositionDiagnosisResult, error) {
	code := pos.StockCode

	quote, err := s.stockSvc.GetRealtimeQuote(code)
	if err != nil {
		return nil, fmt.Errorf("get quote: %w", err)
	}

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
	hardStop := cost - atrStopMultiplier*atr

	netSellPrice := price * (1 - feeSell)
	pnlPct := (netSellPrice - cost) / cost

	snapshot := model.DiagnosticSnapshot{
		Price:        price,
		AvgCost:      cost,
		PnLPct:       pnlPct,
		ATR:          atr,
		MA20:         ma20,
		MA20Slope:    ma20Slope,
		Support:      support,
		Resistance:   resistance,
		HardStopLoss: hardStop,
		Amplitude:    amplitude,
	}

	signal, reasons := s.runDecisionMatrix(pos, quote, snapshot)
	snapshot.Reasons = reasons
	snapshot.CanDoT = (signal == model.SignalBuyT || signal == model.SignalSellT)

	// 更新 hard_stop_loss 到 DB
	stopDec := decimal.NewFromFloat(hardStop)
	pos.HardStopLoss = &stopDec
	_ = s.posRepo.Upsert(ctx, pos)

	// ActionDirective 不在这里填充，由前端手动触发 AnalyzeOne 获取
	return &PositionDiagnosisResult{
		StockCode:       code,
		StockName:       quote.Name,
		Signal:          signal,
		ActionDirective: "", // 空，节省 token
		Snapshot:        snapshot,
		Position:        pos,
		UpdatedAt:       time.Now(),
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// 决策矩阵
// ─────────────────────────────────────────────────────────────────

func (s *PositionGuardianService) runDecisionMatrix(
	pos *model.PositionDetail,
	quote *Quote,
	snap model.DiagnosticSnapshot,
) (model.SignalType, []string) {

	var reasons []string
	price := snap.Price
	cost := snap.AvgCost

	// 优先级 1：强制止损
	if price < snap.Support*(1-supportTolerance) {
		reasons = append(reasons, fmt.Sprintf("价格%.2f跌破支撑位%.2f", price, snap.Support))
		return model.SignalStopLoss, reasons
	}
	if snap.PnLPct < stopLossPct {
		reasons = append(reasons, fmt.Sprintf("浮亏%.1f%%触发-8%%硬止损", snap.PnLPct*100))
		return model.SignalStopLoss, reasons
	}
	if price < snap.HardStopLoss {
		reasons = append(reasons, fmt.Sprintf("价格%.2f低于ATR止损位%.2f(cost-2×ATR)", price, snap.HardStopLoss))
		return model.SignalStopLoss, reasons
	}

	inLoss := snap.PnLPct < 0

	// 优先级 2：做 T 判定
	if snap.Amplitude >= tAmplitudeMin && pos.AvailableQty > 0 {
		nearSupport := price <= snap.Support*(1+supportTolerance)
		nearResistance := price >= snap.Resistance*(1-supportTolerance)
		aboveDayAvg := quote.Price > quote.Open*(1+sellTAboveDayAvg)

		if nearResistance || aboveDayAvg {
			reason := "靠近压力位"
			if aboveDayAvg {
				reason = fmt.Sprintf("高于开盘均价1%%（%.2f）", quote.Open)
			}
			reasons = append(reasons, fmt.Sprintf("振幅%.1f%%达标，%s，建议高抛", snap.Amplitude*100, reason))
			return model.SignalSellT, reasons
		}

		if nearSupport && !inLoss {
			reasons = append(reasons, fmt.Sprintf("振幅%.1f%%达标，靠近支撑位%.2f，可低吸做T", snap.Amplitude*100, snap.Support))
			return model.SignalBuyT, reasons
		}

		if inLoss {
			reasons = append(reasons, fmt.Sprintf("当前亏损%.1f%%，禁止加仓，仅允许高抛减仓", snap.PnLPct*100))
			if nearResistance {
				return model.SignalSellT, reasons
			}
		}
	}

	// 优先级 3：持有或减仓
	if snap.MA20Slope < 0 && price < snap.MA20 {
		reasons = append(reasons, fmt.Sprintf("MA20趋势向下(斜率%.4f)，价格低于MA20(%.2f)，建议减仓观望", snap.MA20Slope, snap.MA20))
		return model.SignalSell, reasons
	}

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

// ─────────────────────────────────────────────────────────────────
// AI 行动指令生成
// ─────────────────────────────────────────────────────────────────

const positionGuardianPrompt = `你是一位专业的A股短线交易员，擅长T+0操作和风控管理。

当前持仓信息：
- 股票：%s（%s）
- 持仓成本：%.2f 元，现价：%.2f 元
- 盈亏：%+.1f%%（含万一免五手续费）
- 可用仓位：%d 股

技术面数据：
- MA20：%.2f（趋势：%s，斜率：%+.4f）
- ATR(20)：%.3f（今日振幅：%.1f%%）
- 支撑位：%.2f，压力位：%.2f
- 硬止损位（cost-2×ATR）：%.2f

量化决策：%s
决策依据：%s

请以专业交易员身份，基于以上量化结果给出明确的行动指令。

要求：
1. 明确说明是否执行做T（高抛/低吸），以及具体价位
2. 明确止损位是否需要调整
3. 若当前盈亏为负，必须包含"绝对禁止亏损加仓"的警告
4. 禁止模棱两可，必须有具体价格或百分比数字
5. 可以用 Markdown 格式，使用加粗和列表增强可读性`

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

	prompt := fmt.Sprintf(positionGuardianPrompt,
		quote.Name, quote.Code,
		snap.AvgCost, snap.Price,
		snap.PnLPct*100,
		availableQty,
		snap.MA20, trend, snap.MA20Slope,
		snap.ATR, snap.Amplitude*100,
		snap.Support, snap.Resistance,
		snap.HardStopLoss,
		signal,
		formatReasons(snap.Reasons),
	)

	report, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		s.log.Warn("AI directive failed, use rule directive",
			zap.String("code", quote.Code),
			zap.Error(err),
		)
		return s.buildRuleDirective(quote, snap, signal)
	}
	return report
}

// buildRuleDirective 无 AI Key 时的规则模板指令
func (s *PositionGuardianService) buildRuleDirective(quote *Quote, snap model.DiagnosticSnapshot, signal model.SignalType) string {
	lossWarning := ""
	if snap.PnLPct < 0 {
		lossWarning = fmt.Sprintf("【⚠️ 亏损%.1f%% — 绝对禁止加仓！】", snap.PnLPct*100)
	}

	switch signal {
	case model.SignalStopLoss:
		return fmt.Sprintf("%s【止损】现价%.2f已触发止损条件，立即执行全部卖出，止损位%.2f。",
			lossWarning, snap.Price, snap.HardStopLoss)
	case model.SignalSellT:
		return fmt.Sprintf("%s【高抛T】现价%.2f靠近压力位%.2f，振幅%.1f%%，建议卖出1/3仓位做T，等待回落至支撑位%.2f再买回。",
			lossWarning, snap.Price, snap.Resistance, snap.Amplitude*100, snap.Support)
	case model.SignalBuyT:
		return fmt.Sprintf("【低吸T】现价%.2f靠近支撑位%.2f，振幅%.1f%%，建议买入做T，止损位%.2f，目标压力位%.2f。",
			snap.Price, snap.Support, snap.Amplitude*100, snap.HardStopLoss, snap.Resistance)
	case model.SignalSell:
		return fmt.Sprintf("%s【减仓】MA20趋势向下(%.2f)，价格低于均线，建议逢高减仓，严守止损%.2f。",
			lossWarning, snap.MA20, snap.HardStopLoss)
	default:
		return fmt.Sprintf("%s【持有】现价%.2f，盈亏%.1f%%，继续持有，硬止损%.2f，压力位%.2f注意减仓。",
			lossWarning, snap.Price, snap.PnLPct*100, snap.HardStopLoss, snap.Resistance)
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
		high := recent[i].High
		low := recent[i].Low
		prevClose := recent[i-1].Close
		tr := math.Max(high-low, math.Max(math.Abs(high-prevClose), math.Abs(low-prevClose)))
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

	slopeWindow := 5
	if n < maPeriod+slopeWindow-1 {
		return ma20, 0
	}
	maVals := make([]float64, slopeWindow)
	for i := 0; i < slopeWindow; i++ {
		start := n - maPeriod - (slopeWindow - 1 - i)
		s := 0.0
		for _, k := range klines[start : start+maPeriod] {
			s += k.Close
		}
		maVals[i] = s / float64(maPeriod)
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
	support = recent[0].Low
	resistance = recent[0].High
	for _, k := range recent[1:] {
		if k.Low < support {
			support = k.Low
		}
		if k.High > resistance {
			resistance = k.High
		}
	}
	return support, resistance
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
