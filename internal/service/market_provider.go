package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// 接口说明（经实际抓包验证，2026-03）
//
// ❌ push2.eastmoney.com/api/qt/stock/get  → TCP RST，已全线不可用
// ✅ push2.eastmoney.com/api/qt/ulist.np/get → 正常，支持批量 secids
//
// ulist.np 字段映射（fltt=2，数值直接为浮点，无需 ÷100）：
//   f12 = 股票代码      f13 = 市场(1=SH, 0=SZ)   f14 = 股票名称
//   f2  = 当前价        f3  = 涨跌幅(%)            f4  = 涨跌额
//   f5  = 成交量(手)    f6  = 成交额(元)            f7  = 振幅(%)
//   f8  = 换手率(%)     f9  = 市盈率               f10 = 量比
//   f15 = 最高价        f16 = 最低价               f17 = 今开
//   f18 = 昨收价        f20 = 总市值               f21 = 流通市值
//   f23 = 市净率
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────
// Quote — 统一行情结构（字段语义不变，底层接口已换）
// ─────────────────────────────────────────────────────────────────

type Quote struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Market      string    `json:"market"`       // "SH" | "SZ"
	Price       float64   `json:"price"`        // 当前价
	Open        float64   `json:"open"`         // 今开
	Close       float64   `json:"close"`        // 昨收
	High        float64   `json:"high"`         // 最高
	Low         float64   `json:"low"`          // 最低
	Volume      int64     `json:"volume"`       // 成交量（手）
	Amount      float64   `json:"amount"`       // 成交额（元）
	Change      float64   `json:"change"`       // 涨跌额
	ChangeRate  float64   `json:"change_rate"`  // 涨跌幅（%）
	Turnover    float64   `json:"turnover"`     // 换手率（%）
	VolumeRatio float64   `json:"volume_ratio"` // 量比
	UpdatedAt   time.Time `json:"updated_at"`
	FromCache   bool      `json:"from_cache"`
}

// ─────────────────────────────────────────────────────────────────
// ulist.np 原始响应结构
// ─────────────────────────────────────────────────────────────────

type ulistNpResp struct {
	RC   int    `json:"rc"` // 0 = 成功
	Data *struct {
		Total int            `json:"total"`
		Diff  []ulistNpItem  `json:"diff"`
	} `json:"data"`
}

// ulistNpItem 用 json.Number 兼容数字和 "-"（非交易时段字段值为 "-"）。
type ulistNpItem struct {
	F12 string      `json:"f12"` // 股票代码
	F13 int         `json:"f13"` // 市场：1=SH, 0=SZ
	F14 string      `json:"f14"` // 股票名称
	F2  json.Number `json:"f2"`  // 当前价
	F3  json.Number `json:"f3"`  // 涨跌幅(%)
	F4  json.Number `json:"f4"`  // 涨跌额
	F5  json.Number `json:"f5"`  // 成交量(手)
	F6  json.Number `json:"f6"`  // 成交额(元)
	F8  json.Number `json:"f8"`  // 换手率(%)
	F10 json.Number `json:"f10"` // 量比
	F15 json.Number `json:"f15"` // 最高价
	F16 json.Number `json:"f16"` // 最低价
	F17 json.Number `json:"f17"` // 今开
	F18 json.Number `json:"f18"` // 昨收
	F20 json.Number `json:"f20"` // 总市值
	F21 json.Number `json:"f21"` // 流通市值
}

// toQuote 将 ulistNpItem 转换为 Quote。
func (item *ulistNpItem) toQuote() *Quote {
	market := "SZ"
	if item.F13 == 1 {
		market = "SH"
	}

	price := jnf(item.F2)
	closePrice := jnf(item.F18)

	// 非交易时段 price=0，用昨收兜底
	if price == 0 && closePrice > 0 {
		price = closePrice
	}

	return &Quote{
		Code:        item.F12,
		Name:        item.F14,
		Market:      market,
		Price:       price,
		Open:        jnf(item.F17),
		Close:       closePrice,
		High:        jnf(item.F15),
		Low:         jnf(item.F16),
		Volume:      int64(jnf(item.F5)),
		Amount:      jnf(item.F6),
		Change:      jnf(item.F4),
		ChangeRate:  jnf(item.F3),
		Turnover:    jnf(item.F8),
		VolumeRatio: jnf(item.F10),
		UpdatedAt:   time.Now(),
		FromCache:   false,
	}
}

// ─────────────────────────────────────────────────────────────────
// MarketProvider
// ─────────────────────────────────────────────────────────────────

const (
	emUlistNpURL  = "https://push2.eastmoney.com/api/qt/ulist.np/get"
	emUlistUt     = "bd1d9ddb04089700cf9c27f6f7426281"
	// 请求的字段列表（不含 f7/f9/f23，减小响应体积）
	emUlistFields = "f12,f13,f14,f2,f3,f4,f5,f6,f8,f10,f15,f16,f17,f18,f20,f21"

	cacheTTL    = 5 * time.Second
	httpTimeout = 8 * time.Second

	// 每批最多 50 只，避免 URL 过长
	batchSize = 50
)

// MarketProvider 封装行情抓取 + 内存缓存。
type MarketProvider struct {
	httpClient *http.Client
	cache      *gocache.Cache
	log        *zap.Logger
}

func NewMarketProvider(log *zap.Logger) *MarketProvider {
	return &MarketProvider{
		httpClient: &http.Client{
			Timeout: httpTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		cache: gocache.New(cacheTTL, 30*time.Second),
		log:   log,
	}
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

// FetchRealtimeQuote 获取单只股票实时行情。
// 5s 内相同 code 直接返回缓存，不发起 HTTP 请求。
func (p *MarketProvider) FetchRealtimeQuote(code string) (*Quote, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("stock code is empty")
	}

	// 1. 查缓存
	cacheKey := "quote:" + code
	if cached, found := p.cache.Get(cacheKey); found {
		q := cached.(*Quote)
		cp := *q // 拷贝一份，避免 FromCache 污染缓存对象
		cp.FromCache = true
		p.log.Sugar().Debugw("cache hit", "code", code)
		return &cp, nil
	}

	// 2. 缓存未命中，通过批量接口拉取（单只也用 ulist.np，保持一致）
	results, err := p.fetchBatch([]string{code})
	if err != nil {
		return nil, fmt.Errorf("FetchRealtimeQuote(%s): %w", code, err)
	}
	q, ok := results[code]
	if !ok {
		return nil, fmt.Errorf("FetchRealtimeQuote(%s): not found in response", code)
	}

	// 3. 写缓存
	p.cache.Set(cacheKey, q, cacheTTL)
	p.log.Sugar().Debugw("fetched",
		"code", code, "price", q.Price, "change_rate", q.ChangeRate)
	return q, nil
}

// FetchMultipleQuotes 批量获取多只股票实时行情。
// 先查内存缓存，剩余 miss 的按 batchSize 分批并发请求 ulist.np。
// 返回 map[code]*Quote，失败的 code 不出现在结果中。
func (p *MarketProvider) FetchMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	if len(codes) == 0 {
		return map[string]*Quote{}, nil
	}

	// 1. 先走缓存
	missing := make([]string, 0, len(codes))
	results := make(map[string]*Quote, len(codes))
	for _, code := range codes {
		if cached, found := p.cache.Get("quote:" + code); found {
			q := cached.(*Quote)
			cp := *q
			cp.FromCache = true
			results[code] = &cp
		} else {
			missing = append(missing, code)
		}
	}

	if len(missing) == 0 {
		return results, nil
	}

	// 2. 分批并发请求
	type batchResult struct {
		quotes map[string]*Quote
		err    error
	}

	batches := splitBatches(missing, batchSize)
	ch := make(chan batchResult, len(batches))

	for _, batch := range batches {
		go func(b []string) {
			q, err := p.fetchBatch(b)
			ch <- batchResult{quotes: q, err: err}
		}(batch)
	}

	var errs []error
	for range batches {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
			p.log.Warn("FetchMultipleQuotes: batch failed", zap.Error(r.err))
			continue
		}
		for code, q := range r.quotes {
			results[code] = q
			p.cache.Set("quote:"+code, q, cacheTTL)
		}
	}

	return results, errs
}

// InvalidateCache 主动清除某只股票的缓存。
func (p *MarketProvider) InvalidateCache(code string) {
	p.cache.Delete("quote:" + code)
}

// ─────────────────────────────────────────────────────────────────
// 私有方法
// ─────────────────────────────────────────────────────────────────

// fetchBatch 用 ulist.np 批量拉取行情。
// codes 已过滤空值，secids 由 buildSecID 拼接。
func (p *MarketProvider) fetchBatch(codes []string) (map[string]*Quote, error) {
	secids := make([]string, 0, len(codes))
	for _, code := range codes {
		secids = append(secids, buildSecID(code))
	}

	url := fmt.Sprintf(
		"%s?fltt=2&invt=2&fields=%s&secids=%s&ut=%s",
		emUlistNpURL, emUlistFields,
		strings.Join(secids, ","), emUlistUt,
	)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected http status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseUlistNpResp(body)
}

// parseUlistNpResp 解析 ulist.np 响应，返回 map[code]*Quote。
func parseUlistNpResp(body []byte) (map[string]*Quote, error) {
	var raw ulistNpResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w | body: %s", err, truncate(body, 200))
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("eastmoney rc=%d | body: %s", raw.RC, truncate(body, 200))
	}
	// 非交易时段 / 代码无效时 diff 为空，返回空 map（不报错，上层 quote=nil）
	if raw.Data == nil || len(raw.Data.Diff) == 0 {
		return map[string]*Quote{}, nil
	}

	results := make(map[string]*Quote, len(raw.Data.Diff))
	for i := range raw.Data.Diff {
		item := &raw.Data.Diff[i]
		if item.F12 == "" {
			continue
		}
		results[item.F12] = item.toQuote()
	}
	return results, nil
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

// buildSecID 6 开头 → 沪市 "1.xxx"，其余 → 深市 "0.xxx"。
func buildSecID(code string) string {
	if strings.HasPrefix(code, "6") {
		return "1." + code
	}
	return "0." + code
}

// jnf (json.Number → float64) 将 json.Number 转为 float64，"-" 或空值返回 0。
func jnf(n json.Number) float64 {
	if n == "" || n == "-" {
		return 0
	}
	f, err := n.Float64()
	if err != nil {
		return 0
	}
	return f
}

// splitBatches 将 codes 切分为每批最多 size 个的二维切片。
func splitBatches(codes []string, size int) [][]string {
	var batches [][]string
	for i := 0; i < len(codes); i += size {
		end := i + size
		if end > len(codes) {
			end = len(codes)
		}
		batches = append(batches, codes[i:end])
	}
	return batches
}

// truncate 截断 byte slice，用于日志输出。
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
