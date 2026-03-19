package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
)

// ═══════════════════════════════════════════════════════════════
// kline_qq.go — 腾讯证券分时数据
//
// 接口一：当日分时
//   GET /appstock/app/minute/query?_var=min_data_{code}&code={code}&r={rand}
//
// 接口二：多日分时（近5个交易日）
//   GET /appstock/app/day/query?_var=fdays_data_{code}&code={code}&r={rand}
//
// 注意：fetchQQHTTP / toQTCode / gbkToUTF8 定义在 market_provider.go
//       腾讯接口返回 GBK 编码，需在解析前转为 UTF-8
// ═══════════════════════════════════════════════════════════════

const (
	qqMinuteURL = "https://web.ifzq.gtimg.cn/appstock/app/minute/query"
	qqDayURL    = "https://web.ifzq.gtimg.cn/appstock/app/day/query"
)

// MinuteBar 一根分时 bar
type MinuteBar struct {
	Time      string  `json:"time"`
	Price     float64 `json:"price"`
	Volume    int64   `json:"volume"`    // 本分钟增量（手）
	Amount    float64 `json:"amount"`    // 本分钟增量（元）
	AvgPrice  float64 `json:"avg_price"`
	CumVolume int64   `json:"cum_volume"`
	CumAmount float64 `json:"cum_amount"`
}

// MinuteResponse 分时数据响应
type MinuteResponse struct {
	Code     string      `json:"code"`
	Name     string      `json:"name"`
	Date     string      `json:"date"`
	PreClose float64     `json:"pre_close"`
	Bars     []MinuteBar `json:"bars"`
	Times    []string    `json:"times"`
	Prices   []float64   `json:"prices"`
	Volumes  [][]any     `json:"volumes"`
	Amounts  []float64   `json:"amounts"`
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

func (s *StockService) GetMinuteData(code string) (*MinuteResponse, error) {
	qtCode := toQTCode(code)
	url := fmt.Sprintf("%s?_var=min_data_%s&code=%s&r=%s",
		qqMinuteURL, qtCode, qtCode, randStr())
	body, err := fetchQQHTTP(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("GetMinuteData fetch: %w", err)
	}
	// GBK → UTF-8
	if utf8, e := gbkToUTF8(body); e == nil {
		body = utf8
	}
	return parseMinuteResponse(body, qtCode, code)
}

func (s *StockService) GetDayMinuteData(code string, days int) ([]*MinuteResponse, error) {
	if days <= 0 || days > 5 {
		days = 5
	}
	qtCode := toQTCode(code)
	url := fmt.Sprintf("%s?_var=fdays_data_%s&code=%s&r=%s",
		qqDayURL, qtCode, qtCode, randStr())
	body, err := fetchQQHTTP(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("GetDayMinuteData fetch: %w", err)
	}
	if utf8, e := gbkToUTF8(body); e == nil {
		body = utf8
	}
	return parseDayMinuteResponse(body, qtCode, code, days)
}

// GetKLineQQ 从腾讯多日分时接口合成日 K 线（近 5 日）
func (s *StockService) GetKLineQQ(code string, limit int) (*KLineResponse, error) {
	days := limit
	if days > 5 {
		days = 5
	}
	results, err := s.GetDayMinuteData(code, days)
	if err != nil {
		return nil, err
	}

	klines := make([]KLine, 0, len(results))
	dates := make([]string, 0, len(results))
	ohlcData := make([][4]float64, 0, len(results))
	volumeData := make([][]any, 0, len(results))

	for i, day := range results {
		if len(day.Bars) == 0 {
			continue
		}
		open := day.Bars[0].Price
		closeP := day.Bars[len(day.Bars)-1].Price
		high, low := open, open
		for _, b := range day.Bars {
			if b.Price > high {
				high = b.Price
			}
			if b.Price < low {
				low = b.Price
			}
		}
		lastBar := day.Bars[len(day.Bars)-1]
		dateStr := formatQQDate(day.Date)

		k := KLine{
			Date:   dateStr,
			Open:   open,
			Close:  closeP,
			High:   high,
			Low:    low,
			Volume: lastBar.CumVolume,
			Amount: lastBar.CumAmount / 10000,
		}
		klines = append(klines, k)
		dates = append(dates, dateStr)
		ohlcData = append(ohlcData, k.ToECharts())

		dir := 1
		if closeP < open {
			dir = -1
		} else if closeP == open {
			dir = 0
		}
		volumeData = append(volumeData, []any{i, lastBar.CumVolume, dir})
	}

	name := code
	if len(results) > 0 {
		name = results[0].Name
	}
	return &KLineResponse{
		Code: code, Name: name, Period: "daily",
		KLines: klines, Dates: dates,
		OHLCData: ohlcData, VolumeData: volumeData,
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// 解析函数（body 已经是 UTF-8）
// ─────────────────────────────────────────────────────────────────

func parseMinuteResponse(body []byte, qtCode, code string) (*MinuteResponse, error) {
	raw := string(body)
	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return nil, fmt.Errorf("parseMinuteResponse: no JSON")
	}

	var resp struct {
		Code int                        `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw[jsonStart:]), &resp); err != nil {
		return nil, fmt.Errorf("parseMinuteResponse unmarshal: %w", err)
	}

	stockRaw, ok := resp.Data[qtCode]
	if !ok {
		return nil, fmt.Errorf("parseMinuteResponse: %s not found", qtCode)
	}

	var stockData struct {
		Data struct {
			Data []string `json:"data"`
			Date string   `json:"date"`
		} `json:"data"`
		Qt map[string]json.RawMessage `json:"qt"`
	}
	if err := json.Unmarshal(stockRaw, &stockData); err != nil {
		return nil, fmt.Errorf("parseMinuteResponse stock unmarshal: %w", err)
	}

	name, preClose := extractQTNameAndPreClose(stockData.Qt, qtCode)
	bars, times, prices, volumes, amounts := parseMinuteBars(stockData.Data.Data)

	return &MinuteResponse{
		Code: code, Name: name, Date: stockData.Data.Date,
		PreClose: preClose, Bars: bars,
		Times: times, Prices: prices, Volumes: volumes, Amounts: amounts,
	}, nil
}

func parseDayMinuteResponse(body []byte, qtCode, code string, days int) ([]*MinuteResponse, error) {
	raw := string(body)
	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return nil, fmt.Errorf("parseDayMinuteResponse: no JSON")
	}

	var resp struct {
		Code int                        `json:"code"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw[jsonStart:]), &resp); err != nil {
		return nil, fmt.Errorf("parseDayMinuteResponse unmarshal: %w", err)
	}

	stockRaw, ok := resp.Data[qtCode]
	if !ok {
		return nil, fmt.Errorf("parseDayMinuteResponse: %s not found", qtCode)
	}

	var stockData struct {
		Data []struct {
			Date string   `json:"date"`
			Data []string `json:"data"`
			Prec string   `json:"prec"`
		} `json:"data"`
		Qt map[string]json.RawMessage `json:"qt"`
	}
	if err := json.Unmarshal(stockRaw, &stockData); err != nil {
		return nil, fmt.Errorf("parseDayMinuteResponse stock unmarshal: %w", err)
	}

	name, _ := extractQTNameAndPreClose(stockData.Qt, qtCode)

	entries := stockData.Data
	if days < len(entries) {
		entries = entries[:days]
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}

	results := make([]*MinuteResponse, 0, len(entries))
	for _, entry := range entries {
		preClose, _ := strconv.ParseFloat(entry.Prec, 64)
		bars, times, prices, volumes, amounts := parseMinuteBars(entry.Data)
		results = append(results, &MinuteResponse{
			Code: code, Name: name, Date: entry.Date,
			PreClose: preClose, Bars: bars,
			Times: times, Prices: prices, Volumes: volumes, Amounts: amounts,
		})
	}
	return results, nil
}

func parseMinuteBars(rawLines []string) (
	bars []MinuteBar,
	times []string,
	prices []float64,
	volumes [][]any,
	amounts []float64,
) {
	bars = make([]MinuteBar, 0, len(rawLines))
	times = make([]string, 0, len(rawLines))
	prices = make([]float64, 0, len(rawLines))
	volumes = make([][]any, 0, len(rawLines))
	amounts = make([]float64, 0, len(rawLines))

	var prevCumVol int64
	var prevCumAmt float64

	for i, line := range rawLines {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		price, _ := strconv.ParseFloat(parts[1], 64)
		cumVol, _ := strconv.ParseInt(parts[2], 10, 64)
		cumAmt, _ := strconv.ParseFloat(parts[3], 64)

		vol := cumVol - prevCumVol
		amt := cumAmt - prevCumAmt
		prevCumVol = cumVol
		prevCumAmt = cumAmt

		t := formatQQTime(parts[0])
		avgPrice := 0.0
		if cumVol > 0 {
			avgPrice = cumAmt / float64(cumVol) / 100
		}

		bars = append(bars, MinuteBar{
			Time: t, Price: price, Volume: vol, Amount: amt,
			AvgPrice: avgPrice, CumVolume: cumVol, CumAmount: cumAmt,
		})
		times = append(times, t)
		prices = append(prices, price)

		dir := 1
		if vol == 0 {
			dir = 0
		}
		volumes = append(volumes, []any{i, vol, dir})
		amounts = append(amounts, amt)
	}
	return
}

func extractQTNameAndPreClose(qt map[string]json.RawMessage, qtCode string) (name string, preClose float64) {
	if qt == nil {
		return qtCode, 0
	}
	raw, ok := qt[qtCode]
	if !ok {
		return qtCode, 0
	}
	var fields []string
	if err := json.Unmarshal(raw, &fields); err != nil {
		return qtCode, 0
	}
	if len(fields) > 1 {
		name = fields[1]
	}
	if len(fields) > 4 {
		preClose, _ = strconv.ParseFloat(fields[4], 64)
	}
	return
}

func formatQQTime(s string) string {
	if len(s) == 4 {
		return s[:2] + ":" + s[2:]
	}
	return s
}

func formatQQDate(s string) string {
	if len(s) == 8 {
		return s[:4] + "-" + s[4:6] + "-" + s[6:]
	}
	return s
}

func randStr() string {
	return strconv.FormatFloat(rand.Float64(), 'f', -1, 64)
}
