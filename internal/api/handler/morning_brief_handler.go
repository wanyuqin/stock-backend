package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// MorningBriefHandler 处理 /api/v1/morning-brief 相关请求。
type MorningBriefHandler struct {
	svc *service.MorningBriefService
	log *zap.Logger
}

func NewMorningBriefHandler(svc *service.MorningBriefService, log *zap.Logger) *MorningBriefHandler {
	return &MorningBriefHandler{svc: svc, log: log}
}

// GET /api/v1/morning-brief?force=1
func (h *MorningBriefHandler) Get(c *gin.Context) {
	force := c.Query("force") == "1"
	brief, err := h.svc.Generate(c.Request.Context(), defaultUserID, force)
	if err != nil {
		InternalError(c, "生成开盘前报告失败: "+err.Error())
		return
	}
	OK(c, brief)
}
