package router

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/api/handler"
	"stock-backend/internal/api/middleware"
	"stock-backend/internal/config"
	"stock-backend/internal/data"
	"stock-backend/internal/repo"
	"stock-backend/internal/service"
	"stock-backend/internal/smartposition"
)

func New(cfg *config.Config, log *zap.Logger) (
	*gin.Engine,
	*service.DiscoveryService,
	*service.AuditService,
	*service.MarketSentinelService,
	*service.StockReportService,
	*service.ValuationService,
	*service.MorningBriefService,
	*service.EquityService,
	*service.ScreenerTemplateService,
) {
	if cfg.AppEnv == "production" {
		gin.SetMode(gin.ReleaseMode)
	}
	service.InitGlobalTokenManager(log)

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.ZapLogger(log))
	r.Use(middleware.CORS(cfg.CORSAllowedOrigins))

	db := data.DB()

	// ── Repo ──────────────────────────────────────────────────────
	stockRepo := repo.NewStockRepo(db)
	watchlistRepo := repo.NewWatchlistRepo(db)
	buyPlanRepo := repo.NewBuyPlanRepo(db)
	tradeRepo := repo.NewTradeLogRepo(db)
	scanRepo := repo.NewScanRepo(db)
	mfRepo := repo.NewMoneyFlowRepo(db)
	alertRepo := repo.NewAlertRepo(db)
	snapshotRepo := repo.NewSnapshotRepo(db)
	positionRepo := repo.NewPositionRepo(db)
	reviewRepo := repo.NewReviewRepo(db)
	behaviorStatsRepo := repo.NewBehaviorStatsRepo(db)
	tradeV2Repo := repo.NewTradeLogV2Repo(db)
	marketSentimentRepo := repo.NewMarketSentimentRepo(db)
	stockReportRepo := repo.NewStockReportRepo(db)
	valuationRepo := repo.NewValuationRepo(db)
	sectorRepo := repo.NewSectorRepo(db)
	accountSnapshotRepo := repo.NewAccountSnapshotRepo(db)
	screenerTemplateRepo := repo.NewScreenerTemplateRepo(db)
	riskRepo := repo.NewRiskRepo(db)
	klineRepo := repo.NewKlineRepo(db) // ★ K线历史

	// ── Service ───────────────────────────────────────────────────
	stockSvc := service.NewStockServiceWithSource(log, cfg.MarketDataSourceDefault)
	bigDealSvc := service.NewBigDealService(service.NewQQDadanFetcher(log), log)
	aiSvc := service.NewAIAnalysisService(log)
	buyPlanSvc := service.NewBuyPlanService(buyPlanRepo, stockSvc, log)
	tradeSvc := service.NewTradeService(tradeRepo, positionRepo, stockSvc, log)
	scanSvc := service.NewScanService(scanRepo, watchlistRepo, stockSvc, log)
	reportSvc := service.NewReportService(scanRepo, aiSvc, log)
	mfSvc := service.NewMoneyFlowService(mfRepo, stockRepo, log)
	discoverySvc := service.NewDiscoveryService(mfSvc, watchlistRepo, alertRepo, stockRepo, log)
	crawlerSvc := service.NewCrawlerService(snapshotRepo, log)
	screenerSvc := service.NewScreenerService(snapshotRepo, log)
	guardianSvc := service.NewPositionGuardianService(positionRepo, sectorRepo, stockSvc, aiSvc, log)
	auditSvc := service.NewAuditService(reviewRepo, tradeV2Repo, stockSvc, aiSvc, log)
	marketSentinelSvc := service.NewMarketSentinelService(marketSentimentRepo, cfg.MarketDataSourceDefault, log)
	stockReportSvc := service.NewStockReportService(stockReportRepo, aiSvc, log)
	valuationSvc := service.NewValuationService(valuationRepo, watchlistRepo, cfg.MarketDataSourceDefault, log)
	scoreSvc := service.NewStockScoreService(guardianSvc, marketSentinelSvc, valuationRepo, watchlistRepo, stockSvc, log)
	newsAnalyzer := service.NewNewsSentimentAnalyzer(aiSvc, buyPlanRepo, positionRepo, log)
	interactiveSvc := service.NewInteractivePlatformService(log)
	morningBriefSvc := service.NewMorningBriefService(marketSentinelSvc, guardianSvc, stockReportSvc, valuationSvc, stockSvc, buyPlanRepo, watchlistRepo, aiSvc, interactiveSvc, log)
	equitySvc := service.NewEquityService(accountSnapshotRepo, tradeSvc, log)
	sectorHeatmapSvc := service.NewSectorHeatmapService(log)
	screenerTemplateSvc := service.NewScreenerTemplateService(screenerTemplateRepo, screenerSvc, log)
	riskSvc := service.NewRiskService(riskRepo, tradeRepo, positionRepo, tradeSvc, guardianSvc, buyPlanRepo, stockRepo, stockReportRepo, log)
	klineSyncSvc := service.NewKlineSyncService(klineRepo, stockSvc, log) // ★ K线同步

	buyPlanSvc.SetGuardianSvc(guardianSvc)

	// ── Handler ───────────────────────────────────────────────────
	stockHandler := handler.NewStockHandler(stockRepo, stockSvc, log)
	watchlistHandler := handler.NewWatchlistHandler(watchlistRepo, stockRepo, stockSvc, log)
	buyPlanHandler := handler.NewBuyPlanHandler(buyPlanSvc, log)
	analysisHandler := handler.NewAnalysisHandler(stockSvc, aiSvc, log)
	tradeHandler := handler.NewTradeHandler(tradeSvc, log)
	scanHandler := handler.NewScanHandler(scanSvc, reportSvc, log)
	reportHandler := handler.NewReportHandler(reportSvc, log)
	alertHandler := handler.NewAlertHandler(discoverySvc, mfSvc, log)
	screenerHandler := handler.NewScreenerHandler(screenerSvc, crawlerSvc, log)
	positionHandler := handler.NewPositionHandler(guardianSvc, log)
	reviewHandler := handler.NewReviewHandler(auditSvc, behaviorStatsRepo, log)
	marketSentinelHandler := handler.NewMarketSentinelHandler(marketSentinelSvc, log)
	stockReportHandler := handler.NewStockReportHandler(stockReportSvc, log)
	valuationHandler := handler.NewValuationHandler(valuationSvc, log)
	bigDealHandler := handler.NewBigDealHandler(bigDealSvc, stockSvc, log)
	scoreHandler := handler.NewStockScoreHandler(scoreSvc, stockSvc, log)
	newsSentimentHandler := handler.NewNewsSentimentHandler(newsAnalyzer, log)
	morningBriefHandler := handler.NewMorningBriefHandler(morningBriefSvc, log)
	equityHandler := handler.NewEquityHandler(equitySvc, log)
	sectorHeatmapHandler := handler.NewSectorHeatmapHandler(sectorHeatmapSvc, log)
	screenerTemplateHandler := handler.NewScreenerTemplateHandler(screenerTemplateSvc, log)
	riskHandler := handler.NewRiskHandler(riskSvc, log)
	klineSyncHandler := handler.NewKlineSyncHandler(klineSyncSvc, log) // ★ K线同步
	healthHandler := handler.NewHealthHandler()
	systemHandler := handler.NewSystemHandler(cfg.MarketDataSourceDefault, log)
	smartPositionRepos := smartposition.NewRepositories(stockSvc, bigDealSvc, valuationRepo, stockReportRepo, marketSentinelSvc, riskRepo, watchlistRepo, stockRepo, buyPlanSvc)
	smartPositionSvc, err := smartposition.NewService(context.Background(), smartPositionRepos, log)
	if err != nil {
		log.Fatal("failed to init smart position service", zap.Error(err))
	}
	smartPositionHandler := handler.NewSmartPositionHandler(smartPositionSvc, log)

	// ── 路由 ──────────────────────────────────────────────────────
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
			stocks.GET("/:code/minute", analysisHandler.GetMinute)
			stocks.GET("/:code/analysis", analysisHandler.GetAnalysis)
			stocks.GET("/:code/money-flow", alertHandler.GetMoneyFlow)
			stocks.POST("/:code/money-flow/refresh", alertHandler.RefreshMoneyFlow)
			stocks.GET("/:code/valuation", valuationHandler.GetValuation)
			stocks.GET("/:code/big-deal", bigDealHandler.GetBigDeal)
			stocks.GET("/:code/buy-plans", buyPlanHandler.ListByCode)
			stocks.GET("/:code/score", scoreHandler.GetScore)
			// ★ K线历史同步
			stocks.POST("/:code/kline/sync", klineSyncHandler.StartSync)
			stocks.GET("/:code/kline/sync-status", klineSyncHandler.GetSyncStatus)
			stocks.GET("/:code/kline/local", klineSyncHandler.GetLocalKLine)
			stocks.DELETE("/:code/kline/sync", klineSyncHandler.DeleteAndReset)
		}

		watchlist := v1.Group("/watchlist")
		{
			watchlist.GET("", watchlistHandler.List)
			watchlist.POST("", watchlistHandler.Add)
			watchlist.DELETE("/:code", watchlistHandler.Remove)
		}

		buyPlans := v1.Group("/buy-plans")
		{
			buyPlans.GET("", buyPlanHandler.List)
			buyPlans.POST("", buyPlanHandler.Create)
			buyPlans.POST("/backtest", buyPlanHandler.Backtest)
			buyPlans.PUT("/:id", buyPlanHandler.Update)
			buyPlans.PATCH("/:id/status", buyPlanHandler.UpdateStatus)
			buyPlans.DELETE("/:id", buyPlanHandler.Delete)
			buyPlans.POST("/check-triggers", buyPlanHandler.CheckTriggers)
		}

		trades := v1.Group("/trades")
		{
			trades.GET("", tradeHandler.ListAll)
			trades.POST("", tradeHandler.AddTrade)
			trades.PUT("/:id", tradeHandler.UpdateTrade)
			trades.DELETE("/:id", tradeHandler.DeleteTrade)
			trades.GET("/code/:code", tradeHandler.ListByCode)
		}
		trade := v1.Group("/trade")
		{
			trade.POST("/precheck", riskHandler.PrecheckTrade)
		}

		review := v1.Group("/review")
		{
			review.POST("/submit", reviewHandler.Submit)
			review.GET("/dashboard", reviewHandler.Dashboard)
			review.GET("/list", reviewHandler.List)
			review.POST("/ai/:id", reviewHandler.TriggerAI)
			review.POST("/tracker/run", reviewHandler.RunTracker)
			review.POST("/init", reviewHandler.InitRecentSells)
			review.GET("/behavior-stats", reviewHandler.BehaviorStats)
		}

		stats := v1.Group("/stats")
		{
			stats.GET("/performance", tradeHandler.GetPerformance)
			stats.GET("/equity-curve", equityHandler.GetCurve)
			stats.POST("/equity-snapshot", equityHandler.TakeSnapshot)
		}

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
			screener.GET("/templates", screenerTemplateHandler.List)
			screener.POST("/templates", screenerTemplateHandler.Create)
			screener.PUT("/templates/:id", screenerTemplateHandler.Update)
			screener.DELETE("/templates/:id", screenerTemplateHandler.Delete)
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
			market.GET("/sector-heatmap", sectorHeatmapHandler.GetHeatmap)
		}

		news := v1.Group("/news")
		{
			news.GET("/sentiment", newsSentimentHandler.GetSentiment)
		}

		risk := v1.Group("/risk")
		{
			risk.GET("/profile", riskHandler.GetProfile)
			risk.PUT("/profile", riskHandler.UpdateProfile)
			risk.GET("/position-size", riskHandler.GetPositionSizeSuggestion)
			risk.GET("/portfolio-exposure", riskHandler.GetPortfolioExposure)
			risk.GET("/daily-state", riskHandler.GetDailyRiskState)
			risk.GET("/event-calendar", riskHandler.GetEventCalendar)
			risk.GET("/today-todo", riskHandler.GetTodayRiskTodo)
			risk.PUT("/today-todo/status", riskHandler.UpdateTodayRiskTodoStatus)
			risk.POST("/today-todo/generate-low-health", riskHandler.GenerateLowHealthTodo)
			risk.GET("/weekly-review", riskHandler.GetWeeklyReview)
			risk.GET("/health-trend", riskHandler.GetHealthTrend)
		}

		systemGroup := v1.Group("/system")
		{
			systemGroup.GET("/data-source-status", systemHandler.GetDataSourceStatus)
		}

		smartPosition := v1.Group("/smart-position")
		{
			smartPosition.POST("/analyze", smartPositionHandler.Analyze)
			smartPosition.POST("/execute", smartPositionHandler.Execute)
			smartPosition.POST("/tasks", smartPositionHandler.CreateTask)
			smartPosition.GET("/tasks/:id", smartPositionHandler.GetTask)
			smartPosition.GET("/tasks/:id/stream", smartPositionHandler.StreamTask)
			smartPosition.POST("/tasks/:id/execute", smartPositionHandler.ExecuteTask)
		}

		// ★ K线历史库汇总
		kline := v1.Group("/kline")
		{
			kline.GET("/synced-stocks", klineSyncHandler.ListSyncedStocks)
		}

		// morning-brief
		mb := v1.Group("/morning-brief")
		{
			mb.GET("", morningBriefHandler.Get)
			sec := mb.Group("/sections")
			{
				sec.GET("/market", morningBriefHandler.GetMarket)
				sec.GET("/position", morningBriefHandler.GetPosition)
				sec.GET("/buy-plans", morningBriefHandler.GetBuyPlans)
				sec.GET("/reports", morningBriefHandler.GetReports)
				sec.GET("/valuation", morningBriefHandler.GetValuation)
				sec.GET("/news", morningBriefHandler.GetNews)
				sec.GET("/external", morningBriefHandler.GetExternal)
				sec.GET("/ai-comment", morningBriefHandler.GetAIComment)
			}
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

	return r, discoverySvc, auditSvc, marketSentinelSvc, stockReportSvc,
		valuationSvc, morningBriefSvc, equitySvc, screenerTemplateSvc
}
