package domain

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"stock-backend/internal/model"
	"stock-backend/internal/service"
)

const (
	DefaultMaxRiskRatio = 0.02
	DefaultATRMultiple  = 2.0
	DefaultAnalysisDays = 20
)

// SmartPositionRequest 是智能建仓分析的统一入口参数。
// 这里将 PRD 中可调参数显式化，便于前后端和 Graph 节点共享同一份契约。
type SmartPositionRequest struct {
	StockCode      string  `json:"stock_code" binding:"required"`
	TotalCapital   float64 `json:"total_capital" binding:"required"`
	MaxRiskRatio   float64 `json:"max_risk_ratio"`
	ATRMultiplier  float64 `json:"atr_multiplier"`
	AnalysisWindow int     `json:"analysis_window"`
}

// SmartPositionExecuteRequest 复用分析入参。
// execute 端会基于同一套分析逻辑重新计算，避免直接信任前端回传的建议结果。
type SmartPositionExecuteRequest struct {
	StockCode      string  `json:"stock_code" binding:"required"`
	TotalCapital   float64 `json:"total_capital" binding:"required"`
	MaxRiskRatio   float64 `json:"max_risk_ratio"`
	ATRMultiplier  float64 `json:"atr_multiplier"`
	AnalysisWindow int     `json:"analysis_window"`
}

type ValuationView struct {
	PEPercentile float64 `json:"pe_percentile"`
	PBPercentile float64 `json:"pb_percentile"`
	Status       string  `json:"status"`
}

type CapitalFlowView struct {
	MainforceCost float64 `json:"mainforce_cost"`
	NetInflow10D  float64 `json:"net_inflow_10d"`
	Label         string  `json:"label"`
}

type KeyPricesView struct {
	BuyRange    [2]float64 `json:"buy_range"`
	StopLoss    float64    `json:"stop_loss"`
	TargetShort float64    `json:"target_short"`
	TargetMid   float64    `json:"target_mid"`
}

type PositionView struct {
	SuggestedAmount float64 `json:"suggested_amount"`
	SuggestedRatio  float64 `json:"suggested_ratio"`
	SuggestedShares int64   `json:"suggested_shares"`
}

// SmartPositionResponse 是对前端暴露的主响应结构。
// 除 PRD 主字段外，增加 warnings/degraded_modules/data_as_of 等运行态信息用于降级展示。
type SmartPositionResponse struct {
	StockCode       string          `json:"stock_code"`
	StockName       string          `json:"stock_name"`
	OverallScore    int             `json:"overall_score"`
	Valuation       ValuationView   `json:"valuation"`
	CapitalFlow     CapitalFlowView `json:"capital_flow"`
	KeyPrices       KeyPricesView   `json:"key_prices"`
	Position        PositionView    `json:"position"`
	Summary         string          `json:"summary"`
	GeneratedAt     string          `json:"generated_at"`
	Warnings        []string        `json:"warnings,omitempty"`
	DegradedModules []string        `json:"degraded_modules,omitempty"`
	DataAsOf        string          `json:"data_as_of,omitempty"`
	Confidence      float64         `json:"confidence,omitempty"`
}

type SmartPositionExecuteResponse struct {
	Analysis       *SmartPositionResponse `json:"analysis"`
	WatchlistAdded bool                   `json:"watchlist_added"`
	BuyPlanID      int64                  `json:"buy_plan_id"`
}

type SmartPositionTaskStatus string

const (
	TaskStatusPending   SmartPositionTaskStatus = "PENDING"
	TaskStatusRunning   SmartPositionTaskStatus = "RUNNING"
	TaskStatusPartial   SmartPositionTaskStatus = "PARTIAL"
	TaskStatusCompleted SmartPositionTaskStatus = "COMPLETED"
	TaskStatusFailed    SmartPositionTaskStatus = "FAILED"
)

type SmartPositionStage string

const (
	StageInitContext      SmartPositionStage = "init_context"
	StageDataAnalyst      SmartPositionStage = "data_analyst_agent"
	StageContentIntel     SmartPositionStage = "content_intel_agent"
	StageRiskExecution    SmartPositionStage = "risk_execution_agent"
	StageCoordinatorMerge SmartPositionStage = "coordinator_merge"
	StageSummaryGenerate  SmartPositionStage = "summary_generate"
)

type SmartPositionEventType string

const (
	EventStarted   SmartPositionEventType = "started"
	EventProgress  SmartPositionEventType = "progress"
	EventPartial   SmartPositionEventType = "partial"
	EventCompleted SmartPositionEventType = "completed"
	EventFailed    SmartPositionEventType = "failed"
	EventHeartbeat SmartPositionEventType = "heartbeat"
)

// SmartPositionProgressEvent 是后端 SSE 和任务快照共用的统一事件体。
// 统一事件结构可以让前端用同一套 reducer 同时处理实时推送和断线补偿。
type SmartPositionProgressEvent struct {
	Type            SmartPositionEventType  `json:"type"`
	TaskID          string                  `json:"task_id"`
	Stage           SmartPositionStage      `json:"stage"`
	Message         string                  `json:"message"`
	Progress        int                     `json:"progress"`
	Status          SmartPositionTaskStatus `json:"status"`
	Warnings        []string                `json:"warnings,omitempty"`
	DegradedModules []string                `json:"degraded_modules,omitempty"`
	Result          *SmartPositionResponse  `json:"result,omitempty"`
	Error           string                  `json:"error,omitempty"`
	Timestamp       string                  `json:"timestamp"`
}

type SmartPositionTaskSnapshot struct {
	TaskID          string                      `json:"task_id"`
	Status          SmartPositionTaskStatus     `json:"status"`
	CurrentStage    SmartPositionStage          `json:"current_stage"`
	ProgressPercent int                         `json:"progress_percent"`
	Warnings        []string                    `json:"warnings,omitempty"`
	DegradedModules []string                    `json:"degraded_modules,omitempty"`
	Result          *SmartPositionResponse      `json:"result,omitempty"`
	Error           string                      `json:"error,omitempty"`
	CreatedAt       string                      `json:"created_at"`
	UpdatedAt       string                      `json:"updated_at"`
	LastEvent       *SmartPositionProgressEvent `json:"last_event,omitempty"`
}

type DataAnalystResult struct {
	MainforceCost float64
	CostZone      [2]float64
	Support       float64
	Resistance    float64
	BuyRange      [2]float64
	NetInflow10D  float64
	Label         string
	Insight       string
}

type ContentIntelResult struct {
	ReportSummary    string
	RatingTilt       string
	SentimentScore   float64
	DivergenceIndex  float64
	ConfidenceHint   string
	ReportCount      int
	ReportHighlights []string
}

type RiskExecutionResult struct {
	ATR             float64
	StopLoss        float64
	SuggestedShares int64
	SuggestedAmount float64
	SuggestedRatio  float64
	TargetShort     float64
	TargetMid       float64
	RiskLevel       string
	Advice          string
}

// GraphState 是 Graph 节点之间传递的共享工作载体。
// 每个分支节点返回 state 的副本，避免并行路径共享同一个指针导致数据竞争。
type GraphState struct {
	Request         SmartPositionRequest
	TraceID         string
	StartedAt       time.Time
	Quote           *service.Quote
	Valuation       *model.StockValuation
	DataAnalyst     *DataAnalystResult
	ContentIntel    *ContentIntelResult
	RiskExecution   *RiskExecutionResult
	Response        *SmartPositionResponse
	Warnings        []string
	DegradedModules []string
	Confidence      float64
}

func (r SmartPositionRequest) Normalize() (SmartPositionRequest, error) {
	r.StockCode = strings.TrimSpace(strings.ToUpper(r.StockCode))
	if len(r.StockCode) != 6 {
		return r, fmt.Errorf("stock_code 格式错误（应为 6 位数字）")
	}
	if r.TotalCapital <= 0 {
		return r, fmt.Errorf("total_capital 必须大于 0")
	}
	if r.MaxRiskRatio <= 0 {
		r.MaxRiskRatio = DefaultMaxRiskRatio
	}
	if r.ATRMultiplier <= 0 {
		r.ATRMultiplier = DefaultATRMultiple
	}
	if r.AnalysisWindow <= 0 {
		r.AnalysisWindow = DefaultAnalysisDays
	}
	return r, nil
}

func (r SmartPositionExecuteRequest) ToAnalyzeRequest() SmartPositionRequest {
	return SmartPositionRequest{
		StockCode:      r.StockCode,
		TotalCapital:   r.TotalCapital,
		MaxRiskRatio:   r.MaxRiskRatio,
		ATRMultiplier:  r.ATRMultiplier,
		AnalysisWindow: r.AnalysisWindow,
	}
}

func (s *GraphState) Clone() *GraphState {
	if s == nil {
		return nil
	}
	cp := *s
	cp.Warnings = slices.Clone(s.Warnings)
	cp.DegradedModules = slices.Clone(s.DegradedModules)
	return &cp
}

func MergeStates(states []*GraphState) (*GraphState, error) {
	var merged *GraphState
	warningSet := map[string]struct{}{}
	degradedSet := map[string]struct{}{}
	confidence := 1.0
	for _, state := range states {
		if state == nil {
			continue
		}
		if merged == nil {
			merged = state.Clone()
			for _, warning := range merged.Warnings {
				warningSet[warning] = struct{}{}
			}
			for _, module := range merged.DegradedModules {
				degradedSet[module] = struct{}{}
			}
		}
		if merged.Quote == nil && state.Quote != nil {
			merged.Quote = state.Quote
		}
		if merged.Valuation == nil && state.Valuation != nil {
			merged.Valuation = state.Valuation
		}
		if merged.DataAnalyst == nil && state.DataAnalyst != nil {
			merged.DataAnalyst = state.DataAnalyst
		}
		if merged.ContentIntel == nil && state.ContentIntel != nil {
			merged.ContentIntel = state.ContentIntel
		}
		if merged.RiskExecution == nil && state.RiskExecution != nil {
			merged.RiskExecution = state.RiskExecution
		}
		for _, warning := range state.Warnings {
			if _, ok := warningSet[warning]; ok {
				continue
			}
			warningSet[warning] = struct{}{}
			merged.Warnings = append(merged.Warnings, warning)
		}
		for _, module := range state.DegradedModules {
			if _, ok := degradedSet[module]; ok {
				continue
			}
			degradedSet[module] = struct{}{}
			merged.DegradedModules = append(merged.DegradedModules, module)
		}
		if state.Confidence > 0 && state.Confidence < confidence {
			confidence = state.Confidence
		}
	}
	if merged == nil {
		return nil, fmt.Errorf("empty graph state")
	}
	if confidence <= 0 || math.IsInf(confidence, 0) || math.IsNaN(confidence) {
		confidence = 0.35
	}
	merged.Confidence = round4(confidence)
	return merged, nil
}

func round2(v float64) float64 {
	return math.Round(v*100) / 100
}

func round4(v float64) float64 {
	return math.Round(v*10000) / 10000
}
