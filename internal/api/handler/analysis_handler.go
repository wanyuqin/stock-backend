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
//
// Query:
//   limit=120         K 线根数（默认 120，东财日K）
//   period=daily      周期，目前只支持 daily
//   source=qq|em      数据源（默认读取服务配置）
//
// 腾讯数据源说明：
//   - 仅支持近 5 个交易日（接口限制）
//   - OHLC 由分时数据推算，精度与东财日K略有差异
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

	source := c.Query("source")
	if source == "" {
		source = h.stockSvc.DefaultMarketSource()
	}

	var (
		klineData *service.KLineResponse
		err       error
	)

	klineData, err = h.stockSvc.GetKLineBySource(code, limit, source)

	if err != nil {
		h.log.Error("GetKLine failed",
			zap.String("code", code),
			zap.String("source", source),
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
// GET /api/v1/stocks/:code/minute
//
// 获取分时数据（腾讯证券接口）
// Query:
//   days=1    返回最近 N 个交易日分时（1=今日，最多5）
//             days=1 时走 minute/query（更实时）
//             days>1 时走 day/query
// ─────────────────────────────────────────────────────────────────

func (h *AnalysisHandler) GetMinute(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	days := 1
	if d := c.Query("days"); d != "" {
		if n, err := strconv.Atoi(d); err == nil && n >= 1 {
			days = n
		}
	}

	if days == 1 {
		// 当日分时，走更实时的 minute/query
		data, err := h.stockSvc.GetMinuteData(code)
		if err != nil {
			h.log.Error("GetMinute(today) failed",
				zap.String("code", code), zap.Error(err))
			c.JSON(http.StatusBadGateway, Response{
				Code: 50200, Message: "分时数据获取失败: " + err.Error(),
			})
			return
		}
		OK(c, data)
		return
	}

	// 多日分时
	results, err := h.stockSvc.GetDayMinuteData(code, days)
	if err != nil {
		h.log.Error("GetMinute(days) failed",
			zap.String("code", code), zap.Error(err))
		c.JSON(http.StatusBadGateway, Response{
			Code: 50200, Message: "分时数据获取失败: " + err.Error(),
		})
		return
	}

	OK(c, gin.H{
		"code":  code,
		"days":  len(results),
		"items": results,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/analysis
// ─────────────────────────────────────────────────────────────────

func (h *AnalysisHandler) GetAnalysis(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	source := c.Query("source")
	if source == "" {
		source = h.stockSvc.DefaultMarketSource()
	}

	quote, err := h.stockSvc.GetRealtimeQuoteBySource(code, source)
	if err != nil {
		h.log.Error("GetAnalysis: fetch quote failed",
			zap.String("code", code),
			zap.String("source", source),
			zap.Error(err))
		c.JSON(http.StatusBadGateway, Response{
			Code:    50200,
			Message: "获取行情数据失败，无法进行 AI 分析: " + err.Error(),
		})
		return
	}

	result, err := h.aiSvc.Analyze(c.Request.Context(), quote)
	if err != nil {
		h.log.Error("GetAnalysis: AI analyze failed",
			zap.String("code", code), zap.Error(err))
		InternalError(c, "AI 分析失败: "+err.Error())
		return
	}

	OK(c, result)
}
