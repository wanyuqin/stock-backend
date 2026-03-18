package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type EquityHandler struct {
	svc *service.EquityService
	log *zap.Logger
}

func NewEquityHandler(svc *service.EquityService, log *zap.Logger) *EquityHandler {
	return &EquityHandler{svc: svc, log: log}
}

// GET /api/v1/stats/equity-curve?days=365
func (h *EquityHandler) GetCurve(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "365"))
	dto, err := h.svc.GetCurve(c.Request.Context(), days)
	if err != nil {
		h.log.Error("GetCurve failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, dto)
}

// POST /api/v1/stats/equity-snapshot  手动触发（调试用）
func (h *EquityHandler) TakeSnapshot(c *gin.Context) {
	if err := h.svc.TakeSnapshot(c.Request.Context(), defaultUserID); err != nil {
		h.log.Error("TakeSnapshot failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"message": "快照已保存"})
}
