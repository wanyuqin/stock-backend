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

func New(cfg *config.Config, log *zap.Logger) (*gin.Engine, *service.DiscoveryService) {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ZapLogger(log))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))

	// ── 依赖注入 ─────────────────────────────────────────────────
	db := data.DB()

	stockRepo     := repo.NewStockRepo(db)
	watchlistRepo := repo.NewWatchlistRepo(db)
	tradeRepo     := repo.NewTradeLogRepo(db)
	scanRepo      := repo.NewScanRepo(db)
	mfRepo        := repo.NewMoneyFlowRepo(db)
	alertRepo     := repo.NewAlertRepo(db)
	snapshotRepo  := repo.NewSnapshotRepo(db)
	positionRepo  := repo.NewPositionRepo(db)

	stockSvc     := service.NewStockService(log)
	aiSvc        := service.NewAIAnalysisService(log)
	tradeSvc     := service.NewTradeService(tradeRepo, stockSvc, log)
	scanSvc      := service.NewScanService(scanRepo, watchlistRepo, stockSvc, log)
	reportSvc    := service.NewReportService(scanRepo, aiSvc, log)
	mfSvc        := service.NewMoneyFlowService(mfRepo, stockRepo, log)
	discoverySvc := service.NewDiscoveryService(mfSvc, watchlistRepo, alertRepo, stockRepo, log)
	crawlerSvc   := service.NewCrawlerService(snapshotRepo, log)
	screenerSvc  := service.NewScreenerService(snapshotRepo, log)
	guardianSvc  := service.NewPositionGuardianService(positionRepo, stockSvc, aiSvc, log)

	stockHandler    := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	analysisHandler := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler    := handler.NewTradeHandler(tradeSvc, log)
	scanHandler     := handler.NewScanHandler(scanSvc, reportSvc, log)
	reportHandler   := handler.NewReportHandler(reportSvc, log)
	alertHandler    := handler.NewAlertHandler(discoverySvc, mfSvc, log)
	screenerHandler := handler.NewScreenerHandler(screenerSvc, crawlerSvc, log)
	positionHandler := handler.NewPositionHandler(guardianSvc, log)
	healthHandler   := handler.NewHealthHandler()

	// ── 路由 ─────────────────────────────────────────────────────
	r.GET("/health", healthHandler.Check)
	r.GET("/readyz",  healthHandler.Ready)

	v1 := r.Group("/api/v1")
	{
		stocks := v1.Group("/stocks")
		{
			stocks.GET("",                   stockHandler.List)
			stocks.GET("/:code",             stockHandler.GetByCode)
			stocks.GET("/:code/quote",       stockHandler.GetQuote)
			stocks.GET("/:code/kline",       analysisHandler.GetKLine)
			stocks.GET("/:code/analysis",    analysisHandler.GetAnalysis)
			stocks.GET("/:code/money-flow",          alertHandler.GetMoneyFlow)
			stocks.POST("/:code/money-flow/refresh", alertHandler.RefreshMoneyFlow)
		}

		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("",          watchlistHandler.List)
			watchlist.POST("",         watchlistHandler.Add)
			watchlist.DELETE("/:code", watchlistHandler.Remove)
		}

		trades := v1.Group("/trades")
		{
			trades.POST("",      tradeHandler.AddTrade)
			trades.GET("/:code", tradeHandler.ListByCode)
		}

		v1.Group("/stats").GET("/performance", tradeHandler.GetPerformance)

		reports := v1.Group("/reports")
		{
			reports.GET("/daily",           reportHandler.GetDailyReport)
			reports.POST("/daily/generate", reportHandler.GenerateDailyReport)
		}

		alerts := v1.Group("/alerts")
		{
			alerts.GET("",       alertHandler.ListAlerts)
			alerts.POST("/read", alertHandler.MarkRead)
		}

		screener := v1.Group("/screener")
		{
			screener.POST("/execute", screenerHandler.Execute)
			screener.POST("/sync",    screenerHandler.SyncMarketData)
			screener.GET("/status",   screenerHandler.Status)
		}

		// ── 持仓守护 ────────────────────────────────────────────────
		// GET  /diagnose          纯指标刷新（定时轮询，不调 AI，节省 token）
		// POST /analyze/:code     手动触发单只 AI 深度分析（按需消耗 token）
		// POST /sync              录入 / 更新持仓成本
		positions := v1.Group("/positions")
		{
			positions.GET("/diagnose",       positionHandler.Diagnose)
			positions.POST("/analyze/:code", positionHandler.AnalyzeOne)
			positions.POST("/sync",          positionHandler.SyncPosition)
		}

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

	return r, discoverySvc
}
