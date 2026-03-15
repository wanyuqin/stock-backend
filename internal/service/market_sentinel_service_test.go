package service

import (
	"context"
	"stock-backend/pkg/logger"
	"testing"
	"time"

	"stock-backend/internal/model"
)

type noopMarketSentimentRepo struct{}

func (noopMarketSentimentRepo) Upsert(ctx context.Context, m *model.MarketSentiment) error {
	return nil
}

func (noopMarketSentimentRepo) GetLatest(ctx context.Context) (*model.MarketSentiment, error) {
	return nil, nil
}

func (noopMarketSentimentRepo) GetByDate(ctx context.Context, date time.Time) (*model.MarketSentiment, error) {
	return nil, nil
}

func TestMarketSentinelService_fetchMarketData(t *testing.T) {
	//ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	//	w.Header().Set("Content-Type", "application/json")
	//	_, _ = io.WriteString(w, `{"data":{"diff":[{"f170":1.1,"f48":100},{"f170":-2,"f48":200},{"f170":9.8,"f48":300},{"f170":-9.8,"f48":400}]}}`)
	//}))
	//t.Cleanup(ts.Close)

	svc := NewMarketSentinelService(noopMarketSentimentRepo{}, logger.New("development"))
	//svc.httpClient = ts.Client()
	//svc.marketDataURL = ts.URL

	ms, err := svc.fetchMarketData()
	if err != nil {
		t.Fatalf("fetchMarketData() err = %v", err)
	}

	if ms.TotalAmount != 100+200+300+400 {
		t.Fatalf("TotalAmount = %v, want %v", ms.TotalAmount, 100+200+300+400)
	}
	if ms.UpCount != 2 {
		t.Fatalf("UpCount = %d, want %d", ms.UpCount, 2)
	}
	if ms.DownCount != 2 {
		t.Fatalf("DownCount = %d, want %d", ms.DownCount, 2)
	}
	if ms.LimitUpCount != 1 {
		t.Fatalf("LimitUpCount = %d, want %d", ms.LimitUpCount, 1)
	}
	if ms.LimitDownCount != 1 {
		t.Fatalf("LimitDownCount = %d, want %d", ms.LimitDownCount, 1)
	}
	if ms.TradeDate.IsZero() {
		t.Fatalf("TradeDate is zero")
	}
}
