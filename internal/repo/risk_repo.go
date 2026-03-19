package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

type RiskRepo interface {
	GetOrInitProfile(ctx context.Context, userID int64) (*model.UserRiskProfile, error)
	UpsertProfile(ctx context.Context, profile *model.UserRiskProfile) error
	CreatePrecheckLog(ctx context.Context, log *model.TradePrecheckLog) error
	ListTodoStatusByDate(ctx context.Context, userID int64, todoDate string) (map[string]bool, error)
	UpsertTodoStatus(ctx context.Context, userID int64, todoDate, todoID string, done bool) error
}

type riskRepo struct{ db *gorm.DB }

func NewRiskRepo(db *gorm.DB) RiskRepo { return &riskRepo{db: db} }

func (r *riskRepo) GetOrInitProfile(ctx context.Context, userID int64) (*model.UserRiskProfile, error) {
	var p model.UserRiskProfile
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&p).Error
	if err == nil {
		return &p, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("RiskRepo.GetOrInitProfile: %w", err)
	}

	p = model.UserRiskProfile{
		UserID:          userID,
		RiskPerTradePct: 1,
		MaxPositionPct:  15,
		AccountSize:     200000,
	}
	if err := r.db.WithContext(ctx).Create(&p).Error; err != nil {
		return nil, fmt.Errorf("RiskRepo.GetOrInitProfile create default: %w", err)
	}
	return &p, nil
}

func (r *riskRepo) UpsertProfile(ctx context.Context, profile *model.UserRiskProfile) error {
	if profile == nil {
		return fmt.Errorf("profile is nil")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.UserRiskProfile
		err := tx.Where("user_id = ?", profile.UserID).First(&existing).Error
		if err == nil {
			updates := map[string]any{
				"risk_per_trade_pct": profile.RiskPerTradePct,
				"max_position_pct":   profile.MaxPositionPct,
				"account_size":       profile.AccountSize,
			}
			if err := tx.Model(&existing).Updates(updates).Error; err != nil {
				return fmt.Errorf("RiskRepo.UpsertProfile update: %w", err)
			}
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("RiskRepo.UpsertProfile query: %w", err)
		}
		if err := tx.Create(profile).Error; err != nil {
			return fmt.Errorf("RiskRepo.UpsertProfile create: %w", err)
		}
		return nil
	})
}

func (r *riskRepo) CreatePrecheckLog(ctx context.Context, log *model.TradePrecheckLog) error {
	if err := r.db.WithContext(ctx).Create(log).Error; err != nil {
		return fmt.Errorf("RiskRepo.CreatePrecheckLog: %w", err)
	}
	return nil
}

func (r *riskRepo) ListTodoStatusByDate(ctx context.Context, userID int64, todoDate string) (map[string]bool, error) {
	rows := make([]*model.RiskTodoStatus, 0, 32)
	if err := r.db.WithContext(ctx).
		Where("user_id = ? AND todo_date = ?", userID, todoDate).
		Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("RiskRepo.ListTodoStatusByDate: %w", err)
	}
	res := make(map[string]bool, len(rows))
	for _, row := range rows {
		res[row.TodoID] = row.Done
	}
	return res, nil
}

func (r *riskRepo) UpsertTodoStatus(ctx context.Context, userID int64, todoDate, todoID string, done bool) error {
	if userID <= 0 || todoDate == "" || todoID == "" {
		return fmt.Errorf("invalid todo status args")
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row model.RiskTodoStatus
		err := tx.Where("user_id = ? AND todo_date = ? AND todo_id = ?", userID, todoDate, todoID).First(&row).Error
		if err == nil {
			if e := tx.Model(&row).Update("done", done).Error; e != nil {
				return fmt.Errorf("RiskRepo.UpsertTodoStatus update: %w", e)
			}
			return nil
		}
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("RiskRepo.UpsertTodoStatus query: %w", err)
		}
		insert := &model.RiskTodoStatus{
			UserID:   userID,
			TodoDate: todoDate,
			TodoID:   todoID,
			Done:     done,
		}
		if e := tx.Create(insert).Error; e != nil {
			return fmt.Errorf("RiskRepo.UpsertTodoStatus create: %w", e)
		}
		return nil
	})
}
