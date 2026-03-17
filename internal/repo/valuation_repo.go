package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// ValuationRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type valuationRepo struct{ db *gorm.DB }

func NewValuationRepo(db *gorm.DB) ValuationRepo {
	return &valuationRepo{db: db}
}

// UpsertSnapshot 写入或更新最新估值快照。
// 使用 ON CONFLICT (stock_code) DO UPDATE，保证幂等。
func (r *valuationRepo) UpsertSnapshot(ctx context.Context, v *model.StockValuation) error {
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stock_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"stock_name", "pe_ttm", "pb",
				"pe_percentile", "pb_percentile",
				"history_days", "updated_at",
			}),
		}).
		Create(v).Error
	if err != nil {
		return fmt.Errorf("UpsertSnapshot %s: %w", v.StockCode, err)
	}
	return nil
}

func (r *valuationRepo) GetSnapshot(ctx context.Context, code string) (*model.StockValuation, error) {
	var v model.StockValuation
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		First(&v).Error
	if err != nil {
		return nil, fmt.Errorf("GetSnapshot %s: %w", code, err)
	}
	return &v, nil
}

func (r *valuationRepo) ListSnapshots(ctx context.Context, codes []string) ([]*model.StockValuation, error) {
	if len(codes) == 0 {
		return []*model.StockValuation{}, nil
	}
	var items []*model.StockValuation
	err := r.db.WithContext(ctx).
		Where("stock_code IN ?", codes).
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("ListSnapshots: %w", err)
	}
	return items, nil
}

// InsertHistory 写入每日历史记录，(stock_code, trade_date) 冲突时跳过（幂等）。
func (r *valuationRepo) InsertHistory(ctx context.Context, h *model.StockValuationHistory) error {
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "stock_code"}, {Name: "trade_date"}},
			DoNothing: true,
		}).
		Create(h).Error
	if err != nil {
		return fmt.Errorf("InsertHistory %s: %w", h.StockCode, err)
	}
	return nil
}

// ListHistory 返回指定股票的历史估值序列，按 trade_date 升序。
// limit=0 表示不限制。
func (r *valuationRepo) ListHistory(ctx context.Context, code string, limit int) ([]*model.StockValuationHistory, error) {
	q := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		Order("trade_date ASC")
	if limit > 0 {
		q = q.Limit(limit)
	}
	var items []*model.StockValuationHistory
	if err := q.Find(&items).Error; err != nil {
		return nil, fmt.Errorf("ListHistory %s: %w", code, err)
	}
	return items, nil
}

func (r *valuationRepo) CountHistory(ctx context.Context, code string) (int, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.StockValuationHistory{}).
		Where("stock_code = ?", code).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("CountHistory %s: %w", code, err)
	}
	return int(count), nil
}
