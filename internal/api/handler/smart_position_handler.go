package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"stock-backend/internal/smartposition"
	"stock-backend/internal/smartposition/domain"
)

// SmartPositionHandler 对外暴露智能建仓分析与一键执行接口。
// Handler 只做参数绑定和响应转换，核心业务全部下沉到 smartposition 模块。
type SmartPositionHandler struct {
	svc *smartposition.Service
	log *zap.Logger
}

func NewSmartPositionHandler(svc *smartposition.Service, log *zap.Logger) *SmartPositionHandler {
	return &SmartPositionHandler{svc: svc, log: log}
}

func (h *SmartPositionHandler) Analyze(c *gin.Context) {
	var req domain.SmartPositionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	resp, err := h.svc.Analyze(c.Request.Context(), req)
	if err != nil {
		h.log.Error("smart position analyze failed", zap.Error(err))
		BadRequest(c, err.Error())
		return
	}
	OK(c, resp)
}

func (h *SmartPositionHandler) Execute(c *gin.Context) {
	var req domain.SmartPositionExecuteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	resp, err := h.svc.Execute(c.Request.Context(), defaultUserID, req)
	if err != nil {
		h.log.Error("smart position execute failed", zap.Error(err))
		BadRequest(c, err.Error())
		return
	}
	OK(c, resp)
}

func (h *SmartPositionHandler) CreateTask(c *gin.Context) {
	var req domain.SmartPositionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		BadRequest(c, "参数错误: "+err.Error())
		return
	}
	snapshot, err := h.svc.CreateTask(c.Request.Context(), req)
	if err != nil {
		h.log.Error("smart position create task failed", zap.Error(err))
		BadRequest(c, err.Error())
		return
	}
	OK(c, snapshot)
}

func (h *SmartPositionHandler) GetTask(c *gin.Context) {
	snapshot, err := h.svc.GetTask(c.Request.Context(), c.Param("id"))
	if err != nil {
		NotFound(c, err.Error())
		return
	}
	OK(c, snapshot)
}

func (h *SmartPositionHandler) ExecuteTask(c *gin.Context) {
	resp, err := h.svc.ExecuteTask(c.Request.Context(), defaultUserID, c.Param("id"))
	if err != nil {
		h.log.Error("smart position execute task failed", zap.Error(err))
		BadRequest(c, err.Error())
		return
	}
	OK(c, resp)
}

func (h *SmartPositionHandler) StreamTask(c *gin.Context) {
	taskID := c.Param("id")
	events, replay, err := h.svc.SubscribeTask(taskID)
	if err != nil {
		NotFound(c, err.Error())
		return
	}
	defer h.svc.UnsubscribeTask(taskID, events)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	flushEvent := func(event domain.SmartPositionProgressEvent) error {
		raw, err := json.Marshal(event)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(c.Writer, "data: %s\n\n", raw); err != nil {
			return err
		}
		c.Writer.Flush()
		return nil
	}

	if replay != nil {
		if err := flushEvent(*replay); err != nil {
			return
		}
		if replay.Status == domain.TaskStatusCompleted || replay.Status == domain.TaskStatusFailed || (replay.Status == domain.TaskStatusPartial && replay.Progress >= 100) {
			return
		}
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-c.Request.Context().Done():
			return
		case event := <-events:
			if err := flushEvent(event); err != nil {
				return
			}
			if event.Status == domain.TaskStatusCompleted || event.Status == domain.TaskStatusFailed || (event.Status == domain.TaskStatusPartial && event.Progress >= 100) {
				return
			}
		case <-heartbeat.C:
			if err := flushEvent(domain.SmartPositionProgressEvent{
				Type:      domain.EventHeartbeat,
				TaskID:    taskID,
				Stage:     domain.StageSummaryGenerate,
				Message:   "heartbeat",
				Progress:  0,
				Status:    domain.TaskStatusRunning,
				Timestamp: time.Now().Format(time.RFC3339),
			}); err != nil {
				return
			}
		}
	}
}
