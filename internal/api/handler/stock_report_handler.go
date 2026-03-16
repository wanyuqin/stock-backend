package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/repo"
	"stock-backend/internal/service"
)

type StockReportHandler struct {
	svc *service.StockReportService
	log *zap.Logger
}

func NewStockReportHandler(svc *service.StockReportService, log *zap.Logger) *StockReportHandler {
	return &StockReportHandler{svc: svc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/reports/intel/sync
//
// 触发研报同步。
//   stock_code=600519  → 个股按需采集（精准命中东财个股接口，默认近1年）
//   stock_code=""      → 全市场采集（默认近 3 天）
//
// Query 参数：
//   stock_code  — 可选，指定则走个股接口
//   days        — 可选，天数（全市场默认 3，个股默认 365，最大 365）
// ─────────────────────────────────────────────────────────────────

func (h *StockReportHandler) Sync(c *gin.Context) {
	stockCode := strings.TrimSpace(c.Query("stock_code"))
	days, _   := strconv.Atoi(c.DefaultQuery("days", "0"))

	var (
		result *service.SyncResult
		err    error
	)

	if stockCode != "" {
		// 个股精准采集
		if days <= 0 || days > 365 {
			days = 365
		}
		result, err = h.svc.SyncByCode(c.Request.Context(), stockCode, days)
	} else {
		// 全市场采集
		if days <= 0 {
			days = 3
		}
		if days > 30 {
			days = 30
		}
		result, err = h.svc.SyncReports(c.Request.Context(), days)
	}

	if err != nil {
		h.log.Error("StockReportHandler.Sync failed",
			zap.String("code", stockCode), zap.Error(err))
		InternalError(c, "研报同步失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"fetched":    result.Fetched,
		"filtered":   result.Filtered,
		"saved":      result.Saved,
		"stock_code": stockCode,
		"message":    "同步完成",
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/reports/intel?page=1&limit=20&stock_code=600519
//
// 分页查询研报。
// 若指定 stock_code，service 层会先触发个股按需同步再返回数据库结果，
// 确保数据是最新的。
// ─────────────────────────────────────────────────────────────────

func (h *StockReportHandler) List(c *gin.Context) {
	page, _    := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _   := strconv.Atoi(c.DefaultQuery("limit", "20"))
	stockCode  := strings.TrimSpace(c.Query("stock_code"))

	if page <= 0 {
		page = 1
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	q := repo.StockReportQuery{
		StockCode: stockCode,
		Page:      page,
		Limit:     limit,
	}

	result, err := h.svc.GetReports(c.Request.Context(), q)
	if err != nil {
		h.log.Error("StockReportHandler.List failed", zap.Error(err))
		InternalError(c, "查询研报失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"total": result.Total,
		"page":  page,
		"limit": limit,
		"items": result.Items,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/reports/intel/ai
//
// 手动触发 AI 摘要批处理。
// ─────────────────────────────────────────────────────────────────

func (h *StockReportHandler) ProcessAI(c *gin.Context) {
	done, err := h.svc.ProcessAISummaries(c.Request.Context())
	if err != nil {
		h.log.Error("StockReportHandler.ProcessAI failed", zap.Error(err))
		InternalError(c, "AI 摘要处理失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"processed": done,
		"message":   "AI 摘要处理完成",
	})
}
