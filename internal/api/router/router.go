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

func New(cfg *config.Config, log *zap.Logger) (
	*gin.Engine,
	*service.DiscoveryService,
	*service.AuditService,
	*service.MarketSentinelService,
	*service.StockReportService,
	*service.ValuationService,
) {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// 全局 Cookie 初始化（所有东财请求依赖此 Cookie）
	service.InitGlobalTokenManager(log)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ZapLogger(log))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))

	db := data.DB()

	// ── Repo 层 ──────────────────────────────────────────────────
	stockRepo           := repo.NewStockRepo(db)
	watchlistRepo        := repo.NewWatchlistRepo(db)
	tradeRepo           := repo.NewTradeLogRepo(db)
	scanRepo            := repo.NewScanRepo(db)
	mfRepo              := repo.NewMoneyFlowRepo(db)
	alertRepo           := repo.NewAlertRepo(db)
	snapshotRepo        := repo.NewSnapshotRepo(db)
	positionRepo        := repo.NewPositionRepo(db)
	reviewRepo          := repo.NewReviewRepo(db)
	tradeV2Repo         := repo.NewTradeLogV2Repo(db)
	marketSentimentRepo := repo.NewMarketSentimentRepo(db)
	stockReportRepo     := repo.NewStockReportRepo(db)
	valuationRepo       := repo.NewValuationRepo(db)

	// ── Service 层 ───────────────────────────────────────────────
	stockSvc          := service.NewStockService(log)
	aiSvc             := service.NewAIAnalysisService(log)
	tradeSvc          := service.NewTradeService(tradeRepo, stockSvc, log)
	scanSvc           := service.NewScanService(scanRepo, watchlistRepo, stockSvc, log)
	reportSvc         := service.NewReportService(scanRepo, aiSvc, log)
	mfSvc             := service.NewMoneyFlowService(mfRepo, stockRepo, log)
	discoverySvc      := service.NewDiscoveryService(mfSvc, watchlistRepo, alertRepo, stockRepo, log)
	crawlerSvc        := service.NewCrawlerService(snapshotRepo, log)
	screenerSvc       := service.NewScreenerService(snapshotRepo, log)
	guardianSvc       := service.NewPositionGuardianService(positionRepo, stockSvc, aiSvc, log)
	auditSvc          := service.NewAuditService(reviewRepo, tradeV2Repo, stockSvc, aiSvc, log)
	marketSentinelSvc := service.NewMarketSentinelService(marketSentimentRepo, log)
	stockReportSvc    := service.NewStockReportService(stockReportRepo, aiSvc, log)
	valuationSvc      := service.NewValuationService(valuationRepo, watchlistRepo, log)

	// ── Handler 层 ───────────────────────────────────────────────
	stockHandler          := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler      := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	analysisHandler       := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler          := handler.NewTradeHandler(tradeSvc, log)
	scanHandler           := handler.NewScanHandler(scanSvc, reportSvc, log)
	reportHandler         := handler.NewReportHandler(reportSvc, log)
	alertHandler          := handler.NewAlertHandler(discoverySvc, mfSvc, log)
	screenerHandler       := handler.NewScreenerHandler(screenerSvc, crawlerSvc, log)
	positionHandler       := handler.NewPositionHandler(guardianSvc, log)
	reviewHandler         := handler.NewReviewHandler(auditSvc, log)
	marketSentinelHandler := handler.NewMarketSentinelHandler(marketSentinelSvc, log)
	stockReportHandler    := handler.NewStockReportHandler(stockReportSvc, log)
	valuationHandler      := handler.NewValuationHandler(valuationSvc, log)
	healthHandler         := handler.NewHealthHandler()

	// ── 路由 ─────────────────────────────────────────────────────
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
			stocks.GET("/:code/valuation", valuationHandler.GetValuation)
		}

		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("", watchlistHandler.List)
			watchlist.POST("", watchlistHandler.Add)
			watchlist.DELETE("/:code", watchlistHandler.Remove)
		}

		trades := v1.Group("/trades")
		{
			trades.GET("", tradeHandler.ListAll)
			trades.POST("", tradeHandler.AddTrade)
			trades.GET("/:code", tradeHandler.ListByCode)
		}

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

		intel := v1.Group("/reports/intel")
		{
			intel.GET("", stockReportHandler.List)
			intel.POST("/sync", stockReportHandler.Sync)
			intel.POST("/ai", stockReportHandler.ProcessAI)
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
			market.GET("/valuation-summary", valuationHandler.GetSummary)
			market.POST("/valuation-sync", valuationHandler.TriggerSync)
			market.POST("/valuation-backfill", valuationHandler.BackfillHistory)
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

	return r, discoverySvc, auditSvc, marketSentinelSvc, stockReportSvc, valuationSvc
}
