package service

// ═══════════════════════════════════════════════════════════════
// interactive_platform_service.go — 互动易/巨潮资讯公告抓取
//
// 数据源：
//   1. 巨潮资讯网 cninfo.com.cn — 结构化 JSON 接口，覆盖沪深北三市
//   2. 深交所互动易 irm.cninfo.com.cn — 个股 Q&A 互动回复（仅深交所）
//
// 使用场景：
//   - 每日开盘前为自选股/买入计划股抓取最近 3 天公告与互动回复
//   - 关键词匹配后触发 Alpha 信号（供货、订单、减持…）
//
// 缓存策略：
//   - 按 (codes + date) 缓存，TTL 30 分钟
// ═══════════════════════════════════════════════════════════════

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// 数据结构
// ─────────────────────────────────────────────────────────────────

// CorporateAnnouncement 企业公告/互动回复条目
type CorporateAnnouncement struct {
	StockCode   string    `json:"stock_code"`
	StockName   string    `json:"stock_name"`
	Content     string    `json:"content"`     // 公告标题或互动摘要
	Source      string    `json:"source"`      // "巨潮" | "互动易"
	PublishedAt time.Time `json:"published_at"`
}

// ─────────────────────────────────────────────────────────────────
// 服务结构
// ─────────────────────────────────────────────────────────────────

type InteractivePlatformService struct {
	log    *zap.Logger
	client *http.Client

	mu          sync.RWMutex
	cachedKey   string
	cachedAt    time.Time
	cachedItems []*CorporateAnnouncement
}

const interactiveCacheTTL = 30 * time.Minute

func NewInteractivePlatformService(log *zap.Logger) *InteractivePlatformService {
	return &InteractivePlatformService{
		log: log,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// ─────────────────────────────────────────────────────────────────
// 主入口
// ─────────────────────────────────────────────────────────────────

// FetchAnnouncements 批量抓取最近 3 天内，指定股票的公告与互动回复。
// codes 为纯数字 A 股代码，如 "603920"。
func (s *InteractivePlatformService) FetchAnnouncements(ctx context.Context, codes []string) ([]*CorporateAnnouncement, error) {
	if len(codes) == 0 {
		return nil, nil
	}

	cacheKey := buildInteractiveCacheKey(codes)
	s.mu.RLock()
	if s.cachedKey == cacheKey && time.Since(s.cachedAt) < interactiveCacheTTL {
		items := s.cachedItems
		s.mu.RUnlock()
		return items, nil
	}
	s.mu.RUnlock()

	type fetchResult struct {
		items []*CorporateAnnouncement
		err   error
	}
	// 每个股票代码并发抓取巨潮 + 互动易（共 2*N 个 goroutine）
	ch := make(chan fetchResult, len(codes)*2)

	for _, code := range codes {
		go func(c string) {
			items, err := s.fetchCninfoAnnouncements(ctx, c)
			ch <- fetchResult{items, err}
		}(code)
		go func(c string) {
			items, err := s.fetchInteractiveQA(ctx, c)
			ch <- fetchResult{items, err}
		}(code)
	}

	results := make([]*CorporateAnnouncement, 0, len(codes)*3)
	for range codes {
		for range [2]struct{}{} {
			r := <-ch
			if r.err != nil {
				s.log.Debug("interactive: fetch error", zap.Error(r.err))
			} else {
				results = append(results, r.items...)
			}
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].PublishedAt.After(results[j].PublishedAt)
	})

	s.mu.Lock()
	s.cachedKey = cacheKey
	s.cachedAt = time.Now()
	s.cachedItems = results
	s.mu.Unlock()

	return results, nil
}

// ─────────────────────────────────────────────────────────────────
// 巨潮资讯网 — 公告列表（沪深北三市）
// ─────────────────────────────────────────────────────────────────

type cninfoAnnouncementResp struct {
	Announcements []struct {
		AnnouncementTitle string `json:"announcementTitle"`
		AnnouncementTime  int64  `json:"announcementTime"` // 毫秒时间戳
		SecCode           string `json:"secCode"`
		SecName           string `json:"secName"`
	} `json:"announcements"`
}

func (s *InteractivePlatformService) fetchCninfoAnnouncements(ctx context.Context, code string) ([]*CorporateAnnouncement, error) {
	exchange := cninfoExchange(code)
	endDate := time.Now().Format("2006-01-02")
	startDate := time.Now().AddDate(0, 0, -3).Format("2006-01-02")

	form := url.Values{
		"stock":     {code + "," + exchange},
		"category":  {""},
		"pageNum":   {"1"},
		"pageSize":  {"5"},
		"column":    {exchange},
		"tabName":   {"fulltext"},
		"plate":     {""},
		"seDate":    {startDate + "~" + endDate},
		"searchkey": {""},
		"secid":     {""},
		"sortName":  {""},
		"sortType":  {""},
		"isHLtitle": {"true"},
	}

	bodyBytes, err := s.doPost(ctx,
		"http://www.cninfo.com.cn/new/hisAnnouncement/query",
		form,
		map[string]string{
			"Referer": "http://www.cninfo.com.cn/new/commonUrl/pageOfSearch?url=disclosure/list/search",
			"Origin":  "http://www.cninfo.com.cn",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("cninfo(%s): %w", code, err)
	}

	var resp cninfoAnnouncementResp
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("cninfo parse(%s): %w", code, err)
	}

	items := make([]*CorporateAnnouncement, 0, len(resp.Announcements))
	for _, ann := range resp.Announcements {
		if ann.AnnouncementTitle == "" {
			continue
		}
		items = append(items, &CorporateAnnouncement{
			StockCode:   code,
			StockName:   ann.SecName,
			Content:     ann.AnnouncementTitle,
			Source:      "巨潮",
			PublishedAt: time.UnixMilli(ann.AnnouncementTime),
		})
	}
	return items, nil
}

// ─────────────────────────────────────────────────────────────────
// 深交所互动易 — 个股问答（仅深交所：0/3 开头）
// ─────────────────────────────────────────────────────────────────

type irmContentResp struct {
	Data []struct {
		Title       string `json:"title"`
		Content     string `json:"content"`
		PublishDate string `json:"publishDate"` // "2024-01-15"
		StockCode   string `json:"stockCode"`
		StockName   string `json:"stockName"`
	} `json:"data"`
}

func (s *InteractivePlatformService) fetchInteractiveQA(ctx context.Context, code string) ([]*CorporateAnnouncement, error) {
	if !isSZSEStock(code) {
		return nil, nil // 互动易仅覆盖深交所
	}

	form := url.Values{
		"stockCode": {code},
		"pageSize":  {"5"},
		"pageNum":   {"1"},
	}
	bodyBytes, err := s.doPost(ctx,
		"https://irm.cninfo.com.cn/ircs/content/contentList.do",
		form,
		map[string]string{
			"Referer": "https://irm.cninfo.com.cn/",
			"Origin":  "https://irm.cninfo.com.cn",
		},
	)
	if err != nil {
		return nil, fmt.Errorf("irm(%s): %w", code, err)
	}

	var resp irmContentResp
	if err := json.Unmarshal(bodyBytes, &resp); err != nil {
		return nil, fmt.Errorf("irm parse(%s): %w", code, err)
	}

	items := make([]*CorporateAnnouncement, 0, len(resp.Data))
	for _, qa := range resp.Data {
		content := qa.Title
		if qa.Content != "" {
			reply := qa.Content
			if len([]rune(reply)) > 100 {
				reply = string([]rune(reply)[:100]) + "…"
			}
			content = qa.Title + " | 回复：" + reply
		}
		t, _ := time.Parse("2006-01-02", qa.PublishDate)
		if t.IsZero() {
			t = time.Now()
		}
		items = append(items, &CorporateAnnouncement{
			StockCode:   code,
			StockName:   qa.StockName,
			Content:     content,
			Source:      "互动易",
			PublishedAt: t,
		})
	}
	return items, nil
}

// ─────────────────────────────────────────────────────────────────
// HTTP 辅助
// ─────────────────────────────────────────────────────────────────

func (s *InteractivePlatformService) doPost(ctx context.Context, targetURL string, form url.Values, extraHeaders map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}
	return body, nil
}

// ─────────────────────────────────────────────────────────────────
// 辅助函数
// ─────────────────────────────────────────────────────────────────

// cninfoExchange 根据股票代码首位判断交易所（巨潮格式）
func cninfoExchange(code string) string {
	if len(code) == 0 {
		return "szse"
	}
	switch code[0] {
	case '6':
		return "sse" // 上海证券交易所
	case '0', '3':
		return "szse" // 深圳证券交易所
	case '8', '4':
		return "bse" // 北京证券交易所
	default:
		return "szse"
	}
}

// isSZSEStock 深交所股票（0/3 开头）
func isSZSEStock(code string) bool {
	return len(code) > 0 && (code[0] == '0' || code[0] == '3')
}

// buildInteractiveCacheKey 按代码列表+日期生成缓存键
func buildInteractiveCacheKey(codes []string) string {
	sorted := make([]string, len(codes))
	copy(sorted, codes)
	sort.Strings(sorted)
	return time.Now().Format("2006-01-02") + ":" + strings.Join(sorted, ",")
}
