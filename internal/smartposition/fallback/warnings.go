package fallback

import (
	"fmt"

	"stock-backend/internal/smartposition/domain"
)

func ApplyAgentFailure(state *domain.GraphState, module string, err error) *domain.GraphState {
	next := state.Clone()
	next.DegradedModules = append(next.DegradedModules, module)
	next.Warnings = append(next.Warnings, fmt.Sprintf("%s 模块暂时不可用：%v", module, err))
	if next.Confidence == 0 || next.Confidence > 0.55 {
		next.Confidence = 0.55
	}
	return next
}
