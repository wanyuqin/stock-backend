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

// StringArray 可以直接 Scan/Value 到 PostgreSQL JSONB 列。
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
	TrackingPending   TrackingStatus = "PENDING"   // 等待第一次价格追踪
	TrackingPartial   TrackingStatus = "PARTIAL"   // 部分数据已填充（1/3日完成，5日未到）
	TrackingCompleted TrackingStatus = "COMPLETED" // 5日全部追踪完毕
)

// ═══════════════════════════════════════════════════════════════
// 逻辑一致性标记枚举
// ═══════════════════════════════════════════════════════════════

type ConsistencyFlag string

const (
	ConsistencyNormal       ConsistencyFlag = "NORMAL"          // 无冲突
	ConsistencyLogicConflict ConsistencyFlag = "LOGIC_CONFLICT"  // 策略与行为冲突（如"长线"持2天就卖）
	ConsistencyChasingHigh  ConsistencyFlag = "CHASING_HIGH"    // 追高（买点偏离MA20过远）
	ConsistencyPanicSell    ConsistencyFlag = "PANIC_SELL"      // 恐慌抛售（单日大跌后快速卖出）
	ConsistencyPrematureExit ConsistencyFlag = "PREMATURE_EXIT" // 过早止盈（未达目标位就退出）
)

// ═══════════════════════════════════════════════════════════════
// TradeReview — trade_reviews 表
// ═══════════════════════════════════════════════════════════════

type TradeReview struct {
	ID          int64   `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TradeLogID  int64   `gorm:"column:trade_log_id;uniqueIndex;not null" json:"trade_log_id"`
	StockCode   string  `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`

	// 卖出后价格追踪
	PriceAtSell   *float64 `gorm:"column:price_at_sell"   json:"price_at_sell"`
	Price1dAfter  *float64 `gorm:"column:price_1d_after"  json:"price_1d_after"`
	Price3dAfter  *float64 `gorm:"column:price_3d_after"  json:"price_3d_after"`
	Price5dAfter  *float64 `gorm:"column:price_5d_after"  json:"price_5d_after"`
	MaxPrice5d    *float64 `gorm:"column:max_price_5d"    json:"max_price_5d"`

	// 量化审计指标
	PnlPct        *float64 `gorm:"column:pnl_pct"         json:"pnl_pct"`
	Post5dGainPct *float64 `gorm:"column:post_5d_gain_pct" json:"post_5d_gain_pct"`
	RegretIndex   *float64 `gorm:"column:regret_index"    json:"regret_index"`
	ExecutionScore *int    `gorm:"column:execution_score" json:"execution_score"`

	// 逻辑一致性
	ConsistencyFlag ConsistencyFlag `gorm:"column:consistency_flag;type:varchar(30);default:NORMAL" json:"consistency_flag"`
	ConsistencyNote string          `gorm:"column:consistency_note;type:text" json:"consistency_note"`

	// 主观/心理标注
	MentalState string      `gorm:"column:mental_state;type:varchar(50)" json:"mental_state"`
	UserNote    string      `gorm:"column:user_note;type:text"           json:"user_note"`
	Tags        StringArray `gorm:"column:tags;type:jsonb"               json:"tags"`

	// AI 审计
	AIAuditComment  string     `gorm:"column:ai_audit_comment;type:text" json:"ai_audit_comment"`
	ImprovementPlan string     `gorm:"column:improvement_plan;type:text" json:"improvement_plan"`

	// 状态机
	TrackingStatus TrackingStatus `gorm:"column:tracking_status;type:varchar(20);default:PENDING" json:"tracking_status"`
	AIGeneratedAt  *time.Time     `gorm:"column:ai_generated_at"  json:"ai_generated_at"`

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (TradeReview) TableName() string { return "trade_reviews" }

// ── 扩展后的 TradeLog（含 buy_reason / sell_reason）────────────

// TradeLogV2 增加了买卖分离理由字段，与 trade_logs 表对应。
// 使用嵌入方式，保持向下兼容。
type TradeLogV2 struct {
	TradeLog
	BuyReason  string `gorm:"column:buy_reason;type:text"  json:"buy_reason"`
	SellReason string `gorm:"column:sell_reason;type:text" json:"sell_reason"`
}

func (TradeLogV2) TableName() string { return "trade_logs" }
