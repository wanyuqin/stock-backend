package repo

import (
	"context"
	"stock-backend/internal/model"
)

// StockRepo 股票基础信息访问接口。
type StockRepo interface {
	GetByCode(ctx context.Context, code string) (*model.Stock, error)
	List(ctx context.Context, limit, offset int) ([]*model.Stock, error)
	Upsert(ctx context.Context, s *model.Stock) error
}

// WatchlistRepo 自选股数据访问接口。
type WatchlistRepo interface {
	ListByUser(ctx context.Context, userID int64) ([]*model.Watchlist, error)
	Add(ctx context.Context, w *model.Watchlist) error
	Remove(ctx context.Context, userID int64, stockCode string) error
}

// TradeLogRepo 交易日志数据访问接口。
type TradeLogRepo interface {
	// Create 新增一条交易记录。
	Create(ctx context.Context, t *model.TradeLog) error

	// ListByUser 分页查询某用户所有交易记录（traded_at 倒序）。
	ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*model.TradeLog, error)

	// ListByCode 查询某只股票的全部交易记录（traded_at 倒序）。
	// 用于 GET /api/v1/trades/:code
	ListByCode(ctx context.Context, userID int64, code string) ([]*model.TradeLog, error)

	// ListAllByUser 查询某用户所有交易记录（不分页，用于盈亏计算）。
	ListAllByUser(ctx context.Context, userID int64) ([]*model.TradeLog, error)
}

// AICacheRepo AI 缓存数据访问接口。
type AICacheRepo interface {
	Get(ctx context.Context, stockCode, prompt string) (*model.AICache, error)
	Set(ctx context.Context, cache *model.AICache) error
}
