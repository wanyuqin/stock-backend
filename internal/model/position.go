package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// ═══════════════════════════════════════════════════════════════
// position_details 表
// ═══════════════════════════════════════════════════════════════

// PositionDetail 记录单只股票的持仓成本与止损位。
type PositionDetail struct {
	ID           int64            `gorm:"column:id;primaryKey;autoIncrement"                  json:"id"`
	StockCode    string           `gorm:"column:stock_code;type:varchar(10);not null"         json:"stock_code"`
	AvgCost      decimal.Decimal  `gorm:"column:avg_cost;type:numeric(15,4);not null"         json:"avg_cost"`
	Quantity     int              `gorm:"column:quantity;not null"                            json:"quantity"`
	AvailableQty int              `gorm:"column:available_qty;not null;default:0"             json:"available_qty"`
	HardStopLoss *decimal.Decimal `gorm:"column:hard_stop_loss;type:numeric(15,4)"            json:"hard_stop_loss"`

	// ── 买入上下文（改善小白体验）───────────────────────────────
	BoughtAt  *time.Time `gorm:"column:bought_at"   json:"bought_at"`   // 买入时间，用于计算持仓天数
	BuyReason string     `gorm:"column:buy_reason"  json:"buy_reason"`  // 买入理由，人工填写

	// ── 关联买入计划（继承止损/目标/理由）───────────────────────
	LinkedPlanID    *int64   `gorm:"column:linked_plan_id"               json:"linked_plan_id"`
	PlanStopLoss    *float64 `gorm:"column:plan_stop_loss;type:numeric(15,4)"   json:"plan_stop_loss"`
	PlanTargetPrice *float64 `gorm:"column:plan_target_price;type:numeric(15,4)" json:"plan_target_price"`
	PlanBuyReason   string   `gorm:"column:plan_buy_reason"              json:"plan_buy_reason"`

	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (PositionDetail) TableName() string { return "position_details" }

// HoldDays 计算持仓天数（BoughtAt 为 nil 时返回 -1）
func (p *PositionDetail) HoldDays() int {
	if p.BoughtAt == nil {
		return -1
	}
	return int(time.Since(*p.BoughtAt).Hours() / 24)
}

// EffectiveStopLoss 返回有效止损位：优先用计划止损，否则用 ATR 止损
func (p *PositionDetail) EffectiveStopLoss() *float64 {
	if p.PlanStopLoss != nil {
		return p.PlanStopLoss
	}
	if p.HardStopLoss != nil {
		v := p.HardStopLoss.InexactFloat64()
		return &v
	}
	return nil
}

// EffectiveBuyReason 返回有效买入理由：优先用计划理由，否则用手填理由
func (p *PositionDetail) EffectiveBuyReason() string {
	if p.PlanBuyReason != "" {
		return p.PlanBuyReason
	}
	return p.BuyReason
}

// ═══════════════════════════════════════════════════════════════
// position_diagnostics 表
// ═══════════════════════════════════════════════════════════════

// SignalType 诊断信号类型
type SignalType string

const (
	SignalHold     SignalType = "HOLD"
	SignalSell     SignalType = "SELL"
	SignalBuyT     SignalType = "BUY_T"
	SignalSellT    SignalType = "SELL_T"
	SignalStopLoss SignalType = "STOP_LOSS"
)

// DiagnosticSnapshot 存入 JSONB 的量化快照数据
type DiagnosticSnapshot struct {
	Price        float64  `json:"price"`
	AvgCost      float64  `json:"avg_cost"`
	PnLPct       float64  `json:"pnl_pct"`
	ATR          float64  `json:"atr"`
	MA20         float64  `json:"ma20"`
	MA20Slope    float64  `json:"ma20_slope"`
	Support      float64  `json:"support"`
	Resistance   float64  `json:"resistance"`
	HardStopLoss float64  `json:"hard_stop_loss"`
	Amplitude    float64  `json:"amplitude"`
	CanDoT       bool     `json:"can_do_t"`
	Reasons      []string `json:"reasons"`

	// ── 计划止损/目标（继承自买入计划）─────────────────────────
	PlanStopLoss    *float64 `json:"plan_stop_loss"`     // 你自己设的止损
	PlanTargetPrice *float64 `json:"plan_target_price"`  // 你自己设的目标价
	PlanBuyReason   string   `json:"plan_buy_reason"`    // 计划里的买入理由

	// ── 持仓上下文 ────────────────────────────────────────────
	HoldDays  int    `json:"hold_days"`   // 持仓天数，-1=未知
	BuyReason string `json:"buy_reason"`  // 手填买入理由

	// ── 一句话行动指令（面向小白）──────────────────────────────
	ActionSummary    string `json:"action_summary"`     // 如：继续持有，今天不操作
	StopDistPct      float64 `json:"stop_dist_pct"`     // 距止损还有多少%（正数）
	TargetDistPct    *float64 `json:"target_dist_pct"`  // 距目标还有多少%（正数），nil=无目标
	NearStopWarning  bool   `json:"near_stop_warning"`  // 距止损 < 2%
	NearTargetNotice bool   `json:"near_target_notice"` // 距目标 < 3%
	SuggestQty       int    `json:"suggest_qty"`        // 建议操作股数（1/3 or 全部）

	// ── 板块相关 ──────────────────────────────────────────────
	SectorName      string  `json:"sector_name"`
	SectorSecID     string  `json:"sector_sec_id"`
	Sector5DChange  float64 `json:"sector_5d_change"`
	RelStrengthDiff float64 `json:"rel_strength_diff"`
	SectorWarning   string  `json:"sector_warning"`

	// ── MA20 压力位 ───────────────────────────────────────────
	MA20DistPct     float64 `json:"ma20_dist_pct"`
	MA20PressureTip string  `json:"ma20_pressure_tip"`
}

// PositionDiagnostic 诊断记录
type PositionDiagnostic struct {
	ID              int64              `gorm:"column:id;primaryKey;autoIncrement"              json:"id"`
	StockCode       string             `gorm:"column:stock_code;type:varchar(10);not null"     json:"stock_code"`
	SignalType      SignalType         `gorm:"column:signal_type;type:varchar(20)"             json:"signal_type"`
	ActionDirective string             `gorm:"column:action_directive;type:text"               json:"action_directive"`
	DataSnapshot    DiagnosticSnapshot `gorm:"column:data_snapshot;type:jsonb;serializer:json" json:"data_snapshot"`
	CreatedAt       time.Time          `gorm:"column:created_at;autoCreateTime"                json:"created_at"`
}

func (PositionDiagnostic) TableName() string { return "position_diagnostics" }
