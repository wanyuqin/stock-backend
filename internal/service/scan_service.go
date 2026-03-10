package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// ScanService — 扫描编排
// ═══════════════════════════════════════════════════════════════

// ScanService 遍历自选股，拉取历史 K 线，执行信号检测，持久化结果。
type ScanService struct {
	scanRepo      repo.ScanRepo
	watchlistRepo repo.WatchlistRepo
	stockSvc      *StockService // 复用 K 线抓取
	log           *zap.Logger
}

func NewScanService(
	scanRepo repo.ScanRepo,
	watchlistRepo repo.WatchlistRepo,
	stockSvc *StockService,
	log *zap.Logger,
) *ScanService {
	return &ScanService{
		scanRepo:      scanRepo,
		watchlistRepo: watchlistRepo,
		stockSvc:      stockSvc,
		log:           log,
	}
}

// ── 返回结构 ──────────────────────────────────────────────────────

// ScanResult 是单次扫描的汇总结果，也是 POST /api/admin/scan/run 的响应体。
type ScanResult struct {
	ScanDate    string       `json:"scan_date"`    // 扫描日期
	Total       int          `json:"total"`        // 扫描股票总数
	HitCount    int          `json:"hit_count"`    // 触发任意信号的股票数
	Skipped     int          `json:"skipped"`      // 数据不足跳过数
	Errors      int          `json:"errors"`       // 拉取数据失败数
	Items       []*ScanItem  `json:"items"`        // 命中明细
	DurationMs  int64        `json:"duration_ms"`  // 耗时（毫秒）
}

// ScanItem 是单只股票的扫描命中详情。
type ScanItem struct {
	StockCode   string   `json:"stock_code"`
	StockName   string   `json:"stock_name"`
	Signals     []string `json:"signals"`
	Price       float64  `json:"price"`
	PctChg      float64  `json:"pct_chg"`
	VolumeRatio float64  `json:"volume_ratio"`
	MAStatus    string   `json:"ma_status"`
}

// ── 核心方法 ──────────────────────────────────────────────────────

// RunScan 执行一次完整扫描：
//  1. 从 watchlist 取出所有股票代码
//  2. 逐只拉取 120 日 K 线
//  3. 调用 CheckSignals 计算信号
//  4. 将命中记录批量写入 daily_scans
func (s *ScanService) RunScan(ctx context.Context) (*ScanResult, error) {
	start := time.Now()
	today := time.Now().Truncate(24 * time.Hour)

	s.log.Info("scan started", zap.String("date", today.Format("2006-01-02")))

	// ── 1. 读取自选股 ──────────────────────────────────────────────
	watchItems, err := s.watchlistRepo.ListByUser(ctx, 1) // 单用户系统，user_id=1
	if err != nil {
		return nil, fmt.Errorf("读取自选股失败: %w", err)
	}
	if len(watchItems) == 0 {
		return &ScanResult{
			ScanDate:   today.Format("2006-01-02"),
			DurationMs: time.Since(start).Milliseconds(),
		}, nil
	}

	result := &ScanResult{
		ScanDate: today.Format("2006-01-02"),
		Total:    len(watchItems),
		Items:    make([]*ScanItem, 0),
	}

	var dbScans []*model.DailyScan

	// ── 2. 逐只扫描 ────────────────────────────────────────────────
	for _, wl := range watchItems {
		code := wl.StockCode

		// 拉 120 日 K 线（前复权）
		klineResp, err := s.stockSvc.GetKLine(code, 120)
		if err != nil {
			s.log.Warn("scan: fetch kline failed",
				zap.String("code", code), zap.Error(err))
			result.Errors++
			continue
		}

		// 数据不足 20 根
		if len(klineResp.KLines) < 20 {
			s.log.Debug("scan: insufficient history",
				zap.String("code", code), zap.Int("bars", len(klineResp.KLines)))
			result.Skipped++
			continue
		}

		// ── 3. 转换为 HistoryBar，计算涨跌幅 ────────────────────────
		bars := toHistoryBars(klineResp.KLines)

		// ── 4. 执行信号检测 ──────────────────────────────────────────
		sr := CheckSignals(bars)

		if len(sr.Signals) == 0 {
			continue // 无信号，不记录
		}

		// 取最新一根 K 线的数据
		last := klineResp.KLines[len(klineResp.KLines)-1]

		item := &ScanItem{
			StockCode:   code,
			StockName:   klineResp.Name,
			Signals:     sr.Signals,
			Price:       last.Close,
			PctChg:      bars[len(bars)-1].PctChg,
			VolumeRatio: sr.VolumeRatio,
			MAStatus:    sr.MAStatus,
		}
		result.Items = append(result.Items, item)

		// 构建 DB model
		dbScans = append(dbScans, &model.DailyScan{
			ScanDate:    today,
			StockCode:   code,
			StockName:   klineResp.Name,
			Signals:     model.SignalList(sr.Signals),
			Price:       last.Close,
			PctChg:      item.PctChg,
			VolumeRatio: sr.VolumeRatio,
			MAStatus:    sr.MAStatus,
		})

		s.log.Info("scan hit",
			zap.String("code", code),
			zap.Strings("signals", sr.Signals),
			zap.Float64("vol_ratio", sr.VolumeRatio),
		)
	}

	// ── 5. 批量写库 ────────────────────────────────────────────────
	if len(dbScans) > 0 {
		if err := s.scanRepo.BatchInsertScans(ctx, dbScans); err != nil {
			// 写库失败不影响结果返回，记录错误即可
			s.log.Error("scan: batch insert failed", zap.Error(err))
		}
	}

	result.HitCount   = len(result.Items)
	result.DurationMs = time.Since(start).Milliseconds()

	s.log.Info("scan finished",
		zap.Int("total", result.Total),
		zap.Int("hit", result.HitCount),
		zap.Int64("ms", result.DurationMs),
	)
	return result, nil
}

// ListTodayScans 查询今日扫描结果（从 DB 读取已持久化的记录）。
func (s *ScanService) ListTodayScans(ctx context.Context) ([]*model.DailyScan, error) {
	return s.scanRepo.ListScansByDate(ctx, time.Now())
}

// ListScansByDate 查询指定日期的扫描结果。
func (s *ScanService) ListScansByDate(ctx context.Context, date time.Time) ([]*model.DailyScan, error) {
	return s.scanRepo.ListScansByDate(ctx, date)
}

// ── 辅助函数 ──────────────────────────────────────────────────────

// toHistoryBars 将 KLine 切片转换为 HistoryBar 切片，并填充 PctChg。
// PctChg = (today.Close - yesterday.Close) / yesterday.Close * 100
func toHistoryBars(klines []KLine) []HistoryBar {
	bars := make([]HistoryBar, len(klines))
	for i, k := range klines {
		pctChg := 0.0
		if i > 0 && klines[i-1].Close > 0 {
			pctChg = (k.Close - klines[i-1].Close) / klines[i-1].Close * 100
		}
		bars[i] = HistoryBar{
			Date:   k.Date,
			Close:  k.Close,
			Volume: k.Volume,
			PctChg: roundF(pctChg, 2),
		}
	}
	return bars
}

// MarketMood 根据命中信号判断市场情绪（简单启发式规则）。
func MarketMood(items []*ScanItem) string {
	if len(items) == 0 {
		return "中性"
	}
	riseCount := 0
	for _, item := range items {
		for _, sig := range item.Signals {
			if strings.Contains(sig, "RISE") || strings.Contains(sig, "BREAK") {
				riseCount++
				break
			}
		}
	}
	ratio := float64(riseCount) / float64(len(items))
	switch {
	case ratio >= 0.6:
		return "贪婪"
	case ratio <= 0.3:
		return "恐惧"
	default:
		return "中性"
	}
}
