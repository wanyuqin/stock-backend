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

// ReviewRepo 复盘记录数据访问接口。
type ReviewRepo interface {
	// Create 创建复盘记录（幂等，trade_log_id 冲突时返回已存在的记录）
	Create(ctx context.Context, r *model.TradeReview) error
	// GetByTradeLogID 按关联交易ID查询
	GetByTradeLogID(ctx context.Context, tradeLogID int64) (*model.TradeReview, error)
	// GetByID 按主键查询
	GetByID(ctx context.Context, id int64) (*model.TradeReview, error)
	// Update 更新字段（全字段覆盖）
	Update(ctx context.Context, r *model.TradeReview) error
	// ListPending 查询需要价格追踪的记录（PENDING / PARTIAL）
	ListPending(ctx context.Context) ([]*model.TradeReview, error)
	// ListByUser 查询用户全量复盘，支持分页
	ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*TradeReviewWithTrade, error)
	// CountByUser 统计总数
	CountByUser(ctx context.Context, userID int64) (int64, error)
	// DashboardStats 聚合看板数据
	DashboardStats(ctx context.Context, userID int64) (*DashboardStats, error)
}

// TradeLogV2Repo 扩展交易日志访问（含 buy_reason / sell_reason）。
type TradeLogV2Repo interface {
	// GetSellsInRange 查询时间范围内所有 SELL 记录
	GetSellsInRange(ctx context.Context, userID int64, from, to time.Time) ([]*model.TradeLogV2, error)
	// GetMatchedBuy 查找与卖出相匹配的最近一次买入（同 code、买入时间 < 卖出时间）
	GetMatchedBuy(ctx context.Context, userID int64, code string, sellTime time.Time) (*model.TradeLogV2, error)
	// UpdateReasons 更新买入/卖出理由
	UpdateReasons(ctx context.Context, id int64, buyReason, sellReason string) error
}

// ─────────────────────────────────────────────────────────────────
// 聚合数据结构
// ─────────────────────────────────────────────────────────────────

// TradeReviewWithTrade 复盘记录 + 关联的卖出交易信息
type TradeReviewWithTrade struct {
	model.TradeReview
	// 来自 trade_logs 的冗余字段（JOIN）
	TradePrice  float64   `json:"trade_price"`
	TradeVolume int64     `json:"trade_volume"`
	TradedAt    time.Time `json:"traded_at"`
	BuyReason   string    `json:"buy_reason"`
	SellReason  string    `json:"sell_reason"`
	StockName   string    `json:"stock_name"`
}

// MentalStateStat 单个情绪状态的统计
type MentalStateStat struct {
	MentalState string  `json:"mental_state"`
	Count       int64   `json:"count"`
	AvgPnlPct   float64 `json:"avg_pnl_pct"` // 平均盈亏%
	WinRate     float64 `json:"win_rate"`    // 胜率%
}

// DashboardStats 看板聚合数据
type DashboardStats struct {
	// 胜率统计
	TotalReviews int64   `json:"total_reviews"`
	WinCount     int64   `json:"win_count"`
	WinRate      float64 `json:"win_rate"` // 盈利次数/总次数

	// 逻辑一致率
	LogicConsistentCount int64   `json:"logic_consistent_count"` // ConsistencyFlag = NORMAL
	LogicConsistencyRate float64 `json:"logic_consistency_rate"` // 一致次数/总次数
	LuckyWinCount        int64   `json:"lucky_win_count"`        // 盈利但有逻辑冲突
	LuckyWinRate         float64 `json:"lucky_win_rate"`         // 瞎猫碰死耗子比例

	// 情绪热力图
	MentalStateStats []*MentalStateStat `json:"mental_state_stats"`

	// 卖飞分析
	AvgRegretIndex float64 `json:"avg_regret_index"` // 平均后悔指数
	MaxRegretIndex float64 `json:"max_regret_index"` // 最大后悔指数
	RegretCount    int64   `json:"regret_count"`     // price_5d_after > price_at_sell 的次数
	RegretRate     float64 `json:"regret_rate"`      // 卖飞比例

	// 执行力
	AvgExecutionScore float64 `json:"avg_execution_score"`

	// 最近活动
	LastReviewAt *time.Time `json:"last_review_at"`
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

func (r *gormReviewRepo) ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*TradeReviewWithTrade, error) {
	rows, err := r.db.WithContext(ctx).Raw(`
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
	`, userID, limit, offset).Rows()
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*TradeReviewWithTrade
	for rows.Next() {
		var item TradeReviewWithTrade
		if err := r.db.ScanRows(rows, &item); err != nil {
			return nil, err
		}
		result = append(result, &item)
	}
	return result, rows.Err()
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

	// ── 基础聚合 ────────────────────────────────────────────────
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
	stats.AvgRegretIndex = round2(base.AvgRegretIndex * 100) // 转为%
	stats.MaxRegretIndex = round2(base.MaxRegretIndex * 100)
	stats.RegretCount = base.RegretCount
	stats.AvgExecutionScore = round2(base.AvgExecutionScore)

	if base.TotalReviews > 0 {
		stats.WinRate = round2(float64(base.WinCount) / float64(base.TotalReviews) * 100)
		stats.LogicConsistencyRate = round2(float64(base.LogicConsistentCount) / float64(base.TotalReviews) * 100)
	}
	// 卖飞比例（相对于追踪完成的记录）
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

	// ── 情绪热力图 ────────────────────────────────────────────
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

	// ── 最近活动时间 ──────────────────────────────────────────
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
	return r.db.WithContext(ctx).
		Model(&model.TradeLogV2{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"buy_reason":  buyReason,
			"sell_reason": sellReason,
		}).Error
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}
