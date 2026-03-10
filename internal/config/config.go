package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config 持有所有运行时配置，字段均从环境变量读取。
// 优先级：进程环境变量 > .env 文件 > 默认值
type Config struct {
	// ── 应用 ──────────────────────────────
	AppEnv     string // development | production
	ServerPort string

	// ── PostgreSQL ────────────────────────
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string
	DBSSLMode  string

	// ── Redis ─────────────────────────────
	RedisAddr     string
	RedisPassword string

	// ── CORS ──────────────────────────────
	// 多个源用逗号分隔，例如 "http://localhost:5173,https://yourapp.com"
	CORSAllowedOrigins string
}

// DSN 返回 PostgreSQL 连接字符串（pgx / sqlx 通用格式）
func (c *Config) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		c.DBHost, c.DBPort, c.DBUser, c.DBPassword, c.DBName, c.DBSSLMode,
	)
}

// Load 从 .env（若存在）和进程环境变量中读取配置。
func Load() (*Config, error) {
	// 尝试加载 .env，文件不存在时不报错（生产环境直接用环境变量即可）
	_ = godotenv.Load()

	cfg := &Config{
		AppEnv:     getEnv("APP_ENV", "development"),
		ServerPort: getEnv("SERVER_PORT", "8888"),

		DBHost:     getEnv("DB_HOST", "localhost"),
		DBPort:     getEnv("DB_PORT", "5432"),
		DBUser:     getEnv("DB_USER", "admin"),
		DBPassword: getEnv("DB_PASSWORD", "stock_password_123"),
		DBName:     getEnv("DB_NAME", "stock_system"),
		DBSSLMode:  getEnv("DB_SSLMODE", "disable"),

		RedisAddr:     getEnv("REDIS_ADDR", "localhost:6379"),
		RedisPassword: getEnv("REDIS_PASSWORD", ""),

		CORSAllowedOrigins: getEnv("CORS_ALLOWED_ORIGINS", "http://localhost:5173"),
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) validate() error {
	if c.DBPassword == "" {
		return fmt.Errorf("DB_PASSWORD is required")
	}
	return nil
}

// getEnv 读取环境变量，不存在时返回 fallback。
func getEnv(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}
