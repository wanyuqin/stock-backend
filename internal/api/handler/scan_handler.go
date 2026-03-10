package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ScanHandler 处理扫描触发和结果查询路由。
type ScanHandler struct {
	scanSvc   *service.ScanService
	reportSvc *service.ReportService
	log       *zap.Logger
}

func NewScanHandler(scanSvc *service.ScanService, reportSvc *service.ReportService, log *zap.Logger) *ScanHandler {
	return &ScanHandler{scanSvc: scanSvc, reportSvc: reportSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/admin/scan/run
//
// 手动触发扫描，扫描完成后异步生成当日复盘简报。
// ─────────────────────────────────────────────────────────────────

func (h *ScanHandler) RunScan(c *gin.Context) {
	h.log.Info("manual scan triggered", zap.String("ip", c.ClientIP()))

	result, err := h.scanSvc.RunScan(c.Request.Context())
	if err != nil {
		h.log.Error("RunScan failed", zap.Error(err))
		InternalError(c, "扫描执行失败: "+err.Error())
		return
	}

	// 扫描完成后异步生成复盘简报（不阻塞 HTTP 响应）
	go func() {
		h.log.Info("async report generation started after scan",
			zap.Int("hit_count", result.HitCount))
		ctx := c.Copy().Request.Context()
		if _, err := h.reportSvc.GenerateDailyReport(ctx); err != nil {
			h.log.Error("async report generation failed", zap.Error(err))
		}
	}()

	OK(c, result)
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/admin/scan/today
// ─────────────────────────────────────────────────────────────────

func (h *ScanHandler) ListTodayScans(c *gin.Context) {
	scans, err := h.scanSvc.ListTodayScans(c.Request.Context())
	if err != nil {
		InternalError(c, "查询扫描结果失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"date":  time.Now().Format("2006-01-02"),
		"count": len(scans),
		"items": scans,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/admin/scan/history?date=YYYY-MM-DD
// ─────────────────────────────────────────────────────────────────

func (h *ScanHandler) ListScansByDate(c *gin.Context) {
	dateStr := c.Query("date")
	var t time.Time
	if dateStr == "" {
		t = time.Now()
	} else {
		var err error
		t, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			BadRequest(c, "date 格式错误，请使用 YYYY-MM-DD")
			return
		}
	}

	scans, err := h.scanSvc.ListScansByDate(c.Request.Context(), t)
	if err != nil {
		InternalError(c, "查询扫描记录失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"date":  t.Format("2006-01-02"),
		"count": len(scans),
		"items": scans,
	})
}
