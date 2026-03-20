package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type StockScoreHandler struct {
	svc      *service.StockScoreService
	stockSvc *service.StockService
	log      *zap.Logger
}

func NewStockScoreHandler(svc *service.StockScoreService, stockSvc *service.StockService, log *zap.Logger) *StockScoreHandler {
	return &StockScoreHandler{svc: svc, stockSvc: stockSvc, log: log}
}

// GET /api/v1/stocks/:code/score?source=qq|em
func (h *StockScoreHandler) GetScore(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	source := c.Query("source")
	if source == "" {
		source = h.stockSvc.DefaultMarketSource()
	}

	result, err := h.svc.ScoreWithSource(c.Request.Context(), code, source)
	if err != nil {
		h.log.Warn("StockScoreHandler.GetScore failed",
			zap.String("code", code), zap.Error(err))
		InternalError(c, "评分计算失败: "+err.Error())
		return
	}
	OK(c, result)
}
