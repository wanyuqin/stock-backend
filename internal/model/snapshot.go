package model

import "time"

// ═══════════════════════════════════════════════════════════════
// StockDailySnapshot — stock_daily_snapshots 宽表
//
// 设计原则：
//   - 所有数值字段用 *float64（指针），区分"未采集(nil)"与"合法零值"
//   - NUMERIC 列经 pgx driver 返回字符串，GORM 自动转 float64，不用 int64
//   - 技术因子（MA5/MA20/IsMultiAligned/Bias20）由后端二次计算填充
// ═══════════════════════════════════════════════════════════════

type StockDailySnapshot struct {
	ID    int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	TradeDate time.Time `gorm:"column:trade_date;type:date"        json:"trade_date"`
	Code  string    `gorm:"column:code;type:varchar(10)"       json:"code"`
	Name  string    `gorm:"column:name;type:varchar(50)"       json:"name"`

	// ── 基础行情 ────────────────────────────────────────────────
	Price        *float64 `gorm:"column:price"         json:"price"`
	PctChg       *float64 `gorm:"column:pct_chg"       json:"pct_chg"`       // 涨跌幅 %
	TurnoverRate *float64 `gorm:"column:turnover_rate" json:"turnover_rate"` // 换手率 %
	VolRatio     *float64 `gorm:"column:vol_ratio"     json:"vol_ratio"`     // 量比

	// ── 资金因子 ────────────────────────────────────────────────
	MainInflow    *float64 `gorm:"column:main_inflow"     json:"main_inflow"`     // 主力净流入（元）
	MainInflowPct *float64 `gorm:"column:main_inflow_pct" json:"main_inflow_pct"` // 主力净流入占比 %

	// ── 技术因子 ────────────────────────────────────────────────
	MA5           *float64 `gorm:"column:ma5"              json:"ma5"`
	MA20          *float64 `gorm:"column:ma20"             json:"ma20"`
	IsMultiAligned *bool   `gorm:"column:is_multi_aligned" json:"is_multi_aligned"` // price>MA5>MA20
	Bias20        *float64 `gorm:"column:bias_20"          json:"bias_20"`          // 20日乖离率

	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime" json:"-"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"-"`
}

func (StockDailySnapshot) TableName() string { return "stock_daily_snapshots" }

// ── 打分结果 ──────────────────────────────────────────────────────

// SnapshotScore 是筛选器对单只股票的打分结果。
type SnapshotScore struct {
	Snapshot *StockDailySnapshot
	Score    int      // 总分（0-100）
	Tags     []string // 触发的信号标签
}
