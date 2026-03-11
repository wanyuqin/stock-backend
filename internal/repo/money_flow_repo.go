package repo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// MoneyFlowRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type moneyFlowRepo struct{ db *gorm.DB }

func NewMoneyFlowRepo(db *gorm.DB) MoneyFlowRepo { return &moneyFlowRepo{db: db} }

func (r *moneyFlowRepo) Insert(ctx context.Context, mf *model.MoneyFlowLog) error {
	if err := r.db.WithContext(ctx).Create(mf).Error; err != nil {
		return fmt.Errorf("MoneyFlow.Insert: %w", err)
	}
	return nil
}

func (r *moneyFlowRepo) ListByCode(ctx context.Context, code string, limit int) ([]*model.MoneyFlowLog, error) {
	if limit <= 0 {
		limit = 20
	}
	var rows []*model.MoneyFlowLog
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		Order("created_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("MoneyFlow.ListByCode: %w", err)
	}
	return rows, nil
}

func (r *moneyFlowRepo) LatestByCode(ctx context.Context, code string) (*model.MoneyFlowLog, error) {
	var row model.MoneyFlowLog
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		Order("created_at DESC").
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil // 首次抓取，正常情况
		}
		return nil, fmt.Errorf("MoneyFlow.LatestByCode: %w", err)
	}
	return &row, nil
}

// ═══════════════════════════════════════════════════════════════
// AlertRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type alertRepo struct{ db *gorm.DB }

func NewAlertRepo(db *gorm.DB) AlertRepo { return &alertRepo{db: db} }

func (r *alertRepo) Create(ctx context.Context, a *model.Alert) error {
	if err := r.db.WithContext(ctx).Create(a).Error; err != nil {
		return fmt.Errorf("Alert.Create: %w", err)
	}
	return nil
}

func (r *alertRepo) ListUnread(ctx context.Context, limit int) ([]*model.Alert, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []*model.Alert
	err := r.db.WithContext(ctx).
		Where("is_read = false").
		Order("triggered_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("Alert.ListUnread: %w", err)
	}
	return rows, nil
}

func (r *alertRepo) ListRecent(ctx context.Context, limit int) ([]*model.Alert, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []*model.Alert
	err := r.db.WithContext(ctx).
		Order("triggered_at DESC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, fmt.Errorf("Alert.ListRecent: %w", err)
	}
	return rows, nil
}

func (r *alertRepo) MarkRead(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	err := r.db.WithContext(ctx).
		Model(&model.Alert{}).
		Where("id IN ?", ids).
		Update("is_read", true).Error
	if err != nil {
		return fmt.Errorf("Alert.MarkRead: %w", err)
	}
	return nil
}
