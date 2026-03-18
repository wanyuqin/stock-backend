package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type ScreenerTemplateHandler struct {
	svc *service.ScreenerTemplateService
	log *zap.Logger
}

func NewScreenerTemplateHandler(svc *service.ScreenerTemplateService, log *zap.Logger) *ScreenerTemplateHandler {
	return &ScreenerTemplateHandler{svc: svc, log: log}
}

// GET /api/v1/screener/templates
func (h *ScreenerTemplateHandler) List(c *gin.Context) {
	items, err := h.svc.List(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"items": items, "count": len(items)})
}

// POST /api/v1/screener/templates
func (h *ScreenerTemplateHandler) Create(c *gin.Context) {
	var req service.CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, err.Error())
		return
	}
	t, err := h.svc.Create(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, t)
}

// PUT /api/v1/screener/templates/:id
func (h *ScreenerTemplateHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		BadRequest(c, "id 格式错误")
		return
	}
	var req service.UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, err.Error())
		return
	}
	t, err := h.svc.Update(c.Request.Context(), defaultUserID, id, &req)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, t)
}

// DELETE /api/v1/screener/templates/:id
func (h *ScreenerTemplateHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		BadRequest(c, "id 格式错误")
		return
	}
	if err := h.svc.Delete(c.Request.Context(), defaultUserID, id); err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, gin.H{"deleted": id})
}
