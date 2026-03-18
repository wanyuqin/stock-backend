package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type SectorHeatmapHandler struct {
	svc *service.SectorHeatmapService
	log *zap.Logger
}

func NewSectorHeatmapHandler(svc *service.SectorHeatmapService, log *zap.Logger) *SectorHeatmapHandler {
	return &SectorHeatmapHandler{svc: svc, log: log}
}

// GET /api/v1/market/sector-heatmap
func (h *SectorHeatmapHandler) GetHeatmap(c *gin.Context) {
	dto, err := h.svc.GetHeatmap(c.Request.Context())
	if err != nil {
		h.log.Error("GetHeatmap failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, dto)
}
