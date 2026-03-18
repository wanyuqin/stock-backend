package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// buy_plans 表 — 买入计划 & 目标价管理
// ═══════════════════════════════════════════════════════════════

// BuyPlanStatus 计划状态
type BuyPlanStatus string

const (
	BuyPlanStatusWatching  BuyPlanStatus = "WATCHING"  // 观察中（默认）
	BuyPlanStatusReady     BuyPlanStatus = "READY"     // 已到买点，等待入场
	BuyPlanStatusExecuted  BuyPlanStatus = "EXECUTED"  // 已执行（买入完成）
	BuyPlanStatusAbandoned BuyPlanStatus = "ABANDONED" // 已放弃（条件不满足）
	BuyPlanStatusExpired   BuyPlanStatus = "EXPIRED"   // 已过期
)

// TriggerConditions 买入触发条件（JSONB 存储）
type TriggerConditions struct {
	// 价格触发
	PriceBelow   *float64 `json:"price_below,omitempty"`    // 价格跌至 X 以下触发
	PriceAbove   *float64 `json:"price_above,omitempty"`    // 价格涨至 X 以上突破触发
	// 技术面触发
	BreakMA20    bool     `json:"break_ma20,omitempty"`     // 放量突破 MA20
	HoldMA20     bool     `json:"hold_ma20,omitempty"`      // 回踩 MA20 不破
	NearSupport  bool     `json:"near_support,omitempty"`   // 靠近支撑位
	// 资金面触发
	MainInflowPct *float64 `json:"main_inflow_pct,omitempty"` // 主力净流入占比 >= X%
	// 自定义文本条件
	CustomNote   string   `json:"custom_note,omitempty"`    // 自定义触发条件描述
}

func (tc TriggerConditions) Value() (driver.Value, error) {
	b, err := json.Marshal(tc)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (tc *TriggerConditions) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("TriggerConditions.Scan: unsupported type %T", value)
	}
	return json.Unmarshal(bytes, tc)
}

// BuyPlan 买入计划
type BuyPlan struct {
	ID         int64         `gorm:"column:id;primaryKey;autoIncrement"              json:"id"`
	UserID     int64         `gorm:"column:user_id;not null;default:1"               json:"user_id"`
	StockCode  string        `gorm:"column:stock_code;type:varchar(10);not null"     json:"stock_code"`
	StockName  string        `gorm:"column:stock_name;type:varchar(50);default:''"   json:"stock_name"`

	// ── 核心计划字段 ──────────────────────────────────────────────
	BuyPrice       *float64 `gorm:"column:buy_price;type:numeric(12,4)"              json:"buy_price"`        // 预期买入价（nil = 市价）
	BuyPriceHigh   *float64 `gorm:"column:buy_price_high;type:numeric(12,4)"         json:"buy_price_high"`   // 买入价区间上限
	TargetPrice    *float64 `gorm:"column:target_price;type:numeric(12,4)"           json:"target_price"`     // 目标价（止盈位）
	StopLossPrice  *float64 `gorm:"column:stop_loss_price;type:numeric(12,4)"        json:"stop_loss_price"`  // 止损价
	PlannedVolume  int      `gorm:"column:planned_volume;default:0"                  json:"planned_volume"`   // 计划买入股数
	PlannedAmount  *float64 `gorm:"column:planned_amount;type:numeric(15,2)"         json:"planned_amount"`   // 计划投入金额（元）

	// ── 仓位计划 ──────────────────────────────────────────────────
	PositionRatio  *float64 `gorm:"column:position_ratio;type:numeric(5,2)"          json:"position_ratio"`  // 计划仓位占比（%，如 10.00）
	BuyBatches     int      `gorm:"column:buy_batches;default:1"                     json:"buy_batches"`     // 计划分几批买入

	// ── 策略理由 ──────────────────────────────────────────────────
	Reason         string   `gorm:"column:reason;type:text;default:''"               json:"reason"`          // 买入逻辑
	Catalyst       string   `gorm:"column:catalyst;type:text;default:''"             json:"catalyst"`        // 催化剂/预期事件

	// ── 触发条件（JSONB） ─────────────────────────────────────────
	TriggerConditions TriggerConditions `gorm:"column:trigger_conditions;type:jsonb;serializer:json" json:"trigger_conditions"`

	// ── 预期收益测算 ──────────────────────────────────────────────
	ExpectedReturnPct *float64 `gorm:"column:expected_return_pct;type:numeric(8,2)"  json:"expected_return_pct"` // 预期收益率（%）
	RiskRewardRatio   *float64 `gorm:"column:risk_reward_ratio;type:numeric(8,2)"    json:"risk_reward_ratio"`   // 盈亏比

	// ── 有效期 ────────────────────────────────────────────────────
	ValidUntil *time.Time `gorm:"column:valid_until"                               json:"valid_until"`      // 计划有效期（nil = 长期有效）

	// ── 状态机 ────────────────────────────────────────────────────
	Status         BuyPlanStatus `gorm:"column:status;type:varchar(20);default:'WATCHING'" json:"status"`
	ExecutedAt     *time.Time    `gorm:"column:executed_at"                           json:"executed_at"`      // 实际执行时间
	TradeLogID     *int64        `gorm:"column:trade_log_id"                          json:"trade_log_id"`     // 关联的交易记录

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (BuyPlan) TableName() string { return "buy_plans" }
