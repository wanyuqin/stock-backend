package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// 辅助类型：JSONB 字符串数组
// ═══════════════════════════════════════════════════════════════

type StringArray []string

func (s StringArray) Value() (driver.Value, error) {
	if s == nil {
		return "[]", nil
	}
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (s *StringArray) Scan(value interface{}) error {
	if value == nil {
		*s = StringArray{}
		return nil
	}
	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("StringArray.Scan: unsupported type %T", value)
	}
	return json.Unmarshal(bytes, s)
}

// ═══════════════════════════════════════════════════════════════
// 追踪状态枚举
// ═══════════════════════════════════════════════════════════════

type TrackingStatus string

const (
	TrackingPending   TrackingStatus = "PENDING"
	TrackingPartial   TrackingStatus = "PARTIAL"
	TrackingCompleted TrackingStatus = "COMPLETED"
)

// ═══════════════════════════════════════════════════════════════
// 逻辑一致性标记枚举
// ═══════════════════════════════════════════════════════════════

type ConsistencyFlag string

const (
	ConsistencyNormal        ConsistencyFlag = "NORMAL"
	ConsistencyLogicConflict ConsistencyFlag = "LOGIC_CONFLICT"
	ConsistencyChasingHigh   ConsistencyFlag = "CHASING_HIGH"
	ConsistencyPanicSell     ConsistencyFlag = "PANIC_SELL"
	ConsistencyPrematureExit ConsistencyFlag = "PREMATURE_EXIT"
)

// ═══════════════════════════════════════════════════════════════
// BuyContext — 买入时的价格行为上下文（存入 JSONB）
//
// 完全基于 K 线数据计算，不依赖用户填写的理由文字。
// 记录买入当天的市场状态，用于事后还原"当时到底是怎么买的"。
// ═══════════════════════════════════════════════════════════════

type BuyContext struct {
	// ── 买入价在当日区间的位置 ─────────────────────────────────
	// 0 = 买在最低点，1 = 买在最高点，0.5 = 买在中间
	// 经验值：> 0.8 强烈追高，0.5~0.8 偏高，< 0.3 低吸
	BuyPositionInDayRange float64 `json:"buy_position_in_day_range"`

	// ── 相对 MA20 的偏离度（%）──────────────────────────────────
	// 正数 = 高于 MA20，负数 = 低于 MA20
	// > 8% 判定追高，< -5% 判定低吸
	MA20DeviationPct float64 `json:"ma20_deviation_pct"`

	// ── 量能背景 ─────────────────────────────────────────────────
	// 买入当日成交量 / 5日均量，> 1.5 放量，< 0.7 缩量
	VolumeRatioVs5d float64 `json:"volume_ratio_vs_5d"`

	// ── 趋势背景 ─────────────────────────────────────────────────
	// true = 买入时 MA20 向上，false = 向下
	MA20Uptrend bool `json:"ma20_uptrend"`

	// MA5 方向，快速趋势
	MA5Uptrend bool `json:"ma5_uptrend"`

	// ── 买入前 N 日涨幅（%）────────────────────────────────────
	// 用于判断是否在连续上涨后追买
	// Prior3dGainPct > 8 = 连续拉升后追高
	Prior3dGainPct  float64 `json:"prior_3d_gain_pct"`  // 前3日涨跌幅
	Prior5dGainPct  float64 `json:"prior_5d_gain_pct"`  // 前5日涨跌幅
	Prior10dGainPct float64 `json:"prior_10d_gain_pct"` // 前10日涨跌幅

	// ── 买入当日特征 ─────────────────────────────────────────────
	// 当日振幅 = (High - Low) / prevClose
	DayAmplitudePct float64 `json:"day_amplitude_pct"`

	// 当日涨跌幅
	DayChangePct float64 `json:"day_change_pct"`

	// ── 行为判断标签（系统自动标注）──────────────────────────────
	// 这些是基于量化数据的客观判断，不依赖用户文字
	IsChasingHigh    bool   `json:"is_chasing_high"`    // 追高：位置>0.8 或 MA20偏离>8%
	IsBottomFishing  bool   `json:"is_bottom_fishing"`  // 低吸：位置<0.25 且 MA20偏离<0
	IsVolumeBreakout bool   `json:"is_volume_breakout"` // 量能突破：放量>1.5倍
	IsTrendAligned   bool   `json:"is_trend_aligned"`   // 顺势：MA5和MA20同向上
	BuyLabel         string `json:"buy_label"`          // 综合标签，如"追高买入"/"低吸顺势"/"逆势抄底"

	// ── 分析时使用的数据参考点 ───────────────────────────────────
	BuyDate    string  `json:"buy_date"`    // 买入日期
	BuyPrice   float64 `json:"buy_price"`   // 买入价
	DayOpen    float64 `json:"day_open"`    // 当日开盘
	DayHigh    float64 `json:"day_high"`    // 当日最高
	DayLow     float64 `json:"day_low"`     // 当日最低
	DayClose   float64 `json:"day_close"`   // 当日收盘
	MA5        float64 `json:"ma5"`         // 买入日 MA5
	MA20       float64 `json:"ma20"`        // 买入日 MA20
	AvgVol5d   float64 `json:"avg_vol_5d"`  // 5日均量（手）
	DayVolume  int64   `json:"day_volume"`  // 当日成交量（手）

	// ── 计算置信度 ───────────────────────────────────────────────
	// K 线数据是否足够（至少需要 20 根才能算 MA20）
	DataSufficient bool   `json:"data_sufficient"`
	AnalyzedAt     string `json:"analyzed_at"`
}

// Value / Scan 实现 driver.Valuer 和 sql.Scanner，用于 JSONB 列存储
func (b BuyContext) Value() (driver.Value, error) {
	bs, err := json.Marshal(b)
	if err != nil {
		return nil, err
	}
	return string(bs), nil
}

func (b *BuyContext) Scan(value interface{}) error {
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
		return fmt.Errorf("BuyContext.Scan: unsupported type %T", value)
	}
	return json.Unmarshal(bytes, b)
}

// ═══════════════════════════════════════════════════════════════
// TradeReview — trade_reviews 表
// ═══════════════════════════════════════════════════════════════

type TradeReview struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TradeLogID int64  `gorm:"column:trade_log_id;uniqueIndex;not null" json:"trade_log_id"`
	StockCode  string `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`

	// 卖出后价格追踪
	PriceAtSell  *float64 `gorm:"column:price_at_sell"   json:"price_at_sell"`
	Price1dAfter *float64 `gorm:"column:price_1d_after"  json:"price_1d_after"`
	Price3dAfter *float64 `gorm:"column:price_3d_after"  json:"price_3d_after"`
	Price5dAfter *float64 `gorm:"column:price_5d_after"  json:"price_5d_after"`
	MaxPrice5d   *float64 `gorm:"column:max_price_5d"    json:"max_price_5d"`

	// 量化审计指标
	PnlPct         *float64 `gorm:"column:pnl_pct"          json:"pnl_pct"`
	Post5dGainPct  *float64 `gorm:"column:post_5d_gain_pct" json:"post_5d_gain_pct"`
	RegretIndex    *float64 `gorm:"column:regret_index"     json:"regret_index"`
	ExecutionScore *int     `gorm:"column:execution_score"  json:"execution_score"`

	// 买入上下文（基于K线的价格行为分析，JSONB）
	// 使用 serializer:json 让 GORM 自动序列化
	BuyContext *BuyContext `gorm:"column:buy_context;type:jsonb;serializer:json" json:"buy_context"`

	// 逻辑一致性
	ConsistencyFlag ConsistencyFlag `gorm:"column:consistency_flag;type:varchar(30);default:NORMAL" json:"consistency_flag"`
	ConsistencyNote string          `gorm:"column:consistency_note;type:text" json:"consistency_note"`

	// 主观/心理标注
	MentalState string      `gorm:"column:mental_state;type:varchar(50)" json:"mental_state"`
	UserNote    string      `gorm:"column:user_note;type:text"           json:"user_note"`
	Tags        StringArray `gorm:"column:tags;type:jsonb"               json:"tags"`

	// AI 审计
	AIAuditComment  string `gorm:"column:ai_audit_comment;type:text" json:"ai_audit_comment"`
	ImprovementPlan string `gorm:"column:improvement_plan;type:text" json:"improvement_plan"`

	// 状态机
	TrackingStatus TrackingStatus `gorm:"column:tracking_status;type:varchar(20);default:PENDING" json:"tracking_status"`
	AIGeneratedAt  *time.Time     `gorm:"column:ai_generated_at" json:"ai_generated_at"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (TradeReview) TableName() string { return "trade_reviews" }

// ── 扩展后的 TradeLog（含 buy_reason / sell_reason）────────────

type TradeLogV2 struct {
	TradeLog
	BuyReason  string `gorm:"column:buy_reason;type:text"  json:"buy_reason"`
	SellReason string `gorm:"column:sell_reason;type:text" json:"sell_reason"`
}

func (TradeLogV2) TableName() string { return "trade_logs" }
