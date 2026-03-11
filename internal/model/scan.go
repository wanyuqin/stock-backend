package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// SignalList — JSONB 数组的自定义类型
// ═══════════════════════════════════════════════════════════════

type SignalList []string

func (s SignalList) Value() (driver.Value, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("SignalList marshal: %w", err)
	}
	return string(b), nil
}

func (s *SignalList) Scan(src any) error {
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("SignalList: unsupported type %T", src)
	}
	return json.Unmarshal(raw, s)
}

// ═══════════════════════════════════════════════════════════════
// DailyScan — daily_scans 表
// ═══════════════════════════════════════════════════════════════

type DailyScan struct {
	ID          int64      `gorm:"column:id;primaryKey;autoIncrement"    json:"id"`
	ScanDate    time.Time  `gorm:"column:scan_date;type:date"            json:"scan_date"`
	StockCode   string     `gorm:"column:stock_code;type:varchar(10)"    json:"stock_code"`
	StockName   string     `gorm:"column:stock_name;type:varchar(50)"    json:"stock_name"`
	Signals     SignalList `gorm:"column:signals;type:jsonb"             json:"signals"`
	Price       float64    `gorm:"column:price;type:numeric(12,4)"       json:"price"`
	PctChg      float64    `gorm:"column:pct_chg;type:numeric(8,2)"      json:"pct_chg"`
	VolumeRatio float64    `gorm:"column:volume_ratio;type:numeric(8,2)" json:"volume_ratio"`
	MAStatus    string     `gorm:"column:ma_status;type:varchar(50)"     json:"ma_status"`
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime"      json:"created_at"`
}

func (DailyScan) TableName() string { return "daily_scans" }

// ═══════════════════════════════════════════════════════════════
// DailyReport — daily_reports 表
// ═══════════════════════════════════════════════════════════════

type DailyReport struct {
	ID         int64     `gorm:"column:id;primaryKey;autoIncrement"  json:"id"`
	ReportDate time.Time `gorm:"column:report_date;type:date"        json:"report_date"`
	Content    string    `gorm:"column:content;type:text"            json:"content"`
	MarketMood string    `gorm:"column:market_mood;type:varchar(20)" json:"market_mood"`
	ScanCount  int       `gorm:"column:scan_count"                   json:"scan_count"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime"    json:"created_at"`
}

func (DailyReport) TableName() string { return "daily_reports" }

const (
	SignalVolumeUp  = "VOLUME_UP"
	SignalMA20Break = "MA20_BREAK"
	SignalBigRise   = "BIG_RISE"
)

// ═══════════════════════════════════════════════════════════════
// MoneyFlowLog — money_flow_logs 表
// ═══════════════════════════════════════════════════════════════
//
// PostgreSQL NUMERIC(15,2) 经 pgx driver 返回字符串，
// GORM 能自动将字符串转换为 float64，但无法转换为 int64。
// 因此所有金额字段统一使用 float64。
// 精度计算（脉冲探测）在 service 层使用 shopspring/decimal，
// 从 float64 构建 decimal 时精度完全满足需求（元级别无误差）。

type MoneyFlowLog struct {
	ID               int64     `gorm:"column:id;primaryKey;autoIncrement"           json:"id"`
	StockCode        string    `gorm:"column:stock_code;type:varchar(10);not null"  json:"stock_code"`
	Date             time.Time `gorm:"column:date;type:date"                        json:"date"`
	MainNetInflow    float64   `gorm:"column:main_net_inflow"                       json:"main_net_inflow"`    // 元
	SuperLargeInflow float64   `gorm:"column:super_large_inflow"                    json:"super_large_inflow"` // 元
	LargeInflow      float64   `gorm:"column:large_inflow"                          json:"large_inflow"`       // 元
	MediumInflow     float64   `gorm:"column:medium_inflow"                         json:"medium_inflow"`      // 元
	SmallInflow      float64   `gorm:"column:small_inflow"                          json:"small_inflow"`       // 元
	MainInflowPct    float64   `gorm:"column:main_inflow_pct"                       json:"main_inflow_pct"`    // 占比 %
	PctChg           float64   `gorm:"column:pct_chg"                               json:"pct_chg"`
	Volume           int64     `gorm:"column:volume;not null"                       json:"volume"`             // 手（BIGINT，可直接 Scan）
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime"             json:"created_at"`
}

func (MoneyFlowLog) TableName() string { return "money_flow_logs" }

// ═══════════════════════════════════════════════════════════════
// Alert — alerts 表
// ═══════════════════════════════════════════════════════════════

const AlertTypeMoneyFlowPulse = "MONEY_FLOW_PULSE"

type Alert struct {
	ID            int64     `gorm:"column:id;primaryKey;autoIncrement"          json:"id"`
	StockCode     string    `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`
	StockName     string    `gorm:"column:stock_name;type:varchar(50)"          json:"stock_name"`
	AlertType     string    `gorm:"column:alert_type;type:varchar(30);not null" json:"alert_type"`
	MainNetInflow float64   `gorm:"column:main_net_inflow"                      json:"main_net_inflow"` // 元
	Delta         float64   `gorm:"column:delta"                                json:"delta"`           // 本次增量（元）
	PctChg        float64   `gorm:"column:pct_chg"                              json:"pct_chg"`
	Message       string    `gorm:"column:message;type:text"                    json:"message"`
	IsRead        bool      `gorm:"column:is_read;default:false"                json:"is_read"`
	TriggeredAt   time.Time `gorm:"column:triggered_at;not null"                json:"triggered_at"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime"            json:"created_at"`
}

func (Alert) TableName() string { return "alerts" }
