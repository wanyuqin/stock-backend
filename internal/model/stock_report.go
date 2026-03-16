package model

import "time"

// StockReport 对应 stock_reports 表。
// info_code 是东方财富研报的唯一 ID，用于幂等去重（UPSERT ON CONFLICT DO NOTHING）。
type StockReport struct {
	ID          int64     `gorm:"column:id;primaryKey;autoIncrement"          json:"id"`
	InfoCode    string    `gorm:"column:info_code;type:varchar(40);uniqueIndex;not null" json:"info_code"`
	StockCode   string    `gorm:"column:stock_code;type:varchar(10);not null"  json:"stock_code"`
	StockName   string    `gorm:"column:stock_name;type:varchar(20)"           json:"stock_name"`
	Title       string    `gorm:"column:title;type:text;not null"              json:"title"`
	OrgName     string    `gorm:"column:org_name;type:varchar(100)"            json:"org_name"`
	OrgSName    string    `gorm:"column:org_sname;type:varchar(50)"            json:"org_sname"`
	RatingName  string    `gorm:"column:rating_name;type:varchar(20)"          json:"rating_name"`
	PublishDate time.Time `gorm:"column:publish_date;not null"                 json:"publish_date"`
	DetailURL   string    `gorm:"column:detail_url;type:varchar(512)"          json:"detail_url"`
	AISummary   string    `gorm:"column:ai_summary;type:text"                  json:"ai_summary"`
	IsProcessed bool      `gorm:"column:is_processed;default:false"            json:"is_processed"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime"             json:"created_at"`
}

func (StockReport) TableName() string { return "stock_reports" }
