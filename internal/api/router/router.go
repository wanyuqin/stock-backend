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

func New(cfg *config.Config, log *zap.Logger) (*gin.Engine, *service.DiscoveryService, *service.AuditService, *service.MarketSentinelService) {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ZapLogger(log))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))

	db := data.DB()

	stockRepo := repo.NewStockRepo(db)
	watchlistRepo := repo.NewWatchlistRepo(db)
	tradeRepo := repo.NewTradeLogRepo(db)
	scanRepo := repo.NewScanRepo(db)
	mfRepo := repo.NewMoneyFlowRepo(db)
	alertRepo := repo.NewAlertRepo(db)
	snapshotRepo := repo.NewSnapshotRepo(db)
	positionRepo := repo.NewPositionRepo(db)
	reviewRepo := repo.NewReviewRepo(db)
	tradeV2Repo := repo.NewTradeLogV2Repo(db)
	marketSentimentRepo := repo.NewMarketSentimentRepo(db)

	stockSvc := service.NewStockService(log)
	aiSvc := service.NewAIAnalysisService(log)
	tradeSvc := service.NewTradeService(tradeRepo, stockSvc, log)
	scanSvc := service.NewScanService(scanRepo, watchlistRepo, stockSvc, log)
	reportSvc := service.NewReportService(scanRepo, aiSvc, log)
	mfSvc := service.NewMoneyFlowService(mfRepo, stockRepo, log)
	discoverySvc := service.NewDiscoveryService(mfSvc, watchlistRepo, alertRepo, stockRepo, log)
	crawlerSvc := service.NewCrawlerService(snapshotRepo, log)
	screenerSvc := service.NewScreenerService(snapshotRepo, log)
	guardianSvc := service.NewPositionGuardianService(positionRepo, stockSvc, aiSvc, log)
	auditSvc := service.NewAuditService(reviewRepo, tradeV2Repo, stockSvc, aiSvc, log)
	marketSentinelSvc := service.NewMarketSentinelService(marketSentimentRepo, log)

	stockHandler := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	analysisHandler := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler := handler.NewTradeHandler(tradeSvc, log)
	scanHandler := handler.NewScanHandler(scanSvc, reportSvc, log)
	reportHandler := handler.NewReportHandler(reportSvc, log)
	alertHandler := handler.NewAlertHandler(discoverySvc, mfSvc, log)
	screenerHandler := handler.NewScreenerHandler(screenerSvc, crawlerSvc, log)
	positionHandler := handler.NewPositionHandler(guardianSvc, log)
	reviewHandler := handler.NewReviewHandler(auditSvc, log)
	marketSentinelHandler := handler.NewMarketSentinelHandler(marketSentinelSvc, log)
	healthHandler := handler.NewHealthHandler()

	r.GET("/health", healthHandler.Check)
	r.GET("/readyz", healthHandler.Ready)

	v1 := r.Group("/api/v1")
	{
		stocks := v1.Group("/stocks")
		{
			stocks.GET("", stockHandler.List)
			stocks.GET("/:code", stockHandler.GetByCode)
			stocks.GET("/:code/quote", stockHandler.GetQuote)
			stocks.GET("/:code/kline", analysisHandler.GetKLine)
			stocks.GET("/:code/analysis", analysisHandler.GetAnalysis)
			stocks.GET("/:code/money-flow", alertHandler.GetMoneyFlow)
			stocks.POST("/:code/money-flow/refresh", alertHandler.RefreshMoneyFlow)
		}

		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("", watchlistHandler.List)
			watchlist.POST("", watchlistHandler.Add)
			watchlist.DELETE("/:code", watchlistHandler.Remove)
		}

		trades := v1.Group("/trades")
		{
			// GET  /trades        — 全量流水（traded_at 倒序，limit/offset 分页）
			// POST /trades        — 新增一条交易记录
			// GET  /trades/:code  — 按股票代码查历史
			trades.GET("", tradeHandler.ListAll)
			trades.POST("", tradeHandler.AddTrade)
			trades.GET("/:code", tradeHandler.ListByCode)
		}

		// 复盘系统 API
		review := v1.Group("/review")
		{
			review.POST("/submit", reviewHandler.Submit)
			review.GET("/dashboard", reviewHandler.Dashboard)
			review.GET("/list", reviewHandler.List)
			review.POST("/ai/:id", reviewHandler.TriggerAI)
			review.POST("/tracker/run", reviewHandler.RunTracker)
			review.POST("/init", reviewHandler.InitRecentSells)
		}

		v1.Group("/stats").GET("/performance", tradeHandler.GetPerformance)

		reports := v1.Group("/reports")
		{
			reports.GET("/daily", reportHandler.GetDailyReport)
			reports.POST("/daily/generate", reportHandler.GenerateDailyReport)
		}

		alerts := v1.Group("/alerts")
		{
			alerts.GET("", alertHandler.ListAlerts)
			alerts.POST("/read", alertHandler.MarkRead)
		}

		screener := v1.Group("/screener")
		{
			screener.POST("/execute", screenerHandler.Execute)
			screener.POST("/sync", screenerHandler.SyncMarketData)
			screener.GET("/status", screenerHandler.Status)
		}

		positions := v1.Group("/positions")
		{
			positions.GET("/diagnose", positionHandler.Diagnose)
			positions.POST("/analyze/:code", positionHandler.AnalyzeOne)
			positions.POST("/sync", positionHandler.SyncPosition)
		}

		market := v1.Group("/market")
		{
			market.GET("/summary", marketSentinelHandler.GetSummary)
		}

		admin := v1.Group("/admin")
		{
			scan := admin.Group("/scan")
			{
				scan.POST("/run", scanHandler.RunScan)
				scan.GET("/today", scanHandler.ListTodayScans)
				scan.GET("/history", scanHandler.ListScansByDate)
			}
		}
	}

	if cfg.AppEnv == "development" {
		for _, ri := range r.Routes() {
			log.Sugar().Debugf("%-8s %s", ri.Method, ri.Path)
		}
	}

	return r, discoverySvc, auditSvc, marketSentinelSvc
}
