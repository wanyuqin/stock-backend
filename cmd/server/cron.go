package main

import (
	"context"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// runDailyPriceTracker 每天 16:05（A股收盘后）自动运行价格追踪器。
// 同时在启动 5 分钟后运行一次，补齐历史待追踪记录。
func runDailyPriceTracker(ctx context.Context, auditSvc *service.AuditService, log *zap.Logger) {
	// 启动 5 分钟后先跑一次（追补历史）
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Minute):
	}
	runTracker(ctx, auditSvc, log)

	// 此后每天 16:05 定时运行
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

	// 先为最近 7 天的新卖出初始化复盘草稿
	created, err := auditSvc.InitReviewsForRecentSells(ctx, 1)
	if err != nil {
		log.Error("cron: init reviews failed", zap.Error(err))
	} else {
		log.Info("cron: reviews initialized", zap.Int("created", created))
	}

	// 再追踪价格
	updated, err := auditSvc.RunPriceTracker(ctx)
	if err != nil {
		log.Error("cron: price tracker failed", zap.Error(err))
	} else {
		log.Info("cron: price tracker done", zap.Int("updated", updated))
	}
}

// nextTriggerTime 返回下一个指定时:分（北京时间）的 time.Time。
func nextTriggerTime(hour, minute int) time.Time {
	cst := time.FixedZone("CST", 8*3600)
	now := time.Now().In(cst)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, cst)
	if next.Before(now) || next.Equal(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
