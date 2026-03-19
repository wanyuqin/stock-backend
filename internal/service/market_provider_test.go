package service

import (
	"encoding/json"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestBuildSecID(t *testing.T) {
	cases := []struct{ code, want string }{
		{"600519", "1.600519"},
		{"601318", "1.601318"},
		{"688001", "1.688001"},
		{"000858", "0.000858"},
		{"300750", "0.300750"},
	}
	for _, c := range cases {
		if got := buildSecID(c.code); got != c.want {
			t.Errorf("buildSecID(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func TestJnf(t *testing.T) {
	cases := []struct {
		name  string
		input json.Number
		want  float64
	}{
		{"normal", "1788.5", 1788.5},
		{"zero", "0", 0},
		{"dash", "-", 0},
		{"empty", "", 0},
		{"negative", "-99.9", -99.9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := jnf(c.input); got != c.want {
				t.Errorf("jnf(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestToQTCode(t *testing.T) {
	cases := []struct{ code, want string }{
		{"600519", "sh600519"},
		{"000858", "sz000858"},
		{"688001", "sh688001"},
		{"300750", "sz300750"},
		{"sh600519", "sh600519"}, // 已有前缀，原样
		{"sz000858", "sz000858"},
	}
	for _, c := range cases {
		if got := toQTCode(c.code); got != c.want {
			t.Errorf("toQTCode(%q) = %q, want %q", c.code, got, c.want)
		}
	}
}

func buildQQLine(prefix string, overrides map[int]string) string {
	fields := make([]string, 55)
	for k, v := range overrides {
		if k < len(fields) {
			fields[k] = v
		}
	}
	content := ""
	for i, f := range fields {
		if i > 0 {
			content += "~"
		}
		content += f
	}
	return `v_` + prefix + `="` + content + `";`
}

func TestParseQQQuoteLine_Normal(t *testing.T) {
	line := buildQQLine("sh603920", map[int]string{
		1: "贵州茅台", 3: "1789.00", 4: "1750.00", 5: "1770.00",
		6: "12345", 31: "39.00", 32: "2.23", 33: "1800.00", 34: "1760.00",
		37: "22000.0", 38: "0.05", 49: "1.25",
	})
	q, err := parseQQQuoteLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Code != "603920"    { t.Errorf("Code = %q", q.Code) }
	if q.Market != "SH"     { t.Errorf("Market = %q", q.Market) }
	if q.Price != 1789.00   { t.Errorf("Price = %v", q.Price) }
	if q.Close != 1750.00   { t.Errorf("Close = %v", q.Close) }
	if q.ChangeRate != 2.23 { t.Errorf("ChangeRate = %v", q.ChangeRate) }
	if q.VolumeRatio != 1.25 { t.Errorf("VolumeRatio = %v", q.VolumeRatio) }
	if q.FromCache           { t.Error("FromCache should be false") }
}

func TestParseQQQuoteLine_SuspendFallback(t *testing.T) {
	line := buildQQLine("sz000858", map[int]string{
		1: "五粮液", 3: "0", 4: "100.00",
	})
	q, err := parseQQQuoteLine(line)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if q.Price != 100.00 { t.Errorf("Price should fallback to 100.00, got %v", q.Price) }
	if q.Market != "SZ"  { t.Errorf("Market = %q", q.Market) }
}

func TestParseQQQuoteBatch(t *testing.T) {
	line1 := buildQQLine("sh603920", map[int]string{1: "贵州茅台", 3: "1789.00", 4: "1750.00"})
	line2 := buildQQLine("sz000858", map[int]string{1: "五粮液",   3: "135.50",  4: "137.50"})
	results, err := parseQQQuoteBatch([]byte(line1 + "\n" + line2))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2, got %d", len(results))
	}
}

func TestSplitBatches(t *testing.T) {
	codes := make([]string, 123)
	batches := splitBatches(codes, 50)
	if len(batches) != 3 {
		t.Fatalf("expected 3 batches, got %d", len(batches))
	}
	if len(batches[0]) != 50 || len(batches[1]) != 50 || len(batches[2]) != 23 {
		t.Errorf("wrong sizes: %d %d %d", len(batches[0]), len(batches[1]), len(batches[2]))
	}
}

func TestCacheTTL(t *testing.T) {
	log, _ := zap.NewDevelopment()
	p := NewMarketProvider(log)
	fake := &Quote{Code: "600519", Price: 1788.5, UpdatedAt: time.Now()}
	p.cache.Set("quote:600519", fake, quoteCacheTTL)
	if _, found := p.cache.Get("quote:600519"); !found {
		t.Fatal("expected cache hit")
	}
	p.InvalidateCache("600519")
	if _, found := p.cache.Get("quote:600519"); found {
		t.Error("expected cache miss after invalidate")
	}
}

func TestParseF(t *testing.T) {
	cases := []struct{ input string; want float64 }{
		{"1789.00", 1789.0}, {"-1.5", -1.5}, {"0", 0}, {"-", 0}, {"", 0},
	}
	for _, c := range cases {
		if got := parseF(c.input); got != c.want {
			t.Errorf("parseF(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

func TestParseI(t *testing.T) {
	cases := []struct{ input string; want int64 }{
		{"12345", 12345}, {"12345.6", 12345}, {"-", 0}, {"", 0},
	}
	for _, c := range cases {
		if got := parseI(c.input); got != c.want {
			t.Errorf("parseI(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}
