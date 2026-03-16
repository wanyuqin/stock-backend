package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ─────────────────────────────────────────────────────────────────
// 复盘价格追踪器：每天 16:05 运行
// ─────────────────────────────────────────────────────────────────

func runDailyPriceTracker(ctx context.Context, auditSvc *service.AuditService, log *zap.Logger) {
	// 启动 5 分钟后先跑一次（补齐历史）
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	runTracker(ctx, auditSvc, log)

	for {
		next := nextTriggerTime(16, 5)
		log.Sugar().Infof("price tracker: next run at %s", next.Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			runTracker(ctx, auditSvc, log)
		}
	}
}

func runTracker(ctx context.Context, auditSvc *service.AuditService, log *zap.Logger) {
	log.Info("cron: price tracker triggered")

	created, err := auditSvc.InitReviewsForRecentSells(ctx, 1)
	if err != nil {
		log.Error("cron: init reviews failed", zap.Error(err))
	} else {
		log.Info("cron: reviews initialized", zap.Int("created", created))
	}

	updated, err := auditSvc.RunPriceTracker(ctx)
	if err != nil {
		log.Error("cron: price tracker failed", zap.Error(err))
	} else {
		log.Info("cron: price tracker done", zap.Int("updated", updated))
	}
}

// ─────────────────────────────────────────────────────────────────
// 研报情报站：采集 + AI 摘要定时任务
// ─────────────────────────────────────────────────────────────────

// runReportWorkers 启动两个定时循环：
//   - 每 6 小时同步一次东方财富研报（同步最近 3 天）
//   - 每 10 分钟处理一批 is_processed=false 的 AI 摘要
//
// 启动后 2 分钟先各跑一次，补齐历史数据。
func runReportWorkers(ctx context.Context, reportSvc *service.StockReportService, log *zap.Logger) {
	// 延迟 2 分钟首次执行，等待其他服务就绪
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}

	// 首次补跑
	doSyncReports(ctx, reportSvc, log)
	doAISummaries(ctx, reportSvc, log)

	syncTicker := time.NewTicker(6 * time.Hour)
	aiTicker := time.NewTicker(10 * time.Minute)
	defer syncTicker.Stop()
	defer aiTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-syncTicker.C:
			doSyncReports(ctx, reportSvc, log)
		case <-aiTicker.C:
			doAISummaries(ctx, reportSvc, log)
		}
	}
}

func doSyncReports(ctx context.Context, svc *service.StockReportService, log *zap.Logger) {
	log.Info("cron: report sync triggered")
	result, err := svc.SyncReports(ctx, 3)
	if err != nil {
		log.Error("cron: report sync failed", zap.Error(err))
		return
	}
	log.Info("cron: report sync done",
		zap.Int("fetched", result.Fetched),
		zap.Int("filtered", result.Filtered),
		zap.Int("saved", result.Saved),
	)
}

func doAISummaries(ctx context.Context, svc *service.StockReportService, log *zap.Logger) {
	log.Debug("cron: AI summaries triggered")
	done, err := svc.ProcessAISummaries(ctx)
	if err != nil {
		log.Error("cron: AI summaries failed", zap.Error(err))
		return
	}
	if done > 0 {
		log.Info("cron: AI summaries done", zap.Int("processed", done))
	}
}

// ─────────────────────────────────────────────────────────────────
// 工具：计算下一个触发时间（北京时间）
// ─────────────────────────────────────────────────────────────────

func nextTriggerTime(hour, minute int) time.Time {
	cst := time.FixedZone("CST", 8*3600)
	now := time.Now().In(cst)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, cst)
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
