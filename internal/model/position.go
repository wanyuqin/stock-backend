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

	// 板块相关性（从 SectorInfo 填充）
	SectorName      string  `json:"sector_name"`       // 所属板块名称
	SectorSecID     string  `json:"sector_sec_id"`     // 板块代码，如 BK0726
	Sector5DChange  float64 `json:"sector_5d_change"`  // 板块今日涨跌幅（%）
	RelStrengthDiff float64 `json:"rel_strength_diff"` // RS = 个股涨跌幅 - 板块涨跌幅（%）
	SectorWarning   string  `json:"sector_warning"`    // 板块偏离警告文案（空=正常）

	// 压力位距离
	MA20DistPct     float64 `json:"ma20_dist_pct"`     // 当前价距离MA20百分比（正=在MA20上方）
	MA20PressureTip string  `json:"ma20_pressure_tip"` // 压力位提示文案
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
