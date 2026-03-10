package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// AnalysisHandler 处理 /api/v1/stocks/:code/analysis 和 /api/v1/stocks/:code/kline
type AnalysisHandler struct {
	stockSvc *service.StockService
	aiSvc    *service.AIAnalysisService
	log      *zap.Logger
}

func NewAnalysisHandler(
	stockSvc *service.StockService,
	aiSvc *service.AIAnalysisService,
	log *zap.Logger,
) *AnalysisHandler {
	return &AnalysisHandler{stockSvc: stockSvc, aiSvc: aiSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/kline
// Query: limit=120 (default), period=daily (only daily supported now)
// ─────────────────────────────────────────────────────────────────

func (h *AnalysisHandler) GetKLine(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	limit := 120
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 {
			limit = n
		}
	}

	klineData, err := h.stockSvc.GetKLine(code, limit)
	if err != nil {
		h.log.Error("GetKLine failed",
			zap.String("code", code),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, Response{
			Code:    50200,
			Message: "K 线数据获取失败: " + err.Error(),
			Data:    nil,
		})
		return
	}

	OK(c, klineData)
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/analysis
// 先拿实时行情，再送给 AI 分析，结果缓存 30 分钟
// ─────────────────────────────────────────────────────────────────

func (h *AnalysisHandler) GetAnalysis(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	// 先获取实时行情作为 AI 分析的输入
	quote, err := h.stockSvc.GetRealtimeQuote(code)
	if err != nil {
		h.log.Error("GetAnalysis: fetch quote failed",
			zap.String("code", code),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, Response{
			Code:    50200,
			Message: "获取行情数据失败，无法进行 AI 分析: " + err.Error(),
			Data:    nil,
		})
		return
	}

	result, err := h.aiSvc.Analyze(c.Request.Context(), quote)
	if err != nil {
		h.log.Error("GetAnalysis: AI analyze failed",
			zap.String("code", code),
			zap.Error(err),
		)
		InternalError(c, "AI 分析失败: "+err.Error())
		return
	}

	OK(c, result)
}
