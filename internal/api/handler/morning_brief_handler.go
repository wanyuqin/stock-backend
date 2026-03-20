package handler

import (
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

// MorningBriefHandler 处理 /api/v1/morning-brief 相关请求。
type MorningBriefHandler struct {
	svc *service.MorningBriefService
	log *zap.Logger
}

func NewMorningBriefHandler(svc *service.MorningBriefService, log *zap.Logger) *MorningBriefHandler {
	return &MorningBriefHandler{svc: svc, log: log}
}

// GET /api/v1/morning-brief?force=1  — 兼容旧接口（全量，带缓存）
func (h *MorningBriefHandler) Get(c *gin.Context) {
	force := c.Query("force") == "1"
	brief, err := h.svc.Generate(c.Request.Context(), defaultUserID, force)
	if err != nil {
		InternalError(c, "生成开盘前报告失败: "+err.Error())
		return
	}
	OK(c, brief)
}

// ─────────────────────────────────────────────────────────────────
// 独立 Section 接口 — 每个接口立即响应，互不阻塞
// ─────────────────────────────────────────────────────────────────

// GET /api/v1/morning-brief/sections/market
func (h *MorningBriefHandler) GetMarket(c *gin.Context) {
	OK(c, h.svc.GetSectionMarket(defaultUserID))
}

// GET /api/v1/morning-brief/sections/position
func (h *MorningBriefHandler) GetPosition(c *gin.Context) {
	OK(c, h.svc.GetSectionPosition(defaultUserID))
}

// GET /api/v1/morning-brief/sections/buy-plans
func (h *MorningBriefHandler) GetBuyPlans(c *gin.Context) {
	OK(c, h.svc.GetSectionBuyPlan(defaultUserID))
}

// GET /api/v1/morning-brief/sections/reports
func (h *MorningBriefHandler) GetReports(c *gin.Context) {
	OK(c, h.svc.GetSectionReports(defaultUserID))
}

// GET /api/v1/morning-brief/sections/valuation
func (h *MorningBriefHandler) GetValuation(c *gin.Context) {
	OK(c, h.svc.GetSectionValuation(defaultUserID))
}

// GET /api/v1/morning-brief/sections/news
// 最慢的接口（LLM 打分），单独暴露，前端可以后置加载
func (h *MorningBriefHandler) GetNews(c *gin.Context) {
	OK(c, h.svc.GetSectionNews(defaultUserID))
}

// GET /api/v1/morning-brief/sections/external
func (h *MorningBriefHandler) GetExternal(c *gin.Context) {
	OK(c, h.svc.GetSectionExternal(defaultUserID))
}

// GET /api/v1/morning-brief/sections/ai-comment
// AI 总结点评，后台生成完了才有内容
func (h *MorningBriefHandler) GetAIComment(c *gin.Context) {
	comment := h.svc.GetAIComment(defaultUserID)
	OK(c, gin.H{"ai_comment": comment, "ready": comment != ""})
}
