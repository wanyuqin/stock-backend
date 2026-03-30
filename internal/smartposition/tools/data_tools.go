package tools

import (
	"context"
	"strings"

	einotool "github.com/cloudwego/eino/components/tool"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition/ports"
)

type QuoteToolInput struct {
	Code string `json:"code"`
}

type KLineToolInput struct {
	Code  string `json:"code"`
	Limit int    `json:"limit"`
}

type BigDealToolInput struct {
	Code       string  `json:"code"`
	ChangeRate float64 `json:"change_rate"`
}

type ValuationToolInput struct {
	Code string `json:"code"`
}

type StockReportToolInput struct {
	Code  string `json:"code"`
	Limit int    `json:"limit"`
}

type RiskProfileToolInput struct {
	UserID int64 `json:"user_id"`
}

type WatchlistToolInput struct {
	UserID int64  `json:"user_id"`
	Code   string `json:"code,omitempty"`
}

type BuyPlanToolInput struct {
	UserID int64                     `json:"user_id"`
	Code   string                    `json:"code,omitempty"`
	Create *ports.BuyPlanCreateInput `json:"create,omitempty"`
}

type quoteTool struct{ einotool.InvokableTool }
type klineTool struct{ einotool.InvokableTool }
type bigDealTool struct{ einotool.InvokableTool }
type valuationTool struct{ einotool.InvokableTool }
type stockReportTool struct{ einotool.InvokableTool }
type marketSentimentTool struct{ einotool.InvokableTool }
type riskProfileTool struct{ einotool.InvokableTool }
type watchlistTool struct{ einotool.InvokableTool }
type buyPlanTool struct{ einotool.InvokableTool }

type ToolSet struct {
	Quote           einotool.InvokableTool
	KLine           einotool.InvokableTool
	BigDeal         einotool.InvokableTool
	Valuation       einotool.InvokableTool
	StockReport     einotool.InvokableTool
	MarketSentiment einotool.InvokableTool
	RiskProfile     einotool.InvokableTool
	Watchlist       einotool.InvokableTool
	BuyPlan         einotool.InvokableTool
}

func NewToolSet(deps ports.Repositories) *ToolSet {
	return &ToolSet{
		Quote: quoteTool{NewJSONTool("quote_tool", "读取股票实时行情", func(ctx context.Context, req QuoteToolInput) (*service.Quote, error) {
			return deps.MarketData.GetQuote(ctx, req.Code)
		})},
		KLine: klineTool{NewJSONTool("kline_tool", "读取股票日线 K 线", func(ctx context.Context, req KLineToolInput) (*service.KLineResponse, error) {
			if req.Limit <= 0 {
				req.Limit = 120
			}
			return deps.MarketData.GetKLine(ctx, req.Code, req.Limit)
		})},
		BigDeal: bigDealTool{NewJSONTool("big_deal_tool", "读取主力大单分析结果", func(ctx context.Context, req BigDealToolInput) (*service.BigDealSummary, error) {
			return deps.BigDeal.GetBigDeal(ctx, req.Code, req.ChangeRate)
		})},
		Valuation: valuationTool{NewJSONTool("valuation_tool", "读取当前估值快照", func(ctx context.Context, req ValuationToolInput) (*model.StockValuation, error) {
			return deps.Valuation.GetSnapshot(ctx, req.Code)
		})},
		StockReport: stockReportTool{NewJSONTool("stock_report_tool", "读取个股最新研报", func(ctx context.Context, req StockReportToolInput) ([]*model.StockReport, error) {
			limit := req.Limit
			if limit <= 0 {
				limit = 6
			}
			return deps.StockReport.List(ctx, req.Code, limit)
		})},
		MarketSentiment: marketSentimentTool{NewJSONTool("market_sentiment_tool", "读取市场情绪概览", func(ctx context.Context, _ struct{}) (*service.MarketSummaryDTO, error) {
			return deps.MarketSentiment.GetSummary(ctx)
		})},
		RiskProfile: riskProfileTool{NewJSONTool("risk_profile_tool", "读取用户默认风控配置", func(ctx context.Context, req RiskProfileToolInput) (*model.UserRiskProfile, error) {
			return deps.RiskProfile.GetProfile(ctx, req.UserID)
		})},
		Watchlist: watchlistTool{NewJSONTool("watchlist_tool", "读取或新增自选股", func(ctx context.Context, req WatchlistToolInput) (map[string]any, error) {
			if strings.TrimSpace(req.Code) != "" {
				added, err := deps.Watchlist.Add(ctx, req.UserID, req.Code)
				if err != nil {
					return nil, err
				}
				return map[string]any{"added": added}, nil
			}
			items, err := deps.Watchlist.ListByUser(ctx, req.UserID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"items": items}, nil
		})},
		BuyPlan: buyPlanTool{NewJSONTool("buy_plan_tool", "读取或创建买入计划", func(ctx context.Context, req BuyPlanToolInput) (map[string]any, error) {
			if req.Create != nil {
				id, err := deps.BuyPlan.Create(ctx, req.UserID, *req.Create)
				if err != nil {
					return nil, err
				}
				return map[string]any{"buy_plan_id": id}, nil
			}
			items, err := deps.BuyPlan.ListByCode(ctx, req.UserID, req.Code)
			if err != nil {
				return nil, err
			}
			return map[string]any{"items": items}, nil
		})},
	}
}

func BuildQQCode(code string) string {
	code = strings.TrimSpace(strings.ToLower(code))
	if strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") {
		return code
	}
	if code != "" && code[0] == '6' {
		return "sh" + code
	}
	return "sz" + code
}

func ReportsFromPage(page *repo.StockReportPage) []*model.StockReport {
	if page == nil {
		return []*model.StockReport{}
	}
	return page.Items
}
