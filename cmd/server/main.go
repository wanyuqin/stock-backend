package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"stock-backend/internal/api/router"
	"stock-backend/internal/config"
	"stock-backend/internal/data"
	"stock-backend/pkg/logger"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	log := logger.New(cfg.AppEnv)
	defer log.Sync() //nolint:errcheck

	log.Sugar().Infow("config loaded", "env", cfg.AppEnv, "port", cfg.ServerPort)

	if _, err := data.InitDB(cfg, log); err != nil {
		log.Sugar().Fatalw("failed to connect database", "err", err)
	}

	ginEngine, discoverySvc, auditSvc, marketSentinelSvc, stockReportSvc,
		valuationSvc, morningBriefSvc, equitySvc, screenerTemplateSvc :=
		router.New(cfg, log)

	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()

	discoverySvc.Start(bgCtx)
	marketSentinelSvc.Start(bgCtx)

	go runDailyPriceTracker(bgCtx, auditSvc, log)
	go runReportWorkers(bgCtx, stockReportSvc, log)
	go runDailyValuationSync(bgCtx, valuationSvc, log)
	go runMorningBriefWorker(bgCtx, morningBriefSvc, log)
	go runDailyEquitySnapshot(bgCtx, equitySvc, log)
	go runScreenerTemplatePush(bgCtx, screenerTemplateSvc, log)

	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      ginEngine,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 3 * time.Minute,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Sugar().Infof("server listening on :%s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Sugar().Fatalw("server error", "err", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Sugar().Info("shutting down server...")
	discoverySvc.Stop()
	bgCancel()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Sugar().Fatalw("forced shutdown", "err", err)
	}
	log.Sugar().Info("server exited cleanly")
}
