package repo

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

type positionRepo struct{ db *gorm.DB }

// NewPositionRepo 创建 PositionGuardianRepo 实现。
func NewPositionRepo(db *gorm.DB) PositionGuardianRepo {
	return &positionRepo{db: db}
}

func (r *positionRepo) ListAll(ctx context.Context) ([]*model.PositionDetail, error) {
	var rows []*model.PositionDetail
	err := r.db.WithContext(ctx).
		Where("quantity > 0").
		Order("stock_code").
		Find(&rows).Error
	return rows, err
}

func (r *positionRepo) GetByCode(ctx context.Context, code string) (*model.PositionDetail, error) {
	var row model.PositionDetail
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func (r *positionRepo) Upsert(ctx context.Context, p *model.PositionDetail) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stock_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"avg_cost", "quantity", "available_qty", "hard_stop_loss", "updated_at",
			}),
		}).
		Create(p).Error
}

func (r *positionRepo) SaveDiagnostic(ctx context.Context, d *model.PositionDiagnostic) error {
	return r.db.WithContext(ctx).Create(d).Error
}
