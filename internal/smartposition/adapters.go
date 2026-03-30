package smartposition

import (
	"context"
	"strings"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition/ports"
)

type marketDataAdapter struct{ stockSvc *service.StockService }

func (a marketDataAdapter) GetQuote(ctx context.Context, code string) (*service.Quote, error) {
	_ = ctx
	return a.stockSvc.GetRealtimeQuote(code)
}
func (a marketDataAdapter) GetKLine(ctx context.Context, code string, limit int) (*service.KLineResponse, error) {
	_ = ctx
	return a.stockSvc.GetKLine(code, limit)
}

type bigDealAdapter struct{ svc *service.BigDealService }

func (a bigDealAdapter) GetBigDeal(ctx context.Context, code string, changeRate float64) (*service.BigDealSummary, error) {
	return a.svc.GetBigDeal(ctx, code, changeRate)
}

type valuationAdapter struct{ repo repo.ValuationRepo }

func (a valuationAdapter) GetSnapshot(ctx context.Context, code string) (*model.StockValuation, error) {
	return a.repo.GetSnapshot(ctx, code)
}

type stockReportAdapter struct{ repo repo.StockReportRepo }

func (a stockReportAdapter) List(ctx context.Context, code string, limit int) ([]*model.StockReport, error) {
	page, err := a.repo.List(ctx, repo.StockReportQuery{StockCode: code, Page: 1, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

type marketSentimentAdapter struct {
	svc *service.MarketSentinelService
}

func (a marketSentimentAdapter) GetSummary(ctx context.Context) (*service.MarketSummaryDTO, error) {
	return a.svc.GetSummary(ctx)
}

type riskProfileAdapter struct{ repo repo.RiskRepo }

func (a riskProfileAdapter) GetProfile(ctx context.Context, userID int64) (*model.UserRiskProfile, error) {
	return a.repo.GetOrInitProfile(ctx, userID)
}

type watchlistAdapter struct {
	repo      repo.WatchlistRepo
	stockRepo repo.StockRepo
	stockSvc  *service.StockService
}

func (a watchlistAdapter) ListByUser(ctx context.Context, userID int64) ([]*model.Watchlist, error) {
	return a.repo.ListByUser(ctx, userID)
}

func (a watchlistAdapter) Add(ctx context.Context, userID int64, code string) (bool, error) {
	items, err := a.repo.ListByUser(ctx, userID)
	if err != nil {
		return false, err
	}
	for _, item := range items {
		if item.StockCode == code {
			return false, nil
		}
	}
	if _, err := a.stockRepo.GetByCode(ctx, code); err != nil {
		quote, qErr := a.stockSvc.GetRealtimeQuote(code)
		if qErr != nil {
			return false, qErr
		}
		market := model.MarketSH
		if len(code) == 6 && (code[0] == '0' || code[0] == '3') {
			market = model.MarketSZ
		}
		_ = a.stockRepo.Upsert(ctx, &model.Stock{Code: code, Name: quote.Name, Market: market})
	}
	if err := a.repo.Add(ctx, &model.Watchlist{UserID: userID, StockCode: code}); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "duplicate") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

type buyPlanAdapter struct{ svc *service.BuyPlanService }

func (a buyPlanAdapter) Create(ctx context.Context, userID int64, input ports.BuyPlanCreateInput) (int64, error) {
	positionRatio := input.SuggestedRate * 100
	plan, err := a.svc.Create(ctx, userID, &service.CreateBuyPlanRequest{
		StockCode:     input.Code,
		BuyPrice:      floatPtr(input.BuyRange[0]),
		BuyPriceHigh:  floatPtr(input.BuyRange[1]),
		TargetPrice:   floatPtr(input.TargetShort),
		StopLossPrice: floatPtr(input.StopLoss),
		PlannedAmount: floatPtr(input.SuggestedAmt),
		PositionRatio: floatPtr(positionRatio),
		BuyBatches:    2,
		Reason:        input.Summary,
		TriggerConditions: model.TriggerConditions{
			CustomNote: "智能建仓一键执行生成",
		},
	})
	if err != nil {
		return 0, err
	}
	return plan.ID, nil
}

func (a buyPlanAdapter) ListByCode(ctx context.Context, userID int64, code string) ([]map[string]any, error) {
	plans, err := a.svc.ListByCode(ctx, userID, code)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(plans))
	for _, plan := range plans {
		items = append(items, map[string]any{
			"id":         plan.ID,
			"stock_code": plan.StockCode,
			"status":     plan.Status,
		})
	}
	return items, nil
}

func NewRepositories(
	stockSvc *service.StockService,
	bigDealSvc *service.BigDealService,
	valuationRepo repo.ValuationRepo,
	stockReportRepo repo.StockReportRepo,
	marketSentinelSvc *service.MarketSentinelService,
	riskRepo repo.RiskRepo,
	watchlistRepo repo.WatchlistRepo,
	stockRepo repo.StockRepo,
	buyPlanSvc *service.BuyPlanService,
) ports.Repositories {
	return ports.Repositories{
		MarketData:      marketDataAdapter{stockSvc: stockSvc},
		BigDeal:         bigDealAdapter{svc: bigDealSvc},
		Valuation:       valuationAdapter{repo: valuationRepo},
		StockReport:     stockReportAdapter{repo: stockReportRepo},
		MarketSentiment: marketSentimentAdapter{svc: marketSentinelSvc},
		RiskProfile:     riskProfileAdapter{repo: riskRepo},
		Watchlist:       watchlistAdapter{repo: watchlistRepo, stockRepo: stockRepo, stockSvc: stockSvc},
		BuyPlan:         buyPlanAdapter{svc: buyPlanSvc},
	}
}

func floatPtr(v float64) *float64 { return &v }
