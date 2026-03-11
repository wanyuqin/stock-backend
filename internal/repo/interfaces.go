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
	UpdateMoneyFlow(ctx context.Context, code string, inflow float64) error
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
	BatchInsertScans(ctx context.Context, scans []*model.DailyScan) error
	ListScansByDate(ctx context.Context, date time.Time) ([]*model.DailyScan, error)
	UpsertReport(ctx context.Context, report *model.DailyReport) error
	GetReportByDate(ctx context.Context, date time.Time) (*model.DailyReport, error)
	ListReports(ctx context.Context, limit int) ([]*model.DailyReport, error)
}

// MoneyFlowRepo 资金流向日志访问接口。
type MoneyFlowRepo interface {
	Insert(ctx context.Context, mf *model.MoneyFlowLog) error
	ListByCode(ctx context.Context, code string, limit int) ([]*model.MoneyFlowLog, error)
	LatestByCode(ctx context.Context, code string) (*model.MoneyFlowLog, error)
}

// AlertRepo 告警事件访问接口。
type AlertRepo interface {
	Create(ctx context.Context, a *model.Alert) error
	ListUnread(ctx context.Context, limit int) ([]*model.Alert, error)
	ListRecent(ctx context.Context, limit int) ([]*model.Alert, error)
	MarkRead(ctx context.Context, ids []int64) error
}

// PositionGuardianRepo 持仓明细 & 诊断快照访问接口。
type PositionGuardianRepo interface {
	// ListAll 返回所有 quantity>0 的持仓
	ListAll(ctx context.Context) ([]*model.PositionDetail, error)
	// GetByCode 按股票代码查单条
	GetByCode(ctx context.Context, code string) (*model.PositionDetail, error)
	// Upsert 按 stock_code 唯一键插入或更新
	Upsert(ctx context.Context, p *model.PositionDetail) error
	// SaveDiagnostic 写入诊断快照
	SaveDiagnostic(ctx context.Context, d *model.PositionDiagnostic) error
}

// SnapshotRepo 全市场每日快照访问接口。
type SnapshotRepo interface {
	// BulkUpsert 批量写入/更新快照（ON CONFLICT (trade_date, code) DO UPDATE）。
	BulkUpsert(ctx context.Context, snapshots []*model.StockDailySnapshot) error
	// ListByDate 读取某日全量快照，供筛选器内存打分。
	ListByDate(ctx context.Context, date time.Time) ([]*model.StockDailySnapshot, error)
	// CountByDate 返回某日快照总数（快速健康检查）。
	CountByDate(ctx context.Context, date time.Time) (int64, error)
}
