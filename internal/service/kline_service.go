package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// kline_service.go — K 线数据服务
//
// 主力数据源：腾讯证券复权 K 线
//   接口：https://web.ifzq.gtimg.cn/appstock/app/fqkline/get
//   param 格式：{qtCode},day,0,{endDate},{limit},qfq
//
// qfqday 实测字段（6列）：
//   [0] 日期       "2026-03-18"
//   [1] 收盘价     "54.210"
//   [2] 开盘价     "54.430"
//   [3] 最高价     "54.880"
//   [4] 最低价     "53.310"
//   [5] 成交量     "183527.000"（手）
//   注意：没有成交额字段（amount 设为 0）
// ═══════════════════════════════════════════════════════════════

type KLine struct {
	Date   string  `json:"date"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	Low    float64 `json:"low"`
	High   float64 `json:"high"`
	Volume int64   `json:"volume"` // 手
	Amount float64 `json:"amount"` // 万元（腾讯接口无此字段，固定为0）
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
	qqFqKLineURL     = "https://web.ifzq.gtimg.cn/appstock/app/fqkline/get"
	qqFqKLineTimeout = 15 * time.Second
)

func (s *StockService) GetKLine(code string, limit int) (*KLineResponse, error) {
	return s.GetKLineEndAt(code, time.Now(), limit)
}

func (s *StockService) GetKLineEndAt(code string, end time.Time, limit int) (*KLineResponse, error) {
	if limit <= 0 || limit > 500 {
		limit = 120
	}

	qtCode := toQTCode(code)
	endDateStr := end.Format("2006-01-02")
	param := fmt.Sprintf("%s,day,0,%s,%d,qfq", qtCode, endDateStr, limit)
	url := fmt.Sprintf("%s?param=%s", qqFqKLineURL, param)

	body, err := fetchQQHTTP(context.Background(), url)
	if err != nil {
		return nil, fmt.Errorf("kline fetch qq(%s): %w", code, err)
	}

	// GBK → UTF-8（腾讯接口统一 GBK 编码）
	if utf8, e := gbkToUTF8(body); e == nil {
		body = utf8
	}

	result, err := parseQQFqKLine(body, code, qtCode)
	if err != nil {
		s.log.Warn("kline: qq parse failed", zap.String("code", code), zap.Error(err))
		return nil, err
	}

	s.log.Sugar().Debugw("kline qq: fetched", "code", code, "bars", len(result.KLines))
	return result, nil
}

// ─────────────────────────────────────────────────────────────────
// parseQQFqKLine — 解析腾讯复权 K 线（body 已转为 UTF-8）
//
// 腾讯 qfqday 实测格式（6字段）：
//   ["2026-03-18", "54.210", "54.430", "54.880", "53.310", "183527.000"]
//    [0]日期        [1]收盘    [2]开盘    [3]最高    [4]最低    [5]成交量(手)
// ─────────────────────────────────────────────────────────────────

func parseQQFqKLine(body []byte, origCode, qtCode string) (*KLineResponse, error) {
	raw := string(body)

	jsonStart := strings.Index(raw, "{")
	if jsonStart < 0 {
		return nil, fmt.Errorf("parseQQFqKLine: no JSON found in response")
	}
	raw = raw[jsonStart:]

	var resp struct {
		Code int                                   `json:"code"`
		Data map[string]map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		return nil, fmt.Errorf("parseQQFqKLine unmarshal: %w", err)
	}

	// 大小写不敏感匹配 qtCode（如 sh603920）
	var codeData map[string]json.RawMessage
	for k, v := range resp.Data {
		if strings.EqualFold(k, qtCode) {
			codeData = v
			break
		}
	}
	if codeData == nil {
		return nil, fmt.Errorf("parseQQFqKLine: code %s not found in data", qtCode)
	}

	// 优先用前复权 qfqday，没有则用不复权 day
	klineRaw, exists := codeData["qfqday"]
	if !exists {
		klineRaw, exists = codeData["day"]
		if !exists {
			return nil, fmt.Errorf("parseQQFqKLine: no qfqday/day field for %s", qtCode)
		}
	}

	rawLines, err := parseKLineRows(klineRaw)
	if err != nil {
		return nil, fmt.Errorf("parseQQFqKLine rows: %w", err)
	}

	// 从 qt 字段提取股票名（Unicode 转义已被 JSON 解析自动处理）
	name := origCode
	if qtRaw, ok := codeData["qt"]; ok {
		var qtMap map[string][]string
		if jsonErr := json.Unmarshal(qtRaw, &qtMap); jsonErr == nil {
			if fields, ok2 := qtMap[qtCode]; ok2 && len(fields) > 1 {
				name = fields[1]
			}
		}
	}

	klines := make([]KLine, 0, len(rawLines))
	dates := make([]string, 0, len(rawLines))
	ohlcData := make([][4]float64, 0, len(rawLines))
	volumeData := make([][]any, 0, len(rawLines))

	for i, row := range rawLines {
		// 最少需要 6 个字段：日期、收盘、开盘、最高、最低、成交量
		if len(row) < 6 {
			continue
		}

		date := row[0]
		closeP := parseF(row[1]) // [1] 收盘
		open := parseF(row[2])   // [2] 开盘
		high := parseF(row[3])   // [3] 最高
		low := parseF(row[4])    // [4] 最低
		volume := parseI(row[5]) // [5] 成交量（手）

		// 成交额：腾讯接口无此字段，保留为 0（不影响技术指标计算）
		amount := 0.0
		if len(row) > 6 {
			amount = parseF(row[6])
		}

		k := KLine{
			Date: date, Open: open, Close: closeP,
			High: high, Low: low,
			Volume: volume, Amount: amount,
		}
		klines = append(klines, k)
		dates = append(dates, date)
		ohlcData = append(ohlcData, k.ToECharts())

		dir := 1
		if closeP < open {
			dir = -1
		} else if closeP == open {
			dir = 0
		}
		volumeData = append(volumeData, []any{i, volume, dir})
	}

	if len(klines) == 0 && len(rawLines) > 0 {
		// 有原始数据但解析后为空，打印第一条辅助调试
		return nil, fmt.Errorf("parseQQFqKLine: all %d rows skipped (first row has %d fields: %v)",
			len(rawLines), len(rawLines[0]), rawLines[0])
	}

	return &KLineResponse{
		Code: origCode, Name: name, Period: "daily",
		KLines: klines, Dates: dates,
		OHLCData: ohlcData, VolumeData: volumeData,
	}, nil
}

// parseKLineRows 处理腾讯 K 线行的两种 JSON 格式：
//   - [][]string（全字符串，常见）
//   - [][]interface{}（混合类型，偶发）
func parseKLineRows(raw json.RawMessage) ([][]string, error) {
	// 先尝试全字符串格式
	var strRows [][]string
	if err := json.Unmarshal(raw, &strRows); err == nil {
		return strRows, nil
	}
	// 退而求其次尝试混合格式
	var anyRows [][]interface{}
	if err := json.Unmarshal(raw, &anyRows); err != nil {
		return nil, fmt.Errorf("cannot parse kline rows: %w", err)
	}
	result := make([][]string, len(anyRows))
	for i, row := range anyRows {
		result[i] = make([]string, len(row))
		for j, v := range row {
			result[i][j] = fmt.Sprintf("%v", v)
		}
	}
	return result, nil
}
