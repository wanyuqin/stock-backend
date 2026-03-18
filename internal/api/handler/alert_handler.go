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
		"count": len(alerts),
		"items": alerts,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/alerts/read
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
// 查询 DB 中已存的资金流向历史快照。
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
// 实时资金流向 —— 改用腾讯证券 qt.gtimg.cn 外盘/内盘接口
// 数据来源与 https://gu.qq.com/sh603920/gp/price 页面相同
//
// 返回字段：
//   outer_vol / inner_vol / net_vol    外盘/内盘/净量（手）
//   outer_amt / inner_amt / net_amt    外盘/内盘/净额（元）
//   net_pct                            净流入占总成交量比例(%)
//   big_buy_pct / big_sell_pct         买卖盘大单比例（来自 s_pk 接口）
//   flow_desc                          可读流向描述文案
// ─────────────────────────────────────────────────────────────────

func (h *AlertHandler) RefreshMoneyFlow(c *gin.Context) {
	code := c.Param("code")
	if code == "" {
		BadRequest(c, "code 不能为空")
		return
	}

	// 调用腾讯 qt 接口，获取外盘/内盘实时数据
	flow, err := service.FetchQTRealtimeFlow(c.Request.Context(), code, h.log)
	if err != nil {
		h.log.Error("RefreshMoneyFlow(QT) failed", zap.String("code", code), zap.Error(err))
		InternalError(c, "抓取实时资金流向失败: "+err.Error())
		return
	}

	// 返回前端 MoneyFlowPanel 需要的完整字段集
	OK(c, gin.H{
		// ── 兼容旧字段（MoneyFlowPanel 现有逻辑用这几个） ──
		"main_net_inflow":    flow.NetAmt,                 // 元，净流入
		"super_large_inflow": flow.OuterAmt * 0.35,        // 外盘中特大单估算（用盘口比例推算）
		"large_inflow":       flow.OuterAmt * 0.65,        // 外盘中大单估算
		"main_inflow_pct":    flow.NetPct,                  // 净流入占比(%)

		// ── 新增：外盘/内盘原始数据 ──
		"outer_vol":   flow.OuterVol,  // 外盘（手）
		"inner_vol":   flow.InnerVol,  // 内盘（手）
		"net_vol":     flow.NetVol,    // 净量（手）
		"outer_amt":   flow.OuterAmt,  // 外盘金额（元）
		"inner_amt":   flow.InnerAmt,  // 内盘金额（元）
		"net_amt":     flow.NetAmt,    // 净流入金额（元）
		"net_pct":     flow.NetPct,    // 净流入占比(%)

		// ── 盘口大单比例（来自腾讯 s_pk 接口） ──
		"big_buy_pct":  flow.BigBuyPct,  // 买盘大单比例
		"big_sell_pct": flow.BigSellPct, // 卖盘大单比例
		"sml_buy_pct":  flow.SmlBuyPct,  // 买盘小单比例
		"sml_sell_pct": flow.SmlSellPct, // 卖盘小单比例

		// ── 行情 ──
		"price":        flow.Price,
		"change":       flow.Change,
		"change_rate":  flow.ChangeRate,
		"volume":       flow.Volume,
		"amount":       flow.Amount,
		"turnover":     flow.Turnover,
		"volume_ratio": flow.VolumeRatio,

		// ── 描述文案 ──
		"flow_desc":  flow.FlowDesc,
		"updated_at": flow.UpdatedAt,
	})
}
