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

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/trades
// ─────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/trades  全量流水（traded_at 倒序）
// Query: limit=200&offset=0
// ─────────────────────────────────────────────────────────────────

func (h *TradeHandler) ListAll(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	dtos, err := h.tradeSvc.ListAll(c.Request.Context(), defaultUserID, limit, offset)
	if err != nil {
		h.log.Error("ListAll trades failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{
		"items":  dtos,
		"count":  len(dtos),
		"limit":  limit,
		"offset": offset,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/trades/:code
// ─────────────────────────────────────────────────────────────────

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

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stats/performance
// ─────────────────────────────────────────────────────────────────

func (h *TradeHandler) GetPerformance(c *gin.Context) {
	report, err := h.tradeSvc.GetPerformance(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("GetPerformance failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, report)
}

// ─────────────────────────────────────────────────────────────────
// 辅助
// ─────────────────────────────────────────────────────────────────

func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range []string{"不能为空", "格式错误", "必须大于", "超出合理范围", "只能是", "无法解析日期"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
