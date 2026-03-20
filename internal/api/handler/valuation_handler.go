package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ValuationHandler 处理估值分位相关路由。
type ValuationHandler struct {
	svc *service.ValuationService
	log *zap.Logger
}

func NewValuationHandler(svc *service.ValuationService, log *zap.Logger) *ValuationHandler {
	return &ValuationHandler{svc: svc, log: log}
}

// GET /api/v1/stocks/:code/valuation
func (h *ValuationHandler) GetValuation(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		BadRequest(c, "股票代码不能为空")
		return
	}

	source := c.Query("source")
	if source == "" {
		source = h.svc.DefaultMarketSource()
	}

	snap, err := h.svc.GetValuationBySource(c.Request.Context(), code, source)
	if err != nil {
		h.log.Warn("ValuationHandler.GetValuation failed",
			zap.String("code", code), zap.String("source", source), zap.Error(err))
		InternalError(c, "获取估值失败: "+err.Error())
		return
	}

	status := service.GetValuationStatus(snap.PETTM, snap.PEPercentile)
	OK(c, gin.H{
		"code":          snap.StockCode,
		"name":          snap.StockName,
		"pe_ttm":        snap.PETTM,
		"pb":            snap.PB,
		"pe_percentile": snap.PEPercentile,
		"pb_percentile": snap.PBPercentile,
		"history_days":  snap.HistoryDays,
		"status":        status,
		"updated_at":    snap.UpdatedAt.Format("2006-01-02 15:04:05"),
	})
}

// GET /api/v1/market/valuation-summary
func (h *ValuationHandler) GetSummary(c *gin.Context) {
	summary, err := h.svc.GetWatchlistSummary(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("ValuationHandler.GetSummary failed", zap.Error(err))
		InternalError(c, "获取估值汇总失败: "+err.Error())
		return
	}
	OK(c, summary)
}

// POST /api/v1/market/valuation-sync
// 手动触发自选股估值同步（今日数据）。
func (h *ValuationHandler) TriggerSync(c *gin.Context) {
	source := c.Query("source")
	if source == "" {
		source = h.svc.DefaultMarketSource()
	}

	result, err := h.svc.SyncWatchlistValuationsBySource(c.Request.Context(), defaultUserID, source)
	if err != nil {
		h.log.Error("ValuationHandler.TriggerSync failed", zap.Error(err))
		InternalError(c, "同步失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"total":   result.Total,
		"success": result.Success,
		"failed":  result.Failed,
		"message": "估值同步完成",
	})
}

// POST /api/v1/market/valuation-backfill?days=90
// 回补历史估值数据：用当前 PE/PB 值模拟填充过去 N 天，
// 快速积累历史序列让分位计算可用（精度不如真实历史，但比空数据强）。
func (h *ValuationHandler) BackfillHistory(c *gin.Context) {
	days := 90 // 默认回补 90 天
	if d := c.Query("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n > 0 && n <= 365 {
			days = n
		}
	}

	result, err := h.svc.BackfillValuationHistory(c.Request.Context(), defaultUserID, days)
	if err != nil {
		h.log.Error("ValuationHandler.BackfillHistory failed", zap.Error(err))
		InternalError(c, "回补失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"days":    days,
		"total":   result.Total,
		"success": result.Success,
		"failed":  result.Failed,
		"message": "历史数据回补完成，分位计算精度将逐步提升",
	})
}
