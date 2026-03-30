package graph

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/compose"

	"stock-backend/internal/model"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition/agents"
	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/fallback"
	"stock-backend/internal/smartposition/summary"
	"stock-backend/internal/smartposition/tools"
)

type Builder struct {
	toolset *tools.ToolSet
	data    *agents.DataAnalystAgent
	intel   *agents.ContentIntelAgent
	risk    *agents.RiskExecutionAgent
}

func NewBuilder(toolset *tools.ToolSet) *Builder {
	return &Builder{
		toolset: toolset,
		data:    agents.NewDataAnalystAgent(toolset),
		intel:   agents.NewContentIntelAgent(toolset),
		risk:    agents.NewRiskExecutionAgent(toolset),
	}
}

func (b *Builder) Build(ctx context.Context) (compose.Runnable[*domain.SmartPositionRequest, *domain.SmartPositionResponse], error) {
	compose.RegisterValuesMergeFunc(domain.MergeStates)

	g := compose.NewGraph[*domain.SmartPositionRequest, *domain.SmartPositionResponse]()

	initNode := compose.InvokableLambda(func(ctx context.Context, req *domain.SmartPositionRequest) (*domain.GraphState, error) {
		reportProgress(ctx, domain.EventStarted, domain.StageInitContext, 5, domain.TaskStatusRunning, "初始化分析上下文", nil, nil)
		if req == nil {
			return nil, fmt.Errorf("empty request")
		}
		norm, err := req.Normalize()
		if err != nil {
			reportProgress(ctx, domain.EventFailed, domain.StageInitContext, 5, domain.TaskStatusFailed, "初始化失败", nil, err)
			return nil, err
		}
		quote, err := tools.InvokeJSON[tools.QuoteToolInput, *service.Quote](ctx, b.toolset.Quote, tools.QuoteToolInput{Code: norm.StockCode})
		if err != nil {
			reportProgress(ctx, domain.EventFailed, domain.StageInitContext, 5, domain.TaskStatusFailed, "行情初始化失败", nil, err)
			return nil, fmt.Errorf("init_context: quote tool: %w", err)
		}
		state := &domain.GraphState{
			Request:    norm,
			TraceID:    fmt.Sprintf("sp-%d", time.Now().UnixNano()),
			StartedAt:  time.Now(),
			Quote:      quote,
			Confidence: 1,
		}
		reportProgress(ctx, domain.EventProgress, domain.StageInitContext, 12, domain.TaskStatusRunning, "上下文初始化完成", state, nil)
		return state, nil
	})

	dataNode := compose.InvokableLambda(func(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
		reportProgress(ctx, domain.EventProgress, domain.StageDataAnalyst, 20, domain.TaskStatusRunning, "数据审计 Agent 开始分析", state, nil)
		out, err := b.data.Run(ctx, state)
		if err != nil {
			out = fallback.ApplyAgentFailure(state, "data_analyst_agent", err)
			reportProgress(ctx, domain.EventPartial, domain.StageDataAnalyst, 35, domain.TaskStatusPartial, "数据审计 Agent 已降级", out, err)
			return out, nil
		}
		reportProgress(ctx, domain.EventProgress, domain.StageDataAnalyst, 35, domain.TaskStatusRunning, "数据审计 Agent 完成", out, nil)
		return out, nil
	})
	intelNode := compose.InvokableLambda(func(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
		reportProgress(ctx, domain.EventProgress, domain.StageContentIntel, 22, domain.TaskStatusRunning, "情报搜集 Agent 开始分析", state, nil)
		out, err := b.intel.Run(ctx, state)
		if err != nil {
			out = fallback.ApplyAgentFailure(state, "content_intel_agent", err)
			reportProgress(ctx, domain.EventPartial, domain.StageContentIntel, 50, domain.TaskStatusPartial, "情报搜集 Agent 已降级", out, err)
			return out, nil
		}
		reportProgress(ctx, domain.EventProgress, domain.StageContentIntel, 50, domain.TaskStatusRunning, "情报搜集 Agent 完成", out, nil)
		return out, nil
	})
	riskNode := compose.InvokableLambda(func(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
		reportProgress(ctx, domain.EventProgress, domain.StageRiskExecution, 24, domain.TaskStatusRunning, "风险执行 Agent 开始分析", state, nil)
		out, err := b.risk.Run(ctx, state)
		if err != nil {
			out = fallback.ApplyAgentFailure(state, "risk_execution_agent", err)
			reportProgress(ctx, domain.EventPartial, domain.StageRiskExecution, 65, domain.TaskStatusPartial, "风险执行 Agent 已降级", out, err)
			return out, nil
		}
		reportProgress(ctx, domain.EventProgress, domain.StageRiskExecution, 65, domain.TaskStatusRunning, "风险执行 Agent 完成", out, nil)
		return out, nil
	})
	mergeNode := compose.InvokableLambda(func(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
		reportProgress(ctx, domain.EventProgress, domain.StageCoordinatorMerge, 78, domain.TaskStatusRunning, "协调汇总结果", state, nil)
		if state == nil {
			return nil, fmt.Errorf("empty merged state")
		}
		resp := &domain.SmartPositionResponse{
			StockCode:       state.Request.StockCode,
			StockName:       state.Quote.Name,
			Valuation:       buildValuation(state),
			CapitalFlow:     buildCapitalFlow(state),
			KeyPrices:       buildKeyPrices(state),
			Position:        buildPosition(state),
			OverallScore:    buildScore(state),
			Warnings:        state.Warnings,
			DegradedModules: state.DegradedModules,
			DataAsOf:        state.Quote.UpdatedAt.Format(time.RFC3339),
			GeneratedAt:     time.Now().Format(time.RFC3339),
			Confidence:      state.Confidence,
		}
		next := state.Clone()
		next.Response = resp
		status := domain.TaskStatusRunning
		if len(next.DegradedModules) > 0 {
			status = domain.TaskStatusPartial
		}
		reportProgress(ctx, domain.EventProgress, domain.StageCoordinatorMerge, 88, status, "协调汇总完成", next, nil)
		return next, nil
	})
	summaryNode := compose.InvokableLambda(func(ctx context.Context, state *domain.GraphState) (*domain.SmartPositionResponse, error) {
		reportProgress(ctx, domain.EventProgress, domain.StageSummaryGenerate, 94, domain.TaskStatusRunning, "生成最终摘要", state, nil)
		if state == nil || state.Response == nil {
			reportProgress(ctx, domain.EventFailed, domain.StageSummaryGenerate, 94, domain.TaskStatusFailed, "生成摘要失败", state, fmt.Errorf("response not initialized"))
			return nil, fmt.Errorf("response not initialized")
		}
		resp := *state.Response
		resp.Summary = summary.BuildSummary(state)
		finalStatus := domain.TaskStatusCompleted
		finalEventType := domain.EventCompleted
		if len(state.DegradedModules) > 0 {
			finalStatus = domain.TaskStatusPartial
			finalEventType = domain.EventPartial
		}
		finalState := state.Clone()
		finalState.Response = &resp
		reportProgress(ctx, finalEventType, domain.StageSummaryGenerate, 100, finalStatus, "智能建仓分析完成", finalState, nil)
		return &resp, nil
	})

	if err := g.AddLambdaNode("init_context", initNode); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("data_analyst_agent", dataNode); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("content_intel_agent", intelNode); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("risk_execution_agent", riskNode); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("coordinator_merge", mergeNode); err != nil {
		return nil, err
	}
	if err := g.AddLambdaNode("summary_generate", summaryNode); err != nil {
		return nil, err
	}

	edges := [][2]string{
		{compose.START, "init_context"},
		{"init_context", "data_analyst_agent"},
		{"init_context", "content_intel_agent"},
		{"init_context", "risk_execution_agent"},
		{"data_analyst_agent", "coordinator_merge"},
		{"content_intel_agent", "coordinator_merge"},
		{"risk_execution_agent", "coordinator_merge"},
		{"coordinator_merge", "summary_generate"},
		{"summary_generate", compose.END},
	}
	for _, edge := range edges {
		if err := g.AddEdge(edge[0], edge[1]); err != nil {
			return nil, err
		}
	}
	return g.Compile(ctx, compose.WithGraphName("smart_position_graph"))
}

func buildValuation(state *domain.GraphState) domain.ValuationView {
	if state.Valuation == nil {
		return domain.ValuationView{}
	}
	return domain.ValuationView{
		PEPercentile: deref(state.Valuation.PEPercentile),
		PBPercentile: deref(state.Valuation.PBPercentile),
		Status:       resolveValuationStatus(state.Valuation),
	}
}

func buildCapitalFlow(state *domain.GraphState) domain.CapitalFlowView {
	if state.DataAnalyst == nil {
		return domain.CapitalFlowView{}
	}
	return domain.CapitalFlowView{
		MainforceCost: state.DataAnalyst.MainforceCost,
		NetInflow10D:  state.DataAnalyst.NetInflow10D,
		Label:         state.DataAnalyst.Label,
	}
}

func buildKeyPrices(state *domain.GraphState) domain.KeyPricesView {
	result := domain.KeyPricesView{}
	if state.DataAnalyst != nil {
		result.BuyRange = state.DataAnalyst.BuyRange
	}
	if state.RiskExecution != nil {
		result.StopLoss = state.RiskExecution.StopLoss
		result.TargetShort = state.RiskExecution.TargetShort
		result.TargetMid = state.RiskExecution.TargetMid
	}
	return result
}

func buildPosition(state *domain.GraphState) domain.PositionView {
	if state.RiskExecution == nil {
		return domain.PositionView{}
	}
	return domain.PositionView{
		SuggestedAmount: state.RiskExecution.SuggestedAmount,
		SuggestedRatio:  state.RiskExecution.SuggestedRatio,
		SuggestedShares: state.RiskExecution.SuggestedShares,
	}
}

func buildScore(state *domain.GraphState) int {
	score := 50
	if state.DataAnalyst != nil {
		if state.DataAnalyst.NetInflow10D > 0 {
			score += 15
		}
		if state.Quote.Price >= state.DataAnalyst.BuyRange[0] && state.Quote.Price <= state.DataAnalyst.BuyRange[1] {
			score += 10
		}
	}
	if state.ContentIntel != nil {
		score += int((state.ContentIntel.SentimentScore - 50) / 5)
	}
	if state.Valuation != nil {
		switch resolveValuationStatus(state.Valuation) {
		case "low":
			score += 12
		case "high":
			score -= 8
		}
	}
	if state.RiskExecution != nil && state.RiskExecution.SuggestedRatio <= 0.35 {
		score += 10
	}
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func WithFallback(state *domain.GraphState, module string, err error) *domain.GraphState {
	return fallback.ApplyAgentFailure(state, module, err)
}

func resolveValuationStatus(v *model.StockValuation) string {
	if v == nil || v.PEPercentile == nil {
		return "medium"
	}
	switch {
	case *v.PEPercentile <= 30:
		return "low"
	case *v.PEPercentile >= 70:
		return "high"
	default:
		return "medium"
	}
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}
