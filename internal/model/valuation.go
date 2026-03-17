package model

import "time"

// ═══════════════════════════════════════════════════════════════
// stock_valuations 表 — 每只股票最新估值快照
// ═══════════════════════════════════════════════════════════════

// StockValuation 存储每只股票当前的估值数据及历史分位。
//
// 历史分位计算策略（"本地积累法"）：
//
//   东财历史估值接口（push2his/financialvaluation、IndexAjax 等）在
//   当前环境下全部不可用（503 / 需要 Cookie）。
//
//   因此采用本地积累法：每日 16:30 盘后，将当天的 PE/PB 存入
//   stock_valuation_history；经过一段时间（建议 ≥30 天）积累后，
//   用本地历史序列计算分位，结果随时间越来越精准。
//
//   分位公式：Percentile = Count(x < currentPE) / TotalCount * 100
//   （过滤掉 PE ≤ 0 的异常值再计算）
type StockValuation struct {
	StockCode    string    `gorm:"column:stock_code;primaryKey"                json:"stock_code"`
	StockName    string    `gorm:"column:stock_name;type:varchar(50)"           json:"stock_name"`
	PETTM        *float64  `gorm:"column:pe_ttm"                               json:"pe_ttm"`        // nil = 亏损/无效
	PB           *float64  `gorm:"column:pb"                                   json:"pb"`
	PEPercentile *float64  `gorm:"column:pe_percentile"                        json:"pe_percentile"` // nil = 历史数据不足
	PBPercentile *float64  `gorm:"column:pb_percentile"                        json:"pb_percentile"`
	HistoryDays  int       `gorm:"column:history_days;default:0"               json:"history_days"`  // 已积累天数
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime"            json:"updated_at"`
}

func (StockValuation) TableName() string { return "stock_valuations" }

// ═══════════════════════════════════════════════════════════════
// stock_valuation_history 表 — 每日历史记录
// ═══════════════════════════════════════════════════════════════

type StockValuationHistory struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"          json:"id"`
	StockCode string    `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`
	TradeDate time.Time `gorm:"column:trade_date;type:date;not null"        json:"trade_date"`
	PETTM     *float64  `gorm:"column:pe_ttm"                               json:"pe_ttm"`
	PB        *float64  `gorm:"column:pb"                                   json:"pb"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"            json:"created_at"`
}

func (StockValuationHistory) TableName() string { return "stock_valuation_history" }
