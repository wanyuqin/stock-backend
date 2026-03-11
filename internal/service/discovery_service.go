package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

const (
	pulseDeltaThreshold = 5_000_000
	discoveryInterval   = 1 * time.Minute
	discoveryUserID     = 1
)

type lastState struct {
	MainNetInflow decimal.Decimal
	UpdatedAt     time.Time
}

type DiscoveryService struct {
	mfSvc         *MoneyFlowService
	watchlistRepo repo.WatchlistRepo
	alertRepo     repo.AlertRepo
	stockRepo     repo.StockRepo

	mu         sync.RWMutex
	lastStates map[string]lastState

	log    *zap.Logger
	stopCh chan struct{}
}

func NewDiscoveryService(
	mfSvc *MoneyFlowService,
	watchlistRepo repo.WatchlistRepo,
	alertRepo repo.AlertRepo,
	stockRepo repo.StockRepo,
	log *zap.Logger,
) *DiscoveryService {
	return &DiscoveryService{
		mfSvc:         mfSvc,
		watchlistRepo: watchlistRepo,
		alertRepo:     alertRepo,
		stockRepo:     stockRepo,
		lastStates:    make(map[string]lastState),
		log:           log,
		stopCh:        make(chan struct{}),
	}
}

func (s *DiscoveryService) Start(ctx context.Context) {
	s.log.Info("DiscoveryService started",
		zap.Duration("interval", discoveryInterval),
		zap.Int64("pulse_threshold", pulseDeltaThreshold),
	)
	go s.loop(ctx)
}

func (s *DiscoveryService) Stop() {
	close(s.stopCh)
	s.log.Info("DiscoveryService stopped")
}

func (s *DiscoveryService) loop(ctx context.Context) {
	s.runOnce(ctx)

	ticker := time.NewTicker(discoveryInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.runOnce(ctx)
		case <-s.stopCh:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (s *DiscoveryService) runOnce(ctx context.Context) {
	start := time.Now()
	s.log.Debug("discovery: round started")

	watchItems, err := s.watchlistRepo.ListByUser(ctx, discoveryUserID)
	if err != nil {
		s.log.Error("discovery: list watchlist failed", zap.Error(err))
		return
	}
	if len(watchItems) == 0 {
		return
	}

	type task struct{ Code, Market string }
	tasks := make([]task, 0, len(watchItems))
	for _, w := range watchItems {
		market := s.resolveMarket(ctx, w.StockCode)
		tasks = append(tasks, task{Code: w.StockCode, Market: market})
	}

	stockTasks := make([]struct{ Code, Market string }, len(tasks))
	for i, t := range tasks {
		stockTasks[i] = struct{ Code, Market string }{t.Code, t.Market}
	}

	flows, err := s.mfSvc.FetchBatch(ctx, stockTasks)
	if err != nil {
		s.log.Error("discovery: FetchBatch failed", zap.Error(err))
		return
	}

	alertCount := 0
	for _, mf := range flows {
		if triggered := s.detectPulse(ctx, mf); triggered {
			alertCount++
		}
	}

	s.log.Info("discovery: round done",
		zap.Int("scanned", len(flows)),
		zap.Int("alerts", alertCount),
		zap.Duration("elapsed", time.Since(start)),
	)
}

func (s *DiscoveryService) detectPulse(ctx context.Context, mf *MoneyFlow) bool {
	s.mu.Lock()
	last, exists := s.lastStates[mf.StockCode]
	s.lastStates[mf.StockCode] = lastState{
		MainNetInflow: mf.MainNetInflow,
		UpdatedAt:     time.Now(),
	}
	s.mu.Unlock()

	if !exists {
		return false
	}

	threshold := decimal.NewFromInt(pulseDeltaThreshold)
	delta := mf.MainNetInflow.Sub(last.MainNetInflow)

	if delta.LessThan(threshold) {
		return false
	}

	if mf.MainNetInflow.IsNegative() {
		return false
	}

	// DB 列是 NUMERIC(15,2)，pgx driver 返回字符串，GORM 只能 Scan 到 float64。
	// 写入时统一用 InexactFloat64()，精度完全够用（元级别）。
	alert := &model.Alert{
		StockCode:     mf.StockCode,
		StockName:     mf.StockName,
		AlertType:     model.AlertTypeMoneyFlowPulse,
		MainNetInflow: mf.MainNetInflow.InexactFloat64(),
		Delta:         delta.InexactFloat64(),
		PctChg:        mf.PctChg.InexactFloat64(),
		Message:       s.buildAlertMessage(mf, delta),
		TriggeredAt:   time.Now(),
	}

	if err := s.alertRepo.Create(ctx, alert); err != nil {
		s.log.Error("discovery: create alert failed",
			zap.String("code", mf.StockCode), zap.Error(err))
		return false
	}

	s.log.Info("🚨 PULSE ALERT",
		zap.String("code", mf.StockCode),
		zap.String("name", mf.StockName),
		zap.String("delta", delta.StringFixed(0)),
		zap.String("main_net", mf.MainNetInflow.StringFixed(0)),
		zap.String("pct_chg", mf.PctChg.StringFixed(2)),
	)
	return true
}

func (s *DiscoveryService) buildAlertMessage(mf *MoneyFlow, delta decimal.Decimal) string {
	deltaWan := delta.Div(decimal.NewFromInt(10000))
	totalWan := mf.MainNetInflow.Div(decimal.NewFromInt(10000))
	return fmt.Sprintf(
		"主力1分钟内净流入增加 %.0f 万元，当日累计净流入 %.0f 万元，涨跌幅 %s%%，主力占比 %s%%",
		deltaWan.InexactFloat64(),
		totalWan.InexactFloat64(),
		mf.PctChg.StringFixed(2),
		mf.MainInflowPct.StringFixed(2),
	)
}

func (s *DiscoveryService) resolveMarket(ctx context.Context, code string) string {
	stock, err := s.stockRepo.GetByCode(ctx, code)
	if err == nil && stock != nil {
		return string(stock.Market)
	}
	if len(code) > 0 && code[0] == '6' {
		return "SH"
	}
	return "SZ"
}

func (s *DiscoveryService) ListAlerts(ctx context.Context, limit int, unreadOnly bool) ([]*model.Alert, error) {
	if unreadOnly {
		return s.alertRepo.ListUnread(ctx, limit)
	}
	return s.alertRepo.ListRecent(ctx, limit)
}

func (s *DiscoveryService) MarkAlertsRead(ctx context.Context, ids []int64) error {
	return s.alertRepo.MarkRead(ctx, ids)
}
