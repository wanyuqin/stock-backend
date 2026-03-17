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

type StockReportQuery struct {
	StockCode string
	Page      int
	Limit     int
}

type StockReportPage struct {
	Total int64                `json:"total"`
	Items []*model.StockReport `json:"items"`
}

type StockReportRepo interface {
	BulkUpsert(ctx context.Context, reports []*model.StockReport) (int64, error)
	ListPending(ctx context.Context, limit int) ([]*model.StockReport, error)
	UpdateAISummary(ctx context.Context, id int64, summary string) error
	List(ctx context.Context, q StockReportQuery) (*StockReportPage, error)
}

// ─────────────────────────────────────────────────────────────────
// ValuationRepo 估值分位数据访问接口。
// ─────────────────────────────────────────────────────────────────

type ValuationRepo interface {
	// UpsertSnapshot 写入/更新最新估值快照
	UpsertSnapshot(ctx context.Context, v *model.StockValuation) error
	// GetSnapshot 获取单只股票的最新估值
	GetSnapshot(ctx context.Context, code string) (*model.StockValuation, error)
	// ListSnapshots 批量获取（供 watchlist summary 用）
	ListSnapshots(ctx context.Context, codes []string) ([]*model.StockValuation, error)
	// InsertHistory 写入每日历史记录（ON CONFLICT DO NOTHING）
	InsertHistory(ctx context.Context, h *model.StockValuationHistory) error
	// ListHistory 获取指定股票的历史 PE/PB 序列（升序，供分位计算）
	ListHistory(ctx context.Context, code string, limit int) ([]*model.StockValuationHistory, error)
	// CountHistory 统计历史天数
	CountHistory(ctx context.Context, code string) (int, error)
}
