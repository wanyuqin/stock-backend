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

// DataAnalystAgent 负责主力成本区、支撑阻力和买点区间判断。
// 该 Agent 只依赖 tool 调数据，核心算法保留在纯 Go 代码中，便于稳定复用和测试。
type DataAnalystAgent struct {
	toolset *tools.ToolSet
}

func NewDataAnalystAgent(toolset *tools.ToolSet) *DataAnalystAgent {
	return &DataAnalystAgent{toolset: toolset}
}

func (a *DataAnalystAgent) Run(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
	next := state.Clone()

	klineResp, err := tools.InvokeJSON[tools.KLineToolInput, *service.KLineResponse](ctx, a.toolset.KLine, tools.KLineToolInput{
		Code:  state.Request.StockCode,
		Limit: max(60, state.Request.AnalysisWindow+20),
	})
	if err != nil {
		return nil, fmt.Errorf("data analyst: kline tool: %w", err)
	}

	bigDeal, err := tools.InvokeJSON[tools.BigDealToolInput, *service.BigDealSummary](ctx, a.toolset.BigDeal, tools.BigDealToolInput{
		Code:       tools.BuildQQCode(state.Request.StockCode),
		ChangeRate: state.Quote.ChangeRate,
	})
	if err != nil {
		return nil, fmt.Errorf("data analyst: big deal tool: %w", err)
	}
	valuation, err := tools.InvokeJSON[tools.ValuationToolInput, *model.StockValuation](ctx, a.toolset.Valuation, tools.ValuationToolInput{
		Code: state.Request.StockCode,
	})
	if err == nil {
		next.Valuation = valuation
	}

	support, resistance := calcSupportResistance(klineResp.KLines, state.Request.AnalysisWindow)
	mainCost := bigDeal.MainAvgCost
	if mainCost <= 0 {
		mainCost = state.Quote.Price
	}
	buyLow := round2(maxFloat(mainCost*0.97, support*0.995))
	buyHigh := round2(minFloat(mainCost*1.03, resistance*0.985))
	if buyHigh <= buyLow {
		buyLow = round2(mainCost * 0.98)
		buyHigh = round2(mainCost * 1.02)
	}

	next.DataAnalyst = &domain.DataAnalystResult{
		MainforceCost: round2(mainCost),
		CostZone:      [2]float64{round2(mainCost * 0.97), round2(mainCost * 1.03)},
		Support:       round2(support),
		Resistance:    round2(resistance),
		BuyRange:      [2]float64{buyLow, buyHigh},
		NetInflow10D:  round2(bigDeal.MainNetFlow),
		Label:         resolveCapitalLabel(bigDeal),
		Insight:       bigDeal.InsightDesc,
	}
	next.Confidence = 0.92
	return next, nil
}

func resolveCapitalLabel(summary *service.BigDealSummary) string {
	switch {
	case summary == nil:
		return "数据缺失"
	case summary.SurgeSignal && summary.MainNetFlow > 0:
		return "主力扫货"
	case summary.MainNetFlow > 0:
		return "主力回流"
	case summary.WashingSignal:
		return "疑似洗盘"
	case summary.MainNetFlow < 0:
		return "主力流出"
	default:
		return "资金均衡"
	}
}

func calcSupportResistance(klines []service.KLine, window int) (float64, float64) {
	if len(klines) == 0 {
		return 0, 0
	}
	if window <= 0 || window > len(klines) {
		window = len(klines)
	}
	start := len(klines) - window
	support := math.MaxFloat64
	resistance := 0.0
	for _, bar := range klines[start:] {
		if bar.Low > 0 && bar.Low < support {
			support = bar.Low
		}
		if bar.High > resistance {
			resistance = bar.High
		}
	}
	if support == math.MaxFloat64 {
		support = klines[len(klines)-1].Low
	}
	return support, resistance
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minFloat(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
