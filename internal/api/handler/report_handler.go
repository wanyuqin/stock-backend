package handler

import (
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ReportHandler 处理每日复盘简报相关路由。
type ReportHandler struct {
	reportSvc *service.ReportService
	log       *zap.Logger
}

func NewReportHandler(reportSvc *service.ReportService, log *zap.Logger) *ReportHandler {
	return &ReportHandler{reportSvc: reportSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/reports/daily
//
// 返回今日（或指定日期）的复盘简报。
// 查询参数：
//   ?date=YYYY-MM-DD  — 不传则为今日
//   ?force=1          — 忽略 DB 缓存，强制重新生成（仅今日有效）
// ─────────────────────────────────────────────────────────────────

func (h *ReportHandler) GetDailyReport(c *gin.Context) {
	dateStr := c.Query("date")
	force   := c.Query("force") == "1"

	// ── 解析日期 ──────────────────────────────────────────────────
	var targetDate time.Time
	if dateStr == "" {
		targetDate = time.Now()
	} else {
		var err error
		targetDate, err = time.Parse("2006-01-02", dateStr)
		if err != nil {
			BadRequest(c, "date 格式错误，请使用 YYYY-MM-DD")
			return
		}
	}

	isToday := targetDate.Format("2006-01-02") == time.Now().Format("2006-01-02")

	var (
		dto *service.DailyReportDTO
		err error
	)

	if isToday && force {
		// 强制重新生成
		h.log.Info("force regenerate daily report", zap.String("ip", c.ClientIP()))
		dto, err = h.reportSvc.GenerateDailyReport(c.Request.Context())
	} else if isToday {
		// 优先走 DB 缓存，无则生成
		dto, err = h.reportSvc.GetTodayReport(c.Request.Context())
	} else {
		// 历史日期只读 DB
		dto, err = h.reportSvc.GetReportByDate(c.Request.Context(), targetDate)
	}

	if err != nil {
		h.log.Error("GetDailyReport failed", zap.Error(err))
		InternalError(c, "获取日报失败: "+err.Error())
		return
	}

	OK(c, dto)
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/reports/daily/generate
//
// 手动触发今日日报生成（强制刷新，覆盖旧报告）。
// 一般在 POST /admin/scan/run 之后调用。
// ─────────────────────────────────────────────────────────────────

func (h *ReportHandler) GenerateDailyReport(c *gin.Context) {
	h.log.Info("manual report generation triggered", zap.String("ip", c.ClientIP()))

	dto, err := h.reportSvc.GenerateDailyReport(c.Request.Context())
	if err != nil {
		h.log.Error("GenerateDailyReport failed", zap.Error(err))
		InternalError(c, "生成日报失败: "+err.Error())
		return
	}

	OK(c, dto)
}
