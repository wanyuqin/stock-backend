package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// 统一行情数据结构
// ═══════════════════════════════════════════════════════════════

// Quote 是对外暴露的统一实时行情结构，屏蔽东方财富的 f43/f44 字段命名。
type Quote struct {
	Code        string    `json:"code"`          // 股票代码，如 "600519"
	Name        string    `json:"name"`          // 股票名称，如 "贵州茅台"
	Market      string    `json:"market"`        // "SH" | "SZ"
	Price       float64   `json:"price"`         // 当前价（f43）
	Open        float64   `json:"open"`          // 开盘价（f46）
	Close       float64   `json:"close"`         // 昨收价（f60）
	High        float64   `json:"high"`          // 最高价（f44）
	Low         float64   `json:"low"`           // 最低价（f45）
	Volume      int64     `json:"volume"`        // 成交量，手（f47）
	Amount      float64   `json:"amount"`        // 成交额，元（f48）
	Change      float64   `json:"change"`        // 涨跌额（f169）
	ChangeRate  float64   `json:"change_rate"`   // 涨跌幅，%（f170）
	Turnover    float64   `json:"turnover"`      // 换手率，%（f168）
	VolumeRatio float64   `json:"volume_ratio"`  // 量比（f50）
	UpdatedAt   time.Time `json:"updated_at"`    // 本次数据获取时间
	FromCache   bool      `json:"from_cache"`    // 是否来自缓存
}

// ═══════════════════════════════════════════════════════════════
// 东方财富 API 原始响应结构
// ═══════════════════════════════════════════════════════════════

// emResponse 是东方财富 /api/qt/stock/get 接口的顶层响应。
type emResponse struct {
	Data    emData `json:"data"`
	Code    int    `json:"code"` // 0 = 成功
	Message string `json:"message"`
}

// emData 是 data 字段，所有行情都在 diff 节点。
// 注意：东方财富将价格 ×100 后作为整数返回（fltt=2 模式返回浮点字符串）。
// 本实现使用 fltt=2，价格直接是浮点数字符串，无需手动 /100。
type emData struct {
	// 东方财富把每个字段都直接放在 data 对象里
	F43  json.RawMessage `json:"f43"`  // 当前价
	F44  json.RawMessage `json:"f44"`  // 最高
	F45  json.RawMessage `json:"f45"`  // 最低
	F46  json.RawMessage `json:"f46"`  // 开盘
	F47  json.RawMessage `json:"f47"`  // 成交量（手）
	F48  json.RawMessage `json:"f48"`  // 成交额（元）
	F50  json.RawMessage `json:"f50"`  // 量比
	F57  string          `json:"f57"`  // 股票代码
	F58  string          `json:"f58"`  // 股票名称
	F60  json.RawMessage `json:"f60"`  // 昨收价
	F168 json.RawMessage `json:"f168"` // 换手率（%）
	F169 json.RawMessage `json:"f169"` // 涨跌额
	F170 json.RawMessage `json:"f170"` // 涨跌幅（%）
}

// ═══════════════════════════════════════════════════════════════
// MarketProvider：实时行情抓取服务
// ═══════════════════════════════════════════════════════════════

const (
	emBaseURL  = "https://push2.eastmoney.com/api/qt/stock/get"
	emUt       = "fa5fd1943c7b386f172d6893dbfba10b"
	emFields   = "f43,f44,f45,f46,f47,f48,f50,f57,f58,f60,f168,f169,f170"
	cacheTTL   = 5 * time.Second  // 缓存有效期
	httpTimeout = 8 * time.Second // HTTP 超时
)

// MarketProvider 封装行情抓取 + 内存缓存逻辑。
type MarketProvider struct {
	httpClient *http.Client
	cache      *gocache.Cache // 内存缓存，TTL = 5s
	log        *zap.Logger
}

// NewMarketProvider 创建一个 MarketProvider 实例。
// 推荐在应用启动时调用一次，全局复用（http.Client 是并发安全的）。
func NewMarketProvider(log *zap.Logger) *MarketProvider {
	return &MarketProvider{
		httpClient: &http.Client{
			Timeout: httpTimeout,
			// 连接池复用，避免每次请求都建立 TCP 连接
			Transport: &http.Transport{
				MaxIdleConns:        20,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     30 * time.Second,
			},
		},
		// 缓存默认 TTL = 5s，每 30s 清理一次过期 key
		cache: gocache.New(cacheTTL, 30*time.Second),
		log:   log,
	}
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

// FetchRealtimeQuote 获取单只股票的实时行情。
// 缓存策略：5 秒内的相同 code 直接返回缓存，不发起 HTTP 请求。
//
// code 格式：纯数字代码，如 "600519"、"000858"
// 市场判断规则：6 开头 → 沪市（SH，secid=1.xxx），其余 → 深市（SZ，secid=0.xxx）
func (p *MarketProvider) FetchRealtimeQuote(code string) (*Quote, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("stock code is empty")
	}

	// ── 1. 先查缓存 ───────────────────────────────────────────────
	cacheKey := "quote:" + code
	if cached, found := p.cache.Get(cacheKey); found {
		q := cached.(*Quote)
		q.FromCache = true
		p.log.Sugar().Debugw("cache hit", "code", code)
		return q, nil
	}

	// ── 2. 缓存未命中，发起 HTTP 请求 ─────────────────────────────
	quote, err := p.fetchFromEastMoney(code)
	if err != nil {
		return nil, fmt.Errorf("fetchRealtimeQuote(%s): %w", code, err)
	}

	// ── 3. 写入缓存 ───────────────────────────────────────────────
	p.cache.Set(cacheKey, quote, cacheTTL)
	p.log.Sugar().Debugw("fetched and cached",
		"code", code,
		"price", quote.Price,
		"change_rate", quote.ChangeRate,
	)

	return quote, nil
}

// FetchMultipleQuotes 批量获取多只股票的实时行情（并发抓取）。
// 返回 map[code]*Quote，失败的 code 不会出现在结果中，错误单独收集。
func (p *MarketProvider) FetchMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	type result struct {
		code  string
		quote *Quote
		err   error
	}

	ch := make(chan result, len(codes))
	for _, code := range codes {
		go func(c string) {
			q, err := p.FetchRealtimeQuote(c)
			ch <- result{code: c, quote: q, err: err}
		}(code)
	}

	quotes := make(map[string]*Quote, len(codes))
	var errs []error
	for range codes {
		r := <-ch
		if r.err != nil {
			errs = append(errs, r.err)
			p.log.Sugar().Warnw("fetch failed", "code", r.code, "err", r.err)
		} else {
			quotes[r.code] = r.quote
		}
	}
	return quotes, errs
}

// InvalidateCache 主动清除某只股票的缓存（测试 / 强制刷新用）。
func (p *MarketProvider) InvalidateCache(code string) {
	p.cache.Delete("quote:" + code)
}

// ─────────────────────────────────────────────────────────────────
// 私有方法
// ─────────────────────────────────────────────────────────────────

// fetchFromEastMoney 向东方财富 API 发起 HTTP GET，解析并转换为 Quote。
func (p *MarketProvider) fetchFromEastMoney(code string) (*Quote, error) {
	secid := buildSecID(code)
	url := buildURL(secid)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	// 伪装成浏览器，避免被反爬
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
			"AppleWebKit/537.36 (KHTML, like Gecko) "+
			"Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseEMResponse(body, code)
}

// buildSecID 根据股票代码判断市场，生成东方财富的 secid 参数。
//
//   - 6 开头 → 沪市主板/科创板 → "1.600519"
//   - 其余   → 深市主板/创业板 → "0.000858"
func buildSecID(code string) string {
	if strings.HasPrefix(code, "6") {
		return "1." + code
	}
	return "0." + code
}

// buildURL 拼接完整的东方财富请求 URL。
func buildURL(secid string) string {
	return fmt.Sprintf(
		"%s?ut=%s&invt=2&fltt=2&fields=%s&secid=%s",
		emBaseURL, emUt, emFields, secid,
	)
}

// parseEMResponse 解析东方财富响应 JSON，转换为统一的 Quote 结构。
func parseEMResponse(body []byte, code string) (*Quote, error) {
	var raw emResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}

	d := raw.Data

	// 东方财富在非交易时段或代码不存在时，f43 等字段返回 "-"（字符串）
	price, err := parseEMFloat(d.F43)
	if err != nil {
		return nil, fmt.Errorf("invalid price field (f43): %w, body=%s", err, truncate(body, 200))
	}

	market := "SZ"
	if strings.HasPrefix(code, "6") {
		market = "SH"
	}

	quote := &Quote{
		Code:        d.F57,
		Name:        d.F58,
		Market:      market,
		Price:       price,
		High:        parseEMFloatOrZero(d.F44),
		Low:         parseEMFloatOrZero(d.F45),
		Open:        parseEMFloatOrZero(d.F46),
		Volume:      parseEMInt(d.F47),
		Amount:      parseEMFloatOrZero(d.F48),
		VolumeRatio: parseEMFloatOrZero(d.F50),
		Close:       parseEMFloatOrZero(d.F60),
		Turnover:    parseEMFloatOrZero(d.F168),
		Change:      parseEMFloatOrZero(d.F169),
		ChangeRate:  parseEMFloatOrZero(d.F170),
		UpdatedAt:   time.Now(),
		FromCache:   false,
	}

	// 兜底：非交易时段 price=0，用昨收价填充
	if quote.Price == 0 && quote.Close > 0 {
		quote.Price = quote.Close
	}

	return quote, nil
}

// ─────────────────────────────────────────────────────────────────
// 解析工具函数
// ─────────────────────────────────────────────────────────────────

// parseEMFloat 解析东方财富返回的数值字段。
// fltt=2 模式下，数值以 JSON number 或 string("-") 返回。
// 当字段为 "-" 时返回 error，调用方决定如何处理。
func parseEMFloat(raw json.RawMessage) (float64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("empty field")
	}
	// 尝试直接解析 number
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return f, nil
	}
	// 尝试解析为字符串（"-" 或 "123.45"）
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return 0, fmt.Errorf("cannot parse %s", raw)
	}
	if s == "-" || s == "" {
		return 0, fmt.Errorf("field is dash (non-trading)")
	}
	return strconv.ParseFloat(s, 64)
}

// parseEMFloatOrZero 解析失败时返回 0（非核心字段使用）。
func parseEMFloatOrZero(raw json.RawMessage) float64 {
	f, _ := parseEMFloat(raw)
	return f
}

// parseEMInt 解析成交量等整数字段，失败返回 0。
func parseEMInt(raw json.RawMessage) int64 {
	if len(raw) == 0 {
		return 0
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil {
		return int64(f)
	}
	return 0
}

// truncate 截断 byte slice，用于日志输出，避免打印超长内容。
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
