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
	GetByID(ctx context.Context, id int64) (*model.TradeLog, error)
	Update(ctx context.Context, t *model.TradeLog) error
	Delete(ctx context.Context, userID, id int64) error
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
	ListDiagnosticsByCodes(ctx context.Context, codes []string, from time.Time) ([]*model.PositionDiagnostic, error)
}

// SnapshotRepo 全市场每日快照访问接口。
type SnapshotRepo interface {
	BulkUpsert(ctx context.Context, snapshots []*model.StockDailySnapshot) error
	ListByDate(ctx context.Context, date time.Time) ([]*model.StockDailySnapshot, error)
	CountByDate(ctx context.Context, date time.Time) (int64, error)
}

// StockReportRepo 研报数据访问接口。
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

// SectorRepo 板块映射缓存数据访问接口。
type SectorRepo interface {
	GetRelation(ctx context.Context, stockCode string) (*model.StockSectorRelation, error)
	UpsertRelation(ctx context.Context, rel *model.StockSectorRelation) error
	UpsertSector(ctx context.Context, s *model.Sector) error
}

// ValuationRepo 估值分位数据访问接口。
type ValuationRepo interface {
	UpsertSnapshot(ctx context.Context, v *model.StockValuation) error
	GetSnapshot(ctx context.Context, code string) (*model.StockValuation, error)
	ListSnapshots(ctx context.Context, codes []string) ([]*model.StockValuation, error)
	InsertHistory(ctx context.Context, h *model.StockValuationHistory) error
	ListHistory(ctx context.Context, code string, limit int) ([]*model.StockValuationHistory, error)
	CountHistory(ctx context.Context, code string) (int, error)
}

// BuyPlanRepo 买入计划数据访问接口。
type BuyPlanRepo interface {
	Create(ctx context.Context, p *model.BuyPlan) error
	Update(ctx context.Context, p *model.BuyPlan) error
	GetByID(ctx context.Context, id int64) (*model.BuyPlan, error)
	ListByUser(ctx context.Context, userID int64, statuses []model.BuyPlanStatus) ([]*model.BuyPlan, error)
	ListByCode(ctx context.Context, userID int64, code string) ([]*model.BuyPlan, error)
	UpdateStatus(ctx context.Context, id int64, status model.BuyPlanStatus) error
	Delete(ctx context.Context, id int64) error
	CountActive(ctx context.Context, userID int64) (int64, error)
}

// KlineRepo K 线历史数据访问接口。
type KlineRepo interface {
	// ── 数据写入 ────────────────────────────────────────────────
	BulkUpsert(ctx context.Context, bars []*model.StockKlineDaily) error
	DeleteByCode(ctx context.Context, code string) error

	// ── 数据读取 ────────────────────────────────────────────────
	GetRange(ctx context.Context, code string, from, to time.Time) ([]*model.StockKlineDaily, error)
	GetLatestN(ctx context.Context, code string, n int) ([]*model.StockKlineDaily, error)
	GetEarliestDate(ctx context.Context, code string) (*time.Time, error)
	GetLatestDate(ctx context.Context, code string) (*time.Time, error)
	CountByCode(ctx context.Context, code string) (int64, error)

	// ── 同步状态 ────────────────────────────────────────────────
	UpsertSyncStatus(ctx context.Context, s *model.StockKlineSyncStatus) error
	GetSyncStatus(ctx context.Context, code string) (*model.StockKlineSyncStatus, error)
	ListSyncStatus(ctx context.Context) ([]*model.StockKlineSyncStatus, error)
	DeleteSyncStatus(ctx context.Context, code string) error
}
