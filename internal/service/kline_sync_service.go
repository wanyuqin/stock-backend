package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// KlineSyncService — 历史 K 线全量同步服务
//
// 翻页机制（实测腾讯接口行为）：
//   - 每批最多 500 根（qfqday 前复权）
//   - endDate 当天包含在返回结果的最后一根
//   - 下批 endDate = 本批 bars[0].Date（最早一根的日期）
//   - 终止条件：返回条数 < 500（到达上市日）
//   - 去重：从第二批开始跳过 bars[0]（与上批末尾重叠）
//
// 速率控制：
//   - 批次间休眠 500ms（≈ 2 QPS），避免被封
//   - 单股全量约 12 批（茅台），耗时约 8 秒
//   - 整体超时 5 分钟
//
// 并发保护：
//   - inFlight sync.Map 防止同一股票并发触发
//   - 数据库 sync_state='running' 超过 10 分钟视为僵尸，允许重新触发
// ═══════════════════════════════════════════════════════════════

const (
	klineSyncBatchSize  = 500
	klineSyncInterval   = 500 * time.Millisecond
	klineSyncMaxTimeout = 5 * time.Minute
)

type KlineSyncService struct {
	klineRepo repo.KlineRepo
	stockSvc  *StockService
	log       *zap.Logger

	inFlight sync.Map // code → struct{}
}

func NewKlineSyncService(
	klineRepo repo.KlineRepo,
	stockSvc *StockService,
	log *zap.Logger,
) *KlineSyncService {
	return &KlineSyncService{
		klineRepo: klineRepo,
		stockSvc:  stockSvc,
		log:       log,
	}
}

// ─────────────────────────────────────────────────────────────────
// SyncHistory — 触发全量同步（异步执行，立即返回）
// ─────────────────────────────────────────────────────────────────

func (s *KlineSyncService) SyncHistory(ctx context.Context, code string) (alreadyRunning bool, err error) {
	if _, loaded := s.inFlight.LoadOrStore(code, struct{}{}); loaded {
		return true, nil
	}

	// 数据库里是 running 且未超时，视为真正在跑，返回 409
	if status, dbErr := s.klineRepo.GetSyncStatus(ctx, code); dbErr == nil {
		if status.SyncState == model.KlineSyncRunning {
			if status.UpdatedAt.After(time.Now().Add(-10 * time.Minute)) {
				s.inFlight.Delete(code)
				return true, nil
			}
		}
	}

	now := time.Now()
	_ = s.klineRepo.UpsertSyncStatus(ctx, &model.StockKlineSyncStatus{
		Code:         code,
		SyncState:    model.KlineSyncRunning,
		LastSyncedAt: &now,
	})

	go func() {
		defer s.inFlight.Delete(code)
		syncCtx, cancel := context.WithTimeout(context.Background(), klineSyncMaxTimeout)
		defer cancel()
		s.runSync(syncCtx, code)
	}()

	return false, nil
}

// ─────────────────────────────────────────────────────────────────
// runSync — 实际同步逻辑（goroutine 内运行）
// ─────────────────────────────────────────────────────────────────

func (s *KlineSyncService) runSync(ctx context.Context, code string) {
	log := s.log.With(zap.String("code", code))
	log.Info("kline sync: started")

	stockName := code
	if q, err := s.stockSvc.GetRealtimeQuote(code); err == nil {
		stockName = q.Name
	}

	var (
		allBars      []*model.StockKlineDaily
		batchNum     int
		endDate      = time.Now()
		isFirstBatch = true
	)

	for {
		select {
		case <-ctx.Done():
			s.markError(code, stockName, allBars, "同步超时或被取消")
			return
		default:
		}

		batchNum++
		bars, err := s.fetchOneBatch(ctx, code, endDate)
		if err != nil {
			log.Warn("kline sync: fetch batch failed",
				zap.Int("batch", batchNum),
				zap.String("endDate", endDate.Format("2006-01-02")),
				zap.Error(err),
			)
			s.markError(code, stockName, allBars, err.Error())
			return
		}

		if len(bars) == 0 {
			break
		}

		// ── 去重：第二批起跳过 bars[0]（与上批末尾重叠的 endDate 那根）
		startIdx := 0
		if !isFirstBatch && len(bars) > 1 {
			startIdx = 1
		}
		isFirstBatch = false

		// 向前翻页：新数据前插，保证 allBars 始终按 trade_date ASC 排列
		newBars := make([]*model.StockKlineDaily, len(bars)-startIdx)
		copy(newBars, bars[startIdx:])
		allBars = append(newBars, allBars...)

		log.Info("kline sync: batch done",
			zap.Int("batch", batchNum),
			zap.Int("got", len(bars)),
			zap.Int("added", len(newBars)),
			zap.String("range", fmt.Sprintf("%s ~ %s",
				bars[0].TradeDate.Format("2006-01-02"),
				bars[len(bars)-1].TradeDate.Format("2006-01-02"),
			)),
		)

		// ── 终止条件：返回不足 500 根，说明已抵达上市日
		if len(bars) < klineSyncBatchSize {
			break
		}

		// 下批 endDate = 本批最早一根的日期
		endDate = bars[0].TradeDate

		// 批次限速
		select {
		case <-ctx.Done():
			s.markError(code, stockName, allBars, "同步超时")
			return
		case <-time.After(klineSyncInterval):
		}
	}

	if len(allBars) == 0 {
		s.markError(code, stockName, nil, "未获取到任何 K 线数据，股票代码可能有误")
		return
	}

	// ── 批量写库
	if err := s.klineRepo.BulkUpsert(ctx, allBars); err != nil {
		log.Error("kline sync: BulkUpsert failed", zap.Error(err))
		s.markError(code, stockName, allBars, "写库失败: "+err.Error())
		return
	}

	// ── 更新状态为 done
	earliest := allBars[0].TradeDate
	latest   := allBars[len(allBars)-1].TradeDate
	now := time.Now()
	_ = s.klineRepo.UpsertSyncStatus(ctx, &model.StockKlineSyncStatus{
		Code:         code,
		StockName:    stockName,
		EarliestDate: &earliest,
		LatestDate:   &latest,
		TotalBars:    len(allBars),
		SyncState:    model.KlineSyncDone,
		LastSyncedAt: &now,
	})

	log.Info("kline sync: completed",
		zap.Int("total_bars", len(allBars)),
		zap.Int("batches", batchNum),
		zap.String("range", fmt.Sprintf("%s ~ %s",
			earliest.Format("2006-01-02"),
			latest.Format("2006-01-02"),
		)),
	)
}

// ─────────────────────────────────────────────────────────────────
// fetchOneBatch — 拉一批，返回按 trade_date ASC 排列的 bars
// ─────────────────────────────────────────────────────────────────

func (s *KlineSyncService) fetchOneBatch(ctx context.Context, code string, endDate time.Time) ([]*model.StockKlineDaily, error) {
	resp, err := s.stockSvc.GetKLineEndAt(code, endDate, klineSyncBatchSize)
	if err != nil {
		return nil, fmt.Errorf("fetchOneBatch(%s, %s): %w", code, endDate.Format("2006-01-02"), err)
	}

	bars := make([]*model.StockKlineDaily, 0, len(resp.KLines))
	for _, k := range resp.KLines {
		t, parseErr := time.ParseInLocation("2006-01-02", k.Date, time.Local)
		if parseErr != nil {
			continue
		}
		bars = append(bars, &model.StockKlineDaily{
			Code:      code,
			TradeDate: t,
			Open:      k.Open,
			Close:     k.Close,
			High:      k.High,
			Low:       k.Low,
			Volume:    k.Volume,
			Amount:    k.Amount,
		})
	}
	return bars, nil
}

// ─────────────────────────────────────────────────────────────────
// markError — 写错误状态（保留已有日期范围信息）
// ─────────────────────────────────────────────────────────────────

func (s *KlineSyncService) markError(code, name string, partial []*model.StockKlineDaily, errMsg string) {
	status := &model.StockKlineSyncStatus{
		Code:      code,
		StockName: name,
		SyncState: model.KlineSyncError,
		LastError: errMsg,
	}
	if len(partial) > 0 {
		earliest := partial[0].TradeDate
		latest   := partial[len(partial)-1].TradeDate
		status.EarliestDate = &earliest
		status.LatestDate   = &latest
		status.TotalBars    = len(partial)
	}
	_ = s.klineRepo.UpsertSyncStatus(context.Background(), status)
	s.log.Error("kline sync: failed", zap.String("code", code), zap.String("error", errMsg))
}

// ─────────────────────────────────────────────────────────────────
// 查询接口
// ─────────────────────────────────────────────────────────────────

func (s *KlineSyncService) GetSyncStatus(ctx context.Context, code string) (*model.StockKlineSyncStatus, error) {
	status, err := s.klineRepo.GetSyncStatus(ctx, code)
	if err != nil {
		return &model.StockKlineSyncStatus{
			Code:      code,
			SyncState: model.KlineSyncIdle,
		}, nil
	}
	if _, ok := s.inFlight.Load(code); ok {
		status.SyncState = model.KlineSyncRunning
	}
	return status, nil
}

func (s *KlineSyncService) ListSyncedStocks(ctx context.Context) ([]*model.StockKlineSyncStatus, error) {
	list, err := s.klineRepo.ListSyncStatus(ctx)
	if err != nil {
		return nil, err
	}
	for _, item := range list {
		if _, ok := s.inFlight.Load(item.Code); ok {
			item.SyncState = model.KlineSyncRunning
		}
	}
	return list, nil
}

func (s *KlineSyncService) GetLocalKLine(ctx context.Context, code string, limit int) ([]*model.StockKlineDaily, error) {
	bars, err := s.klineRepo.GetLatestN(ctx, code, limit)
	if err != nil || len(bars) == 0 {
		resp, fetchErr := s.stockSvc.GetKLine(code, limit)
		if fetchErr != nil {
			return nil, fetchErr
		}
		return klineRespToBars(code, resp), nil
	}
	return bars, nil
}

func (s *KlineSyncService) GetLocalKLineRange(ctx context.Context, code string, from, to time.Time) ([]*model.StockKlineDaily, error) {
	return s.klineRepo.GetRange(ctx, code, from, to)
}

func (s *KlineSyncService) DeleteAndReset(ctx context.Context, code string) error {
	if _, ok := s.inFlight.Load(code); ok {
		return fmt.Errorf("股票 %s 正在同步中，请等待完成后再删除", code)
	}
	if err := s.klineRepo.DeleteByCode(ctx, code); err != nil {
		return fmt.Errorf("删除 K 线数据失败: %w", err)
	}
	return s.klineRepo.DeleteSyncStatus(ctx, code)
}

// ─────────────────────────────────────────────────────────────────
// 工具
// ─────────────────────────────────────────────────────────────────

func klineRespToBars(code string, resp *KLineResponse) []*model.StockKlineDaily {
	bars := make([]*model.StockKlineDaily, 0, len(resp.KLines))
	for _, k := range resp.KLines {
		t, err := time.ParseInLocation("2006-01-02", k.Date, time.Local)
		if err != nil {
			continue
		}
		bars = append(bars, &model.StockKlineDaily{
			Code:      code,
			TradeDate: t,
			Open:      k.Open,
			Close:     k.Close,
			High:      k.High,
			Low:       k.Low,
			Volume:    k.Volume,
			Amount:    k.Amount,
		})
	}
	return bars
}
