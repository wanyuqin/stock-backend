package handler

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// AlertHandler 处理异动告警 & 资金流向查询路由。
type AlertHandler struct {
	discoverySvc *service.DiscoveryService
	mfSvc        *service.MoneyFlowService
	log          *zap.Logger
}

func NewAlertHandler(
	discoverySvc *service.DiscoveryService,
	mfSvc *service.MoneyFlowService,
	log *zap.Logger,
) *AlertHandler {
	return &AlertHandler{discoverySvc: discoverySvc, mfSvc: mfSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/alerts
//
// 查询异动告警列表。
// 查询参数：
//   ?limit=50          最多返回条数（默认 50，上限 200）
//   ?unread_only=true  只返回未读告警
// ─────────────────────────────────────────────────────────────────

func (h *AlertHandler) ListAlerts(c *gin.Context) {
	limit := 50
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	unreadOnly := strings.ToLower(c.Query("unread_only")) == "true"

	alerts, err := h.discoverySvc.ListAlerts(c.Request.Context(), limit, unreadOnly)
	if err != nil {
		h.log.Error("ListAlerts failed", zap.Error(err))
		InternalError(c, "查询告警失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"count":  len(alerts),
		"items":  alerts,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/alerts/read
//
// 将指定 ID 列表的告警标记为已读。
// Body: {"ids": [1, 2, 3]}
// ─────────────────────────────────────────────────────────────────

func (h *AlertHandler) MarkRead(c *gin.Context) {
	var body struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || len(body.IDs) == 0 {
		BadRequest(c, "ids 字段不能为空")
		return
	}

	if err := h.discoverySvc.MarkAlertsRead(c.Request.Context(), body.IDs); err != nil {
		h.log.Error("MarkRead failed", zap.Error(err))
		InternalError(c, "标记已读失败: "+err.Error())
		return
	}

	OK(c, gin.H{"marked": len(body.IDs)})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/stocks/:code/money-flow
//
// 查询单只股票的资金流向历史（DB 已存快照）。
// 查询参数：?limit=20
// ─────────────────────────────────────────────────────────────────

func (h *AlertHandler) GetMoneyFlow(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}

	logs, err := h.mfSvc.ListHistory(c.Request.Context(), code, limit)
	if err != nil {
		h.log.Error("GetMoneyFlow failed", zap.String("code", code), zap.Error(err))
		InternalError(c, "查询资金流向失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"code":  code,
		"count": len(logs),
		"items": logs,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/stocks/:code/money-flow/refresh
//
// 手动触发单只股票的实时资金流向抓取（立即发请求，不走缓存）。
// 供调试和前端"刷新"按钮使用。
// ─────────────────────────────────────────────────────────────────

func (h *AlertHandler) RefreshMoneyFlow(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	// 从 DB 读取 market，推断不到时降级
	market := "SH"
	if len(code) > 0 && code[0] != '6' {
		market = "SZ"
	}

	mf, err := h.mfSvc.FetchAndSave(c.Request.Context(), code, market)
	if err != nil {
		h.log.Error("RefreshMoneyFlow failed", zap.String("code", code), zap.Error(err))
		InternalError(c, "抓取资金流向失败: "+err.Error())
		return
	}

	OK(c, gin.H{
		"code":               mf.StockCode,
		"market":             mf.Market,
		"main_net_inflow":    mf.MainNetInflow.StringFixed(0),
		"super_large_inflow": mf.SuperLargeInflow.StringFixed(0),
		"large_inflow":       mf.LargeInflow.StringFixed(0),
		"medium_inflow":      mf.MediumInflow.StringFixed(0),
		"small_inflow":       mf.SmallInflow.StringFixed(0),
		"main_inflow_pct":    mf.MainInflowPct.StringFixed(4),
		"pct_chg":            mf.PctChg.StringFixed(2),
		"volume":             mf.Volume,
		"date":               mf.Date.Format("2006-01-02"),
	})
}
