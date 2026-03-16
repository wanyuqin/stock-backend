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
	ListAll(ctx context.Context) ([]*model.PositionDetail, error)
	GetByCode(ctx context.Context, code string) (*model.PositionDetail, error)
	Upsert(ctx context.Context, p *model.PositionDetail) error
	SaveDiagnostic(ctx context.Context, d *model.PositionDiagnostic) error
}

// SnapshotRepo 全市场每日快照访问接口。
type SnapshotRepo interface {
	BulkUpsert(ctx context.Context, snapshots []*model.StockDailySnapshot) error
	ListByDate(ctx context.Context, date time.Time) ([]*model.StockDailySnapshot, error)
	CountByDate(ctx context.Context, date time.Time) (int64, error)
}

// ─────────────────────────────────────────────────────────────────
// StockReportRepo 研报数据访问接口。
// ─────────────────────────────────────────────────────────────────

// StockReportQuery 分页查询参数。
type StockReportQuery struct {
	StockCode string // 可选，按代码筛选
	Page      int    // 从 1 开始
	Limit     int    // 每页条数，默认 20，最大 100
}

// StockReportPage 分页查询结果。
type StockReportPage struct {
	Total int64                 `json:"total"`
	Items []*model.StockReport  `json:"items"`
}

type StockReportRepo interface {
	// BulkUpsert 批量写入，info_code 冲突时忽略（幂等）
	BulkUpsert(ctx context.Context, reports []*model.StockReport) (int64, error)
	// ListPending 查询尚未处理 AI 摘要的记录
	ListPending(ctx context.Context, limit int) ([]*model.StockReport, error)
	// UpdateAISummary 更新 AI 摘要，标记已处理
	UpdateAISummary(ctx context.Context, id int64, summary string) error
	// List 分页查询（支持 stock_code 筛选）
	List(ctx context.Context, q StockReportQuery) (*StockReportPage, error)
}
