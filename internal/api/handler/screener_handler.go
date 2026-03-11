package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ScreenerHandler 处理量化筛选器相关路由。
type ScreenerHandler struct {
	screenerSvc *service.ScreenerService
	crawlerSvc  *service.CrawlerService
	log         *zap.Logger
}

func NewScreenerHandler(
	screenerSvc *service.ScreenerService,
	crawlerSvc *service.CrawlerService,
	log *zap.Logger,
) *ScreenerHandler {
	return &ScreenerHandler{
		screenerSvc: screenerSvc,
		crawlerSvc:  crawlerSvc,
		log:         log,
	}
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/screener/execute
//
// 执行多因子筛选，返回打分 TopN 列表。
//
// Body:
//   {
//     "min_score": 40,   // 最低分（默认 0）
//     "limit":     50,   // 返回条数（默认 50，上限 500）
//     "date":      "2025-03-11"  // 可选，空 = 今日
//   }
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) Execute(c *gin.Context) {
	var req service.ScreenerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		// 允许空 body（使用全部默认值）
		req = service.ScreenerRequest{}
	}

	result, err := h.screenerSvc.Execute(c.Request.Context(), req)
	if err != nil {
		h.log.Error("screener execute failed", zap.Error(err))
		InternalError(c, "筛选执行失败: "+err.Error())
		return
	}

	OK(c, result)
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/screener/sync
//
// 手动触发全市场数据同步（抓取东方财富 → 写入 stock_daily_snapshots）。
// 正常情况由定时任务驱动，此接口供调试和补抓使用。
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) SyncMarketData(c *gin.Context) {
	h.log.Info("manual market sync triggered", zap.String("ip", c.ClientIP()))

	count, err := h.crawlerSvc.SyncFullMarketData(c.Request.Context())
	if err != nil {
		h.log.Error("SyncFullMarketData failed", zap.Error(err))
		// 区分网络超时与内部错误
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"code":    50300,
			"message": "市场数据同步失败，可能是东方财富接口超时: " + err.Error(),
			"data":    nil,
		})
		return
	}

	OK(c, gin.H{
		"synced": count,
		"message": "同步完成",
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/screener/status
//
// 查询今日快照数量，用于判断是否已完成同步。
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) Status(c *gin.Context) {
	// 复用 Execute 传 0 分限制来获取 total 数量
	result, err := h.screenerSvc.Execute(c.Request.Context(), service.ScreenerRequest{
		MinScore: -9999, // 极低分，让全部股票通过过滤
		Limit:    1,
	})
	if err != nil {
		InternalError(c, err.Error())
		return
	}

	OK(c, gin.H{
		"date":  result.Date,
		"total": result.Total,
		"ready": result.Total > 0,
	})
}
