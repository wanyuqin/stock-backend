package summary

import (
	"fmt"
	"strings"

	"stock-backend/internal/smartposition/domain"
)

// BuildSummary 使用结构化结果拼装最终摘要。
// 首版采用可解释的规则文案，避免把最终输出完全绑定到外部 LLM 稳定性。
func BuildSummary(state *domain.GraphState) string {
	data := state.DataAnalyst
	content := state.ContentIntel
	risk := state.RiskExecution
	if data == nil || risk == nil {
		return "当前数据不足，建议稍后重试。"
	}
	parts := []string{
		fmt.Sprintf("%s 目前主力成本区约在 %.2f~%.2f 元", state.Quote.Name, data.CostZone[0], data.CostZone[1]),
		fmt.Sprintf("资金面信号为「%s」", data.Label),
	}
	if content != nil {
		parts = append(parts, fmt.Sprintf("研报观点%s，情绪分 %.0f", content.RatingTilt, content.SentimentScore))
	}
	parts = append(parts,
		fmt.Sprintf("建议关注 %.2f~%.2f 元买入区间", data.BuyRange[0], data.BuyRange[1]),
		fmt.Sprintf("止损位 %.2f 元", risk.StopLoss),
		fmt.Sprintf("建议买入 %.0f 元（%.1f%% 仓位）", risk.SuggestedAmount, risk.SuggestedRatio*100),
		fmt.Sprintf("短中期目标参考 %.2f / %.2f 元", risk.TargetShort, risk.TargetMid),
	)
	return strings.Join(parts, "，") + "。"
}
