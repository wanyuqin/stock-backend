package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/repo"
	"stock-backend/internal/service"
)

// ═══════════════════════════════════════════════════════════════
// ReviewHandler
// ═══════════════════════════════════════════════════════════════

type ReviewHandler struct {
	auditSvc        *service.AuditService
	behaviorStatsRepo repo.BehaviorStatsRepo
	log             *zap.Logger
}

func NewReviewHandler(auditSvc *service.AuditService, behaviorStatsRepo repo.BehaviorStatsRepo, log *zap.Logger) *ReviewHandler {
	return &ReviewHandler{
		auditSvc:          auditSvc,
		behaviorStatsRepo: behaviorStatsRepo,
		log:               log,
	}
}

// POST /api/v1/review/submit
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
	OK(c, gin.H{"review": dto, "message": aiMsg})
}

// GET /api/v1/review/dashboard
func (h *ReviewHandler) Dashboard(c *gin.Context) {
	dto, err := h.auditSvc.GetDashboard(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("GetDashboard failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, dto)
}

// GET /api/v1/review/list
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

	OK(c, gin.H{"items": items, "total": total, "limit": limit, "offset": offset})
}

// POST /api/v1/review/ai/:id
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

	items, _ := h.auditSvc.ListReviews(c.Request.Context(), defaultUserID, 100, 0)
	for _, item := range items {
		if item.ID == id {
			OK(c, item)
			return
		}
	}
	OK(c, gin.H{"message": "AI 审计完成"})
}

// POST /api/v1/review/tracker/run
func (h *ReviewHandler) RunTracker(c *gin.Context) {
	count, err := h.auditSvc.RunPriceTracker(c.Request.Context())
	if err != nil {
		h.log.Error("RunPriceTracker failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"updated": count, "message": "价格追踪完成"})
}

// POST /api/v1/review/init
func (h *ReviewHandler) InitRecentSells(c *gin.Context) {
	count, err := h.auditSvc.InitReviewsForRecentSells(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("InitRecentSells failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, gin.H{"created": count, "message": "复盘草稿初始化完成"})
}

// GET /api/v1/review/behavior-stats
// 返回各类交易行为（PANIC_SELL / CHASING_HIGH 等）的次数、胜率、平均盈亏
func (h *ReviewHandler) BehaviorStats(c *gin.Context) {
	result, err := h.behaviorStatsRepo.GetBehaviorStats(c.Request.Context(), defaultUserID)
	if err != nil {
		h.log.Error("BehaviorStats failed", zap.Error(err))
		InternalError(c, err.Error())
		return
	}
	OK(c, result)
}
