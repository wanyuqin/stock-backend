package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// ═══════════════════════════════════════════════════════════════
// ReviewHandler
// ═══════════════════════════════════════════════════════════════

type ReviewHandler struct {
	auditSvc *service.AuditService
	log      *zap.Logger
}

func NewReviewHandler(auditSvc *service.AuditService, log *zap.Logger) *ReviewHandler {
	return &ReviewHandler{auditSvc: auditSvc, log: log}
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/review/submit
//
// 用户提交复盘主观信息：情绪、标签、自我总结，可选触发 AI 审计。
//
// Body:
//   {
//     "trade_log_id": 42,
//     "mental_state": "恐惧",
//     "user_note":    "当时看到大盘跌了就慌了",
//     "tags":         ["卖早了", "被情绪左右"],
//     "buy_reason":   "均线多头排列，量能放大突破",
//     "sell_reason":  "跌破5日线，止损",
//     "trigger_ai":   true
//   }
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) Submit(c *gin.Context) {
	var req service.SubmitReviewRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "请求体格式错误: "+err.Error())
		return
	}
	if req.TradeLogID <= 0 {
		BadRequest(c, "trade_log_id 必须大于 0")
		return
	}

	dto, err := h.auditSvc.SubmitReview(c.Request.Context(), defaultUserID, &req)
	if err != nil {
		h.log.Error("SubmitReview failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}

	aiMsg := "复盘已保存"
	if req.TriggerAI {
		aiMsg = "复盘已保存，AI 审计正在后台生成…"
	}
	OK(c, gin.H{
		"review":  dto,
		"message": aiMsg,
	})
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/review/dashboard
//
// 返回复盘看板聚合数据：
//   - 胜率 vs 逻辑一致率
//   - 情绪热力图
//   - 平均卖飞空间
//   - 最近 5 条复盘
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) Dashboard(c *gin.Context) {
	dto, err := h.auditSvc.GetDashboard(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("GetDashboard failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, dto)
}

// ─────────────────────────────────────────────────────────────────
// GET /api/v1/review/list
//
// 查询全量复盘列表，支持分页。
// Query: limit=20&offset=0
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) List(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	items, err := h.auditSvc.ListReviews(c.Request.Context(), defaultUserID, limit, offset)
	if err != nil {
		h.log.Error("List reviews failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	total, _ := h.auditSvc.CountReviews(c.Request.Context(), defaultUserID)

	OK(c, gin.H{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/review/ai/:id
//
// 手动触发单条复盘的 AI 审计（同步，等待返回）。
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) TriggerAI(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		BadRequest(c, "无效的 review id")
		return
	}

	if err := h.auditSvc.GenerateAIAudit(c.Request.Context(), id); err != nil {
		h.log.Error("TriggerAI audit failed", zap.Int64("id", id), zap.Error(err))
		InternalError(c, "AI 审计失败: "+err.Error())
		return
	}

	// 返回最新记录
	items, _ := h.auditSvc.ListReviews(c.Request.Context(), defaultUserID, 100, 0)
	for _, item := range items {
		if item.ID == id {
			OK(c, item)
			return
		}
	}
	OK(c, gin.H{"message": "AI 审计完成"})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/review/tracker/run
//
// 手动触发价格追踪器（正常由 Cron 调用，此接口供调试使用）。
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) RunTracker(c *gin.Context) {
	count, err := h.auditSvc.RunPriceTracker(c.Request.Context())
	if err != nil {
		h.log.Error("RunPriceTracker failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{
		"updated": count,
		"message": "价格追踪完成",
	})
}

// ─────────────────────────────────────────────────────────────────
// POST /api/v1/review/init
//
// 为最近 7 天的卖出记录批量初始化复盘草稿。
// ─────────────────────────────────────────────────────────────────

func (h *ReviewHandler) InitRecentSells(c *gin.Context) {
	count, err := h.auditSvc.InitReviewsForRecentSells(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("InitRecentSells failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{
		"created": count,
		"message": "复盘草稿初始化完成",
	})
}
