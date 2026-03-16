package repo

import (
	"context"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// StockReportRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type stockReportRepo struct{ db *gorm.DB }

func NewStockReportRepo(db *gorm.DB) StockReportRepo {
	return &stockReportRepo{db: db}
}

// BulkUpsert 批量写入研报，info_code 冲突时跳过（不覆盖已有数据）。
// 返回实际新增的行数。
func (r *stockReportRepo) BulkUpsert(ctx context.Context, reports []*model.StockReport) (int64, error) {
	if len(reports) == 0 {
		return 0, nil
	}

	result := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "info_code"}},
			DoNothing: true, // 已存在则静默跳过
		}).
		CreateInBatches(reports, 50)

	if result.Error != nil {
		return 0, fmt.Errorf("BulkUpsert stock_reports: %w", result.Error)
	}
	return result.RowsAffected, nil
}

// ListPending 查询 is_processed=false 的记录，供 AI Worker 消费。
func (r *stockReportRepo) ListPending(ctx context.Context, limit int) ([]*model.StockReport, error) {
	if limit <= 0 {
		limit = 20
	}
	var items []*model.StockReport
	err := r.db.WithContext(ctx).
		Where("is_processed = FALSE").
		Order("publish_date DESC").
		Limit(limit).
		Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("ListPending stock_reports: %w", err)
	}
	return items, nil
}

// UpdateAISummary 将 AI 摘要写回，标记已处理。
func (r *stockReportRepo) UpdateAISummary(ctx context.Context, id int64, summary string) error {
	err := r.db.WithContext(ctx).
		Model(&model.StockReport{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"ai_summary":   summary,
			"is_processed": true,
		}).Error
	if err != nil {
		return fmt.Errorf("UpdateAISummary stock_report id=%d: %w", id, err)
	}
	return nil
}

// List 分页查询（支持 stock_code 筛选，按 publish_date 降序）。
func (r *stockReportRepo) List(ctx context.Context, q StockReportQuery) (*StockReportPage, error) {
	// 参数边界处理
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.Limit <= 0 || q.Limit > 100 {
		q.Limit = 20
	}
	offset := (q.Page - 1) * q.Limit

	db := r.db.WithContext(ctx).Model(&model.StockReport{})
	if q.StockCode != "" {
		db = db.Where("stock_code = ?", q.StockCode)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, fmt.Errorf("List stock_reports count: %w", err)
	}

	var items []*model.StockReport
	if err := db.Order("publish_date DESC, id DESC").
		Limit(q.Limit).Offset(offset).
		Find(&items).Error; err != nil {
		return nil, fmt.Errorf("List stock_reports find: %w", err)
	}
	if items == nil {
		items = []*model.StockReport{}
	}

	return &StockReportPage{Total: total, Items: items}, nil
}
