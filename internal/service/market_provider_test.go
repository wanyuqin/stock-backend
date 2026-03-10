package service

import (
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// 工具函数单元测试（无网络依赖）
// ─────────────────────────────────────────────────────────────────

func TestBuildSecID(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"600519", "1.600519"}, // 沪市主板
		{"601318", "1.601318"}, // 沪市主板
		{"688001", "1.688001"}, // 科创板（6开头）
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

func TestParseEMFloat(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    float64
		wantErr bool
	}{
		{"number",      `1788.5`,  1788.5, false},
		{"zero",        `0`,       0,      false},
		{"dash",        `"-"`,     0,      true},
		{"empty string",`""`,      0,      true},
		{"string number",`"99.9"`, 99.9,   false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseEMFloat(json.RawMessage(c.input))
			if c.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil (value=%v)", got)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestParseEMResponse_MockBody(t *testing.T) {
	// 模拟东方财富返回的 JSON（fltt=2 格式）
	mockBody := `{
		"code": 0,
		"message": "ok",
		"data": {
			"f43": 1788.5,
			"f44": 1800.0,
			"f45": 1760.0,
			"f46": 1770.0,
			"f47": 12345,
			"f48": 2200000000.0,
			"f50": 1.23,
			"f57": "600519",
			"f58": "贵州茅台",
			"f60": 1750.0,
			"f168": 0.05,
			"f169": 38.5,
			"f170": 2.2
		}
	}`

	q, err := parseEMResponse([]byte(mockBody), "600519")
	if err != nil {
		t.Fatalf("parseEMResponse error: %v", err)
	}

	if q.Code != "600519" {
		t.Errorf("Code = %q, want 600519", q.Code)
	}
	if q.Name != "贵州茅台" {
		t.Errorf("Name = %q, want 贵州茅台", q.Name)
	}
	if q.Market != "SH" {
		t.Errorf("Market = %q, want SH", q.Market)
	}
	if q.Price != 1788.5 {
		t.Errorf("Price = %v, want 1788.5", q.Price)
	}
	if q.Close != 1750.0 {
		t.Errorf("Close = %v, want 1750.0", q.Close)
	}
	if q.ChangeRate != 2.2 {
		t.Errorf("ChangeRate = %v, want 2.2", q.ChangeRate)
	}
	if q.FromCache {
		t.Error("FromCache should be false on fresh fetch")
	}
}

func TestParseEMResponse_NonTradingHours(t *testing.T) {
	// 非交易时段，价格字段返回 "-"，应使用昨收价兜底
	mockBody := `{
		"code": 0,
		"data": {
			"f43": "-",
			"f44": "-",
			"f45": "-",
			"f46": "-",
			"f47": 0,
			"f48": 0,
			"f50": "-",
			"f57": "000858",
			"f58": "五粮液",
			"f60": 160.5,
			"f168": "-",
			"f169": "-",
			"f170": "-"
		}
	}`

	q, err := parseEMResponse([]byte(mockBody), "000858")
	if err != nil {
		t.Fatalf("unexpected error in non-trading: %v", err)
	}
	// 非交易时段 price 用昨收兜底
	if q.Price != 160.5 {
		t.Errorf("Price should fall back to Close=160.5, got %v", q.Price)
	}
}

// ─────────────────────────────────────────────────────────────────
// 内存缓存逻辑测试（无网络依赖）
// ─────────────────────────────────────────────────────────────────

func TestCacheTTL(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := NewMarketProvider(log)

	// 手动写入缓存
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
