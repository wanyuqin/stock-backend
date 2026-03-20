package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

type positionRepo struct{ db *gorm.DB }

func NewPositionRepo(db *gorm.DB) PositionGuardianRepo {
	return &positionRepo{db: db}
}

// ─────────────────────────────────────────────────────────────────
// ListAll — 查询所有持仓，兼容迁移未执行情况
// ─────────────────────────────────────────────────────────────────

func (r *positionRepo) ListAll(ctx context.Context) ([]*model.PositionDetail, error) {
	var rows []*model.PositionDetail
	err := r.db.WithContext(ctx).
		Where("quantity > 0").
		Order("stock_code").
		Find(&rows).Error

	if err != nil {
		// 列不存在 → 迁移未执行，降级只查核心列
		if isMissingColumnError(err) {
			return r.listCoreOnly(ctx)
		}
		return nil, err
	}
	return rows, nil
}

// listCoreOnly 降级：只查迁移前就存在的列
func (r *positionRepo) listCoreOnly(ctx context.Context) ([]*model.PositionDetail, error) {
	var rows []*model.PositionDetail
	err := r.db.WithContext(ctx).
		Select("id, stock_code, avg_cost, quantity, available_qty, hard_stop_loss, updated_at").
		Where("quantity > 0").
		Order("stock_code").
		Find(&rows).Error
	return rows, err
}

// ─────────────────────────────────────────────────────────────────
// GetByCode
// ─────────────────────────────────────────────────────────────────

func (r *positionRepo) GetByCode(ctx context.Context, code string) (*model.PositionDetail, error) {
	var row model.PositionDetail
	err := r.db.WithContext(ctx).
		Where("stock_code = ?", code).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		// 列不存在时降级
		if isMissingColumnError(err) {
			return r.getByCodeCoreOnly(ctx, code)
		}
		return nil, err
	}
	return &row, nil
}

func (r *positionRepo) getByCodeCoreOnly(ctx context.Context, code string) (*model.PositionDetail, error) {
	var row model.PositionDetail
	err := r.db.WithContext(ctx).
		Select("id, stock_code, avg_cost, quantity, available_qty, hard_stop_loss, updated_at").
		Where("stock_code = ?", code).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// ─────────────────────────────────────────────────────────────────
// Upsert — 写入持仓，兼容迁移未执行情况
// ─────────────────────────────────────────────────────────────────

func (r *positionRepo) Upsert(ctx context.Context, p *model.PositionDetail) error {
	// 先尝试完整 upsert（含新列）
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "stock_code"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"avg_cost", "quantity", "available_qty", "hard_stop_loss",
				"bought_at", "buy_reason",
				"linked_plan_id", "plan_stop_loss", "plan_target_price", "plan_buy_reason",
				"updated_at",
			}),
		}).
		Create(p).Error

	if err == nil {
		return nil
	}

	// 列不存在 → 迁移未执行，只更新核心列
	if isMissingColumnError(err) {
		return r.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "stock_code"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"avg_cost", "quantity", "available_qty", "hard_stop_loss", "updated_at",
				}),
			}).
			Create(p).Error
	}

	return err
}

// ─────────────────────────────────────────────────────────────────
// SaveDiagnostic
// ─────────────────────────────────────────────────────────────────

func (r *positionRepo) SaveDiagnostic(ctx context.Context, d *model.PositionDiagnostic) error {
	return r.db.WithContext(ctx).Create(d).Error
}

func (r *positionRepo) ListDiagnosticsByCodes(ctx context.Context, codes []string, from time.Time) ([]*model.PositionDiagnostic, error) {
	if len(codes) == 0 {
		return []*model.PositionDiagnostic{}, nil
	}
	normCodes := make([]string, 0, len(codes))
	for _, c := range codes {
		cc := strings.TrimSpace(c)
		if cc == "" {
			continue
		}
		normCodes = append(normCodes, cc)
	}
	if len(normCodes) == 0 {
		return []*model.PositionDiagnostic{}, nil
	}
	rows := make([]*model.PositionDiagnostic, 0, 256)
	q := r.db.WithContext(ctx).
		Where("stock_code IN ?", normCodes).
		Order("stock_code ASC, created_at ASC, id ASC")
	if !from.IsZero() {
		q = q.Where("created_at >= ?", from)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

// isMissingColumnError 判断是否为「列不存在」的数据库错误
// 覆盖 PostgreSQL 的 "column X does not exist" 错误
func isMissingColumnError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strContainsAny(msg, []string{
		"does not exist",
		"unknown column",
		"no column",
		"column not found",
	})
}

func strContainsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strHas(s, sub) {
			return true
		}
	}
	return false
}

func strHas(s, sub string) bool {
	if len(s) < len(sub) {
		return false
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
