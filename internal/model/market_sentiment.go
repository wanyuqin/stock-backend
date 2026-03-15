package model

import (
	"time"
)

// MarketSentiment 市场情绪/宏观监控数据
type MarketSentiment struct {
	ID             int64     `gorm:"column:id;primaryKey;autoIncrement"      json:"id"`
	TradeDate      time.Time `gorm:"column:trade_date;unique;not null"       json:"trade_date"` // 交易日
	TotalAmount    float64   `gorm:"column:total_amount;type:numeric(18,2)"  json:"total_amount"` // 两市成交额
	UpCount        int       `gorm:"column:up_count"                         json:"up_count"`
	DownCount      int       `gorm:"column:down_count"                       json:"down_count"`
	LimitUpCount   int       `gorm:"column:limit_up_count"                   json:"limit_up_count"`
	LimitDownCount int       `gorm:"column:limit_down_count"                 json:"limit_down_count"`
	SentimentScore int       `gorm:"column:sentiment_score"                  json:"sentiment_score"` // 综合热度(0-100)
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime"        json:"created_at"`
}

func (MarketSentiment) TableName() string {
	return "market_sentiment"
}
