package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

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
// 查询参数：
//   - limit  int (default=50, max=200)
//   - offset int (default=0)
// ─────────────────────────────────────────────────────────────────

// List godoc
//
//	@Summary     列出所有股票
//	@Tags        stocks
//	@Produce     json
//	@Param       limit   query  int  false  "每页数量(default 50)"
//	@Param       offset  query  int  false  "偏移量(default 0)"
//	@Success     200  {object}  Response
//	@Router      /api/v1/stocks [get]
func (h *StockHandler) List(c *gin.Context) {
	limit := queryInt(c, "limit", 50)
	offset := queryInt(c, "offset", 0)

	// 防止参数越界
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
// 查询数据库中的股票基础信息
// ─────────────────────────────────────────────────────────────────

func (h *StockHandler) GetByCode(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	stock, err := h.stockRepo.GetByCode(c.Request.Context(), code)
	if err != nil {
		h.log.Warn("StockHandler.GetByCode not found", zap.String("code", code), zap.Error(err))
		NotFound(c, "股票不存在: "+code)
		return
	}

	OK(c, stock)
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/quote
// 调用 MarketProvider 获取实时行情（带 5s 内存缓存）
// ─────────────────────────────────────────────────────────────────

// GetQuote godoc
//
//	@Summary     获取股票实时行情
//	@Tags        stocks
//	@Produce     json
//	@Param       code  path  string  true  "股票代码，如 600519"
//	@Success     200  {object}  Response{data=service.Quote}
//	@Router      /api/v1/stocks/{code}/quote [get]
func (h *StockHandler) GetQuote(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	quote, err := h.stockSvc.GetRealtimeQuote(code)
	if err != nil {
		h.log.Error("StockHandler.GetQuote",
			zap.String("code", code),
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

// queryInt 从 query string 读取整数参数，失败时返回 defaultVal。
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
