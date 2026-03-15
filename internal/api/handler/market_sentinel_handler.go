package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type MarketSentinelHandler struct {
	svc *service.MarketSentinelService
	log *zap.Logger
}

func NewMarketSentinelHandler(svc *service.MarketSentinelService, log *zap.Logger) *MarketSentinelHandler {
	return &MarketSentinelHandler{svc: svc, log: log}
}

// GetSummary GET /api/v1/market/summary
func (h *MarketSentinelHandler) GetSummary(c *gin.Context) {
	summary, err := h.svc.GetSummary(c.Request.Context())
	if err != nil {
		h.log.Error("get market summary failed", zap.Error(err))
		// 如果获取失败，尝试触发一次实时分析
		// 注意：这可能会增加延迟，但保证了数据的新鲜度（如果是首次启动）
		if err := h.svc.RunAnalysis(c.Request.Context()); err == nil {
			summary, err = h.svc.GetSummary(c.Request.Context())
		}

		if err != nil {
			InternalError(c, "无法获取市场数据: "+err.Error())
			return
		}
	}
	OK(c, summary)
}
