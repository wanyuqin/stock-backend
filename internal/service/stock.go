package service

import (
	"strings"

	"go.uber.org/zap"
)

// StockService 封装股票行情业务逻辑。
// GetKLine 方法在 kline_service.go 中实现。
type StockService struct {
	market              *MarketProvider
	log                 *zap.Logger
	defaultMarketSource string // qq | em
}

func NewStockService(log *zap.Logger) *StockService {
	return NewStockServiceWithSource(log, "qq")
}

func NewStockServiceWithSource(log *zap.Logger, defaultSource string) *StockService {
	return &StockService{
		market:              NewMarketProvider(log),
		log:                 log,
		defaultMarketSource: normalizeMarketSource(defaultSource),
	}
}

func (s *StockService) DefaultMarketSource() string { return s.defaultMarketSource }

// GetRealtimeQuote 获取单只股票实时行情（5s 内存缓存）。
func (s *StockService) GetRealtimeQuote(code string) (*Quote, error) {
	return s.GetRealtimeQuoteBySource(code, "")
}

// GetRealtimeQuoteBySource 按数据源获取实时行情（当前 qq 为主，em 会降级到 qq）。
func (s *StockService) GetRealtimeQuoteBySource(code, source string) (*Quote, error) {
	switch normalizeMarketSource(sourceOrDefault(source, s.defaultMarketSource)) {
	case "qq":
		return s.market.FetchRealtimeQuote(code)
	case "em":
		// 当前未保留东财 quote 解析链路，先平滑降级到腾讯，避免调用方报错。
		s.log.Warn("GetRealtimeQuoteBySource: em not implemented, fallback to qq",
			zap.String("code", code))
		recordDataSourceFallback("quote", "em", "qq")
		return s.market.FetchRealtimeQuote(code)
	default:
		return s.market.FetchRealtimeQuote(code)
	}
}

// GetKLineBySource 按数据源获取日K（当前 qq 为主，em 会降级到 qq）。
func (s *StockService) GetKLineBySource(code string, limit int, source string) (*KLineResponse, error) {
	switch normalizeMarketSource(sourceOrDefault(source, s.defaultMarketSource)) {
	case "qq":
		return s.GetKLine(code, limit)
	case "em":
		s.log.Warn("GetKLineBySource: em not implemented, fallback to qq",
			zap.String("code", code))
		recordDataSourceFallback("kline", "em", "qq")
		return s.GetKLine(code, limit)
	default:
		return s.GetKLine(code, limit)
	}
}

func sourceOrDefault(source, fallback string) string {
	if strings.TrimSpace(source) == "" {
		return fallback
	}
	return source
}

func normalizeMarketSource(source string) string {
	s := strings.ToLower(strings.TrimSpace(source))
	switch s {
	case "", "qq", "tencent", "tx":
		return "qq"
	case "em", "eastmoney", "dfcf":
		return "em"
	default:
		return "qq"
	}
}

// GetMultipleQuotes 并发批量获取多只股票实时行情。
func (s *StockService) GetMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	return s.market.FetchMultipleQuotes(codes)
}

// GetMultipleQuotesBySource 按来源批量取行情，暂统一走腾讯。
func (s *StockService) GetMultipleQuotesBySource(codes []string, source string) (map[string]*Quote, []error) {
	norm := normalizeMarketSource(sourceOrDefault(source, s.defaultMarketSource))
	if norm == "em" {
		s.log.Warn("GetMultipleQuotesBySource: em not implemented, fallback to qq")
		recordDataSourceFallback("quote_batch", "em", "qq")
	}
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
