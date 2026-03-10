package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New 根据运行环境返回合适的 zap.Logger。
//   - development: 彩色 Console 输出，Debug 级别
//   - production : JSON 输出，Info 级别
func New(env string) *zap.Logger {
	var cfg zap.Config

	if env == "production" {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	log, err := cfg.Build()
	if err != nil {
		panic("failed to init logger: " + err.Error())
	}
	return log
}
