package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// PositionHandler 处理 /api/v1/positions/* 路由
type PositionHandler struct {
	svc *service.PositionGuardianService
	log *zap.Logger
}

func NewPositionHandler(svc *service.PositionGuardianService, log *zap.Logger) *PositionHandler {
	return &PositionHandler{svc: svc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/positions/diagnose
//
// 纯量化指标刷新（不调 AI），供前端定时轮询使用。
// 返回：最新行情 + ATR/MA20/支撑压力 + 信号类型 + 量化决策依据
// ActionDirective 为空字符串，节省 token。
// ─────────────────────────────────────────────────────────────────

func (h *PositionHandler) Diagnose(c *gin.Context) {
	results, err := h.svc.DiagnoseAll(c.Request.Context())
	if err != nil {
		h.log.Error("DiagnoseAll failed", zap.Error(err))
		InternalError(c, "持仓诊断失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"count": len(results),
		"items": results,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/positions/analyze/:code
//
// 手动触发单只持仓的 AI 深度分析，会消耗 AI token。
// 前端点击「AI 分析」按钮时调用，结果由前端自行缓存展示。
// ─────────────────────────────────────────────────────────────────

func (h *PositionHandler) AnalyzeOne(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "stock_code is required")
		return
	}

	result, err := h.svc.AnalyzeOne(c.Request.Context(), code)
	if err != nil {
		h.log.Error("AnalyzeOne failed",
			zap.String("code", code),
			zap.Error(err),
		)
		c.JSON(http.StatusBadRequest, Response{
			Code:    CodeBadRequest,
			Message: err.Error(),
			Data:    nil,
		})
		return
	}

	OK(c, result)
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/positions/sync
//
// 同步手动录入的持仓：成本价、数量、可用数量。
// ─────────────────────────────────────────────────────────────────

func (h *PositionHandler) SyncPosition(c *gin.Context) {
	var req service.SyncPositionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}

	pos, err := h.svc.SyncPosition(c.Request.Context(), &req)
	if err != nil {
		h.log.Error("SyncPosition failed",
			zap.String("code", req.StockCode),
			zap.Error(err),
		)
		c.JSON(http.StatusBadRequest, Response{
			Code:    CodeBadRequest,
			Message: err.Error(),
			Data:    nil,
		})
		return
	}

	OK(c, pos)
}
