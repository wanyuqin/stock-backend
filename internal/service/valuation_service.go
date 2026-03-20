package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// ValuationService — 个股估值分位（优化版）
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 本地积累历史数据，计算分位
//
// 接口：push2.eastmoney.com/api/qt/ulist.np/get
//   f9=PE-TTM  f23=PB  f12=代码  f14=名称
// ═══════════════════════════════════════════════════════════════

const (
	valUlistURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2&fields=f12,f13,f14,f9,f23&ut=bd1d9ddb04089700cf9c27f6f7426281&secids=%s"

	valReqTimeout  = 15 * time.Second
	valBatchSize   = 50
	minHistoryDays = 10
)

type valUlistResp struct {
	RC   int `json:"rc"`
	Data *struct {
		Total int            `json:"total"`
		Diff  []valUlistItem `json:"diff"`
	} `json:"data"`
}

type valUlistItem struct {
	F12 string      `json:"f12"`
	F13 int         `json:"f13"`
	F14 string      `json:"f14"`
	F9  json.Number `json:"f9"`
	F23 json.Number `json:"f23"`
}

type ValuationSyncResult struct {
	Total   int `json:"total"`
	Success int `json:"success"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type ValuationService struct {
	valRepo             repo.ValuationRepo
	watchlistRepo       repo.WatchlistRepo
	defaultMarketSource string
	log                 *zap.Logger
}

func NewValuationService(
	valRepo repo.ValuationRepo,
	watchlistRepo repo.WatchlistRepo,
	defaultMarketSource string,
	log *zap.Logger,
) *ValuationService {
	return &ValuationService{
		valRepo:             valRepo,
		watchlistRepo:       watchlistRepo,
		defaultMarketSource: normalizeMarketSource(defaultMarketSource),
		log:                 log,
	}
}

func (s *ValuationService) DefaultMarketSource() string { return s.defaultMarketSource }

// GetValuationBySource 按 source 获取估值（当前 em 为主，qq 暂回退 em）。
func (s *ValuationService) GetValuationBySource(ctx context.Context, code, source string) (*model.StockValuation, error) {
	norm := normalizeMarketSource(sourceOrDefault(source, s.defaultMarketSource))
	if norm == "qq" {
		snap, err := s.getValuationFromQQ(ctx, code)
		if err == nil {
			return snap, nil
		}
		s.log.Warn("GetValuationBySource: qq failed, fallback to em",
			zap.String("code", code), zap.Error(err))
		recordDataSourceFallback("valuation", "qq", "em")
	}
	return s.GetValuation(ctx, code)
}

func (s *ValuationService) SyncWatchlistValuationsBySource(ctx context.Context, userID int64, source string) (*ValuationSyncResult, error) {
	norm := normalizeMarketSource(sourceOrDefault(source, s.defaultMarketSource))
	if norm == "qq" {
		res, err := s.syncWatchlistValuationsFromQQ(ctx, userID)
		if err == nil {
			return res, nil
		}
		s.log.Warn("SyncWatchlistValuationsBySource: qq failed, fallback to em", zap.Error(err))
		recordDataSourceFallback("valuation_sync", "qq", "em")
	}
	return s.SyncWatchlistValuations(ctx, userID)
}

func (s *ValuationService) getValuationFromQQ(ctx context.Context, code string) (*model.StockValuation, error) {
	items, err := s.fetchQQValuation(ctx, []string{code})
	if err != nil {
		return nil, err
	}
	it, ok := items[code]
	if !ok {
		return nil, fmt.Errorf("qq valuation missing %s", code)
	}
	return s.persistValuationSnapshot(ctx, code, it.Name, it.PE, it.PB), nil
}

func (s *ValuationService) syncWatchlistValuationsFromQQ(ctx context.Context, userID int64) (*ValuationSyncResult, error) {
	watchItems, err := s.watchlistRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ListByUser: %w", err)
	}
	if len(watchItems) == 0 {
		return &ValuationSyncResult{}, nil
	}
	codes := make([]string, 0, len(watchItems))
	for _, w := range watchItems {
		codes = append(codes, w.StockCode)
	}
	result := &ValuationSyncResult{Total: len(codes)}
	for _, batch := range splitBatches(codes, valBatchSize) {
		items, e := s.fetchQQValuation(ctx, batch)
		if e != nil {
			result.Failed += len(batch)
			continue
		}
		for _, code := range batch {
			it, ok := items[code]
			if !ok {
				result.Failed++
				continue
			}
			_ = s.persistValuationSnapshot(ctx, code, it.Name, it.PE, it.PB)
			result.Success++
		}
	}
	return result, nil
}

type qqValuationItem struct {
	Name string
	PE   *float64
	PB   *float64
}

func (s *ValuationService) fetchQQValuation(ctx context.Context, codes []string) (map[string]qqValuationItem, error) {
	qtCodes := make([]string, 0, len(codes))
	normalized := make(map[string]string, len(codes))
	for _, c := range codes {
		code := strings.TrimSpace(c)
		if code == "" {
			continue
		}
		normalized[code] = code
		qtCodes = append(qtCodes, toQTCode(code))
	}
	if len(qtCodes) == 0 {
		return map[string]qqValuationItem{}, nil
	}
	body, err := fetchQQHTTP(ctx, fmt.Sprintf(qqQuoteURL, strings.Join(qtCodes, ",")))
	if err != nil {
		return nil, fmt.Errorf("qq valuation http: %w", err)
	}
	utf8Body, _ := gbkToUTF8(body)
	if len(utf8Body) == 0 {
		utf8Body = body
	}
	out := make(map[string]qqValuationItem, len(codes))
	for _, line := range strings.Split(string(utf8Body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "v_") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq < 0 {
			continue
		}
		start := strings.Index(line, "\"")
		end := strings.LastIndex(line, "\"")
		if start < 0 || end <= start {
			continue
		}
		fields := strings.Split(line[start+1:end], "~")
		if len(fields) < 47 {
			continue
		}
		code := strings.TrimSpace(fields[2])
		if code == "" {
			continue
		}
		pe := numPtr(parseQQFloat(fields, 39))
		pb := numPtr(parseQQFloat(fields, 46))
		out[code] = qqValuationItem{
			Name: strings.TrimSpace(fields[1]),
			PE:   pe,
			PB:   pb,
		}
	}
	return out, nil
}

func parseQQFloat(fields []string, idx int) float64 {
	if idx < 0 || idx >= len(fields) {
		return 0
	}
	v, _ := strconv.ParseFloat(strings.TrimSpace(fields[idx]), 64)
	return v
}

func numPtr(v float64) *float64 {
	if v <= 0 {
		return nil
	}
	v2 := math.Round(v*1000) / 1000
	return &v2
}

func (s *ValuationService) persistValuationSnapshot(ctx context.Context, code, name string, peTTM, pb *float64) *model.StockValuation {
	today := time.Now().Truncate(24 * time.Hour)
	_ = s.valRepo.InsertHistory(ctx, &model.StockValuationHistory{
		StockCode: code,
		TradeDate: today,
		PETTM:     peTTM,
		PB:        pb,
	})
	pePercentile, pbPercentile, histDays := s.calcPercentiles(ctx, code, peTTM, pb)
	snap := &model.StockValuation{
		StockCode:    code,
		StockName:    name,
		PETTM:        peTTM,
		PB:           pb,
		PEPercentile: pePercentile,
		PBPercentile: pbPercentile,
		HistoryDays:  histDays,
	}
	_ = s.valRepo.UpsertSnapshot(ctx, snap)
	return snap
}

// ─────────────────────────────────────────────────────────────────
// SyncWatchlistValuations
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) SyncWatchlistValuations(ctx context.Context, userID int64) (*ValuationSyncResult, error) {
	watchItems, err := s.watchlistRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ListByUser: %w", err)
	}
	if len(watchItems) == 0 {
		s.log.Info("valuation: watchlist empty, skip")
		return &ValuationSyncResult{}, nil
	}

	type stockMeta struct{ code, secid string }
	metas := make([]stockMeta, 0, len(watchItems))
	for _, w := range watchItems {
		prefix := "0"
		if strings.HasPrefix(w.StockCode, "6") {
			prefix = "1"
		}
		metas = append(metas, stockMeta{code: w.StockCode, secid: prefix + "." + w.StockCode})
	}

	result := &ValuationSyncResult{Total: len(metas)}
	today := time.Now().Truncate(24 * time.Hour)

	for start := 0; start < len(metas); start += valBatchSize {
		end := start + valBatchSize
		if end > len(metas) {
			end = len(metas)
		}
		batch := metas[start:end]

		secids := make([]string, len(batch))
		for i, m := range batch {
			secids[i] = m.secid
		}

		items, err := s.fetchValuation(ctx, strings.Join(secids, ","))
		if err != nil {
			s.log.Warn("valuation: batch fetch failed", zap.Int("start", start), zap.Error(err))
			result.Failed += len(batch)
			continue
		}

		itemMap := make(map[string]*valUlistItem, len(items))
		for i := range items {
			itemMap[items[i].F12] = &items[i]
		}

		for _, meta := range batch {
			item, ok := itemMap[meta.code]
			if !ok {
				result.Failed++
				continue
			}

			peTTM := numPtrFromNumber(item.F9)
			pb := numPtrFromNumber(item.F23)

			_ = s.valRepo.InsertHistory(ctx, &model.StockValuationHistory{
				StockCode: meta.code,
				TradeDate: today,
				PETTM:     peTTM,
				PB:        pb,
			})

			pePercentile, pbPercentile, histDays := s.calcPercentiles(ctx, meta.code, peTTM, pb)

			snap := &model.StockValuation{
				StockCode:    meta.code,
				StockName:    item.F14,
				PETTM:        peTTM,
				PB:           pb,
				PEPercentile: pePercentile,
				PBPercentile: pbPercentile,
				HistoryDays:  histDays,
			}
			if err := s.valRepo.UpsertSnapshot(ctx, snap); err != nil {
				s.log.Warn("valuation: UpsertSnapshot failed",
					zap.String("code", meta.code), zap.Error(err))
				result.Failed++
				continue
			}
			result.Success++
		}

		if end < len(metas) {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}

	s.log.Info("valuation: sync done",
		zap.Int("total", result.Total),
		zap.Int("success", result.Success),
		zap.Int("failed", result.Failed),
	)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// GetValuation
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) GetValuation(ctx context.Context, code string) (*model.StockValuation, error) {
	prefix := "0"
	if strings.HasPrefix(code, "6") {
		prefix = "1"
	}

	items, err := s.fetchValuation(ctx, prefix+"."+code)
	if err != nil {
		s.log.Warn("valuation: real-time fetch failed, returning cached",
			zap.String("code", code), zap.Error(err))
		cached, dbErr := s.valRepo.GetSnapshot(ctx, code)
		if dbErr != nil {
			return nil, fmt.Errorf("fetch failed and no cache: fetch=%w, cache=%w", err, dbErr)
		}
		return cached, nil
	}

	if len(items) == 0 {
		return nil, fmt.Errorf("GetValuation %s: empty response", code)
	}

	item := &items[0]
	today := time.Now().Truncate(24 * time.Hour)
	peTTM := numPtrFromNumber(item.F9)
	pb := numPtrFromNumber(item.F23)

	_ = s.valRepo.InsertHistory(ctx, &model.StockValuationHistory{
		StockCode: code,
		TradeDate: today,
		PETTM:     peTTM,
		PB:        pb,
	})

	pePercentile, pbPercentile, histDays := s.calcPercentiles(ctx, code, peTTM, pb)

	snap := &model.StockValuation{
		StockCode:    code,
		StockName:    item.F14,
		PETTM:        peTTM,
		PB:           pb,
		PEPercentile: pePercentile,
		PBPercentile: pbPercentile,
		HistoryDays:  histDays,
	}
	_ = s.valRepo.UpsertSnapshot(ctx, snap)
	return snap, nil
}

// ─────────────────────────────────────────────────────────────────
// GetWatchlistSummary
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) GetWatchlistSummary(ctx context.Context, userID int64) (*ValuationSummary, error) {
	watchItems, err := s.watchlistRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if len(watchItems) == 0 {
		return &ValuationSummary{}, nil
	}

	codes := make([]string, len(watchItems))
	for i, w := range watchItems {
		codes[i] = w.StockCode
	}

	snaps, err := s.valRepo.ListSnapshots(ctx, codes)
	if err != nil {
		return nil, err
	}
	return buildSummary(snaps), nil
}

// ─────────────────────────────────────────────────────────────────
// BackfillValuationHistory — 历史数据回补
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) BackfillValuationHistory(ctx context.Context, userID int64, days int) (*ValuationSyncResult, error) {
	if days <= 0 || days > 365 {
		days = 90
	}

	watchItems, err := s.watchlistRepo.ListByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("ListByUser: %w", err)
	}
	if len(watchItems) == 0 {
		return &ValuationSyncResult{}, nil
	}

	codes := make([]string, len(watchItems))
	for i, w := range watchItems {
		codes[i] = w.StockCode
	}

	snaps, err := s.valRepo.ListSnapshots(ctx, codes)
	if err != nil {
		return nil, fmt.Errorf("ListSnapshots: %w", err)
	}

	// 快照为空时先同步一次
	if len(snaps) == 0 {
		s.log.Info("valuation backfill: no snapshots, triggering sync first")
		if _, syncErr := s.SyncWatchlistValuations(ctx, userID); syncErr != nil {
			return nil, fmt.Errorf("initial sync failed: %w", syncErr)
		}
		snaps, err = s.valRepo.ListSnapshots(ctx, codes)
		if err != nil || len(snaps) == 0 {
			return nil, fmt.Errorf("still no snapshots after sync")
		}
	}

	result := &ValuationSyncResult{Total: len(snaps) * days}
	today := time.Now().Truncate(24 * time.Hour)

	for _, snap := range snaps {
		if snap.PETTM == nil && snap.PB == nil {
			result.Skipped += days
			continue
		}

		for i := 1; i <= days; i++ {
			date := today.AddDate(0, 0, -i)
			wd := date.Weekday()
			if wd == time.Saturday || wd == time.Sunday {
				result.Skipped++
				continue
			}

			hist := &model.StockValuationHistory{
				StockCode: snap.StockCode,
				TradeDate: date,
				PETTM:     snap.PETTM,
				PB:        snap.PB,
			}
			if err := s.valRepo.InsertHistory(ctx, hist); err != nil {
				result.Skipped++ // ON CONFLICT DO NOTHING
			} else {
				result.Success++
			}
		}

		// 重算分位并更新快照
		pePercentile, pbPercentile, histDays := s.calcPercentiles(ctx, snap.StockCode, snap.PETTM, snap.PB)
		snap.PEPercentile = pePercentile
		snap.PBPercentile = pbPercentile
		snap.HistoryDays = histDays
		if upsertErr := s.valRepo.UpsertSnapshot(ctx, snap); upsertErr != nil {
			s.log.Warn("valuation backfill: UpsertSnapshot failed",
				zap.String("code", snap.StockCode), zap.Error(upsertErr))
			result.Failed++
		}

		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
	}

	s.log.Info("valuation backfill done",
		zap.Int("stocks", len(snaps)),
		zap.Int("days", days),
		zap.Int("inserted", result.Success),
		zap.Int("skipped", result.Skipped),
	)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// HTTP 抓取（使用统一客户端）
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) fetchValuation(ctx context.Context, secids string) ([]valUlistItem, error) {
	url := fmt.Sprintf(valUlistURL, secids)

	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, url, &EMRequestOption{
		Timeout:    valReqTimeout,
		MaxRetries: 3,
	})
	if err != nil {
		return nil, err
	}

	var parsed valUlistResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal: %w | body: %.200s", err, body)
	}
	if parsed.RC != 0 {
		return nil, fmt.Errorf("eastmoney rc=%d", parsed.RC)
	}
	if parsed.Data == nil {
		return []valUlistItem{}, nil
	}
	return parsed.Data.Diff, nil
}

// ─────────────────────────────────────────────────────────────────
// 分位计算引擎
// ─────────────────────────────────────────────────────────────────

func (s *ValuationService) calcPercentiles(
	ctx context.Context,
	code string,
	currentPE, currentPB *float64,
) (pePercentile, pbPercentile *float64, histDays int) {
	history, err := s.valRepo.ListHistory(ctx, code, 0)
	if err != nil || len(history) < minHistoryDays {
		histDays = len(history)
		return nil, nil, histDays
	}
	histDays = len(history)

	if currentPE != nil && *currentPE > 0 {
		pePercentile = ptrFloat64(CalculatePercentile(collectPE(history), *currentPE))
	}
	if currentPB != nil && *currentPB > 0 {
		pbPercentile = ptrFloat64(CalculatePercentile(collectPB(history), *currentPB))
	}
	return
}

func CalculatePercentile(series []float64, current float64) float64 {
	valid := make([]float64, 0, len(series))
	for _, v := range series {
		if v > 0 {
			valid = append(valid, v)
		}
	}
	if len(valid) < 2 {
		return -1
	}

	mean, std := meanStd(valid)
	if std > 0 {
		filtered := valid[:0]
		for _, v := range valid {
			if math.Abs(v-mean) <= 3*std {
				filtered = append(filtered, v)
			}
		}
		if len(filtered) >= 2 {
			valid = filtered
		}
	}

	sort.Float64s(valid)
	count := 0
	for _, v := range valid {
		if v < current {
			count++
		}
	}
	return math.Round(float64(count)/float64(len(valid))*100*10) / 10
}

// ─────────────────────────────────────────────────────────────────
// 估值标签与汇总
// ─────────────────────────────────────────────────────────────────

type ValuationStatus string

const (
	StatusUndervalued ValuationStatus = "undervalued"
	StatusNormal      ValuationStatus = "normal"
	StatusOvervalued  ValuationStatus = "overvalued"
	StatusUnknown     ValuationStatus = "unknown"
	StatusLoss        ValuationStatus = "loss"
)

func GetValuationStatus(peTTM, pePercentile *float64) ValuationStatus {
	if peTTM != nil && *peTTM < 0 {
		return StatusLoss
	}
	if pePercentile == nil {
		return StatusUnknown
	}
	if *pePercentile < 30 {
		return StatusUndervalued
	}
	if *pePercentile > 70 {
		return StatusOvervalued
	}
	return StatusNormal
}

type ValuationSummary struct {
	Total       int             `json:"total"`
	Undervalued int             `json:"undervalued"`
	Normal      int             `json:"normal"`
	Overvalued  int             `json:"overvalued"`
	Unknown     int             `json:"unknown"`
	Items       []*ValuationDTO `json:"items"`
}

type ValuationDTO struct {
	StockCode    string          `json:"code"`
	StockName    string          `json:"name"`
	PETTM        *float64        `json:"pe_ttm"`
	PB           *float64        `json:"pb"`
	PEPercentile *float64        `json:"pe_percentile"`
	PBPercentile *float64        `json:"pb_percentile"`
	HistoryDays  int             `json:"history_days"`
	Status       ValuationStatus `json:"status"`
	UpdatedAt    string          `json:"updated_at"`
}

func buildSummary(snaps []*model.StockValuation) *ValuationSummary {
	summary := &ValuationSummary{Total: len(snaps), Items: make([]*ValuationDTO, 0, len(snaps))}
	for _, s := range snaps {
		status := GetValuationStatus(s.PETTM, s.PEPercentile)
		switch status {
		case StatusUndervalued:
			summary.Undervalued++
		case StatusOvervalued:
			summary.Overvalued++
		case StatusNormal:
			summary.Normal++
		default:
			summary.Unknown++
		}
		summary.Items = append(summary.Items, &ValuationDTO{
			StockCode:    s.StockCode,
			StockName:    s.StockName,
			PETTM:        s.PETTM,
			PB:           s.PB,
			PEPercentile: s.PEPercentile,
			PBPercentile: s.PBPercentile,
			HistoryDays:  s.HistoryDays,
			Status:       status,
			UpdatedAt:    s.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	return summary
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

func numPtrFromNumber(n json.Number) *float64 {
	if n == "" || n == "-" {
		return nil
	}
	f, err := n.Float64()
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return nil
	}
	return &f
}

func ptrFloat64(f float64) *float64 {
	if f < 0 {
		return nil
	}
	return &f
}

func collectPE(history []*model.StockValuationHistory) []float64 {
	out := make([]float64, 0, len(history))
	for _, h := range history {
		if h.PETTM != nil {
			out = append(out, *h.PETTM)
		}
	}
	return out
}

func collectPB(history []*model.StockValuationHistory) []float64 {
	out := make([]float64, 0, len(history))
	for _, h := range history {
		if h.PB != nil {
			out = append(out, *h.PB)
		}
	}
	return out
}

func meanStd(data []float64) (mean, std float64) {
	if len(data) == 0 {
		return 0, 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	mean = sum / float64(len(data))
	variance := 0.0
	for _, v := range data {
		d := v - mean
		variance += d * d
	}
	std = math.Sqrt(variance / float64(len(data)))
	return
}
