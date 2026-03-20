package handler

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
)

// StockHandler 处理 /api/v1/stocks 相关请求。
type StockHandler struct {
	stockRepo repo.StockRepo
	stockSvc  *service.StockService
	log       *zap.Logger
}

func NewStockHandler(
	stockRepo repo.StockRepo,
	stockSvc *service.StockService,
	log *zap.Logger,
) *StockHandler {
	return &StockHandler{
		stockRepo: stockRepo,
		stockSvc:  stockSvc,
		log:       log,
	}
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks
// ─────────────────────────────────────────────────────────────────

func (h *StockHandler) List(c *gin.Context) {
	limit := queryInt(c, "limit", 50)
	offset := queryInt(c, "offset", 0)
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	stocks, err := h.stockRepo.List(c.Request.Context(), limit, offset)
	if err != nil {
		h.log.Error("StockHandler.List", zap.Error(err))
		InternalError(c, "获取股票列表失败")
		return
	}

	OK(c, gin.H{
		"items":  stocks,
		"limit":  limit,
		"offset": offset,
		"count":  len(stocks),
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code
//
// 优先查 stocks 表；查不到则从实时行情接口获取股票名称并自动入库，
// 避免未提前爬取的合法 A 股代码报"股票不存在"。
// ─────────────────────────────────────────────────────────────────

func (h *StockHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	ctx := c.Request.Context()

	// 1. 先查数据库
	stock, err := h.stockRepo.GetByCode(ctx, code)
	if err == nil {
		OK(c, stock)
		return
	}

	// 2. 数据库未入库 → 从行情接口拉取，自动注册
	h.log.Info("GetByCode: not in db, fetching from market", zap.String("code", code))
	quote, qErr := h.stockSvc.GetRealtimeQuote(code)
	if qErr != nil || quote == nil || quote.Name == "" {
		h.log.Warn("GetByCode: market fetch failed", zap.String("code", code), zap.Error(qErr))
		NotFound(c, "股票不存在: "+code)
		return
	}

	// 推断市场（0/3 开头为深交所，其余为上交所）
	market := model.MarketSH
	if len(code) == 6 && (code[0] == '0' || code[0] == '3') {
		market = model.MarketSZ
	}

	newStock := &model.Stock{
		Code:   code,
		Name:   quote.Name,
		Market: market,
	}

	// 异步入库，不阻塞响应；用 Background context 避免请求结束后 ctx 被取消
	go func(s *model.Stock) {
		if uErr := h.stockRepo.Upsert(context.Background(), s); uErr != nil {
			h.log.Warn("GetByCode: upsert failed", zap.String("code", s.Code), zap.Error(uErr))
		}
	}(newStock)

	OK(c, newStock)
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/quote
// ─────────────────────────────────────────────────────────────────

func (h *StockHandler) GetQuote(c *gin.Context) {
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
		h.log.Error("StockHandler.GetQuote",
			zap.String("code", code),
			zap.String("source", source),
			zap.Error(err),
		)
		c.JSON(http.StatusBadGateway, Response{
			Code:    50200,
			Message: "行情数据获取失败: " + err.Error(),
			Data:    nil,
		})
		return
	}

	OK(c, quote)
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

func queryInt(c *gin.Context, key string, defaultVal int) int {
	s := c.Query(key)
	if s == "" {
		return defaultVal
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return defaultVal
	}
	return v
}
