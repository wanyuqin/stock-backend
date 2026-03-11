package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ═══════════════════════════════════════════════════════════════
// position_details 表
// ═══════════════════════════════════════════════════════════════

// PositionDetail 记录单只股票的持仓成本与止损位。
// avg_cost / hard_stop_loss 来自 NUMERIC(15,4)，用 decimal 保证计算精度。
type PositionDetail struct {
	ID           int64           `gorm:"column:id;primaryKey;autoIncrement"           json:"id"`
	StockCode    string          `gorm:"column:stock_code;type:varchar(10);not null"  json:"stock_code"`
	AvgCost      decimal.Decimal `gorm:"column:avg_cost;type:numeric(15,4);not null"  json:"avg_cost"`
	Quantity     int             `gorm:"column:quantity;not null"                     json:"quantity"`
	AvailableQty int             `gorm:"column:available_qty;not null;default:0"      json:"available_qty"`
	HardStopLoss *decimal.Decimal `gorm:"column:hard_stop_loss;type:numeric(15,4)"    json:"hard_stop_loss"`
	UpdatedAt    time.Time       `gorm:"column:updated_at;autoUpdateTime"             json:"updated_at"`
}

func (PositionDetail) TableName() string { return "position_details" }

// ═══════════════════════════════════════════════════════════════
// position_diagnostics 表
// ═══════════════════════════════════════════════════════════════

// SignalType 诊断信号类型
type SignalType string

const (
	SignalHold      SignalType = "HOLD"
	SignalSell      SignalType = "SELL"
	SignalBuyT      SignalType = "BUY_T"
	SignalSellT     SignalType = "SELL_T"
	SignalStopLoss  SignalType = "STOP_LOSS"
)

// DiagnosticSnapshot 存入 JSONB 的量化快照数据
type DiagnosticSnapshot struct {
	Price        float64  `json:"price"`
	AvgCost      float64  `json:"avg_cost"`
	PnLPct       float64  `json:"pnl_pct"`        // 盈亏百分比
	ATR          float64  `json:"atr"`             // ATR(20)
	MA20         float64  `json:"ma20"`
	MA20Slope    float64  `json:"ma20_slope"`      // MA20 斜率（正=上行）
	Support      float64  `json:"support"`         // 近20日支撑位
	Resistance   float64  `json:"resistance"`      // 近20日压力位
	HardStopLoss float64  `json:"hard_stop_loss"`  // cost - 2×ATR
	Amplitude    float64  `json:"amplitude"`       // 今日振幅
	CanDoT       bool     `json:"can_do_t"`
	Reasons      []string `json:"reasons"`         // 决策依据列表
}

// PositionDiagnostic 诊断记录
type PositionDiagnostic struct {
	ID              int64               `gorm:"column:id;primaryKey;autoIncrement"           json:"id"`
	StockCode       string              `gorm:"column:stock_code;type:varchar(10);not null"  json:"stock_code"`
	SignalType      SignalType          `gorm:"column:signal_type;type:varchar(20)"          json:"signal_type"`
	ActionDirective string              `gorm:"column:action_directive;type:text"            json:"action_directive"`
	DataSnapshot    DiagnosticSnapshot  `gorm:"column:data_snapshot;type:jsonb;serializer:json" json:"data_snapshot"`
	CreatedAt       time.Time           `gorm:"column:created_at;autoCreateTime"             json:"created_at"`
}

func (PositionDiagnostic) TableName() string { return "position_diagnostics" }
