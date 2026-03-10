package repo

import (
	"context"
	"time"

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
	Create(ctx context.Context, t *model.TradeLog) error
	ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*model.TradeLog, error)
	ListByCode(ctx context.Context, userID int64, code string) ([]*model.TradeLog, error)
	ListAllByUser(ctx context.Context, userID int64) ([]*model.TradeLog, error)
}

// AICacheRepo AI 缓存数据访问接口。
type AICacheRepo interface {
	Get(ctx context.Context, stockCode, prompt string) (*model.AICache, error)
	Set(ctx context.Context, cache *model.AICache) error
}

// ScanRepo 扫描结果 & 日报数据访问接口。
type ScanRepo interface {
	// ── daily_scans ───────────────────────────────────────────────

	// BatchInsertScans 批量写入当次扫描命中的记录（同一 scan_date 可多次写入）。
	BatchInsertScans(ctx context.Context, scans []*model.DailyScan) error

	// ListScansByDate 查询某日所有扫描结果（scan_date 倒序创建时间）。
	ListScansByDate(ctx context.Context, date time.Time) ([]*model.DailyScan, error)

	// ── daily_reports ─────────────────────────────────────────────

	// UpsertReport 写入或覆盖某日报告（依赖 report_date UNIQUE 约束做 ON CONFLICT UPDATE）。
	UpsertReport(ctx context.Context, report *model.DailyReport) error

	// GetReportByDate 查询某日报告，不存在返回 nil, nil。
	GetReportByDate(ctx context.Context, date time.Time) (*model.DailyReport, error)

	// ListReports 按 report_date 倒序列出最近 n 条报告。
	ListReports(ctx context.Context, limit int) ([]*model.DailyReport, error)
}
