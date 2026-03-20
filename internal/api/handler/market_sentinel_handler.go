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
// 每次都返回实时数据（服务层有 5s 内存缓存，不会打穿接口）
func (h *MarketSentinelHandler) GetSummary(c *gin.Context) {
	summary, err := h.svc.GetSummary(c.Request.Context())
	if err != nil {
		h.log.Error("get market summary failed", zap.Error(err))
		InternalError(c, "无法获取市场数据: "+err.Error())
		return
	}
	OK(c, summary)
}
