package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// CrawlerService — 全市场行情同步（优化版）
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 并发解析，Worker Pool 模式
// 3. 去重与幂等入库
// ═══════════════════════════════════════════════════════════════

const (
	emFullMarketBaseURL = "https://push2.eastmoney.com/api/qt/clist/get" +
		"?fltt=2&fid=f3&po=1" +
		"&fields=f2,f3,f5,f6,f8,f10,f12,f14,f62,f184" +
		"&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23"

	crawlerPageSize   = 100
	crawlerWorkers    = 10
	crawlerPageDelay  = 150 * time.Millisecond
	crawlerReqTimeout = 30 * time.Second
)

// ═══════════════════════════════════════════════════════════════
// SyncError — 可分类的同步错误
// ═══════════════════════════════════════════════════════════════

type SyncErrorKind string

const (
	SyncErrNetwork   SyncErrorKind = "network"
	SyncErrEmptyData SyncErrorKind = "empty_data"
	SyncErrAPI       SyncErrorKind = "api_error"
	SyncErrParse     SyncErrorKind = "parse_error"
)

type SyncError struct {
	Kind    SyncErrorKind
	Message string
	Raw     error
}

func (e *SyncError) Error() string { return e.Message }

func newSyncErr(kind SyncErrorKind, msg string, raw error) *SyncError {
	return &SyncError{Kind: kind, Message: msg, Raw: raw}
}

// ═══════════════════════════════════════════════════════════════
// 东方财富原始响应结构
// ═══════════════════════════════════════════════════════════════

type emFullMarketResp struct {
	Data struct {
		Total int             `json:"total"`
		Diff  json.RawMessage `json:"diff"`
	} `json:"data"`
	RC int `json:"rc"`
}

func parseDiff(raw json.RawMessage) ([]map[string]interface{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}

	switch trimmed[0] {
	case '[':
		var items []map[string]interface{}
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("diff array unmarshal: %w", err)
		}
		return items, nil

	case '{':
		var objMap map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &objMap); err != nil {
			return nil, nil
		}
		if len(objMap) == 0 {
			return nil, nil
		}
		keys := make([]string, 0, len(objMap))
		for k := range objMap {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			ni, _ := strconv.Atoi(keys[i])
			nj, _ := strconv.Atoi(keys[j])
			return ni < nj
		})
		items := make([]map[string]interface{}, 0, len(objMap))
		for _, k := range keys {
			var item map[string]interface{}
			if err := json.Unmarshal(objMap[k], &item); err == nil {
				items = append(items, item)
			}
		}
		return items, nil

	default:
		return nil, nil
	}
}

// ═══════════════════════════════════════════════════════════════
// CrawlerService
// ═══════════════════════════════════════════════════════════════

type CrawlerService struct {
	snapshotRepo repo.SnapshotRepo
	log          *zap.Logger
}

func NewCrawlerService(snapshotRepo repo.SnapshotRepo, log *zap.Logger) *CrawlerService {
	return &CrawlerService{
		snapshotRepo: snapshotRepo,
		log:          log,
	}
}

// SyncFullMarketData 分页拉取全市场行情 → 并发解析 → 去重 → 批量 Upsert。
func (s *CrawlerService) SyncFullMarketData(ctx context.Context) (int, error) {
	start := time.Now()
	s.log.Info("crawler: SyncFullMarketData started")

	allItems, err := s.fetchAllPages(ctx)
	if err != nil {
		return 0, err
	}

	if len(allItems) == 0 {
		s.log.Warn("crawler: API returned 0 items, likely non-trading hours")
		return 0, newSyncErr(
			SyncErrEmptyData,
			"东方财富接口返回空数据，可能当前为非交易时段（收盘后/周末/节假日）",
			nil,
		)
	}

	s.log.Info("crawler: total fetched",
		zap.Int("raw_count", len(allItems)),
		zap.Duration("fetch_elapsed", time.Since(start)),
	)

	tradeDate := todayDate()
	snapshots, err := s.parseWithWorkerPool(allItems, tradeDate)
	if err != nil {
		return 0, newSyncErr(SyncErrParse, "数据解析失败: "+err.Error(), err)
	}
	s.log.Info("crawler: parsed", zap.Int("parsed_count", len(snapshots)))

	before := len(snapshots)
	snapshots = deduplicateSnapshots(snapshots)
	if dups := before - len(snapshots); dups > 0 {
		s.log.Warn("crawler: removed duplicate codes", zap.Int("duplicates", dups))
	}

	if err := s.snapshotRepo.BulkUpsert(ctx, snapshots); err != nil {
		return 0, fmt.Errorf("BulkUpsert: %w", err)
	}

	total := len(snapshots)
	s.log.Info("crawler: SyncFullMarketData done",
		zap.Int("upserted", total),
		zap.Duration("total_elapsed", time.Since(start)),
	)
	return total, nil
}

// ── 分页抓取 ──────────────────────────────────────────────────────

func (s *CrawlerService) fetchAllPages(ctx context.Context) ([]map[string]interface{}, error) {
	// 第 1 页
	firstPage, total, err := s.fetchPage(ctx, 1)
	if err != nil {
		return nil, newSyncErr(SyncErrNetwork, "抓取第1页失败: "+err.Error(), err)
	}

	s.log.Info("crawler: first page",
		zap.Int("declared_total", total),
		zap.Int("page_items", len(firstPage)),
	)

	allItems := make([]map[string]interface{}, 0, total)
	allItems = append(allItems, firstPage...)

	if total <= crawlerPageSize {
		return allItems, nil
	}

	totalPages := (total + crawlerPageSize - 1) / crawlerPageSize
	for page := 2; page <= totalPages; page++ {
		select {
		case <-ctx.Done():
			return allItems, ctx.Err()
		default:
		}

		time.Sleep(crawlerPageDelay)

		items, _, err := s.fetchPage(ctx, page)
		if err != nil {
			s.log.Warn("crawler: page failed, skip", zap.Int("page", page), zap.Error(err))
			continue
		}
		if len(items) == 0 {
			break
		}
		allItems = append(allItems, items...)
	}

	return allItems, nil
}

func (s *CrawlerService) fetchPage(ctx context.Context, page int) ([]map[string]interface{}, int, error) {
	url := fmt.Sprintf("%s&pn=%d&pz=%d", emFullMarketBaseURL, page, crawlerPageSize)

	// 使用统一的 HTTP 客户端
	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, url, &EMRequestOption{
		Timeout:    crawlerReqTimeout,
		MaxRetries: 3,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("http get page %d: %w", page, err)
	}

	var parsed emFullMarketResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("unmarshal page %d: %w", page, err)
	}
	if parsed.RC != 0 {
		return nil, 0, fmt.Errorf("API rc=%d on page %d", parsed.RC, page)
	}

	items, err := parseDiff(parsed.Data.Diff)
	if err != nil {
		return nil, 0, fmt.Errorf("parseDiff page %d: %w", page, err)
	}

	return items, parsed.Data.Total, nil
}

// ── Worker Pool 并发解析 ──────────────────────────────────────────

func (s *CrawlerService) parseWithWorkerPool(
	items []map[string]interface{},
	tradeDate time.Time,
) ([]*model.StockDailySnapshot, error) {
	total := len(items)
	workers := crawlerWorkers
	if workers > runtime.NumCPU()*2 {
		workers = runtime.NumCPU() * 2
	}

	results := make([]*model.StockDailySnapshot, total)
	var firstErr error
	var errOnce sync.Once

	type job struct {
		idx  int
		item map[string]interface{}
	}
	jobCh := make(chan job, total)
	for i, item := range items {
		jobCh <- job{idx: i, item: item}
	}
	close(jobCh)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				snap, err := parseSnapshot(j.item, tradeDate)
				if err != nil {
					errOnce.Do(func() { firstErr = err })
					continue
				}
				if snap != nil {
					results[j.idx] = snap
				}
			}
		}()
	}
	wg.Wait()

	if firstErr != nil {
		s.log.Warn("crawler: some items failed to parse", zap.Error(firstErr))
	}

	valid := make([]*model.StockDailySnapshot, 0, total)
	for _, r := range results {
		if r != nil {
			valid = append(valid, r)
		}
	}
	return valid, nil
}

// ── 去重 ──────────────────────────────────────────────────────────

func deduplicateSnapshots(snaps []*model.StockDailySnapshot) []*model.StockDailySnapshot {
	seen := make(map[string]int, len(snaps))
	for i, s := range snaps {
		seen[s.Code] = i
	}
	result := make([]*model.StockDailySnapshot, 0, len(seen))
	added := make(map[string]bool, len(seen))
	for i := len(snaps) - 1; i >= 0; i-- {
		code := snaps[i].Code
		if !added[code] && seen[code] == i {
			result = append(result, snaps[i])
			added[code] = true
		}
	}
	for l, r := 0, len(result)-1; l < r; l, r = l+1, r-1 {
		result[l], result[r] = result[r], result[l]
	}
	return result
}

// ── 单条解析 ──────────────────────────────────────────────────────

func parseSnapshot(raw map[string]interface{}, tradeDate time.Time) (*model.StockDailySnapshot, error) {
	code := safeStr(raw["f12"])
	if code == "" || code == "-" {
		return nil, nil
	}
	snap := &model.StockDailySnapshot{
		TradeDate: tradeDate,
		Code:      code,
		Name:      safeStr(raw["f14"]),
	}
	snap.Price = safeFloatPtr(raw["f2"])
	snap.PctChg = safeFloatPtr(raw["f3"])
	snap.TurnoverRate = safeFloatPtr(raw["f8"])
	snap.VolRatio = safeFloatPtr(raw["f10"])

	mainInflow := safeDecimal(raw["f62"])
	f := mainInflow.InexactFloat64()
	snap.MainInflow = &f
	snap.MainInflowPct = safeFloatPtr(raw["f184"])
	return snap, nil
}

// ── 辅助函数 ──────────────────────────────────────────────────────

func todayDate() time.Time {
	now := time.Now()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func safeStr(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case float64:
		return fmt.Sprintf("%.0f", val)
	default:
		return fmt.Sprintf("%v", val)
	}
}

func safeFloatPtr(v interface{}) *float64 {
	if v == nil {
		return nil
	}
	switch val := v.(type) {
	case float64:
		return &val
	case string:
		if val == "-" || val == "" {
			return nil
		}
		d, err := decimal.NewFromString(val)
		if err != nil {
			return nil
		}
		f := d.InexactFloat64()
		return &f
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

func safeDecimal(v interface{}) decimal.Decimal {
	if v == nil {
		return decimal.Zero
	}
	switch val := v.(type) {
	case float64:
		return decimal.NewFromFloat(val)
	case string:
		if val == "-" || val == "" {
			return decimal.Zero
		}
		d, err := decimal.NewFromString(val)
		if err != nil {
			return decimal.Zero
		}
		return d
	case json.Number:
		d, err := decimal.NewFromString(string(val))
		if err != nil {
			return decimal.Zero
		}
		return d
	default:
		return decimal.Zero
	}
}
