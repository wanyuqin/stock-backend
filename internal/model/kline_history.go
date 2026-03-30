package model

import "time"

// ═══════════════════════════════════════════════════════════════
// StockKlineDaily — stock_kline_daily 表
//
// 存储每只股票的前复权日K线历史数据。
//
// 翻页抓取机制（腾讯 qfqday 接口）：
//   - 单次最多 500 根，endDate 当天包含在结果中（重叠 1 根）
//   - 向前翻页：下批 endDate = 本批 bars[0].Date
//   - 终止条件：返回条数 < 500
//   - 去重：跳过每批第[0]根（与上批末尾重叠），仅第一批保留第[0]根
// ═══════════════════════════════════════════════════════════════

type StockKlineDaily struct {
	Code      string    `gorm:"column:code;primaryKey;type:varchar(10)"  json:"code"`
	TradeDate time.Time `gorm:"column:trade_date;primaryKey;type:date"   json:"trade_date"`
	Open      float64   `gorm:"column:open;type:numeric(10,3)"           json:"open"`
	Close     float64   `gorm:"column:close;type:numeric(10,3)"          json:"close"`
	High      float64   `gorm:"column:high;type:numeric(10,3)"           json:"high"`
	Low       float64   `gorm:"column:low;type:numeric(10,3)"            json:"low"`
	Volume    int64     `gorm:"column:volume;default:0"                  json:"volume"` // 单位：手
	Amount    float64   `gorm:"column:amount;type:numeric(16,2);default:0" json:"amount"` // 腾讯接口无此字段，存0
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"         json:"-"`
}

func (StockKlineDaily) TableName() string { return "stock_kline_daily" }

// ═══════════════════════════════════════════════════════════════
// StockKlineSyncStatus — stock_kline_sync_status 表
//
// 记录每只股票的同步进度，支持断点续传。
// ═══════════════════════════════════════════════════════════════

type KlineSyncState string

const (
	KlineSyncIdle    KlineSyncState = "idle"
	KlineSyncRunning KlineSyncState = "running"
	KlineSyncDone    KlineSyncState = "done"
	KlineSyncError   KlineSyncState = "error"
)

type StockKlineSyncStatus struct {
	Code          string         `gorm:"column:code;primaryKey;type:varchar(10)"    json:"code"`
	StockName     string         `gorm:"column:stock_name;type:varchar(50)"         json:"stock_name"`
	EarliestDate  *time.Time     `gorm:"column:earliest_date;type:date"             json:"earliest_date"`
	LatestDate    *time.Time     `gorm:"column:latest_date;type:date"               json:"latest_date"`
	TotalBars     int            `gorm:"column:total_bars;default:0"                json:"total_bars"`
	SyncState     KlineSyncState `gorm:"column:sync_state;type:varchar(20);default:idle" json:"sync_state"`
	LastError     string         `gorm:"column:last_error;type:text"                json:"last_error,omitempty"`
	LastSyncedAt  *time.Time     `gorm:"column:last_synced_at"                      json:"last_synced_at"`
	CreatedAt     time.Time      `gorm:"column:created_at;autoCreateTime"           json:"-"`
	UpdatedAt     time.Time      `gorm:"column:updated_at;autoUpdateTime"           json:"-"`
}

func (StockKlineSyncStatus) TableName() string { return "stock_kline_sync_status" }
