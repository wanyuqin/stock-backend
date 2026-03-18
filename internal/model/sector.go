package model

import "time"

// ─────────────────────────────────────────────────────────────────
// sectors 表
// ─────────────────────────────────────────────────────────────────

type Sector struct {
	Code      string    `gorm:"column:code;primaryKey"         json:"code"`
	Name      string    `gorm:"column:name;not null"           json:"name"`
	MarketID  int       `gorm:"column:market_id;default:90"   json:"market_id"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Sector) TableName() string { return "sectors" }

// ─────────────────────────────────────────────────────────────────
// stock_sector_relations 表
// ─────────────────────────────────────────────────────────────────

type StockSectorRelation struct {
	StockCode  string    `gorm:"column:stock_code;primaryKey"  json:"stock_code"`
	SectorCode string    `gorm:"column:sector_code;not null"   json:"sector_code"`
	SectorName string    `gorm:"column:sector_name;not null"   json:"sector_name"`
	SyncedAt   time.Time `gorm:"column:synced_at;autoUpdateTime" json:"synced_at"`
}

func (StockSectorRelation) TableName() string { return "stock_sector_relations" }
