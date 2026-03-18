package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type BuyPlanHandler struct {
	svc *service.BuyPlanService
	log *zap.Logger
}

func NewBuyPlanHandler(svc *service.BuyPlanService, log *zap.Logger) *BuyPlanHandler {
	return &BuyPlanHandler{svc: svc, log: log}
}

// GET /api/v1/buy-plans?status=active|watching|ready|done|executed
func (h *BuyPlanHandler) List(c *gin.Context) {
	status := c.DefaultQuery("status", "active")
	plans, err := h.svc.List(c.Request.Context(), defaultUserID, status)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"items": plans, "count": len(plans)})
}

// GET /api/v1/stocks/:code/buy-plans
func (h *BuyPlanHandler) ListByCode(c *gin.Context) {
	code := c.Param("code")
	plans, err := h.svc.ListByCode(c.Request.Context(), defaultUserID, code)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"items": plans, "count": len(plans)})
}

// POST /api/v1/buy-plans
func (h *BuyPlanHandler) Create(c *gin.Context) {
	var req service.CreateBuyPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	plan, err := h.svc.Create(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, plan)
}

// PUT /api/v1/buy-plans/:id
func (h *BuyPlanHandler) Update(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		BadRequest(c, "id 格式错误")
		return
	}
	var req service.UpdateBuyPlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	plan, err := h.svc.Update(c.Request.Context(), defaultUserID, id, &req)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, plan)
}

// PATCH /api/v1/buy-plans/:id/status
// Body: {"status": "EXECUTED", "trade_log_id": 123}
// trade_log_id 可选，传入时自动关联交易记录 → 形成「计划→执行」闭环
func (h *BuyPlanHandler) UpdateStatus(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		BadRequest(c, "id 格式错误")
		return
	}
	var body struct {
		Status     string `json:"status"       binding:"required"`
		TradeLogID *int64 `json:"trade_log_id"` // 可选：执行时关联的交易记录 ID
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	if err := h.svc.UpdateStatus(c.Request.Context(), defaultUserID, id, body.Status, body.TradeLogID); err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, gin.H{"updated": id, "status": body.Status})
}

// DELETE /api/v1/buy-plans/:id
func (h *BuyPlanHandler) Delete(c *gin.Context) {
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

// POST /api/v1/buy-plans/check-triggers
func (h *BuyPlanHandler) CheckTriggers(c *gin.Context) {
	triggered, err := h.svc.CheckTriggers(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"triggered": len(triggered), "items": triggered})
}
