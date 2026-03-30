package agents

import (
	"context"
	"fmt"
	"strings"

	"stock-backend/internal/model"
	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/tools"
)

// ContentIntelAgent 汇总研报、评级与市场情绪。
// 首版不依赖新的 LLM 总结链，而是优先消费现有 stock_reports 中已存在的 AI 摘要。
type ContentIntelAgent struct {
	toolset *tools.ToolSet
}

func NewContentIntelAgent(toolset *tools.ToolSet) *ContentIntelAgent {
	return &ContentIntelAgent{toolset: toolset}
}

func (a *ContentIntelAgent) Run(ctx context.Context, state *domain.GraphState) (*domain.GraphState, error) {
	next := state.Clone()

	reports, err := tools.InvokeJSON[tools.StockReportToolInput, []*model.StockReport](ctx, a.toolset.StockReport, tools.StockReportToolInput{
		Code:  state.Request.StockCode,
		Limit: 6,
	})
	if err != nil {
		return nil, fmt.Errorf("content intel: stock report tool: %w", err)
	}
	marketSummary, err := tools.InvokeJSON[struct{}, map[string]any](ctx, a.toolset.MarketSentiment, struct{}{})
	if err != nil {
		return nil, fmt.Errorf("content intel: market sentiment tool: %w", err)
	}

	highlights, ratingTilt, sentimentScore, divergence := summarizeReports(reports)
	if score, ok := marketSummary["sentiment_score"].(float64); ok {
		sentimentScore = (sentimentScore*0.7 + score*0.3)
	}

	next.ContentIntel = &domain.ContentIntelResult{
		ReportSummary:    strings.Join(highlights, "；"),
		RatingTilt:       ratingTilt,
		SentimentScore:   sentimentScore,
		DivergenceIndex:  divergence,
		ConfidenceHint:   confidenceHint(len(reports), divergence),
		ReportCount:      len(reports),
		ReportHighlights: highlights,
	}
	next.Confidence = 0.85
	return next, nil
}

func summarizeReports(reports []*model.StockReport) ([]string, string, float64, float64) {
	if len(reports) == 0 {
		return []string{"暂无最新研报，情报模块以市场情绪替代"}, "研报不足", 45, 50
	}
	buyCount := 0
	neutralCount := 0
	highlights := make([]string, 0, min(len(reports), 3))
	for i, report := range reports {
		rating := strings.TrimSpace(report.RatingName)
		if strings.Contains(rating, "买") || strings.Contains(strings.ToLower(rating), "buy") {
			buyCount++
		} else {
			neutralCount++
		}
		summary := strings.TrimSpace(report.AISummary)
		if summary == "" {
			summary = strings.TrimSpace(report.Title)
		}
		if i < 3 {
			highlights = append(highlights, summary)
		}
	}
	reportCount := float64(len(reports))
	buyRatio := float64(buyCount) / reportCount
	sentiment := 50 + buyRatio*35 - float64(neutralCount)*3
	if sentiment < 20 {
		sentiment = 20
	}
	if sentiment > 90 {
		sentiment = 90
	}
	divergence := 100 - mathAbs(float64(buyCount-neutralCount))/reportCount*100
	if divergence < 10 {
		divergence = 10
	}
	ratingTilt := fmt.Sprintf("%d 家偏多 / %d 家中性", buyCount, neutralCount)
	return highlights, ratingTilt, sentiment, divergence
}

func confidenceHint(reportCount int, divergence float64) string {
	switch {
	case reportCount == 0:
		return "研报覆盖不足，建议降低情报权重"
	case divergence >= 70:
		return "研报分歧较大，建议更重视资金面确认"
	default:
		return "研报观点相对集中，可作为辅助确认信号"
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func mathAbs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
