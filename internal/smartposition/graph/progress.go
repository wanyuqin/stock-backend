package graph

import (
	"context"
	"time"

	"stock-backend/internal/smartposition/domain"
)

type progressReporter interface {
	Report(event domain.SmartPositionProgressEvent)
}

type progressReporterKey struct{}

func WithProgressReporter(ctx context.Context, reporter progressReporter) context.Context {
	return context.WithValue(ctx, progressReporterKey{}, reporter)
}

func reportProgress(ctx context.Context, eventType domain.SmartPositionEventType, stage domain.SmartPositionStage, progress int, status domain.SmartPositionTaskStatus, message string, state *domain.GraphState, err error) {
	reporter, ok := ctx.Value(progressReporterKey{}).(progressReporter)
	if !ok || reporter == nil {
		return
	}

	event := domain.SmartPositionProgressEvent{
		Type:      eventType,
		TaskID:    "",
		Stage:     stage,
		Message:   message,
		Progress:  progress,
		Status:    status,
		Timestamp: time.Now().Format(time.RFC3339),
	}
	if state != nil {
		event.Warnings = append(event.Warnings, state.Warnings...)
		event.DegradedModules = append(event.DegradedModules, state.DegradedModules...)
		if state.Response != nil && eventType == domain.EventCompleted {
			event.Result = state.Response
		}
	}
	if err != nil {
		event.Error = err.Error()
	}
	reporter.Report(event)
}
