package handler

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/service"
)

type NewsSentimentHandler struct {
	analyzer *service.NewsSentimentAnalyzer
	log      *zap.Logger
}

func NewNewsSentimentHandler(analyzer *service.NewsSentimentAnalyzer, log *zap.Logger) *NewsSentimentHandler {
	return &NewsSentimentHandler{analyzer: analyzer, log: log}
}

// GET /api/v1/news/sentiment?user_id=1
func (h *NewsSentimentHandler) GetSentiment(c *gin.Context) {
	userID := int64(defaultUserID)
	if uid := c.Query("user_id"); uid != "" {
		if parsed, err := strconv.ParseInt(uid, 10, 64); err == nil {
			userID = parsed
		}
	}

	result, err := h.analyzer.Analyze(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("news sentiment analysis failed", zap.Error(err))
		InternalError(c, "新闻情绪分析失败: "+err.Error())
		return
	}
	OK(c, result)
}
