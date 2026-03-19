package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// ═══════════════════════════════════════════════════════════════
// market_provider.go — 实时行情服务（腾讯 qt.gtimg.cn 版）
//
// 数据源：腾讯 qt.gtimg.cn（主力，无需 Cookie，稳定）
//   URL：https://qt.gtimg.cn/q=sh603920,sz000858
//   响应编码：GBK（需转为 UTF-8）
//   响应格式：每行一个 JS 赋值，~分隔字段
//   [1]名 [3]价 [4]昨收 [5]开 [6]量(手)
//   [31]涨跌额 [32]涨跌幅% [33]高 [34]低 [37]额(万元) [38]换手% [49]量比
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────
// Quote — 统一行情结构
// ─────────────────────────────────────────────────────────────────

type Quote struct {
	Code        string    `json:"code"`
	Name        string    `json:"name"`
	Market      string    `json:"market"`
	Price       float64   `json:"price"`
	Open        float64   `json:"open"`
	Close       float64   `json:"close"`        // 昨收价
	High        float64   `json:"high"`
	Low         float64   `json:"low"`
	Volume      int64     `json:"volume"`       // 成交量（手）
	Amount      float64   `json:"amount"`       // 成交额（万元）
	Change      float64   `json:"change"`       // 涨跌额
	ChangeRate  float64   `json:"change_rate"`  // 涨跌幅(%)
	Turnover    float64   `json:"turnover"`     // 换手率(%)
	VolumeRatio float64   `json:"volume_ratio"` // 量比
	UpdatedAt   time.Time `json:"updated_at"`
	FromCache   bool      `json:"from_cache"`
}

// ─────────────────────────────────────────────────────────────────
// 常量
// ─────────────────────────────────────────────────────────────────

const (
	qqQuoteURL     = "https://qt.gtimg.cn/q=%s"
	qqQuoteReferer = "https://gu.qq.com/"

	quoteCacheTTL  = 5 * time.Second
	quoteBatchSize = 50
	qqQuoteTimeout = 8 * time.Second
)

// ─────────────────────────────────────────────────────────────────
// MarketProvider
// ─────────────────────────────────────────────────────────────────

type MarketProvider struct {
	cache *gocache.Cache
	log   *zap.Logger
}

func NewMarketProvider(log *zap.Logger) *MarketProvider {
	return &MarketProvider{
		cache: gocache.New(quoteCacheTTL, 30*time.Second),
		log:   log,
	}
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

func (p *MarketProvider) FetchRealtimeQuote(code string) (*Quote, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("stock code is empty")
	}

	cacheKey := "quote:" + strings.ToUpper(code)
	if cached, found := p.cache.Get(cacheKey); found {
		q := cached.(*Quote)
		cp := *q
		cp.FromCache = true
		return &cp, nil
	}

	results, err := p.fetchBatch(context.Background(), []string{code})
	if err != nil {
		return nil, fmt.Errorf("FetchRealtimeQuote(%s): %w", code, err)
	}

	q, ok := results[strings.ToUpper(code)]
	if !ok {
		// 兜底：取第一个
		for _, v := range results {
			q = v
			ok = true
			break
		}
	}
	if !ok {
		return nil, fmt.Errorf("FetchRealtimeQuote(%s): not found in response", code)
	}

	p.cache.Set(cacheKey, q, quoteCacheTTL)
	return q, nil
}

func (p *MarketProvider) FetchMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	if len(codes) == 0 {
		return map[string]*Quote{}, nil
	}

	missing := make([]string, 0, len(codes))
	results := make(map[string]*Quote, len(codes))
	for _, code := range codes {
		upperCode := strings.ToUpper(code)
		if cached, found := p.cache.Get("quote:" + upperCode); found {
			q := cached.(*Quote)
			cp := *q
			cp.FromCache = true
			results[upperCode] = &cp
		} else {
			missing = append(missing, code)
		}
	}

	if len(missing) == 0 {
		return results, nil
	}

	var errs []error
	for _, batch := range splitBatches(missing, quoteBatchSize) {
		batchQuotes, err := p.fetchBatch(context.Background(), batch)
		if err != nil {
			errs = append(errs, err)
			p.log.Warn("FetchMultipleQuotes: batch failed", zap.Error(err))
			continue
		}
		for code, q := range batchQuotes {
			results[code] = q
			p.cache.Set("quote:"+code, q, quoteCacheTTL)
		}
	}
	return results, errs
}

func (p *MarketProvider) InvalidateCache(code string) {
	p.cache.Delete("quote:" + strings.ToUpper(code))
}

// ─────────────────────────────────────────────────────────────────
// 腾讯行情抓取与解析
// ─────────────────────────────────────────────────────────────────

func (p *MarketProvider) fetchBatch(ctx context.Context, codes []string) (map[string]*Quote, error) {
	qtCodes := make([]string, 0, len(codes))
	for _, code := range codes {
		qtCodes = append(qtCodes, toQTCode(code))
	}
	url := fmt.Sprintf(qqQuoteURL, strings.Join(qtCodes, ","))
	body, err := fetchQQHTTP(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetchBatch qq: %w", err)
	}
	return parseQQQuoteBatch(body)
}

// gbkToUTF8 将 GBK 编码的字节转换为 UTF-8
// 腾讯 qt.gtimg.cn 接口返回 GBK 编码
func gbkToUTF8(b []byte) ([]byte, error) {
	reader := transform.NewReader(
		strings.NewReader(string(b)),
		simplifiedchinese.GBK.NewDecoder(),
	)
	buf := new(strings.Builder)
	tmp := make([]byte, 4096)
	for {
		n, err := reader.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if err != nil {
			break
		}
	}
	return []byte(buf.String()), nil
}

// parseQQQuoteBatch 解析腾讯批量行情响应
// 腾讯接口返回 GBK 编码，先转 UTF-8 再解析
func parseQQQuoteBatch(body []byte) (map[string]*Quote, error) {
	// GBK → UTF-8
	utf8Body, err := gbkToUTF8(body)
	if err != nil {
		// 转换失败则尝试直接解析（可能已经是 UTF-8）
		utf8Body = body
	}

	results := make(map[string]*Quote)
	for _, line := range strings.Split(string(utf8Body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "v_") {
			continue
		}
		q, parseErr := parseQQQuoteLine(line)
		if parseErr != nil || q == nil {
			continue
		}
		results[q.Code] = q
	}
	return results, nil
}

// parseQQQuoteLine 解析单行腾讯行情（已是 UTF-8）
// 格式：v_sh603920="1~贵州茅台~603920~price~preclose~...";
func parseQQQuoteLine(line string) (*Quote, error) {
	eqIdx := strings.Index(line, "=")
	if eqIdx < 0 {
		return nil, fmt.Errorf("no = in line")
	}
	varName := line[:eqIdx]

	start := strings.Index(line, `"`)
	end := strings.LastIndex(line, `"`)
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no quotes in line")
	}
	fields := strings.Split(line[start+1:end], "~")
	if len(fields) < 38 {
		return nil, fmt.Errorf("too few fields: %d", len(fields))
	}

	market, pureCode := extractMarketCode(varName)

	price := parseF(fields[3])
	preClose := parseF(fields[4])
	if price == 0 && preClose > 0 {
		price = preClose
	}

	volRatio := 0.0
	if len(fields) > 49 {
		volRatio = parseF(fields[49])
	}

	return &Quote{
		Code:        pureCode,
		Name:        fields[1],
		Market:      market,
		Price:       price,
		Open:        parseF(fields[5]),
		Close:       preClose,
		High:        parseF(fields[33]),
		Low:         parseF(fields[34]),
		Volume:      parseI(fields[6]),
		Amount:      parseF(fields[37]),
		Change:      parseF(fields[31]),
		ChangeRate:  parseF(fields[32]),
		Turnover:    parseF(fields[38]),
		VolumeRatio: volRatio,
		UpdatedAt:   time.Now(),
		FromCache:   false,
	}, nil
}

// extractMarketCode 从 "v_sh603920" 解析市场和纯数字代码（大写）
func extractMarketCode(varName string) (market, code string) {
	s := strings.TrimPrefix(varName, "v_")
	if strings.HasPrefix(s, "sh") {
		return "SH", strings.ToUpper(s[2:])
	}
	if strings.HasPrefix(s, "sz") {
		return "SZ", strings.ToUpper(s[2:])
	}
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "6") {
		return "SH", upper
	}
	return "SZ", upper
}

// ─────────────────────────────────────────────────────────────────
// HTTP 工具
// ─────────────────────────────────────────────────────────────────

// fetchQQHTTP 通过共享连接池请求腾讯接口（跳过东财 Cookie）
func fetchQQHTTP(ctx context.Context, url string) ([]byte, error) {
	return GetEMHTTPClient().FetchBody(ctx, url, &EMRequestOption{
		Timeout: qqQuoteTimeout,
		Headers: map[string]string{
			"Referer":    qqQuoteReferer,
			"User-Agent": "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
		},
		SkipCookie: true,
	})
}

// ─────────────────────────────────────────────────────────────────
// 工具函数（供本包全局共用）
// ─────────────────────────────────────────────────────────────────

// toQTCode 将纯数字股票代码转为腾讯格式（小写，如 sh603920 / sz000858）
func toQTCode(code string) string {
	s := strings.TrimSpace(code)
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "sh") || strings.HasPrefix(lower, "sz") {
		return lower
	}
	if strings.HasPrefix(s, "6") || strings.HasPrefix(s, "9") {
		return "sh" + s
	}
	return "sz" + s
}

// buildSecID 构建东财 secid（供 crawler/sector/money_flow 使用）
func buildSecID(code string) string {
	if strings.HasPrefix(code, "6") {
		return "1." + code
	}
	return "0." + code
}

// jnf 解析东财 json.Number
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

func parseF(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseI(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return int64(v)
}

func truncateStr(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
