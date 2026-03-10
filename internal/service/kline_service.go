package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ═══════════════════════════════════════════════════════════════
// K 线数据结构
// ═══════════════════════════════════════════════════════════════

// KLine 单根 K 线。
type KLine struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Volume int64   `json:"volume"` // 成交量（手）
	Amount float64 `json:"amount"` // 成交额（元）
}

// ToECharts 转换为 ECharts candlestick 格式：[open, close, low, high]
func (k KLine) ToECharts() [4]float64 {
	return [4]float64{k.Open, k.Close, k.Low, k.High}
}

// KLineResponse 是 GetKLine 的返回结构，包含前端可直接使用的预处理数据。
type KLineResponse struct {
	Code       string       `json:"code"`
	Name       string       `json:"name"`
	Period     string       `json:"period"`
	KLines     []KLine      `json:"klines"`
	Dates      []string     `json:"dates"`       // ECharts X 轴
	OHLCData   [][4]float64 `json:"ohlc_data"`   // candlestick series.data
	VolumeData [][]any      `json:"volume_data"` // bar series.data
}

// ═══════════════════════════════════════════════════════════════
// 东方财富历史 K 线 API
// ═══════════════════════════════════════════════════════════════

const (
	emKLineURL = "https://push2his.eastmoney.com/api/qt/stock/kline/get"
	emKLineUt  = "fa5fd1943c7b386f172d6893dbfba10b"
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

// GetKLine 是 StockService 上的方法，获取日 K 线数据（最多 limit 根，前复权）。
func (s *StockService) GetKLine(code string, limit int) (*KLineResponse, error) {
	if limit <= 0 || limit > 500 {
		limit = 120
	}

	secid := buildSecID(code)
	rawURL := fmt.Sprintf(
		"%s?ut=%s&secid=%s&fields1=f1,f2,f3,f4,f5,f6&fields2=f51,f52,f53,f54,f55,f56,f57,f58&klt=101&fqt=1&beg=0&end=20500101&lmt=%d",
		emKLineURL, emKLineUt, secid, limit,
	)

	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build kline request: %w", err)
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("kline http get: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("kline read body: %w", err)
	}

	result, err := parseKLineResponse(body, code)
	if err != nil {
		return nil, err
	}

	s.log.Sugar().Debugw("kline fetched",
		"code", code,
		"bars", len(result.KLines),
	)
	return result, nil
}

// parseKLineResponse 解析原始响应并构建前端友好的格式。
func parseKLineResponse(body []byte, code string) (*KLineResponse, error) {
	var raw emKLineRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("kline unmarshal: %w", err)
	}

	d := raw.Data
	klines := make([]KLine, 0, len(d.Klines))
	dates := make([]string, 0, len(d.Klines))
	ohlcData := make([][4]float64, 0, len(d.Klines))
	volumeData := make([][]any, 0, len(d.Klines))

	for i, line := range d.Klines {
		// 格式："日期,开,收,高,低,量,额,振幅,涨跌幅,涨跌额,换手率"
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

	return &KLineResponse{
		Code: d.Code, Name: name, Period: "daily",
		KLines: klines, Dates: dates,
		OHLCData: ohlcData, VolumeData: volumeData,
	}, nil
}
