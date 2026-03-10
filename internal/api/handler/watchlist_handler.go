package handler

import (
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
)

// WatchlistHandler 处理 /api/v1/watchlist 相关请求。
type WatchlistHandler struct {
	watchlistRepo repo.WatchlistRepo
	stockRepo     repo.StockRepo
	stockSvc      *service.StockService
	log           *zap.Logger
}

func NewWatchlistHandler(
	watchlistRepo repo.WatchlistRepo,
	stockRepo repo.StockRepo,
	stockSvc *service.StockService,
	log *zap.Logger,
) *WatchlistHandler {
	return &WatchlistHandler{
		watchlistRepo: watchlistRepo,
		stockRepo:     stockRepo,
		stockSvc:      stockSvc,
		log:           log,
	}
}

// 当前单用户系统固定 user_id = 1。
const defaultUserID int64 = 1

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/watchlist
// 返回自选股列表，并附带每只股票的实时行情（异步并发抓取）。
// ─────────────────────────────────────────────────────────────────

// WatchlistItem 是对外的自选股响应体，合并了数据库记录和实时行情。
type WatchlistItem struct {
	ID        int64          `json:"id"`
	StockCode string         `json:"stock_code"`
	Note      string         `json:"note"`
	CreatedAt time.Time      `json:"created_at"`
	Quote     *service.Quote `json:"quote,omitempty"` // 实时行情（可能为 nil）
}

// List godoc
//
//	@Summary     获取自选股列表（附带实时行情）
//	@Tags        watchlist
//	@Produce     json
//	@Success     200  {object}  Response
//	@Router      /api/v1/watchlist [get]
func (h *WatchlistHandler) List(c *gin.Context) {
	items, err := h.watchlistRepo.ListByUser(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("WatchlistHandler.List", zap.Error(err))
		InternalError(c, "获取自选股失败")
		return
	}

	if len(items) == 0 {
		OK(c, gin.H{"items": []any{}, "count": 0})
		return
	}

	// ── 并发抓取所有自选股的实时行情 ─────────────────────────────
	codes := make([]string, len(items))
	for i, item := range items {
		codes[i] = item.StockCode
	}
	quotes, _ := h.stockSvc.GetMultipleQuotes(codes)
	// 行情抓取失败不阻断主流程，quote 字段为 nil 即可

	result := make([]WatchlistItem, len(items))
	for i, item := range items {
		result[i] = WatchlistItem{
			ID:        item.ID,
			StockCode: item.StockCode,
			Note:      item.Note,
			CreatedAt: item.CreatedAt,
			Quote:     quotes[item.StockCode], // nil-safe：map 查不到返回零值 nil
		}
	}

	OK(c, gin.H{
		"items": result,
		"count": len(result),
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/watchlist
// Body: {"stock_code": "600519", "note": "白酒龙头"}
// ─────────────────────────────────────────────────────────────────

type addWatchlistReq struct {
	StockCode string `json:"stock_code" binding:"required"`
	Note      string `json:"note"`
}

// Add godoc
//
//	@Summary     添加自选股
//	@Tags        watchlist
//	@Accept      json
//	@Produce     json
//	@Param       body  body  addWatchlistReq  true  "股票代码和备注"
//	@Success     200   {object}  Response
//	@Router      /api/v1/watchlist [post]
func (h *WatchlistHandler) Add(c *gin.Context) {
	var req addWatchlistReq
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}

	req.StockCode = strings.TrimSpace(req.StockCode)
	if req.StockCode == "" {
		BadRequest(c, "stock_code 不能为空")
		return
	}

	// ── 校验股票是否在 stocks 表中存在 ────────────────────────────
	stock, err := h.stockRepo.GetByCode(c.Request.Context(), req.StockCode)
	if err != nil {
		// 股票不在本地库，尝试从东方财富验证后写入
		quote, fetchErr := h.stockSvc.GetRealtimeQuote(req.StockCode)
		if fetchErr != nil {
			h.log.Warn("WatchlistHandler.Add: stock not found",
				zap.String("code", req.StockCode),
				zap.Error(fetchErr),
			)
			NotFound(c, "股票代码不存在或无法获取行情: "+req.StockCode)
			return
		}
		// 行情抓到了，顺手写入 stocks 表（自动同步）
		newStock := &model.Stock{
			Code:   quote.Code,
			Name:   quote.Name,
			Market: model.Market(quote.Market),
		}
		_ = h.stockRepo.Upsert(c.Request.Context(), newStock)
		stock = newStock
	}

	// ── 写入 watchlist 表 ─────────────────────────────────────────
	entry := &model.Watchlist{
		UserID:    defaultUserID,
		StockCode: stock.Code,
		Note:      strings.TrimSpace(req.Note),
	}
	if err := h.watchlistRepo.Add(c.Request.Context(), entry); err != nil {
		h.log.Error("WatchlistHandler.Add DB", zap.Error(err))
		InternalError(c, "添加自选股失败")
		return
	}

	h.log.Info("watchlist added",
		zap.String("code", req.StockCode),
		zap.Int64("user_id", defaultUserID),
	)
	OK(c, entry)
}

// ─────────────────────────────────────────────────────────────────
// DELETE /api/v1/watchlist/:code
// ─────────────────────────────────────────────────────────────────

// Remove godoc
//
//	@Summary     移除自选股
//	@Tags        watchlist
//	@Produce     json
//	@Param       code  path  string  true  "股票代码"
//	@Success     200  {object}  Response
//	@Router      /api/v1/watchlist/{code} [delete]
func (h *WatchlistHandler) Remove(c *gin.Context) {
	code := strings.TrimSpace(c.Param("code"))
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	if err := h.watchlistRepo.Remove(c.Request.Context(), defaultUserID, code); err != nil {
		h.log.Warn("WatchlistHandler.Remove", zap.String("code", code), zap.Error(err))
		NotFound(c, "自选股不存在: "+code)
		return
	}

	OK(c, gin.H{"removed": code})
}
