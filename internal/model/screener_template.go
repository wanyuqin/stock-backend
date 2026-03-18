package model

import (
	"time"

	"gorm.io/datatypes"
)

// ScreenerTemplate 用户保存的筛选模板
type ScreenerTemplate struct {
	ID          int64          `json:"id"           gorm:"primaryKey;autoIncrement"`
	UserID      int64          `json:"user_id"      gorm:"index;not null"`
	Name        string         `json:"name"         gorm:"not null"`
	Description string         `json:"description"`
	// Params 存储筛选参数的 JSON，如 {"min_score":60,"min_main_inflow_pct":10}
	Params      datatypes.JSON `json:"params"       gorm:"type:jsonb;not null"`
	// PushEnabled 是否开启每日定时推送
	PushEnabled bool           `json:"push_enabled" gorm:"default:false"`
	CreatedAt   time.Time      `json:"created_at"   gorm:"autoCreateTime"`
	UpdatedAt   time.Time      `json:"updated_at"   gorm:"autoUpdateTime"`
}

func (ScreenerTemplate) TableName() string { return "screener_templates" }
