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

	// ── 4. 构建路由（同时返回后台服务引用）────────────────────────
	ginEngine, discoverySvc := router.New(cfg, log)

	// ── 5. 启动 DiscoveryService（主力脉冲轮询）──────────────────
	bgCtx, bgCancel := context.WithCancel(context.Background())
	defer bgCancel()
	discoverySvc.Start(bgCtx)

	// ── 6. 启动 HTTP Server ───────────────────────────────────────
	srv := &http.Server{
		Addr:    ":" + cfg.ServerPort,
		Handler: ginEngine,

		ReadTimeout: 15 * time.Second,

		// WriteTimeout 必须大于所有慢接口的最长处理时间。
		// AI 分析接口需要调用外部大模型，硅基流动响应可能超过 60s，
		// 设为 3 分钟确保不会在模型返回前强制切断连接。
		// （之前 15s 的设置导致：后端仍在等大模型 → 15s 后 server 强切写连接
		//   → 前端报"服务器内部错误"；第二次请求命中内存缓存所以立刻成功）
		WriteTimeout: 3 * time.Minute,

		IdleTimeout: 60 * time.Second,
	}

	go func() {
		log.Sugar().Infof("server listening on :%s", cfg.ServerPort)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Sugar().Fatalw("server error", "err", err)
		}
	}()

	// ── 7. 优雅关闭 ───────────────────────────────────────────────
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
