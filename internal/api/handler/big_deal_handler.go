package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// BigDealHandler 处理大单分析路由
// GET /api/v1/stocks/:code/big-deal
type BigDealHandler struct {
	svc      *service.BigDealService
	stockSvc *service.StockService // 用于获取实时涨跌幅
	log      *zap.Logger
}

func NewBigDealHandler(svc *service.BigDealService, stockSvc *service.StockService, log *zap.Logger) *BigDealHandler {
	return &BigDealHandler{svc: svc, stockSvc: stockSvc, log: log}
}

// GetBigDeal GET /api/v1/stocks/:code/big-deal
//
// 按需拉取大单分析数据（腾讯 getDadan 接口，30秒缓存）
// 查询参数：?change_rate=0.00（可选，股价涨跌幅，用于洗盘信号判断）
func (h *BigDealHandler) GetBigDeal(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	// 优先从 query 参数取涨跌幅，避免二次请求
	changeRate := 0.0
	if cr := c.Query("change_rate"); cr != "" {
		if f, err := strconv.ParseFloat(cr, 64); err == nil {
			changeRate = f
		}
	} else {
		// 没传则实时查询（已有缓存）
		if quote, err := h.stockSvc.GetRealtimeQuote(code); err == nil {
			changeRate = quote.ChangeRate
		}
	}

	// 腾讯接口 code 格式：sh603920 / sz000858
	qqCode := buildQQCode(code)
	summary, err := h.svc.GetBigDeal(c.Request.Context(), qqCode, changeRate)
	if err != nil {
		h.log.Warn("GetBigDeal failed",
			zap.String("code", code),
			zap.Error(err),
		)
		InternalError(c, "大单数据获取失败: "+err.Error())
		return
	}

	OK(c, summary)
}

// buildQQCode 将内部代码格式转为腾讯接口格式
// 603920 → sh603920，000858 → sz000858
func buildQQCode(code string) string {
	if len(code) == 0 {
		return code
	}
	// 已带前缀则原样返回
	if code[:2] == "sh" || code[:2] == "sz" {
		return code
	}
	if code[0] == '6' {
		return "sh" + code
	}
	return "sz" + code
}
