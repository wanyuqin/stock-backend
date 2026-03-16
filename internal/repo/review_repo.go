package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// 接口定义
// ═══════════════════════════════════════════════════════════════

type ReviewRepo interface {
	Create(ctx context.Context, r *model.TradeReview) error
	GetByTradeLogID(ctx context.Context, tradeLogID int64) (*model.TradeReview, error)
	GetByID(ctx context.Context, id int64) (*model.TradeReview, error)
	// GetWithTradeByID 按 review id 查询，同时 JOIN trade_logs 返回完整信息
	GetWithTradeByID(ctx context.Context, id int64) (*TradeReviewWithTrade, error)
	Update(ctx context.Context, r *model.TradeReview) error
	ListPending(ctx context.Context) ([]*model.TradeReview, error)
	ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*TradeReviewWithTrade, error)
	CountByUser(ctx context.Context, userID int64) (int64, error)
	DashboardStats(ctx context.Context, userID int64) (*DashboardStats, error)
}

type TradeLogV2Repo interface {
	GetSellsInRange(ctx context.Context, userID int64, from, to time.Time) ([]*model.TradeLogV2, error)
	GetMatchedBuy(ctx context.Context, userID int64, code string, sellTime time.Time) (*model.TradeLogV2, error)
	UpdateReasons(ctx context.Context, id int64, buyReason, sellReason string) error
}

// ─────────────────────────────────────────────────────────────────
// 聚合数据结构
// ─────────────────────────────────────────────────────────────────

// TradeReviewWithTrade 复盘记录 + 关联的卖出交易信息（JOIN 结果）
type TradeReviewWithTrade struct {
	model.TradeReview
	TradePrice  float64   `json:"trade_price"  gorm:"column:trade_price"`
	TradeVolume int64     `json:"trade_volume" gorm:"column:trade_volume"`
	TradedAt    time.Time `json:"traded_at"    gorm:"column:traded_at"`
	BuyReason   string    `json:"buy_reason"   gorm:"column:buy_reason"`
	SellReason  string    `json:"sell_reason"  gorm:"column:sell_reason"`
	StockName   string    `json:"stock_name"   gorm:"column:stock_name"`
}

type MentalStateStat struct {
	MentalState string  `json:"mental_state"`
	Count       int64   `json:"count"`
	AvgPnlPct   float64 `json:"avg_pnl_pct"`
	WinRate     float64 `json:"win_rate"`
}

type DashboardStats struct {
	TotalReviews         int64   `json:"total_reviews"`
	WinCount             int64   `json:"win_count"`
	WinRate              float64 `json:"win_rate"`
	LogicConsistentCount int64   `json:"logic_consistent_count"`
	LogicConsistencyRate float64 `json:"logic_consistency_rate"`
	LuckyWinCount        int64   `json:"lucky_win_count"`
	LuckyWinRate         float64 `json:"lucky_win_rate"`
	MentalStateStats     []*MentalStateStat `json:"mental_state_stats"`
	AvgRegretIndex       float64 `json:"avg_regret_index"`
	MaxRegretIndex       float64 `json:"max_regret_index"`
	RegretCount          int64   `json:"regret_count"`
	RegretRate           float64 `json:"regret_rate"`
	AvgExecutionScore    float64 `json:"avg_execution_score"`
	LastReviewAt         *time.Time `json:"last_review_at"`
}

// ═══════════════════════════════════════════════════════════════
// ReviewRepo 实现
// ═══════════════════════════════════════════════════════════════

type gormReviewRepo struct {
	db *gorm.DB
}

func NewReviewRepo(db *gorm.DB) ReviewRepo {
	return &gormReviewRepo{db: db}
}

func (r *gormReviewRepo) Create(ctx context.Context, rev *model.TradeReview) error {
	return r.db.WithContext(ctx).
		Where(model.TradeReview{TradeLogID: rev.TradeLogID}).
		FirstOrCreate(rev).Error
}

func (r *gormReviewRepo) GetByTradeLogID(ctx context.Context, tradeLogID int64) (*model.TradeReview, error) {
	var rev model.TradeReview
	err := r.db.WithContext(ctx).
		Where("trade_log_id = ?", tradeLogID).
		First(&rev).Error
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

func (r *gormReviewRepo) GetByID(ctx context.Context, id int64) (*model.TradeReview, error) {
	var rev model.TradeReview
	err := r.db.WithContext(ctx).First(&rev, id).Error
	if err != nil {
		return nil, err
	}
	return &rev, nil
}

// GetWithTradeByID 用单条 JOIN 查询替代原来 ListByUser(1000) + 遍历的低效做法
func (r *gormReviewRepo) GetWithTradeByID(ctx context.Context, id int64) (*TradeReviewWithTrade, error) {
	var item TradeReviewWithTrade
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			tr.*,
			tl.price        AS trade_price,
			tl.volume       AS trade_volume,
			tl.traded_at    AS traded_at,
			tl.buy_reason   AS buy_reason,
			tl.sell_reason  AS sell_reason,
			COALESCE(s.name, tr.stock_code) AS stock_name
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		LEFT JOIN stocks s ON s.code = tr.stock_code
		WHERE tr.id = ?
		LIMIT 1
	`, id).Scan(&item).Error
	if err != nil {
		return nil, err
	}
	// Scan 不报 not found，检查主键
	if item.ID == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &item, nil
}

func (r *gormReviewRepo) Update(ctx context.Context, rev *model.TradeReview) error {
	return r.db.WithContext(ctx).Save(rev).Error
}

func (r *gormReviewRepo) ListPending(ctx context.Context) ([]*model.TradeReview, error) {
	var reviews []*model.TradeReview
	err := r.db.WithContext(ctx).
		Where("tracking_status IN ?", []string{"PENDING", "PARTIAL"}).
		Order("created_at ASC").
		Find(&reviews).Error
	return reviews, err
}

// ListByUser FIX: 改用 Scan 到 slice，避免 ScanRows 对嵌套 struct 的支持问题
func (r *gormReviewRepo) ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*TradeReviewWithTrade, error) {
	var result []*TradeReviewWithTrade
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			tr.*,
			tl.price        AS trade_price,
			tl.volume       AS trade_volume,
			tl.traded_at    AS traded_at,
			tl.buy_reason   AS buy_reason,
			tl.sell_reason  AS sell_reason,
			COALESCE(s.name, tr.stock_code) AS stock_name
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		LEFT JOIN stocks s ON s.code = tr.stock_code
		WHERE tl.user_id = ?
		ORDER BY tr.created_at DESC
		LIMIT ? OFFSET ?
	`, userID, limit, offset).Scan(&result).Error
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = []*TradeReviewWithTrade{}
	}
	return result, nil
}

func (r *gormReviewRepo) CountByUser(ctx context.Context, userID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Raw(`
		SELECT COUNT(*)
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ?
	`, userID).Scan(&count).Error
	return count, err
}

func (r *gormReviewRepo) DashboardStats(ctx context.Context, userID int64) (*DashboardStats, error) {
	stats := &DashboardStats{}

	type baseRow struct {
		TotalReviews         int64   `gorm:"column:total_reviews"`
		WinCount             int64   `gorm:"column:win_count"`
		LogicConsistentCount int64   `gorm:"column:logic_consistent_count"`
		LuckyWinCount        int64   `gorm:"column:lucky_win_count"`
		AvgRegretIndex       float64 `gorm:"column:avg_regret_index"`
		MaxRegretIndex       float64 `gorm:"column:max_regret_index"`
		RegretCount          int64   `gorm:"column:regret_count"`
		AvgExecutionScore    float64 `gorm:"column:avg_execution_score"`
	}
	var base baseRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*)                                                  AS total_reviews,
			COUNT(*) FILTER (WHERE tr.pnl_pct > 0)                  AS win_count,
			COUNT(*) FILTER (WHERE tr.consistency_flag = 'NORMAL')   AS logic_consistent_count,
			COUNT(*) FILTER (
				WHERE tr.pnl_pct > 0
				  AND tr.consistency_flag != 'NORMAL'
			)                                                         AS lucky_win_count,
			COALESCE(AVG(tr.regret_index) FILTER (WHERE tr.regret_index IS NOT NULL), 0) AS avg_regret_index,
			COALESCE(MAX(tr.regret_index), 0)                        AS max_regret_index,
			COUNT(*) FILTER (
				WHERE tr.price_5d_after IS NOT NULL
				  AND tr.price_at_sell IS NOT NULL
				  AND tr.price_5d_after > tr.price_at_sell
			)                                                         AS regret_count,
			COALESCE(AVG(tr.execution_score) FILTER (WHERE tr.execution_score IS NOT NULL), 0) AS avg_execution_score
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ?
	`, userID).Scan(&base).Error
	if err != nil {
		return nil, err
	}

	stats.TotalReviews = base.TotalReviews
	stats.WinCount = base.WinCount
	stats.LogicConsistentCount = base.LogicConsistentCount
	stats.LuckyWinCount = base.LuckyWinCount
	stats.AvgRegretIndex = round2(base.AvgRegretIndex * 100)
	stats.MaxRegretIndex = round2(base.MaxRegretIndex * 100)
	stats.RegretCount = base.RegretCount
	stats.AvgExecutionScore = round2(base.AvgExecutionScore)

	if base.TotalReviews > 0 {
		stats.WinRate = round2(float64(base.WinCount) / float64(base.TotalReviews) * 100)
		stats.LogicConsistencyRate = round2(float64(base.LogicConsistentCount) / float64(base.TotalReviews) * 100)
	}

	var completedCount int64
	r.db.WithContext(ctx).Raw(`
		SELECT COUNT(*) FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ? AND tr.tracking_status = 'COMPLETED'
	`, userID).Scan(&completedCount)
	if completedCount > 0 {
		stats.RegretRate = round2(float64(base.RegretCount) / float64(completedCount) * 100)
	}
	if base.WinCount > 0 {
		stats.LuckyWinRate = round2(float64(base.LuckyWinCount) / float64(base.WinCount) * 100)
	}

	type mentalRow struct {
		MentalState string  `gorm:"column:mental_state"`
		Count       int64   `gorm:"column:count"`
		AvgPnlPct   float64 `gorm:"column:avg_pnl_pct"`
		WinCount    int64   `gorm:"column:win_count"`
	}
	var mentalRows []mentalRow
	r.db.WithContext(ctx).Raw(`
		SELECT
			tr.mental_state,
			COUNT(*)                                       AS count,
			COALESCE(AVG(tr.pnl_pct), 0)                 AS avg_pnl_pct,
			COUNT(*) FILTER (WHERE tr.pnl_pct > 0)       AS win_count
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ? AND tr.mental_state != ''
		GROUP BY tr.mental_state
		ORDER BY COUNT(*) DESC
	`, userID).Scan(&mentalRows)

	for _, row := range mentalRows {
		stat := &MentalStateStat{
			MentalState: row.MentalState,
			Count:       row.Count,
			AvgPnlPct:   round2(row.AvgPnlPct * 100),
		}
		if row.Count > 0 {
			stat.WinRate = round2(float64(row.WinCount) / float64(row.Count) * 100)
		}
		stats.MentalStateStats = append(stats.MentalStateStats, stat)
	}
	if stats.MentalStateStats == nil {
		stats.MentalStateStats = []*MentalStateStat{}
	}

	var lastTime time.Time
	r.db.WithContext(ctx).Raw(`
		SELECT MAX(tr.created_at)
		FROM trade_reviews tr
		INNER JOIN trade_logs tl ON tl.id = tr.trade_log_id
		WHERE tl.user_id = ?
	`, userID).Scan(&lastTime)
	if !lastTime.IsZero() {
		stats.LastReviewAt = &lastTime
	}

	return stats, nil
}

// ═══════════════════════════════════════════════════════════════
// TradeLogV2Repo 实现
// ═══════════════════════════════════════════════════════════════

type gormTradeLogV2Repo struct {
	db *gorm.DB
}

func NewTradeLogV2Repo(db *gorm.DB) TradeLogV2Repo {
	return &gormTradeLogV2Repo{db: db}
}

func (r *gormTradeLogV2Repo) GetSellsInRange(
	ctx context.Context, userID int64, from, to time.Time,
) ([]*model.TradeLogV2, error) {
	var logs []*model.TradeLogV2
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND action = 'SELL' AND traded_at BETWEEN ? AND ?", userID, from, to).
		Order("traded_at DESC").
		Find(&logs).Error
	return logs, err
}

func (r *gormTradeLogV2Repo) GetMatchedBuy(
	ctx context.Context, userID int64, code string, sellTime time.Time,
) (*model.TradeLogV2, error) {
	var log model.TradeLogV2
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND stock_code = ? AND action = 'BUY' AND traded_at < ?",
			userID, code, sellTime).
		Order("traded_at DESC").
		First(&log).Error
	if err != nil {
		return nil, err
	}
	return &log, nil
}

func (r *gormTradeLogV2Repo) UpdateReasons(ctx context.Context, id int64, buyReason, sellReason string) error {
	updates := map[string]interface{}{}
	if buyReason != "" {
		updates["buy_reason"] = buyReason
	}
	if sellReason != "" {
		updates["sell_reason"] = sellReason
	}
	if len(updates) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).
		Model(&model.TradeLogV2{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
