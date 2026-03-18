package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// BuyPlanRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type buyPlanRepo struct{ db *gorm.DB }

func NewBuyPlanRepo(db *gorm.DB) BuyPlanRepo { return &buyPlanRepo{db: db} }

func (r *buyPlanRepo) Create(ctx context.Context, p *model.BuyPlan) error {
	if err := r.db.WithContext(ctx).Create(p).Error; err != nil {
		return fmt.Errorf("BuyPlanRepo.Create: %w", err)
	}
	return nil
}

func (r *buyPlanRepo) Update(ctx context.Context, p *model.BuyPlan) error {
	if err := r.db.WithContext(ctx).Save(p).Error; err != nil {
		return fmt.Errorf("BuyPlanRepo.Update: %w", err)
	}
	return nil
}

func (r *buyPlanRepo) GetByID(ctx context.Context, id int64) (*model.BuyPlan, error) {
	var p model.BuyPlan
	if err := r.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, fmt.Errorf("BuyPlanRepo.GetByID %d: %w", id, err)
	}
	return &p, nil
}

func (r *buyPlanRepo) ListByUser(ctx context.Context, userID int64, statuses []model.BuyPlanStatus) ([]*model.BuyPlan, error) {
	q := r.db.WithContext(ctx).Where("user_id = ?", userID)
	if len(statuses) > 0 {
		q = q.Where("status IN ?", statuses)
	}
	var plans []*model.BuyPlan
	if err := q.Order("created_at DESC").Find(&plans).Error; err != nil {
		return nil, fmt.Errorf("BuyPlanRepo.ListByUser: %w", err)
	}
	return plans, nil
}

func (r *buyPlanRepo) ListByCode(ctx context.Context, userID int64, code string) ([]*model.BuyPlan, error) {
	var plans []*model.BuyPlan
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND stock_code = ?", userID, code).
		Order("created_at DESC").Find(&plans).Error
	if err != nil {
		return nil, fmt.Errorf("BuyPlanRepo.ListByCode: %w", err)
	}
	return plans, nil
}

func (r *buyPlanRepo) UpdateStatus(ctx context.Context, id int64, status model.BuyPlanStatus) error {
	err := r.db.WithContext(ctx).
		Model(&model.BuyPlan{}).
		Where("id = ?", id).
		Update("status", status).Error
	if err != nil {
		return fmt.Errorf("BuyPlanRepo.UpdateStatus: %w", err)
	}
	return nil
}

func (r *buyPlanRepo) Delete(ctx context.Context, id int64) error {
	if err := r.db.WithContext(ctx).Delete(&model.BuyPlan{}, id).Error; err != nil {
		return fmt.Errorf("BuyPlanRepo.Delete: %w", err)
	}
	return nil
}

// CountActive 返回某用户当前 WATCHING / READY 的计划数（用于告警推送）
func (r *buyPlanRepo) CountActive(ctx context.Context, userID int64) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&model.BuyPlan{}).
		Where("user_id = ? AND status IN ?", userID,
			[]model.BuyPlanStatus{model.BuyPlanStatusWatching, model.BuyPlanStatusReady}).
		Count(&count).Error
	if err != nil {
		return 0, fmt.Errorf("BuyPlanRepo.CountActive: %w", err)
	}
	return count, nil
}
