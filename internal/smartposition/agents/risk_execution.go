package agents

import (
	"context"
	"fmt"
	"math"

	"stock-backend/internal/model"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/tools"
)

// RiskExecutionAgent 负责 ATR 止损和仓位金额建议。
// 这里显式保留算法注释，便于后续审计规则口径和前端联调。
type RiskExecutionAgent struct {
	toolset *tools.ToolSet
}

func NewRiskExecutionAgent(toolset *tools.ToolSet) *RiskExecutionAgent {
	return &RiskExecutionAgent{toolset: toolset}
}

func (a *RiskExecutionAgent) Run(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
	next := state.Clone()

	klineResp, err := tools.InvokeJSON[tools.KLineToolInput, *service.KLineResponse](ctx, a.toolset.KLine, tools.KLineToolInput{
		Code:  state.Request.StockCode,
		Limit: max(60, state.Request.AnalysisWindow+20),
	})
	if err != nil {
		return nil, fmt.Errorf("risk execution: kline tool: %w", err)
	}
	profile, err := tools.InvokeJSON[tools.RiskProfileToolInput, *model.UserRiskProfile](ctx, a.toolset.RiskProfile, tools.RiskProfileToolInput{
		UserID: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("risk execution: risk profile tool: %w", err)
	}

	atr := calcATR(klineResp.KLines, 14)
	if atr <= 0 {
		atr = state.Quote.Price * 0.03
	}
	stopLoss := round2(state.Quote.Price - state.Request.ATRMultiplier*atr)
	riskPerShare := state.Quote.Price - stopLoss
	if riskPerShare <= 0 {
		riskPerShare = state.Quote.Price * 0.02
		stopLoss = round2(state.Quote.Price - riskPerShare)
	}

	// 单笔风险预算优先使用本次请求参数；用户画像中的 account_size 只作为兜底参考。
	baseCapital := state.Request.TotalCapital
	if baseCapital <= 0 && profile != nil {
		baseCapital = profile.AccountSize
	}
	riskAmount := baseCapital * state.Request.MaxRiskRatio
	shares := int64(math.Floor(riskAmount/riskPerShare/100.0) * 100)
	if shares < 0 {
		shares = 0
	}
	suggestedAmount := round2(float64(shares) * state.Quote.Price)
	suggestedRatio := 0.0
	if baseCapital > 0 {
		suggestedRatio = suggestedAmount / baseCapital
	}
	targetShort := round2(state.Quote.Price + 2.5*atr)
	targetMid := round2(state.Quote.Price + 4.5*atr)

	next.RiskExecution = &domain.RiskExecutionResult{
		ATR:             round2(atr),
		StopLoss:        stopLoss,
		SuggestedShares: shares,
		SuggestedAmount: suggestedAmount,
		SuggestedRatio:  round4(suggestedRatio),
		TargetShort:     targetShort,
		TargetMid:       targetMid,
		RiskLevel:       resolveRiskLevel(suggestedRatio, state.Request.MaxRiskRatio),
		Advice:          buildRiskAdvice(shares, suggestedAmount, suggestedRatio),
	}
	next.Confidence = 0.95
	return next, nil
}

func calcATR(klines []service.KLine, period int) float64 {
	if len(klines) < 2 {
		return 0
	}
	if period <= 0 || period >= len(klines) {
		period = len(klines) - 1
	}
	start := len(klines) - period
	sum := 0.0
	count := 0
	for i := start; i < len(klines); i++ {
		prevClose := klines[i-1].Close
		bar := klines[i]
		tr := maxFloat(bar.High-bar.Low, maxFloat(mathAbs(bar.High-prevClose), mathAbs(bar.Low-prevClose)))
		sum += tr
		count++
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func resolveRiskLevel(positionRatio, maxRiskRatio float64) string {
	switch {
	case positionRatio >= 0.4:
		return "high"
	case positionRatio >= maxRiskRatio*8:
		return "medium"
	default:
		return "low"
	}
}

func buildRiskAdvice(shares int64, amount, ratio float64) string {
	switch {
	case shares <= 0:
		return "当前风险参数下建议暂不建仓，可缩小止损距离或降低买入价后重算"
	case ratio >= 0.35:
		return fmt.Sprintf("建议控制在 %.1f%% 仓位内分批建仓，避免一次性打满", ratio*100)
	default:
		return fmt.Sprintf("建议买入金额约 %.0f 元，可按两批节奏逐步建仓", amount)
	}
}

func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
