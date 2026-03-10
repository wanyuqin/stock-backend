package model

import "time"

// ═══════════════════════════════════════════════════════════════
// stocks 表
// ═══════════════════════════════════════════════════════════════

type Stock struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"        json:"id"`
	Code      string    `gorm:"column:code;type:varchar(10);not null"     json:"code"`
	Name      string    `gorm:"column:name;type:varchar(50);not null"     json:"name"`
	Market    Market    `gorm:"column:market;type:varchar(4);not null"    json:"market"`
	Sector    string    `gorm:"column:sector;type:varchar(50)"            json:"sector"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"          json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"          json:"updated_at"`
}

func (Stock) TableName() string { return "stocks" }

type Market string

const (
	MarketSH Market = "SH"
	MarketSZ Market = "SZ"
)

// ═══════════════════════════════════════════════════════════════
// watchlist 表
// ═══════════════════════════════════════════════════════════════

type Watchlist struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"          json:"id"`
	UserID    int64     `gorm:"column:user_id;not null;default:1"           json:"user_id"`
	StockCode string    `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`
	Note      string    `gorm:"column:note;type:text"                       json:"note"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"            json:"created_at"`
}

func (Watchlist) TableName() string { return "watchlist" }

// ═══════════════════════════════════════════════════════════════
// trade_logs 表
// ═══════════════════════════════════════════════════════════════

// TradeLog 交易记录。
// Reason 字段对应 trade_logs.reason（交易理由/备注，可为空）。
type TradeLog struct {
	ID        int64       `gorm:"column:id;primaryKey;autoIncrement"          json:"id"`
	UserID    int64       `gorm:"column:user_id;not null;default:1"           json:"user_id"`
	StockCode string      `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`
	Action    TradeAction `gorm:"column:action;type:varchar(4);not null"      json:"action"`
	Price     float64     `gorm:"column:price;type:numeric(12,4);not null"    json:"price"`
	Volume    int64       `gorm:"column:volume;not null"                      json:"volume"`
	TradedAt  time.Time   `gorm:"column:traded_at;not null"                   json:"traded_at"`
	Reason    string      `gorm:"column:reason;type:text"                     json:"reason"`
	CreatedAt time.Time   `gorm:"column:created_at;autoCreateTime"            json:"created_at"`
}

func (TradeLog) TableName() string { return "trade_logs" }

// Amount 返回此笔交易的资金量（价格 × 数量）。
func (t *TradeLog) Amount() float64 {
	return t.Price * float64(t.Volume)
}

// TradeAction 枚举：买入 / 卖出
type TradeAction string

const (
	TradeActionBuy  TradeAction = "BUY"
	TradeActionSell TradeAction = "SELL"
)

// ═══════════════════════════════════════════════════════════════
// ai_cache 表
// ═══════════════════════════════════════════════════════════════

type AICache struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement"           json:"id"`
	StockCode string    `gorm:"column:stock_code;type:varchar(10);not null"  json:"stock_code"`
	Prompt    string    `gorm:"column:prompt;type:text;not null"             json:"prompt"`
	Response  string    `gorm:"column:response;type:text;not null"           json:"response"`
	ModelUsed string    `gorm:"column:model_used;type:varchar(50)"           json:"model_used"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"             json:"created_at"`
	ExpiresAt time.Time `gorm:"column:expires_at;not null"                   json:"expires_at"`
}

func (AICache) TableName() string { return "ai_cache" }

func (a *AICache) IsExpired() bool { return time.Now().After(a.ExpiresAt) }
