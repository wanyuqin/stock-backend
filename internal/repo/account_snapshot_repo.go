package repo

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

// AccountSnapshotRepo 账户净值快照数据访问接口
type AccountSnapshotRepo interface {
	Upsert(ctx context.Context, s *model.AccountSnapshot) error
	ListByDateRange(ctx context.Context, from, to time.Time) ([]*model.AccountSnapshot, error)
	Latest(ctx context.Context) (*model.AccountSnapshot, error)
}

type gormAccountSnapshotRepo struct {
	db *gorm.DB
}

func NewAccountSnapshotRepo(db *gorm.DB) AccountSnapshotRepo {
	return &gormAccountSnapshotRepo{db: db}
}

func (r *gormAccountSnapshotRepo) Upsert(ctx context.Context, s *model.AccountSnapshot) error {
	// 用 ON CONFLICT DO UPDATE 确保列名完全走 model tag，不走 GORM 推断
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "snapshot_date"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"equity", "realized_pnl", "unrealized_pnl",
			}),
		}).
		Create(s).Error
}

func (r *gormAccountSnapshotRepo) ListByDateRange(ctx context.Context, from, to time.Time) ([]*model.AccountSnapshot, error) {
	var list []*model.AccountSnapshot
	err := r.db.WithContext(ctx).
		Where("snapshot_date >= ? AND snapshot_date <= ?", from, to).
		Order("snapshot_date ASC").
		Find(&list).Error
	return list, err
}

func (r *gormAccountSnapshotRepo) Latest(ctx context.Context) (*model.AccountSnapshot, error) {
	var s model.AccountSnapshot
	err := r.db.WithContext(ctx).
		Order("snapshot_date DESC").
		First(&s).Error
	if err != nil {
		return nil, err
	}
	return &s, nil
}
