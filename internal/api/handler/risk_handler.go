package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type RiskHandler struct {
	svc *service.RiskService
	log *zap.Logger
}

func NewRiskHandler(svc *service.RiskService, log *zap.Logger) *RiskHandler {
	return &RiskHandler{svc: svc, log: log}
}

func (h *RiskHandler) GetProfile(c *gin.Context) {
	profile, err := h.svc.GetProfile(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, profile)
}

func (h *RiskHandler) UpdateProfile(c *gin.Context) {
	var req service.UpdateRiskProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	profile, err := h.svc.UpdateProfile(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, profile)
}

func (h *RiskHandler) PrecheckTrade(c *gin.Context) {
	var req service.TradePrecheckRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	res, err := h.svc.PrecheckTrade(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) GetPositionSizeSuggestion(c *gin.Context) {
	buyPrice, err := strconv.ParseFloat(c.Query("buy_price"), 64)
	if err != nil {
		BadRequest(c, "buy_price 参数错误")
		return
	}
	stopLossPrice, err := strconv.ParseFloat(c.Query("stop_loss_price"), 64)
	if err != nil {
		BadRequest(c, "stop_loss_price 参数错误")
		return
	}
	res, err := h.svc.SuggestPositionSize(c.Request.Context(), defaultUserID, buyPrice, stopLossPrice)
	if err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) GetPortfolioExposure(c *gin.Context) {
	res, err := h.svc.GetPortfolioExposure(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) GetDailyRiskState(c *gin.Context) {
	res, err := h.svc.GetDailyRiskState(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) GetEventCalendar(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	res, err := h.svc.GetEventCalendar(c.Request.Context(), defaultUserID, days)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) GetTodayRiskTodo(c *gin.Context) {
	res, err := h.svc.GetTodayRiskTodo(c.Request.Context(), defaultUserID)
	if err != nil {
		InternalError(c, err.Error())
		return
	}
	OK(c, res)
}

func (h *RiskHandler) UpdateTodayRiskTodoStatus(c *gin.Context) {
	var req service.UpdateTodayRiskTodoRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	if err := h.svc.UpdateTodayRiskTodoStatus(c.Request.Context(), defaultUserID, &req); err != nil {
		BadRequest(c, err.Error())
		return
	}
	OK(c, gin.H{"success": true})
}
