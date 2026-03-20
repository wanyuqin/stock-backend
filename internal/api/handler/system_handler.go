package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type SystemHandler struct {
	defaultMarketSource string
	log                 *zap.Logger
}

func NewSystemHandler(defaultMarketSource string, log *zap.Logger) *SystemHandler {
	return &SystemHandler{
		defaultMarketSource: defaultMarketSource,
		log:                 log,
	}
}

// GET /api/v1/system/data-source-status
func (h *SystemHandler) GetDataSourceStatus(c *gin.Context) {
	OK(c, service.BuildDataSourceStatus(h.defaultMarketSource))
}
