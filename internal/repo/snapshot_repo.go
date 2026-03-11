package repo

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

type snapshotRepo struct{ db *gorm.DB }

func NewSnapshotRepo(db *gorm.DB) SnapshotRepo { return &snapshotRepo{db: db} }

const snapshotBatchSize = 500

// BulkUpsert 批量 Upsert（ON CONFLICT (trade_date, code) DO UPDATE）。
// updated_at 不列入 DoUpdates，由 GORM autoUpdateTime 标签自动处理。
func (r *snapshotRepo) BulkUpsert(ctx context.Context, snapshots []*model.StockDailySnapshot) error {
	if len(snapshots) == 0 {
		return nil
	}

	// 只列出业务数据列，不列 id / trade_date / code / created_at / updated_at
	updateCols := []string{
		"name",
		"price", "pct_chg", "turnover_rate", "vol_ratio",
		"main_inflow", "main_inflow_pct",
		"ma5", "ma20", "is_multi_aligned", "bias_20",
	}

	for i := 0; i < len(snapshots); i += snapshotBatchSize {
		end := i + snapshotBatchSize
		if end > len(snapshots) {
			end = len(snapshots)
		}
		batch := snapshots[i:end]

		err := r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "trade_date"}, {Name: "code"}},
				DoUpdates: clause.AssignmentColumns(updateCols),
			}).
			Create(&batch).Error
		if err != nil {
			return fmt.Errorf("SnapshotRepo.BulkUpsert batch[%d:%d]: %w", i, end, err)
		}
	}
	return nil
}

// ListByDate 读取某日全量快照，按 code 升序。
func (r *snapshotRepo) ListByDate(ctx context.Context, date time.Time) ([]*model.StockDailySnapshot, error) {
	var rows []*model.StockDailySnapshot
	dateStr := date.Format("2006-01-02")
	err := r.db.WithContext(ctx).
		Select("id,trade_date,code,name,price,pct_chg,turnover_rate,vol_ratio," +
			"main_inflow,main_inflow_pct,ma5,ma20,is_multi_aligned,bias_20").
		Where("trade_date = ?::date", dateStr).
		Order("code ASC").
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("SnapshotRepo.ListByDate: %w", err)
	}
	return rows, nil
}

// CountByDate 返回某日快照总数。
func (r *snapshotRepo) CountByDate(ctx context.Context, date time.Time) (int64, error) {
	var count int64
	dateStr := date.Format("2006-01-02")
	err := r.db.WithContext(ctx).
		Model(&model.StockDailySnapshot{}).
		Where("trade_date = ?::date", dateStr).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("SnapshotRepo.CountByDate: %w", err)
	}
	return count, nil
}
