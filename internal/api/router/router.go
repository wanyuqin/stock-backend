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
	scanRepo      := repo.NewScanRepo(db)

	stockSvc  := service.NewStockService(log)
	aiSvc     := service.NewAIAnalysisService(log)
	tradeSvc  := service.NewTradeService(tradeRepo, stockSvc, log)
	scanSvc   := service.NewScanService(scanRepo, watchlistRepo, stockSvc, log)
	reportSvc := service.NewReportService(scanRepo, aiSvc, log)

	stockHandler     := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	analysisHandler  := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler     := handler.NewTradeHandler(tradeSvc, log)
	scanHandler      := handler.NewScanHandler(scanSvc, reportSvc, log)
	reportHandler    := handler.NewReportHandler(reportSvc, log)
	healthHandler    := handler.NewHealthHandler()

	// ── 健康检查 ──────────────────────────────────────────────────
	r.GET("/health", healthHandler.Check)
	r.GET("/readyz",  healthHandler.Ready)

	// ── API v1 ───────────────────────────────────────────────────
	v1 := r.Group("/api/v1")
	{
		// ── 股票 ────────────────────────────────────────────────
		stocks := v1.Group("/stocks")
		{
			stocks.GET("",                stockHandler.List)
			stocks.GET("/:code",          stockHandler.GetByCode)
			stocks.GET("/:code/quote",    stockHandler.GetQuote)
			stocks.GET("/:code/kline",    analysisHandler.GetKLine)
			stocks.GET("/:code/analysis", analysisHandler.GetAnalysis)
		}

		// ── 自选股 ──────────────────────────────────────────────
		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("",          watchlistHandler.List)
			watchlist.POST("",         watchlistHandler.Add)
			watchlist.DELETE("/:code", watchlistHandler.Remove)
		}

		// ── 交易日志 ─────────────────────────────────────────────
		trades := v1.Group("/trades")
		{
			trades.POST("",      tradeHandler.AddTrade)
			trades.GET("/:code", tradeHandler.ListByCode)
		}

		// ── 统计 ─────────────────────────────────────────────────
		stats := v1.Group("/stats")
		{
			stats.GET("/performance", tradeHandler.GetPerformance)
		}

		// ── 复盘报告 ─────────────────────────────────────────────
		// GET  /api/v1/reports/daily              — 获取今日报告（DB 缓存优先）
		// GET  /api/v1/reports/daily?date=YYYY    — 获取指定日期报告
		// GET  /api/v1/reports/daily?force=1      — 强制重新生成今日报告
		// POST /api/v1/reports/daily/generate     — 手动触发生成（供 admin 使用）
		reports := v1.Group("/reports")
		{
			reports.GET("/daily",            reportHandler.GetDailyReport)
			reports.POST("/daily/generate",  reportHandler.GenerateDailyReport)
		}

		// ── 管理 / 扫描 ──────────────────────────────────────────
		// POST /api/v1/admin/scan/run     — 扫描 + 自动触发报告生成
		// GET  /api/v1/admin/scan/today   — 今日扫描结果
		// GET  /api/v1/admin/scan/history — 历史扫描结果 ?date=YYYY-MM-DD
		admin := v1.Group("/admin")
		{
			scan := admin.Group("/scan")
			{
				scan.POST("/run",    scanHandler.RunScan)
				scan.GET("/today",   scanHandler.ListTodayScans)
				scan.GET("/history", scanHandler.ListScansByDate)
			}
		}
	}

	if cfg.AppEnv == "development" {
		for _, ri := range r.Routes() {
			log.Sugar().Debugf("%-8s %s", ri.Method, ri.Path)
		}
	}

	return r
}
