package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// ScanRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type scanRepo struct{ db *gorm.DB }

func NewScanRepo(db *gorm.DB) ScanRepo { return &scanRepo{db: db} }

// ── daily_scans ───────────────────────────────────────────────────

// BatchInsertScans 批量插入扫描结果，每批 100 条。
func (r *scanRepo) BatchInsertScans(ctx context.Context, scans []*model.DailyScan) error {
	if len(scans) == 0 {
		return nil
	}
	if err := r.db.WithContext(ctx).CreateInBatches(scans, 100).Error; err != nil {
		return fmt.Errorf("BatchInsertScans: %w", err)
	}
	return nil
}

// ListScansByDate 查询某日所有扫描结果，按 created_at 倒序。
func (r *scanRepo) ListScansByDate(ctx context.Context, date time.Time) ([]*model.DailyScan, error) {
	var scans []*model.DailyScan
	dateStr := date.Format("2006-01-02")
	err := r.db.WithContext(ctx).
		Where("scan_date = ?::date", dateStr).
		Order("created_at DESC").
		Find(&scans).Error
	if err != nil {
		return nil, fmt.Errorf("ListScansByDate: %w", err)
	}
	return scans, nil
}

// ── daily_reports ─────────────────────────────────────────────────

// UpsertReport 写入或覆盖某日报告（ON CONFLICT ON report_date DO UPDATE）。
func (r *scanRepo) UpsertReport(ctx context.Context, report *model.DailyReport) error {
	err := r.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "report_date"}},
			DoUpdates: clause.AssignmentColumns([]string{"content", "market_mood", "scan_count"}),
		}).
		Create(report).Error
	if err != nil {
		return fmt.Errorf("UpsertReport: %w", err)
	}
	return nil
}

// GetReportByDate 查询某日报告，记录不存在返回 nil, nil。
func (r *scanRepo) GetReportByDate(ctx context.Context, date time.Time) (*model.DailyReport, error) {
	var rpt model.DailyReport
	dateStr := date.Format("2006-01-02")
	err := r.db.WithContext(ctx).
		Where("report_date = ?::date", dateStr).
		First(&rpt).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("GetReportByDate: %w", err)
	}
	return &rpt, nil
}

// ListReports 按 report_date 倒序列出最近 limit 条报告。
func (r *scanRepo) ListReports(ctx context.Context, limit int) ([]*model.DailyReport, error) {
	if limit <= 0 {
		limit = 30
	}
	var reports []*model.DailyReport
	err := r.db.WithContext(ctx).
		Order("report_date DESC").
		Limit(limit).
		Find(&reports).Error
	if err != nil {
		return nil, fmt.Errorf("ListReports: %w", err)
	}
	return reports, nil
}
