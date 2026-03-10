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

// SignalList 是存入 daily_scans.signals（JSONB）的信号名称列表。
// GORM 通过 Value/Scan 接口与 PostgreSQL JSONB 互转。
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

// DailyScan 存储某次扫描中某只股票触发的所有信号及当时的关键指标。
type DailyScan struct {
	ID          int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ScanDate    time.Time  `gorm:"column:scan_date;type:date"         json:"scan_date"`
	StockCode   string     `gorm:"column:stock_code;type:varchar(10)" json:"stock_code"`
	StockName   string     `gorm:"column:stock_name;type:varchar(50)" json:"stock_name"`
	Signals     SignalList `gorm:"column:signals;type:jsonb"          json:"signals"`
	Price       float64    `gorm:"column:price;type:numeric(12,4)"    json:"price"`
	PctChg      float64    `gorm:"column:pct_chg;type:numeric(8,2)"   json:"pct_chg"`
	VolumeRatio float64    `gorm:"column:volume_ratio;type:numeric(8,2)" json:"volume_ratio"`
	MAStatus    string     `gorm:"column:ma_status;type:varchar(50)"  json:"ma_status"`
	CreatedAt   time.Time  `gorm:"column:created_at;autoCreateTime"   json:"created_at"`
}

func (DailyScan) TableName() string { return "daily_scans" }

// ═══════════════════════════════════════════════════════════════
// DailyReport — daily_reports 表
// ═══════════════════════════════════════════════════════════════

// DailyReport 存储每日的 AI 汇总报告（每天唯一，report_date UNIQUE）。
type DailyReport struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ReportDate  time.Time `gorm:"column:report_date;type:date"       json:"report_date"`
	Content     string    `gorm:"column:content;type:text"           json:"content"`
	MarketMood  string    `gorm:"column:market_mood;type:varchar(20)" json:"market_mood"`
	ScanCount   int       `gorm:"column:scan_count"                  json:"scan_count"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"   json:"created_at"`
}

func (DailyReport) TableName() string { return "daily_reports" }

// ── 信号常量 ──────────────────────────────────────────────────────

const (
	SignalVolumeUp  = "VOLUME_UP"   // 成交量 > 5日均量 × 2
	SignalMA20Break = "MA20_BREAK"  // 今日收盘价上穿 MA20
	SignalBigRise   = "BIG_RISE"    // 今日涨跌幅 > 5%
)
