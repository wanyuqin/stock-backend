package service

import (
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// buildSecID 单元测试（无网络依赖）
// ─────────────────────────────────────────────────────────────────

func TestBuildSecID(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"600519", "1.600519"}, // 沪市主板
		{"601318", "1.601318"}, // 沪市主板
		{"688001", "1.688001"}, // 科创板（6 开头）
		{"000858", "0.000858"}, // 深市主板
		{"300750", "0.300750"}, // 创业板
		{"002594", "0.002594"}, // 深市中小板
	}
	for _, c := range cases {
		got := buildSecID(c.code)
		if got != c.want {
			t.Errorf("buildSecID(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────
// jnf 单元测试
// ─────────────────────────────────────────────────────────────────

func TestJnf(t *testing.T) {
	cases := []struct {
		name  string
		input json.Number
		want  float64
	}{
		{"normal number",  "1788.5", 1788.5},
		{"zero",           "0",      0},
		{"dash",           "-",      0},
		{"empty",          "",       0},
		{"negative",       "-99.9",  -99.9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := jnf(c.input)
			if got != c.want {
				t.Errorf("jnf(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

// ─────────────────────────────────────────────────────────────────
// parseUlistNpResp 单元测试（Mock 响应体，无网络）
// ─────────────────────────────────────────────────────────────────

func TestParseUlistNpResp_Normal(t *testing.T) {
	// 模拟 ulist.np 返回两只股票（fltt=2 格式）
	mockBody := `{
		"rc": 0,
		"data": {
			"total": 2,
			"diff": [
				{
					"f12": "600519",
					"f13": 1,
					"f14": "贵州茅台",
					"f2": 1788.5,
					"f3": 2.2,
					"f4": 38.5,
					"f5": 12345,
					"f6": 2200000000.0,
					"f8": 0.05,
					"f10": 1.23,
					"f15": 1800.0,
					"f16": 1760.0,
					"f17": 1770.0,
					"f18": 1750.0,
					"f20": 0,
					"f21": 0
				},
				{
					"f12": "000858",
					"f13": 0,
					"f14": "五粮液",
					"f2": 135.5,
					"f3": -1.5,
					"f4": -2.06,
					"f5": 8000,
					"f6": 1100000000.0,
					"f8": 0.03,
					"f10": 0.95,
					"f15": 138.0,
					"f16": 134.0,
					"f17": 137.0,
					"f18": 137.56,
					"f20": 0,
					"f21": 0
				}
			]
		}
	}`

	results, err := parseUlistNpResp([]byte(mockBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	// 验证贵州茅台
	moutai := results["600519"]
	if moutai == nil {
		t.Fatal("600519 not found in results")
	}
	if moutai.Code != "600519" {
		t.Errorf("Code = %q, want 600519", moutai.Code)
	}
	if moutai.Name != "贵州茅台" {
		t.Errorf("Name = %q, want 贵州茅台", moutai.Name)
	}
	if moutai.Market != "SH" {
		t.Errorf("Market = %q, want SH", moutai.Market)
	}
	if moutai.Price != 1788.5 {
		t.Errorf("Price = %v, want 1788.5", moutai.Price)
	}
	if moutai.Close != 1750.0 {
		t.Errorf("Close = %v, want 1750.0", moutai.Close)
	}
	if moutai.ChangeRate != 2.2 {
		t.Errorf("ChangeRate = %v, want 2.2", moutai.ChangeRate)
	}
	if moutai.FromCache {
		t.Error("FromCache should be false on fresh fetch")
	}

	// 验证五粮液（深市）
	wuliangye := results["000858"]
	if wuliangye == nil {
		t.Fatal("000858 not found in results")
	}
	if wuliangye.Market != "SZ" {
		t.Errorf("Market = %q, want SZ", wuliangye.Market)
	}
	if wuliangye.ChangeRate != -1.5 {
		t.Errorf("ChangeRate = %v, want -1.5", wuliangye.ChangeRate)
	}
}

func TestParseUlistNpResp_NonTradingHours(t *testing.T) {
	// 非交易时段：当前价为 "-"，应用昨收价兜底
	mockBody := `{
		"rc": 0,
		"data": {
			"total": 1,
			"diff": [
				{
					"f12": "000858",
					"f13": 0,
					"f14": "五粮液",
					"f2": "-",
					"f3": "-",
					"f4": "-",
					"f5": "0",
					"f6": "0",
					"f8": "-",
					"f10": "-",
					"f15": "-",
					"f16": "-",
					"f17": "-",
					"f18": 160.5,
					"f20": "0",
					"f21": "0"
				}
			]
		}
	}`

	results, err := parseUlistNpResp([]byte(mockBody))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	q := results["000858"]
	if q == nil {
		t.Fatal("000858 not found")
	}
	// 非交易时段 price 应回退到昨收价
	if q.Price != 160.5 {
		t.Errorf("Price should fall back to Close=160.5, got %v", q.Price)
	}
}

func TestParseUlistNpResp_EmptyDiff(t *testing.T) {
	// diff 为空（无效代码或非交易时段全空）
	mockBody := `{"rc": 0, "data": {"total": 0, "diff": []}}`
	results, err := parseUlistNpResp([]byte(mockBody))
	if err != nil {
		t.Fatalf("unexpected error on empty diff: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestParseUlistNpResp_RCError(t *testing.T) {
	// rc != 0 应返回 error
	mockBody := `{"rc": 102, "data": null}`
	_, err := parseUlistNpResp([]byte(mockBody))
	if err == nil {
		t.Error("expected error when rc != 0, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────
// splitBatches 单元测试
// ─────────────────────────────────────────────────────────────────

func TestSplitBatches(t *testing.T) {
	codes := make([]string, 123)
	for i := range codes {
		codes[i] = "code"
	}

	batches := splitBatches(codes, 50)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 50 {
		t.Errorf("batch[0] len = %d, want 50", len(batches[0]))
	}
	if len(batches[1]) != 50 {
		t.Errorf("batch[1] len = %d, want 50", len(batches[1]))
	}
	if len(batches[2]) != 23 {
		t.Errorf("batch[2] len = %d, want 23", len(batches[2]))
	}
}

// ─────────────────────────────────────────────────────────────────
// 内存缓存逻辑测试（无网络依赖）
// ─────────────────────────────────────────────────────────────────

func TestCacheTTL(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := NewMarketProvider(log)

	fakeQuote := &Quote{
		Code:      "600519",
		Name:      "贵州茅台",
		Price:     1788.5,
		UpdatedAt: time.Now(),
		FromCache: false,
	}
	p.cache.Set("quote:600519", fakeQuote, cacheTTL)

	// 立即读取 → 命中缓存
	cached, found := p.cache.Get("quote:600519")
	if !found {
		t.Fatal("expected cache hit immediately after Set")
	}
	if cached.(*Quote).Price != 1788.5 {
		t.Errorf("cached price = %v, want 1788.5", cached.(*Quote).Price)
	}

	// 清除后读取 → 未命中
	p.InvalidateCache("600519")
	_, found = p.cache.Get("quote:600519")
	if found {
		t.Error("expected cache miss after InvalidateCache")
	}
}
