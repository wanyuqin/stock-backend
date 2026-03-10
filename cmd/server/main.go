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
	// ── 1. 加载配置 ──────────────────────────────────────────────
	cfg, err := config.Load()
	if err != nil {
		panic("failed to load config: " + err.Error())
	}

	// ── 2. 初始化 Logger ─────────────────────────────────────────
	log := logger.New(cfg.AppEnv)
	defer log.Sync() //nolint:errcheck

	log.Sugar().Infow("config loaded", "env", cfg.AppEnv, "port", cfg.ServerPort)

	// ── 3. 连接数据库 ─────────────────────────────────────────────
	if _, err := data.InitDB(cfg, log); err != nil {
		log.Sugar().Fatalw("failed to connect database", "err", err)
	}

	// ── 4. 初始化 Gin 路由 ────────────────────────────────────────
	r := router.New(cfg, log)

	// ── 5. 启动 HTTP Server ───────────────────────────────────────
	srv := &http.Server{
		Addr:         ":" + cfg.ServerPort,
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Sugar().Infof("server listening on :%s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Sugar().Fatalw("server error", "err", err)
		}
	}()

	// ── 6. 优雅关闭 ───────────────────────────────────────────────
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Sugar().Info("shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Sugar().Fatalw("forced shutdown", "err", err)
	}
	log.Sugar().Info("server exited cleanly")
}
