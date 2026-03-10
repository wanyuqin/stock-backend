package router

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/api/handler"
	"stock-backend/internal/api/middleware"
	"stock-backend/internal/config"
	"stock-backend/internal/data"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
)

func New(cfg *config.Config, log *zap.Logger) *gin.Engine {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()

	// ── 全局中间件 ───────────────────────────────────────────────
	r.Use(gin.Recovery())
	r.Use(middleware.ZapLogger(log))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))

	// ── 依赖注入 ─────────────────────────────────────────────────
	db := data.DB()

	stockRepo     := repo.NewStockRepo(db)
	watchlistRepo := repo.NewWatchlistRepo(db)
	tradeRepo     := repo.NewTradeLogRepo(db)

	stockSvc    := service.NewStockService(log)
	aiSvc       := service.NewAIAnalysisService(log)
	tradeSvc    := service.NewTradeService(tradeRepo, stockSvc, log)

	stockHandler     := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	analysisHandler  := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler     := handler.NewTradeHandler(tradeSvc, log)
	healthHandler    := handler.NewHealthHandler()

	// ── 健康检查 ──────────────────────────────────────────────────
	r.GET("/health", healthHandler.Check)
	r.GET("/readyz", healthHandler.Ready)

	// ── API v1 ───────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// ── 股票 ────────────────────────────────────────────────
		stocks := v1.Group("/stocks")
		{
			stocks.GET("",                stockHandler.List)           // GET  /api/v1/stocks
			stocks.GET("/:code",          stockHandler.GetByCode)      // GET  /api/v1/stocks/:code
			stocks.GET("/:code/quote",    stockHandler.GetQuote)       // GET  /api/v1/stocks/:code/quote
			stocks.GET("/:code/kline",    analysisHandler.GetKLine)    // GET  /api/v1/stocks/:code/kline
			stocks.GET("/:code/analysis", analysisHandler.GetAnalysis) // GET  /api/v1/stocks/:code/analysis
		}

		// ── 自选股 ──────────────────────────────────────────────
		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("",          watchlistHandler.List)   // GET    /api/v1/watchlist
			watchlist.POST("",         watchlistHandler.Add)    // POST   /api/v1/watchlist
			watchlist.DELETE("/:code", watchlistHandler.Remove) // DELETE /api/v1/watchlist/:code
		}

		// ── 交易日志 ─────────────────────────────────────────────
		trades := v1.Group("/trades")
		{
			trades.POST("",       tradeHandler.AddTrade)   // POST /api/v1/trades
			trades.GET("/:code",  tradeHandler.ListByCode) // GET  /api/v1/trades/:code
		}

		// ── 统计 ─────────────────────────────────────────────────
		stats := v1.Group("/stats")
		{
			stats.GET("/performance", tradeHandler.GetPerformance) // GET /api/v1/stats/performance
		}
	}

	if cfg.AppEnv == "development" {
		for _, ri := range r.Routes() {
			log.Sugar().Debugf("%-8s %s", ri.Method, ri.Path)
		}
	}

	return r
}
