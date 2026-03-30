package smartposition

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"stock-backend/internal/smartposition/domain"
	"stock-backend/internal/smartposition/graph"
)

type taskRecord struct {
	id          string
	request     domain.SmartPositionRequest
	status      domain.SmartPositionTaskStatus
	stage       domain.SmartPositionStage
	progress    int
	warnings    []string
	degraded    []string
	result      *domain.SmartPositionResponse
	err         string
	createdAt   time.Time
	updatedAt   time.Time
	lastEvent   *domain.SmartPositionProgressEvent
	subscribers map[chan domain.SmartPositionProgressEvent]struct{}
}

type taskManager struct {
	mu    sync.RWMutex
	tasks map[string]*taskRecord
	log   *zap.Logger
}

func newTaskManager(log *zap.Logger) *taskManager {
	return &taskManager{
		tasks: make(map[string]*taskRecord),
		log:   log,
	}
}

func (m *taskManager) create(req domain.SmartPositionRequest) *taskRecord {
	now := time.Now()
	id := uuid.NewString()
	task := &taskRecord{
		id:          id,
		request:     req,
		status:      domain.TaskStatusPending,
		stage:       domain.StageInitContext,
		progress:    0,
		createdAt:   now,
		updatedAt:   now,
		subscribers: make(map[chan domain.SmartPositionProgressEvent]struct{}),
	}
	m.mu.Lock()
	m.tasks[id] = task
	m.mu.Unlock()
	return task
}

func (m *taskManager) get(id string) (*taskRecord, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	return task, ok
}

func (m *taskManager) snapshot(id string) (*domain.SmartPositionTaskSnapshot, error) {
	task, ok := m.get(id)
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	cp := &domain.SmartPositionTaskSnapshot{
		TaskID:          task.id,
		Status:          task.status,
		CurrentStage:    task.stage,
		ProgressPercent: task.progress,
		Warnings:        append([]string{}, task.warnings...),
		DegradedModules: append([]string{}, task.degraded...),
		Result:          task.result,
		Error:           task.err,
		CreatedAt:       task.createdAt.Format(time.RFC3339),
		UpdatedAt:       task.updatedAt.Format(time.RFC3339),
		LastEvent:       task.lastEvent,
	}
	return cp, nil
}

func (m *taskManager) subscribe(id string) (chan domain.SmartPositionProgressEvent, *domain.SmartPositionProgressEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return nil, nil, fmt.Errorf("task not found")
	}
	ch := make(chan domain.SmartPositionProgressEvent, 16)
	task.subscribers[ch] = struct{}{}
	var replay *domain.SmartPositionProgressEvent
	if task.lastEvent != nil {
		cp := *task.lastEvent
		replay = &cp
	}
	return ch, replay, nil
}

func (m *taskManager) unsubscribe(id string, ch chan domain.SmartPositionProgressEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.tasks[id]
	if !ok {
		return
	}
	delete(task.subscribers, ch)
	close(ch)
}

func (m *taskManager) publish(id string, event domain.SmartPositionProgressEvent) {
	m.mu.Lock()
	task, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return
	}
	event.TaskID = id
	task.status = event.Status
	task.stage = event.Stage
	task.progress = event.Progress
	task.updatedAt = time.Now()
	task.warnings = append([]string{}, event.Warnings...)
	task.degraded = append([]string{}, event.DegradedModules...)
	if event.Result != nil {
		task.result = event.Result
	}
	if event.Error != "" {
		task.err = event.Error
	}
	cp := event
	task.lastEvent = &cp
	subs := make([]chan domain.SmartPositionProgressEvent, 0, len(task.subscribers))
	for ch := range task.subscribers {
		subs = append(subs, ch)
	}
	m.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			m.log.Warn("smart position subscriber channel full", zap.String("task_id", id))
		}
	}
}

type taskReporter struct {
	taskID string
	tasks  *taskManager
}

func (r *taskReporter) Report(event domain.SmartPositionProgressEvent) {
	r.tasks.publish(r.taskID, event)
}

func (s *Service) CreateTask(ctx context.Context, req domain.SmartPositionRequest) (*domain.SmartPositionTaskSnapshot, error) {
	norm, err := req.Normalize()
	if err != nil {
		return nil, err
	}
	task := s.tasks.create(norm)
	go s.runTask(task.id, norm)
	return s.tasks.snapshot(task.id)
}

func (s *Service) GetTask(ctx context.Context, taskID string) (*domain.SmartPositionTaskSnapshot, error) {
	_ = ctx
	return s.tasks.snapshot(taskID)
}

func (s *Service) SubscribeTask(taskID string) (chan domain.SmartPositionProgressEvent, *domain.SmartPositionProgressEvent, error) {
	return s.tasks.subscribe(taskID)
}

func (s *Service) UnsubscribeTask(taskID string, ch chan domain.SmartPositionProgressEvent) {
	s.tasks.unsubscribe(taskID, ch)
}

func (s *Service) ExecuteTask(ctx context.Context, userID int64, taskID string) (*domain.SmartPositionExecuteResponse, error) {
	task, ok := s.tasks.get(taskID)
	if !ok {
		return nil, fmt.Errorf("task not found")
	}
	if task.result == nil {
		return nil, fmt.Errorf("task result not ready")
	}
	return s.execution.Execute(ctx, userID, task.result)
}

func (s *Service) runTask(taskID string, req domain.SmartPositionRequest) {
	ctx := graph.WithProgressReporter(context.Background(), &taskReporter{taskID: taskID, tasks: s.tasks})
	_, err := s.Analyze(ctx, req)
	if err != nil {
		s.tasks.publish(taskID, domain.SmartPositionProgressEvent{
			Type:      domain.EventFailed,
			TaskID:    taskID,
			Stage:     domain.StageSummaryGenerate,
			Message:   "智能建仓分析失败",
			Progress:  100,
			Status:    domain.TaskStatusFailed,
			Error:     err.Error(),
			Timestamp: time.Now().Format(time.RFC3339),
		})
		return
	}
}
