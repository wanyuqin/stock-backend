package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type StockScoreHandler struct {
	svc *service.StockScoreService
	log *zap.Logger
}

func NewStockScoreHandler(svc *service.StockScoreService, log *zap.Logger) *StockScoreHandler {
	return &StockScoreHandler{svc: svc, log: log}
}

// GET /api/v1/stocks/:code/score
func (h *StockScoreHandler) GetScore(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}
	result, err := h.svc.Score(c.Request.Context(), code)
	if err != nil {
		h.log.Warn("StockScoreHandler.GetScore failed",
			zap.String("code", code), zap.Error(err))
		InternalError(c, "评分计算失败: "+err.Error())
		return
	}
	OK(c, result)
}
