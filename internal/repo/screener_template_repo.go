package repo

import (
	"context"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

type ScreenerTemplateRepo interface {
	Create(ctx context.Context, t *model.ScreenerTemplate) error
	Update(ctx context.Context, t *model.ScreenerTemplate) error
	Delete(ctx context.Context, id int64) error
	GetByID(ctx context.Context, id int64) (*model.ScreenerTemplate, error)
	ListByUser(ctx context.Context, userID int64) ([]*model.ScreenerTemplate, error)
}

type gormScreenerTemplateRepo struct{ db *gorm.DB }

func NewScreenerTemplateRepo(db *gorm.DB) ScreenerTemplateRepo {
	return &gormScreenerTemplateRepo{db: db}
}

func (r *gormScreenerTemplateRepo) Create(ctx context.Context, t *model.ScreenerTemplate) error {
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *gormScreenerTemplateRepo) Update(ctx context.Context, t *model.ScreenerTemplate) error {
	return r.db.WithContext(ctx).Save(t).Error
}

func (r *gormScreenerTemplateRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&model.ScreenerTemplate{}, id).Error
}

func (r *gormScreenerTemplateRepo) GetByID(ctx context.Context, id int64) (*model.ScreenerTemplate, error) {
	var t model.ScreenerTemplate
	err := r.db.WithContext(ctx).First(&t, id).Error
	return &t, err
}

func (r *gormScreenerTemplateRepo) ListByUser(ctx context.Context, userID int64) ([]*model.ScreenerTemplate, error) {
	var list []*model.ScreenerTemplate
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&list).Error
	return list, err
}
