package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// MarketProvider — 实时行情服务（优化版）
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 本地缓存（5s TTL）减少请求
// 3. 批量查询，单次最多 50 只
//
// ulist.np 字段映射（fltt=2）：
//   f12=代码 f13=市场 f14=名称 f2=价格 f3=涨跌幅 f4=涨跌额
//   f5=成交量 f6=成交额 f8=换手率 f10=量比
//   f15=最高 f16=最低 f17=今开 f18=昨收 f20=总市值 f21=流通市值
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
	Close       float64   `json:"close"`
	High        float64   `json:"high"`
	Low         float64   `json:"low"`
	Volume      int64     `json:"volume"`
	Amount      float64   `json:"amount"`
	Change      float64   `json:"change"`
	ChangeRate  float64   `json:"change_rate"`
	Turnover    float64   `json:"turnover"`
	VolumeRatio float64   `json:"volume_ratio"`
	UpdatedAt   time.Time `json:"updated_at"`
	FromCache   bool      `json:"from_cache"`
}

// ─────────────────────────────────────────────────────────────────
// ulist.np 原始响应结构
// ─────────────────────────────────────────────────────────────────

type ulistNpResp struct {
	RC   int    `json:"rc"`
	Data *struct {
		Total int           `json:"total"`
		Diff  []ulistNpItem `json:"diff"`
	} `json:"data"`
}

type ulistNpItem struct {
	F12 string      `json:"f12"`
	F13 int         `json:"f13"`
	F14 string      `json:"f14"`
	F2  json.Number `json:"f2"`
	F3  json.Number `json:"f3"`
	F4  json.Number `json:"f4"`
	F5  json.Number `json:"f5"`
	F6  json.Number `json:"f6"`
	F8  json.Number `json:"f8"`
	F10 json.Number `json:"f10"`
	F15 json.Number `json:"f15"`
	F16 json.Number `json:"f16"`
	F17 json.Number `json:"f17"`
	F18 json.Number `json:"f18"`
	F20 json.Number `json:"f20"`
	F21 json.Number `json:"f21"`
}

func (item *ulistNpItem) toQuote() *Quote {
	market := "SZ"
	if item.F13 == 1 {
		market = "SH"
	}
	price := jnf(item.F2)
	closePrice := jnf(item.F18)
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
	emUlistFields = "f12,f13,f14,f2,f3,f4,f5,f6,f8,f10,f15,f16,f17,f18,f20,f21"

	quoteCacheTTL = 5 * time.Second
	quoteBatchSize = 50
)

type MarketProvider struct {
	cache *gocache.Cache
	log   *zap.Logger
}

func NewMarketProvider(log *zap.Logger) *MarketProvider {
	p := &MarketProvider{
		cache: gocache.New(quoteCacheTTL, 30*time.Second),
		log:   log,
	}

	// 预热 Cookie（使用全局 TokenManager）
	go func() {
		if globalTM != nil {
			if _, err := globalTM.GetStockCookie(); err != nil {
				log.Warn("market_provider: cookie pre-warm failed, will retry on first request", zap.Error(err))
			} else {
				log.Info("market_provider: cookie pre-warm succeeded")
			}
		}
	}()

	return p
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

func (p *MarketProvider) FetchRealtimeQuote(code string) (*Quote, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return nil, fmt.Errorf("stock code is empty")
	}

	cacheKey := "quote:" + code
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
	q, ok := results[code]
	if !ok {
		return nil, fmt.Errorf("FetchRealtimeQuote(%s): not found in response", code)
	}

	p.cache.Set(cacheKey, q, quoteCacheTTL)
	p.log.Sugar().Debugw("fetched", "code", code, "price", q.Price)
	return q, nil
}

func (p *MarketProvider) FetchMultipleQuotes(codes []string) (map[string]*Quote, []error) {
	if len(codes) == 0 {
		return map[string]*Quote{}, nil
	}

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

	var errs []error
	ctx := context.Background()
	for _, batch := range splitBatches(missing, quoteBatchSize) {
		batchQuotes, err := p.fetchBatch(ctx, batch)
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
	p.cache.Delete("quote:" + code)
}

// ─────────────────────────────────────────────────────────────────
// 内部方法
// ─────────────────────────────────────────────────────────────────

// fetchBatch 批量获取行情（使用统一 HTTP 客户端）
func (p *MarketProvider) fetchBatch(ctx context.Context, codes []string) (map[string]*Quote, error) {
	secids := make([]string, 0, len(codes))
	for _, code := range codes {
		secids = append(secids, buildSecID(code))
	}

	rawURL := fmt.Sprintf(
		"%s?fltt=2&invt=2&fields=%s&secids=%s&ut=%s",
		emUlistNpURL, emUlistFields,
		strings.Join(secids, ","), emUlistUt,
	)

	// 使用统一的 HTTP 客户端
	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, rawURL, nil)
	if err != nil {
		return nil, err
	}

	return parseUlistNpResp(body)
}

func parseUlistNpResp(body []byte) (map[string]*Quote, error) {
	var raw ulistNpResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w | body: %s", err, truncateBytes(body, 200))
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("eastmoney rc=%d | body: %s", raw.RC, truncateBytes(body, 200))
	}
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

func buildSecID(code string) string {
	if strings.HasPrefix(code, "6") {
		return "1." + code
	}
	return "0." + code
}

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
