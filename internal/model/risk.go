package model

import "time"

// UserRiskProfile 保存用户默认风控参数（面向小白开箱即用）。
type UserRiskProfile struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID          int64     `gorm:"column:user_id;uniqueIndex;not null;default:1" json:"user_id"`
	RiskPerTradePct float64   `gorm:"column:risk_per_trade_pct;type:numeric(5,2);not null;default:1" json:"risk_per_trade_pct"`
	MaxPositionPct  float64   `gorm:"column:max_position_pct;type:numeric(5,2);not null;default:15" json:"max_position_pct"`
	AccountSize     float64   `gorm:"column:account_size;type:numeric(15,2);not null;default:200000" json:"account_size"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt       time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (UserRiskProfile) TableName() string { return "user_risk_profiles" }

// TradePrecheckLog 记录每次下单前校验，便于后续复盘行为。
type TradePrecheckLog struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID          int64     `gorm:"column:user_id;not null;default:1" json:"user_id"`
	StockCode       string    `gorm:"column:stock_code;type:varchar(10);not null" json:"stock_code"`
	BuyPrice        float64   `gorm:"column:buy_price;type:numeric(12,4);not null" json:"buy_price"`
	StopLossPrice   float64   `gorm:"column:stop_loss_price;type:numeric(12,4);not null" json:"stop_loss_price"`
	TargetPrice     *float64  `gorm:"column:target_price;type:numeric(12,4)" json:"target_price"`
	PlannedAmount   float64   `gorm:"column:planned_amount;type:numeric(15,2);not null" json:"planned_amount"`
	Reason          string    `gorm:"column:reason;type:text;not null;default:''" json:"reason"`
	EstimatedVolume int64     `gorm:"column:estimated_volume;not null;default:0" json:"estimated_volume"`
	WorstLossAmount float64   `gorm:"column:worst_loss_amount;type:numeric(15,2);not null;default:0" json:"worst_loss_amount"`
	WorstLossPct    float64   `gorm:"column:worst_loss_pct;type:numeric(8,2);not null;default:0" json:"worst_loss_pct"`
	Pass            bool      `gorm:"column:pass;not null;default:false" json:"pass"`
	FailReason      string    `gorm:"column:fail_reason;type:text;not null;default:''" json:"fail_reason"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (TradePrecheckLog) TableName() string { return "trade_precheck_logs" }

// RiskTodoStatus 保存每日风险待办完成状态。
type RiskTodoStatus struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID    int64     `gorm:"column:user_id;not null;index:idx_risk_todo_user_date,priority:1" json:"user_id"`
	TodoDate  string    `gorm:"column:todo_date;type:date;not null;index:idx_risk_todo_user_date,priority:2;index:idx_risk_todo_unique,priority:2" json:"todo_date"`
	TodoID    string    `gorm:"column:todo_id;type:varchar(120);not null;index:idx_risk_todo_unique,priority:1" json:"todo_id"`
	Done      bool      `gorm:"column:done;not null;default:false" json:"done"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (RiskTodoStatus) TableName() string { return "risk_todo_statuses" }
