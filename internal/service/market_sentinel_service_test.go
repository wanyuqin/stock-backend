package service

import (
	"context"
	"testing"
	"time"

	"stock-backend/internal/model"
	"stock-backend/pkg/logger"
)

type noopMarketSentimentRepo struct{}

func (noopMarketSentimentRepo) Upsert(_ context.Context, _ *model.MarketSentiment) error { return nil }
func (noopMarketSentimentRepo) GetLatest(_ context.Context) (*model.MarketSentiment, error) {
	return nil, nil
}
func (noopMarketSentimentRepo) GetByDate(_ context.Context, _ time.Time) (*model.MarketSentiment, error) {
	return nil, nil
}

func TestMarketSentinelService_fetchFromBkRank(t *testing.T) {
	svc := NewMarketSentinelService(noopMarketSentimentRepo{}, "qq", logger.New("development"))

	ctx := context.Background()
	ms, err := svc.fetchFromBkRank(ctx)
	if err != nil {
		t.Skipf("fetchFromBkRank network error (expected in CI): %v", err)
	}

	if ms.UpCount == 0 && ms.DownCount == 0 {
		t.Log("zero counts — market may be closed, skipping value assertions")
		return
	}
	if ms.UpCount <= 0 {
		t.Errorf("UpCount = %d, want > 0", ms.UpCount)
	}
	if ms.DownCount <= 0 {
		t.Errorf("DownCount = %d, want > 0", ms.DownCount)
	}
	if ms.TotalAmount <= 0 {
		t.Errorf("TotalAmount = %.0f, want > 0", ms.TotalAmount)
	}
	t.Logf("bkqtRank: up=%d down=%d limitUp=%d amount=%.0f亿",
		ms.UpCount, ms.DownCount, ms.LimitUpCount, ms.TotalAmount/1e8)
}

func TestMarketSentinelService_GetSummary(t *testing.T) {
	svc := NewMarketSentinelService(noopMarketSentimentRepo{}, "qq", logger.New("development"))

	ctx := context.Background()
	dto, err := svc.GetSummary(ctx)
	if err != nil {
		t.Skipf("GetSummary network error (expected in CI): %v", err)
	}

	t.Logf("summary: score=%d up=%d down=%d amount=%.0f亿 alert=%s",
		dto.SentimentScore, dto.UpCount, dto.DownCount, dto.TotalAmount/1e8, dto.AlertStatus)
}
