package service

import "go.uber.org/zap"

// StockService 封装股票行情业务逻辑。
// GetKLine 方法在 kline_service.go 中实现。
type StockService struct {
	market *MarketProvider
	log    *zap.Logger
}

func NewStockService(log *zap.Logger) *StockService {
	return &StockService{
		market: NewMarketProvider(log),
		log:    log,
	}
}

// GetRealtimeQuote 获取单只股票实时行情（5s 内存缓存）。
func (s *StockService) GetRealtimeQuote(code string) (*Quote, error) {
	return s.market.FetchRealtimeQuote(code)
}

// GetMultipleQuotes 并发批量获取多只股票实时行情。
func (s *StockService) GetMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	return s.market.FetchMultipleQuotes(codes)
}

// ─────────────────────────────────────────────────────────────────
// WatchlistService（业务逻辑较简，直接由 handler 调用 repo 实现）
// ─────────────────────────────────────────────────────────────────

type WatchlistService struct{ log *zap.Logger }

func NewWatchlistService(log *zap.Logger) *WatchlistService {
	return &WatchlistService{log: log}
}

// TradeService 和 AIAnalysisService 定义在各自独立文件中：
//   trade_service.go        — TradeService
//   ai_analysis_service.go  — AIAnalysisService
