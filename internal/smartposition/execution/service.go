package execution

import (
	"context"

	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/ports"
)

// Service 将分析结果映射到现有自选股与买入计划体系。
// 分析链和执行链分离，避免 Graph 主链路被副作用污染。
type Service struct {
	watchlist ports.WatchlistPort
	buyPlan   ports.BuyPlanPort
}

func NewService(watchlist ports.WatchlistPort, buyPlan ports.BuyPlanPort) *Service {
	return &Service{
		watchlist: watchlist,
		buyPlan:   buyPlan,
	}
}

func (s *Service) Execute(ctx context.Context, userID int64, analysis *domain.SmartPositionResponse) (*domain.SmartPositionExecuteResponse, error) {
	added, err := s.watchlist.Add(ctx, userID, analysis.StockCode)
	if err != nil {
		return nil, err
	}
	planID, err := s.buyPlan.Create(ctx, userID, ports.BuyPlanCreateInput{
		Code:          analysis.StockCode,
		Name:          analysis.StockName,
		BuyRange:      analysis.KeyPrices.BuyRange,
		StopLoss:      analysis.KeyPrices.StopLoss,
		TargetShort:   analysis.KeyPrices.TargetShort,
		SuggestedAmt:  analysis.Position.SuggestedAmount,
		SuggestedRate: analysis.Position.SuggestedRatio,
		Summary:       analysis.Summary,
	})
	if err != nil {
		return nil, err
	}
	return &domain.SmartPositionExecuteResponse{
		Analysis:       analysis,
		WatchlistAdded: added,
		BuyPlanID:      planID,
	}, nil
}
