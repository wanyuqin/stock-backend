package data

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"stock-backend/internal/config"
)

var (
	db   *gorm.DB
	once sync.Once
)

// InitDB 初始化全局数据库连接（幂等，只执行一次）。
// 由 main.go 在启动时调用。
func InitDB(cfg *config.Config, log *zap.Logger) (*gorm.DB, error) {
	var initErr error

	once.Do(func() {
		// ── GORM 日志级别 ─────────────────────────────────────────
		logLevel := gormlogger.Warn
		if cfg.AppEnv == "development" {
			logLevel = gormlogger.Info
		}

		gormCfg := &gorm.Config{
			Logger: gormlogger.Default.LogMode(logLevel),
		}

		// ── 建立连接 ──────────────────────────────────────────────
		conn, err := gorm.Open(postgres.Open(cfg.DSN()), gormCfg)
		if err != nil {
			initErr = fmt.Errorf("gorm open: %w", err)
			return
		}

		// ── 连接池配置 ────────────────────────────────────────────
		sqlDB, err := conn.DB()
		if err != nil {
			initErr = fmt.Errorf("get sql.DB: %w", err)
			return
		}
		sqlDB.SetMaxIdleConns(5)
		sqlDB.SetMaxOpenConns(20)
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
		sqlDB.SetConnMaxIdleTime(10 * time.Minute)

		// ── Ping 验证连通性 ───────────────────────────────────────
		if err := sqlDB.Ping(); err != nil {
			initErr = fmt.Errorf("db ping: %w", err)
			return
		}

		db = conn
		log.Sugar().Infow("database connected",
			"host", cfg.DBHost,
			"port", cfg.DBPort,
			"name", cfg.DBName,
		)
	})

	if initErr != nil {
		return nil, initErr
	}
	return db, nil
}

// DB 返回已初始化的全局 *gorm.DB 实例。
// 必须在 InitDB 之后调用，否则 panic。
func DB() *gorm.DB {
	if db == nil {
		panic("data.DB() called before data.InitDB()")
	}
	return db
}
