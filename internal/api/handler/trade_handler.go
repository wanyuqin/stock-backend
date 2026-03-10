package handler

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// TradeHandler 处理交易日志相关路由。
type TradeHandler struct {
	tradeSvc *service.TradeService
	log      *zap.Logger
}

func NewTradeHandler(tradeSvc *service.TradeService, log *zap.Logger) *TradeHandler {
	return &TradeHandler{tradeSvc: tradeSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/trades
// Body: { stock_code, action, price, volume, traded_at?, reason? }
// ─────────────────────────────────────────────────────────────────

func (h *TradeHandler) AddTrade(c *gin.Context) {
	var req service.AddTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}

	dto, err := h.tradeSvc.AddTradeLog(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		// 区分业务校验错误（400）与系统错误（500）
		if isValidationErr(err) {
			BadRequest(c, err.Error())
		} else {
			h.log.Error("AddTrade failed", zap.Error(err))
			InternalError(c, err.Error())
		}
		return
	}

	c.JSON(http.StatusCreated, Response{
		Code:    0,
		Message: "ok",
		Data:    dto,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/trades/:code
// 返回某只股票的全部交易历史（traded_at 倒序）
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

	OK(c, gin.H{
		"items": dtos,
		"count": len(dtos),
		"code":  code,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stats/performance
// 计算总盈亏：已平仓实现盈亏 + 持仓浮动盈亏
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

// defaultUserID 当前为单用户系统，固定 user_id = 1。

// isValidationErr 判断是否为业务层校验错误（应返回 400）。
// 校验错误由 validateTradeRequest / parseTradedAt 产生，
// 包含特定关键词，区别于数据库 IO 错误。
func isValidationErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	validationKeywords := []string{
		"不能为空", "格式错误", "必须大于", "超出合理范围",
		"只能是", "无法解析日期",
	}
	for _, kw := range validationKeywords {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
