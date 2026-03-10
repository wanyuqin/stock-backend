package handler

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"stock-backend/internal/data"
)

// HealthHandler 提供基础的存活与就绪探针接口。
type HealthHandler struct {
	startTime time.Time
}

func NewHealthHandler() *HealthHandler {
	return &HealthHandler{startTime: time.Now()}
}

// Check  GET /health — 存活探针（liveness）
// 只要进程在跑就返回 200，不检查外部依赖。
func (h *HealthHandler) Check(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"uptime":  time.Since(h.startTime).String(),
		"service": "stock-backend",
	})
}

// Ready  GET /readyz — 就绪探针（readiness）
// 会真正 ping 数据库，任意依赖不通则返回 503。
func (h *HealthHandler) Ready(c *gin.Context) {
	if err := data.Ping(); err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unavailable",
			"error":  err.Error(),
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ready"})
}
