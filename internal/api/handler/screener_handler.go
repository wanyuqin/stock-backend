package handler

import (
	"errors"
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
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) Execute(c *gin.Context) {
	var req service.ScreenerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
// 返回码说明：
//   200 synced>0   — 同步成功
//   200 synced=0   — 非交易时段，接口空数据（不是错误）
//   500            — 网络超时 / 解析失败 / DB 写入失败（真实错误）
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) SyncMarketData(c *gin.Context) {
	h.log.Info("manual market sync triggered", zap.String("ip", c.ClientIP()))

	count, err := h.crawlerSvc.SyncFullMarketData(c.Request.Context())
	if err != nil {
		// 判断错误类型
		var syncErr *service.SyncError
		if errors.As(err, &syncErr) {
			switch syncErr.Kind {
			case service.SyncErrEmptyData:
				// 非交易时段 — 正常情况，200 返回，前端据此展示提示
				h.log.Warn("sync: non-trading hours, empty data")
				c.JSON(http.StatusOK, gin.H{
					"code":    0,
					"message": "ok",
					"data": gin.H{
						"synced":       0,
						"non_trading":  true,
						"notice":       "当前为非交易时段（收盘后/周末/节假日），行情数据暂不可用，请在交易日 09:25–15:00 期间同步",
					},
				})
				return

			case service.SyncErrNetwork:
				// 网络问题，可重试
				h.log.Error("sync: network error", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{
					"code":    50001,
					"message": "网络请求失败，请检查网络后重试: " + syncErr.Message,
					"data":    nil,
				})
				return
			}
		}

		// 其他未知错误
		h.log.Error("SyncFullMarketData failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    50000,
			"message": "同步失败: " + err.Error(),
			"data":    nil,
		})
		return
	}

	h.log.Info("sync: completed", zap.Int("synced", count))
	c.JSON(http.StatusOK, gin.H{
		"code":    0,
		"message": "ok",
		"data": gin.H{
			"synced":      count,
			"non_trading": false,
			"notice":      "",
		},
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/screener/status
// ─────────────────────────────────────────────────────────────────

func (h *ScreenerHandler) Status(c *gin.Context) {
	result, err := h.screenerSvc.Execute(c.Request.Context(), service.ScreenerRequest{
		MinScore: -9999,
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
