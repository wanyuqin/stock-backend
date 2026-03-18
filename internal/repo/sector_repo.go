package repo

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

type sectorRepo struct{ db *gorm.DB }

// NewSectorRepo 创建 SectorRepo 实现。
func NewSectorRepo(db *gorm.DB) SectorRepo {
	return &sectorRepo{db: db}
}

func (r *sectorRepo) GetRelation(ctx context.Context, stockCode string) (*model.StockSectorRelation, error) {
	var rel model.StockSectorRelation
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", stockCode).
		First(&rel).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rel, nil
}

func (r *sectorRepo) UpsertRelation(ctx context.Context, rel *model.StockSectorRelation) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stock_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"sector_code", "sector_name", "synced_at",
			}),
		}).
		Create(rel).Error
}

func (r *sectorRepo) UpsertSector(ctx context.Context, s *model.Sector) error {
	return r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"name", "market_id", "updated_at",
			}),
		}).
		Create(s).Error
}
