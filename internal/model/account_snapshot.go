package model

import "time"

// AccountSnapshot 每日账户净值快照，每天盘后 16:35 写入一次
type AccountSnapshot struct {
	ID            int64     `json:"id"             gorm:"primaryKey;autoIncrement"`
	SnapshotDate  time.Time `json:"snapshot_date"  gorm:"type:date;uniqueIndex;not null;column:snapshot_date"`
	Equity        float64   `json:"equity"         gorm:"type:numeric(18,2);not null;column:equity"`
	RealizedPnL   float64   `json:"realized_pnl"   gorm:"type:numeric(18,2);not null;column:realized_pnl"`
	UnrealizedPnL float64   `json:"unrealized_pnl" gorm:"type:numeric(18,2);not null;column:unrealized_pnl"`
	CreatedAt     time.Time `json:"created_at"     gorm:"autoCreateTime;column:created_at"`
}

func (AccountSnapshot) TableName() string { return "account_snapshots" }
