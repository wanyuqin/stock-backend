package repo

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// klineRepo — GORM 实现
//
// 索引设计（对应 DDL）：
//   PRIMARY KEY (code, trade_date)
//     → 覆盖 GetRange / GetLatestN / GetEarliestDate / CountByCode
//     → 所有按 code 的查询走主键前缀，无需额外索引
// ═══════════════════════════════════════════════════════════════

const klineBatchSize = 500

type klineRepo struct{ db *gorm.DB }

func NewKlineRepo(db *gorm.DB) KlineRepo { return &klineRepo{db: db} }

// ── 数据写入 ──────────────────────────────────────────────────

func (r *klineRepo) BulkUpsert(ctx context.Context, bars []*model.StockKlineDaily) error {
	if len(bars) == 0 {
		return nil
	}
	for i := 0; i < len(bars); i += klineBatchSize {
		end := i + klineBatchSize
		if end > len(bars) {
			end = len(bars)
		}
		batch := bars[i:end]
		err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "code"}, {Name: "trade_date"}},
				DoNothing: true, // 已有的不覆盖（前复权数据以第一次写入为准）
			}).
			Create(&batch).Error
		if err != nil {
			return fmt.Errorf("KlineRepo.BulkUpsert batch[%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

func (r *klineRepo) DeleteByCode(ctx context.Context, code string) error {
	return r.db.WithContext(ctx).
		Where("code = ?", code).
		Delete(&model.StockKlineDaily{}).Error
}

// ── 数据读取 ──────────────────────────────────────────────────

// GetRange 按时间范围读，走主键 (code, trade_date)，Index Range Scan
func (r *klineRepo) GetRange(ctx context.Context, code string, from, to time.Time) ([]*model.StockKlineDaily, error) {
	q := r.db.WithContext(ctx).
		Select("code, trade_date, open, close, high, low, volume, amount").
		Where("code = ?", code).
		Order("trade_date ASC")
	if !from.IsZero() {
		q = q.Where("trade_date >= ?", from.Format("2006-01-02"))
	}
	if !to.IsZero() {
		q = q.Where("trade_date <= ?", to.Format("2006-01-02"))
	}
	var bars []*model.StockKlineDaily
	if err := q.Find(&bars).Error; err != nil {
		return nil, fmt.Errorf("KlineRepo.GetRange(%s): %w", code, err)
	}
	return bars, nil
}

// GetLatestN 读最近 N 根，走主键倒序，LIMIT N，返回时重新正序
// SQL: SELECT ... WHERE code=? ORDER BY trade_date DESC LIMIT N
// 执行计划：主键 (code, trade_date) 倒序扫，Limit 截断，无全表扫
func (r *klineRepo) GetLatestN(ctx context.Context, code string, n int) ([]*model.StockKlineDaily, error) {
	if n <= 0 {
		n = 120
	}
	var bars []*model.StockKlineDaily
	err := r.db.WithContext(ctx).
		Select("code, trade_date, open, close, high, low, volume, amount").
		Where("code = ?", code).
		Order("trade_date DESC").
		Limit(n).
		Find(&bars).Error
	if err != nil {
		return nil, fmt.Errorf("KlineRepo.GetLatestN(%s,%d): %w", code, n, err)
	}
	// 倒转为升序
	for i, j := 0, len(bars)-1; i < j; i, j = i+1, j-1 {
		bars[i], bars[j] = bars[j], bars[i]
	}
	return bars, nil
}

// GetEarliestDate Index Only Scan on (code, trade_date)
func (r *klineRepo) GetEarliestDate(ctx context.Context, code string) (*time.Time, error) {
	var result struct{ MinDate *time.Time }
	err := r.db.WithContext(ctx).
		Model(&model.StockKlineDaily{}).
		Select("MIN(trade_date) AS min_date").
		Where("code = ?", code).
		Scan(&result).Error
	if err != nil {
		return nil, err
	}
	return result.MinDate, nil
}

// GetLatestDate Index Only Scan on (code, trade_date)
func (r *klineRepo) GetLatestDate(ctx context.Context, code string) (*time.Time, error) {
	var result struct{ MaxDate *time.Time }
	err := r.db.WithContext(ctx).
		Model(&model.StockKlineDaily{}).
		Select("MAX(trade_date) AS max_date").
		Where("code = ?", code).
		Scan(&result).Error
	if err != nil {
		return nil, err
	}
	return result.MaxDate, nil
}

// CountByCode Index Only Scan on (code, trade_date)
func (r *klineRepo) CountByCode(ctx context.Context, code string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&model.StockKlineDaily{}).
		Where("code = ?", code).
		Count(&count).Error
	return count, err
}

// ── 同步状态 ──────────────────────────────────────────────────

func (r *klineRepo) UpsertSyncStatus(ctx context.Context, s *model.StockKlineSyncStatus) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"stock_name", "earliest_date", "latest_date",
				"total_bars", "sync_state", "last_error",
				"last_synced_at", "updated_at",
			}),
		}).
		Create(s).Error
}

func (r *klineRepo) GetSyncStatus(ctx context.Context, code string) (*model.StockKlineSyncStatus, error) {
	var s model.StockKlineSyncStatus
	err := r.db.WithContext(ctx).
		Where("code = ?", code).
		First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *klineRepo) ListSyncStatus(ctx context.Context) ([]*model.StockKlineSyncStatus, error) {
	var list []*model.StockKlineSyncStatus
	err := r.db.WithContext(ctx).
		Order("latest_date DESC NULLS LAST").
		Find(&list).Error
	return list, err
}

func (r *klineRepo) DeleteSyncStatus(ctx context.Context, code string) error {
	return r.db.WithContext(ctx).
		Where("code = ?", code).
		Delete(&model.StockKlineSyncStatus{}).Error
}
