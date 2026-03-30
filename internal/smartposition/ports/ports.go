package ports

import (
	"context"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition/domain"
)

// MarketDataPort 聚合了智能建仓最常用的实时行情与 K 线读取。
type MarketDataPort interface {
	GetQuote(ctx context.Context, code string) (*service.Quote, error)
	GetKLine(ctx context.Context, code string, limit int) (*service.KLineResponse, error)
}

type BigDealPort interface {
	GetBigDeal(ctx context.Context, code string, changeRate float64) (*service.BigDealSummary, error)
}

type ValuationPort interface {
	GetSnapshot(ctx context.Context, code string) (*model.StockValuation, error)
}

type StockReportPort interface {
	List(ctx context.Context, code string, limit int) ([]*model.StockReport, error)
}

type MarketSentimentPort interface {
	GetSummary(ctx context.Context) (*service.MarketSummaryDTO, error)
}

type RiskProfilePort interface {
	GetProfile(ctx context.Context, userID int64) (*model.UserRiskProfile, error)
}

type WatchlistPort interface {
	ListByUser(ctx context.Context, userID int64) ([]*model.Watchlist, error)
	Add(ctx context.Context, userID int64, code string) (bool, error)
}

type BuyPlanCreateInput struct {
	Code          string
	Name          string
	BuyRange      [2]float64
	StopLoss      float64
	TargetShort   float64
	SuggestedAmt  float64
	SuggestedRate float64
	Summary       string
}

type BuyPlanPort interface {
	Create(ctx context.Context, userID int64, input BuyPlanCreateInput) (int64, error)
	ListByCode(ctx context.Context, userID int64, code string) ([]map[string]any, error)
}

type AnalyzerPort interface {
	Analyze(ctx context.Context, req domain.SmartPositionRequest) (*domain.SmartPositionResponse, error)
}

type Repositories struct {
	MarketData      MarketDataPort
	BigDeal         BigDealPort
	Valuation       ValuationPort
	StockReport     StockReportPort
	MarketSentiment MarketSentimentPort
	RiskProfile     RiskProfilePort
	Watchlist       WatchlistPort
	BuyPlan         BuyPlanPort
}

type StockReportQuery = repo.StockReportQuery
