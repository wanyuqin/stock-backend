package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
// 常量
// ═══════════════════════════════════════════════════════════════

const (
	// fltt=2：所有数值字段保持原始精度（价格单位=元，百分比=实际值）
	// fltt=1（默认）会把价格*10、百分比*100，导致数值放大
	emFullMarketBaseURL = "https://push2.eastmoney.com/api/qt/clist/get" +
		"?fltt=2&fid=f3&po=1" +
		"&fields=f2,f3,f5,f6,f8,f10,f12,f14,f62,f184" +
		"&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23"

	crawlerPageSize    = 100
	crawlerWorkers     = 10
	crawlerHTTPTimeout = 30 * time.Second
	crawlerPageDelay   = 200 * time.Millisecond
)

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

// parseDiff 将 data.diff 字节安全解析为 []map[string]interface{}。
// 兼容：JSON数组 / 数字键对象 / null / 空对象
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
	client       *http.Client
	log          *zap.Logger
}

func NewCrawlerService(snapshotRepo repo.SnapshotRepo, log *zap.Logger) *CrawlerService {
	return &CrawlerService{
		snapshotRepo: snapshotRepo,
		client:       &http.Client{Timeout: crawlerHTTPTimeout},
		log:          log,
	}
}

// SyncFullMarketData 分页拉取全市场行情 → 并发解析 → 去重 → 批量 Upsert。
func (s *CrawlerService) SyncFullMarketData(ctx context.Context) (int, error) {
	start := time.Now()
	s.log.Info("crawler: SyncFullMarketData started")

	allItems, err := s.fetchAllPages(ctx)
	if err != nil {
		return 0, fmt.Errorf("fetchAllPages: %w", err)
	}
	if len(allItems) == 0 {
		s.log.Warn("crawler: API returned 0 items")
		return 0, nil
	}
	s.log.Info("crawler: total fetched",
		zap.Int("raw_count", len(allItems)),
		zap.Duration("fetch_elapsed", time.Since(start)),
	)

	tradeDate := todayDate()
	snapshots, err := s.parseWithWorkerPool(allItems, tradeDate)
	if err != nil {
		return 0, fmt.Errorf("parseWithWorkerPool: %w", err)
	}
	s.log.Info("crawler: parsed", zap.Int("parsed_count", len(snapshots)))

	before := len(snapshots)
	snapshots = deduplicateSnapshots(snapshots)
	if dups := before - len(snapshots); dups > 0 {
		s.log.Warn("crawler: removed duplicate codes", zap.Int("duplicates", dups))
	}
	s.log.Info("crawler: after dedup", zap.Int("final_count", len(snapshots)))

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
	firstPage, total, err := s.fetchPage(1)
	if err != nil {
		return nil, fmt.Errorf("page 1: %w", err)
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
	s.log.Info("crawler: will fetch pages", zap.Int("total_pages", totalPages))

	for page := 2; page <= totalPages; page++ {
		select {
		case <-ctx.Done():
			return allItems, ctx.Err()
		default:
		}

		time.Sleep(crawlerPageDelay)

		items, _, err := s.fetchPage(page)
		if err != nil {
			s.log.Warn("crawler: page failed", zap.Int("page", page), zap.Error(err))
			continue
		}
		if len(items) == 0 {
			s.log.Info("crawler: empty page, stopping", zap.Int("page", page))
			break
		}

		allItems = append(allItems, items...)
		s.log.Debug("crawler: page ok",
			zap.Int("page", page),
			zap.Int("items", len(items)),
			zap.Int("accumulated", len(allItems)),
		)
	}

	return allItems, nil
}

func (s *CrawlerService) fetchPage(page int) ([]map[string]interface{}, int, error) {
	url := fmt.Sprintf("%s&pn=%d&pz=%d", emFullMarketBaseURL, page, crawlerPageSize)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://finance.eastmoney.com/")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http get page %d: %w", page, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("http status %d on page %d", resp.StatusCode, page)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body page %d: %w", page, err)
	}

	if page == 1 {
		preview := body
		if len(preview) > 400 {
			preview = preview[:400]
		}
		s.log.Debug("crawler: page1 preview", zap.String("body", string(preview)))
	}

	var parsed emFullMarketResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		preview := body
		if len(preview) > 200 {
			preview = preview[:200]
		}
		s.log.Error("crawler: unmarshal failed",
			zap.Int("page", page),
			zap.String("preview", string(preview)),
			zap.Error(err),
		)
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

	// fltt=2 时所有字段均为原始精度：
	// f2  = 价格（元，如 15.23）
	// f3  = 涨跌幅（%，如 3.25，不是 325）
	// f8  = 换手率（%，如 2.11）
	// f10 = 量比（如 1.85）
	// f62 = 主力净流入（元，大数）
	// f184= 主力净流入占比（%，如 8.33）
	snap.Price        = safeFloatPtr(raw["f2"])
	snap.PctChg       = safeFloatPtr(raw["f3"])
	snap.TurnoverRate = safeFloatPtr(raw["f8"])
	snap.VolRatio     = safeFloatPtr(raw["f10"])

	mainInflow := safeDecimal(raw["f62"])
	f := mainInflow.InexactFloat64()
	snap.MainInflow    = &f
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
