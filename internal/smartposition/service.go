package smartposition

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
	"go.uber.org/zap"

	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/execution"
	"stock-backend/internal/smartposition/graph"
	"stock-backend/internal/smartposition/ports"
	"stock-backend/internal/smartposition/tools"
)

type Service struct {
	graph     compose.Runnable[*domain.SmartPositionRequest, *domain.SmartPositionResponse]
	execution *execution.Service
	tasks     *taskManager
	log       *zap.Logger
}

func NewService(ctx context.Context, deps ports.Repositories, log *zap.Logger) (*Service, error) {
	toolset := tools.NewToolSet(deps)
	builder := graph.NewBuilder(toolset)
	runnable, err := builder.Build(ctx)
	if err != nil {
		return nil, fmt.Errorf("build smart position graph: %w", err)
	}
	return &Service{
		graph:     runnable,
		execution: execution.NewService(deps.Watchlist, deps.BuyPlan),
		tasks:     newTaskManager(log),
		log:       log,
	}, nil
}

func (s *Service) Analyze(ctx context.Context, req domain.SmartPositionRequest) (*domain.SmartPositionResponse, error) {
	resp, err := s.graph.Invoke(ctx, &req)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *Service) Execute(ctx context.Context, userID int64, req domain.SmartPositionExecuteRequest) (*domain.SmartPositionExecuteResponse, error) {
	analysis, err := s.Analyze(ctx, req.ToAnalyzeRequest())
	if err != nil {
		return nil, err
	}
	return s.execution.Execute(ctx, userID, analysis)
}
