package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type TradeHandler struct {
	tradeSvc *service.TradeService
	log      *zap.Logger
}

func NewTradeHandler(tradeSvc *service.TradeService, log *zap.Logger) *TradeHandler {
	return &TradeHandler{tradeSvc: tradeSvc, log: log}
}

// POST /api/v1/trades
func (h *TradeHandler) AddTrade(c *gin.Context) {
	var req service.AddTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	dto, err := h.tradeSvc.AddTradeLog(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		if isValidationErr(err) {
			BadRequest(c, err.Error())
		} else {
			h.log.Error("AddTrade failed", zap.Error(err))
			InternalError(c, err.Error())
		}
		return
	}
	c.JSON(http.StatusCreated, Response{Code: 0, Message: "ok", Data: dto})
}

// PUT /api/v1/trades/:id
func (h *TradeHandler) UpdateTrade(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		BadRequest(c, "无效的 id")
		return
	}
	var req service.UpdateTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	dto, err := h.tradeSvc.UpdateTradeLog(c.Request.Context(), defaultUserID, id, &req)
	if err != nil {
		if isValidationErr(err) {
			BadRequest(c, err.Error())
		} else {
			h.log.Error("UpdateTrade failed", zap.Int64("id", id), zap.Error(err))
			InternalError(c, err.Error())
		}
		return
	}
	OK(c, dto)
}

// DELETE /api/v1/trades/:id
func (h *TradeHandler) DeleteTrade(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		BadRequest(c, "无效的 id")
		return
	}
	if err := h.tradeSvc.DeleteTradeLog(c.Request.Context(), defaultUserID, id); err != nil {
		h.log.Error("DeleteTrade failed", zap.Int64("id", id), zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"deleted": true, "id": id})
}

// GET /api/v1/trades
func (h *TradeHandler) ListAll(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	dtos, err := h.tradeSvc.ListAll(c.Request.Context(), defaultUserID, limit, offset)
	if err != nil {
		h.log.Error("ListAll trades failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"items": dtos, "count": len(dtos), "limit": limit, "offset": offset})
}

// GET /api/v1/trades/:code
func (h *TradeHandler) ListByCode(c *gin.Context) {
	code := strings.ToUpper(strings.TrimSpace(c.Param("code")))
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}
	dtos, err := h.tradeSvc.ListByCode(c.Request.Context(), defaultUserID, code)
	if err != nil {
		h.log.Error("ListByCode failed", zap.String("code", code), zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"items": dtos, "count": len(dtos), "code": code})
}

// GET /api/v1/stats/performance
func (h *TradeHandler) GetPerformance(c *gin.Context) {
	report, err := h.tradeSvc.GetPerformance(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("GetPerformance failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, report)
}

func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range []string{"不能为空", "格式错误", "必须大于", "超出合理范围", "只能是", "无法解析日期", "不存在", "无权限"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
