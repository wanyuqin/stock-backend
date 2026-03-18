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

func runReportWorkers(ctx context.Context, reportSvc *service.StockReportService, log *zap.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
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
// 估值同步：每天 16:30 运行
// ─────────────────────────────────────────────────────────────────

func runDailyValuationSync(ctx context.Context, valSvc *service.ValuationService, log *zap.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(3 * time.Minute):
	}
	doValuationSync(ctx, valSvc, log)

	for {
		next := nextTriggerTime(16, 30)
		log.Sugar().Infof("valuation sync: next run at %s", next.Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			doValuationSync(ctx, valSvc, log)
		}
	}
}

func doValuationSync(ctx context.Context, svc *service.ValuationService, log *zap.Logger) {
	log.Info("cron: valuation sync triggered")
	result, err := svc.SyncWatchlistValuations(ctx, 1)
	if err != nil {
		log.Error("cron: valuation sync failed", zap.Error(err))
		return
	}
	log.Info("cron: valuation sync done",
		zap.Int("total", result.Total),
		zap.Int("success", result.Success),
		zap.Int("failed", result.Failed),
	)
}

// ─────────────────────────────────────────────────────────────────
// 账户净值快照：每天 16:35 盘后
// ─────────────────────────────────────────────────────────────────

func runDailyEquitySnapshot(ctx context.Context, equitySvc *service.EquityService, log *zap.Logger) {
	for {
		next := nextTriggerTime(16, 35)
		log.Sugar().Infof("equity snapshot: next run at %s", next.Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			doEquitySnapshot(ctx, equitySvc, log)
		}
	}
}

func doEquitySnapshot(ctx context.Context, svc *service.EquityService, log *zap.Logger) {
	log.Info("cron: equity snapshot triggered")
	if err := svc.TakeSnapshot(ctx, 1); err != nil {
		log.Error("cron: equity snapshot failed", zap.Error(err))
		return
	}
	log.Info("cron: equity snapshot done")
}

// ─────────────────────────────────────────────────────────────────
// 筛选模板定时推送：每天 16:00 收盘后执行全部 push_enabled 模板
// ─────────────────────────────────────────────────────────────────

func runScreenerTemplatePush(ctx context.Context, templateSvc *service.ScreenerTemplateService, log *zap.Logger) {
	for {
		next := nextTriggerTime(16, 0)
		log.Sugar().Infof("screener template push: next run at %s", next.Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			log.Info("cron: screener template push triggered")
			if err := templateSvc.RunAllPushTemplates(ctx, 1); err != nil {
				log.Error("cron: screener template push failed", zap.Error(err))
			} else {
				log.Info("cron: screener template push done")
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// 开盘前报告：每天 08:50 预生成
// ─────────────────────────────────────────────────────────────────

func runMorningBriefWorker(ctx context.Context, briefSvc *service.MorningBriefService, log *zap.Logger) {
	for {
		next := nextTriggerTime(8, 50)
		log.Sugar().Infof("morning brief: next pre-gen at %s", next.Format("2006-01-02 15:04:05"))
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
			doMorningBrief(ctx, briefSvc, log)
		}
	}
}

func doMorningBrief(ctx context.Context, svc *service.MorningBriefService, log *zap.Logger) {
	log.Info("cron: morning brief pre-generation triggered")
	brief, err := svc.Generate(ctx, 1, true)
	if err != nil {
		log.Error("cron: morning brief failed", zap.Error(err))
		return
	}
	log.Info("cron: morning brief generated",
		zap.String("date", brief.Date),
		zap.String("mood", brief.MarketMood),
		zap.Int("sections", len(brief.Sections)),
	)
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
