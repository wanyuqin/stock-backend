package repo

import (
	"context"

	"gorm.io/gorm"
)

// BehaviorStat 单类行为的统计数据
type BehaviorStat struct {
	Flag     string  `json:"flag"      gorm:"column:flag"`
	Count    int64   `json:"count"     gorm:"column:count"`
	AvgPnl   float64 `json:"avg_pnl"   gorm:"column:avg_pnl"`   // 百分比，已乘 100
	WinRate  float64 `json:"win_rate"  gorm:"column:win_rate"`  // 0-100
	TotalPnl float64 `json:"total_pnl" gorm:"column:total_pnl"` // 百分比累计
}

// BehaviorStatsResult 行为归因统计汇总
type BehaviorStatsResult struct {
	Items      []*BehaviorStat `json:"items"`
	TotalTrades int64          `json:"total_trades"`
}

// BehaviorStatsRepo 行为归因统计查询接口
type BehaviorStatsRepo interface {
	GetBehaviorStats(ctx context.Context, userID int64) (*BehaviorStatsResult, error)
}

type gormBehaviorStatsRepo struct {
	db *gorm.DB
}

func NewBehaviorStatsRepo(db *gorm.DB) BehaviorStatsRepo {
	return &gormBehaviorStatsRepo{db: db}
}

func (r *gormBehaviorStatsRepo) GetBehaviorStats(ctx context.Context, userID int64) (*BehaviorStatsResult, error) {
	type row struct {
		Flag     string  `gorm:"column:flag"`
		Count    int64   `gorm:"column:count"`
		AvgPnl   float64 `gorm:"column:avg_pnl"`
		WinCount int64   `gorm:"column:win_count"`
		TotalPnl float64 `gorm:"column:total_pnl"`
	}
	var rows []row

	err := r.db.WithContext(ctx).Raw(`
		SELECT
			tr.consistency_flag                                          AS flag,
			COUNT(*)                                                     AS count,
			COALESCE(AVG(tr.pnl_pct), 0) * 100                         AS avg_pnl,
			COUNT(*) FILTER (WHERE tr.pnl_pct > 0)                     AS win_count,
			COALESCE(SUM(tr.pnl_pct), 0) * 100                         AS total_pnl
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ?
		  AND tr.consistency_flag IS NOT NULL
		  AND tr.consistency_flag != ''
		GROUP BY tr.consistency_flag
		ORDER BY COUNT(*) DESC
	`, userID).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	var total int64
	items := make([]*BehaviorStat, 0, len(rows))
	for _, r := range rows {
		total += r.Count
		winRate := 0.0
		if r.Count > 0 {
			winRate = float64(r.WinCount) / float64(r.Count) * 100
		}
		items = append(items, &BehaviorStat{
			Flag:     r.Flag,
			Count:    r.Count,
			AvgPnl:   round2(r.AvgPnl),
			WinRate:  round2(winRate),
			TotalPnl: round2(r.TotalPnl),
		})
	}

	return &BehaviorStatsResult{
		Items:       items,
		TotalTrades: total,
	}, nil
}
