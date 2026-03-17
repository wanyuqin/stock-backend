package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// K 线服务 - 优化版
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 指数退避重试策略（最多 3 次）
// 3. 统一的 Cookie 注入和请求头
// ═══════════════════════════════════════════════════════════════

type KLine struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Volume int64   `json:"volume"`
	Amount float64 `json:"amount"`
}

func (k KLine) ToECharts() [4]float64 {
	return [4]float64{k.Open, k.Close, k.Low, k.High}
}

type KLineResponse struct {
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Period     string       `json:"period"`
	KLines     []KLine      `json:"klines"`
	Dates      []string     `json:"dates"`
	OHLCData   [][4]float64 `json:"ohlc_data"`
	VolumeData [][]any      `json:"volume_data"`
}

const (
	emKLineURL = "https://push2his.eastmoney.com/api/qt/stock/kline/get"
	emKLineUt  = "fa5fd1943c7b386f172d6893dbfba10b"

	// K 线接口可能返回大量数据，超时稍长
	klineRequestTimeout = 20 * time.Second
)

type emKLineRaw struct {
	Data struct {
		Code   string   `json:"code"`
		Name   string   `json:"name"`
		Klines []string `json:"klines"`
	} `json:"data"`
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

func (s *StockService) GetKLine(code string, limit int) (*KLineResponse, error) {
	return s.GetKLineEndAt(code, time.Now(), limit)
}

func (s *StockService) GetKLineEndAt(code string, end time.Time, limit int) (*KLineResponse, error) {
	if limit <= 0 || limit > 500 {
		limit = 120
	}

	secid := buildSecID(code)
	endDateStr := end.Format("20060102")
	rawURL := fmt.Sprintf(
		"%s?ut=%s&secid=%s&fields1=f1,f2,f3,f4,f5,f6&fields2=f51,f52,f53,f54,f55,f56,f57,f58&klt=101&fqt=1&beg=0&end=%s&lmt=%d",
		emKLineURL, emKLineUt, secid, endDateStr, limit,
	)

	// 使用统一的 HTTP 客户端，带自动重试
	client := GetEMHTTPClient()
	body, err := client.FetchBody(context.Background(), rawURL, &EMRequestOption{
		Timeout:    klineRequestTimeout,
		MaxRetries: 3,
	})
	if err != nil {
		return nil, fmt.Errorf("kline fetch: %w", err)
	}

	result, err := parseKLineResponse(body, code)
	if err != nil {
		return nil, err
	}

	s.log.Sugar().Debugw("kline: fetched",
		"code", code,
		"bars", len(result.KLines),
	)
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// 解析逻辑
// ─────────────────────────────────────────────────────────────────

func parseKLineResponse(body []byte, code string) (*KLineResponse, error) {
	var raw emKLineRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("kline unmarshal: %w (body: %s)", err, truncateBytes(body, 100))
	}

	// 检查 API 错误
	if raw.Code != 0 && raw.Message != "" {
		return nil, fmt.Errorf("eastmoney api error: code=%d, msg=%s", raw.Code, raw.Message)
	}

	d := raw.Data
	klines := make([]KLine, 0, len(d.Klines))
	dates := make([]string, 0, len(d.Klines))
	ohlcData := make([][4]float64, 0, len(d.Klines))
	volumeData := make([][]any, 0, len(d.Klines))

	for i, line := range d.Klines {
		var (
			date                   string
			open, close, high, low float64
			volume                 int64
			amount                 float64
		)
		n, err := fmt.Sscanf(line, "%10s,%f,%f,%f,%f,%d,%f",
			&date, &open, &close, &high, &low, &volume, &amount)
		if err != nil || n < 7 {
			continue
		}

		k := KLine{Date: date, Open: open, Close: close, High: high, Low: low, Volume: volume, Amount: amount}
		klines = append(klines, k)
		dates = append(dates, date)
		ohlcData = append(ohlcData, k.ToECharts())

		direction := 1
		if close < open {
			direction = -1
		} else if close == open {
			direction = 0
		}
		volumeData = append(volumeData, []any{i, volume, direction})
	}

	name := d.Name
	if name == "" {
		name = code
	}
	if name == "" {
		name = d.Code
	}

	return &KLineResponse{
		Code: d.Code, Name: name, Period: "daily",
		KLines: klines, Dates: dates,
		OHLCData: ohlcData, VolumeData: volumeData,
	}, nil
}
