package handler

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// KlineSyncHandler 历史 K 线同步接口
type KlineSyncHandler struct {
	svc *service.KlineSyncService
	log *zap.Logger
}

func NewKlineSyncHandler(svc *service.KlineSyncService, log *zap.Logger) *KlineSyncHandler {
	return &KlineSyncHandler{svc: svc, log: log}
}

// POST /api/v1/stocks/:code/kline/sync
// 触发全量历史 K 线同步（异步执行，立即返回）
func (h *KlineSyncHandler) StartSync(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	alreadyRunning, err := h.svc.SyncHistory(c.Request.Context(), code)
	if err != nil {
		InternalError(c, "触发同步失败: "+err.Error())
		return
	}
	if alreadyRunning {
		c.JSON(409, gin.H{
			"code":    40900,
			"message": "该股票正在同步中，请稍后查询进度",
			"data":    gin.H{"state": "running"},
		})
		return
	}

	c.JSON(202, gin.H{
		"code":    0,
		"message": "同步任务已启动，预计 8-30 秒完成",
		"data": gin.H{
			"code":  code,
			"state": "running",
		},
	})
}

// GET /api/v1/stocks/:code/kline/sync-status
// 查询同步进度
func (h *KlineSyncHandler) GetSyncStatus(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	status, err := h.svc.GetSyncStatus(c.Request.Context(), code)
	if err != nil {
		InternalError(c, "查询同步状态失败: "+err.Error())
		return
	}
	OK(c, status)
}

// GET /api/v1/stocks/:code/kline/local
// 读本地 K 线数据
//
// Query:
//   limit=120        最近 N 根（默认 120）
//   from=2023-01-01  开始日期（与 limit 互斥，有 from/to 时忽略 limit）
//   to=2024-01-01    结束日期
func (h *KlineSyncHandler) GetLocalKLine(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	fromStr := c.Query("from")
	toStr   := c.Query("to")

	if fromStr != "" || toStr != "" {
		// 按时间范围查
		var from, to time.Time
		if fromStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", fromStr, time.Local); err == nil {
				from = t
			}
		}
		if toStr != "" {
			if t, err := time.ParseInLocation("2006-01-02", toStr, time.Local); err == nil {
				to = t
			}
		}
		bars, err := h.svc.GetLocalKLineRange(c.Request.Context(), code, from, to)
		if err != nil {
			InternalError(c, "读取 K 线失败: "+err.Error())
			return
		}
		OK(c, gin.H{
			"code":  code,
			"count": len(bars),
			"bars":  bars,
		})
		return
	}

	// 按数量查
	limit := 120
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 10000 {
			limit = n
		}
	}

	bars, err := h.svc.GetLocalKLine(c.Request.Context(), code, limit)
	if err != nil {
		InternalError(c, "读取 K 线失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"code":  code,
		"count": len(bars),
		"bars":  bars,
	})
}

// GET /api/v1/kline/synced-stocks
// 列出所有已同步股票及状态
func (h *KlineSyncHandler) ListSyncedStocks(c *gin.Context) {
	list, err := h.svc.ListSyncedStocks(c.Request.Context())
	if err != nil {
		InternalError(c, "查询失败: "+err.Error())
		return
	}
	OK(c, gin.H{
		"total": len(list),
		"items": list,
	})
}

// DELETE /api/v1/stocks/:code/kline/sync
// 删除本地数据并重置状态（支持重新全量同步）
func (h *KlineSyncHandler) DeleteAndReset(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	if err := h.svc.DeleteAndReset(c.Request.Context(), code); err != nil {
		if err.Error() != "" {
			c.JSON(409, gin.H{"code": 40900, "message": err.Error()})
			return
		}
		InternalError(c, "删除失败: "+err.Error())
		return
	}
	OK(c, gin.H{"message": "已删除，可重新触发同步"})
}
