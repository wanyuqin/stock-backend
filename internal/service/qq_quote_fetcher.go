package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// qq_quote_fetcher.go — 腾讯实时外盘/内盘资金流向
//
// 注意：toQTCode / fetchQQHTTP / parseF / parseI / gbkToUTF8
//       定义在 market_provider.go
//       qqQuoteURL 常量也定义在 market_provider.go
// ═══════════════════════════════════════════════════════════════

const (
	qtPKURL   = "https://qt.gtimg.cn/q=s_pk%s"
	qtTimeout = 8 * time.Second
)

// QTRealtimeFlow 腾讯实时外盘/内盘资金流向
type QTRealtimeFlow struct {
	StockCode string `json:"stock_code"`
	StockName string `json:"stock_name"`

	OuterVol int64   `json:"outer_vol"`
	InnerVol int64   `json:"inner_vol"`
	NetVol   int64   `json:"net_vol"`
	NetPct   float64 `json:"net_pct"`

	OuterAmt float64 `json:"outer_amt"`
	InnerAmt float64 `json:"inner_amt"`
	NetAmt   float64 `json:"net_amt"`

	Price       float64 `json:"price"`
	Change      float64 `json:"change"`
	ChangeRate  float64 `json:"change_rate"`
	Volume      int64   `json:"volume"`
	Amount      float64 `json:"amount"`
	Turnover    float64 `json:"turnover"`
	VolumeRatio float64 `json:"volume_ratio"`

	BigBuyPct  float64 `json:"big_buy_pct"`
	BigSellPct float64 `json:"big_sell_pct"`
	SmlBuyPct  float64 `json:"sml_buy_pct"`
	SmlSellPct float64 `json:"sml_sell_pct"`

	FlowDesc  string `json:"flow_desc"`
	UpdatedAt string `json:"updated_at"`
}

// FetchQTRealtimeFlow 从腾讯 qt 接口获取实时外盘/内盘资金流向
func FetchQTRealtimeFlow(ctx context.Context, code string, log *zap.Logger) (*QTRealtimeFlow, error) {
	qtCode := toQTCode(code)

	type fetchResult struct {
		body []byte
		err  error
	}

	qtCh := make(chan fetchResult, 1)
	pkCh := make(chan fetchResult, 1)

	go func() {
		url := fmt.Sprintf(qqQuoteURL, qtCode)
		body, err := fetchQQHTTP(ctx, url)
		qtCh <- fetchResult{body, err}
	}()

	go func() {
		url := fmt.Sprintf(qtPKURL, qtCode)
		body, err := fetchQQHTTP(ctx, url)
		pkCh <- fetchResult{body, err}
	}()

	qtRes := <-qtCh
	pkRes := <-pkCh

	if qtRes.err != nil {
		return nil, fmt.Errorf("FetchQTRealtimeFlow qt(%s): %w", code, qtRes.err)
	}

	flow, err := parseQTFlowFromLines(qtRes.body, code)
	if err != nil {
		return nil, fmt.Errorf("FetchQTRealtimeFlow parse(%s): %w", code, err)
	}

	if pkRes.err == nil {
		parsePKInto(pkRes.body, flow)
	} else if log != nil {
		log.Warn("FetchQTRealtimeFlow: s_pk failed", zap.String("code", code), zap.Error(pkRes.err))
	}

	flow.FlowDesc = buildFlowDesc(flow)
	flow.UpdatedAt = time.Now().Format("15:04:05")
	return flow, nil
}

// parseQTFlowFromLines 从腾讯行情原始响应（GBK）解析外盘/内盘
func parseQTFlowFromLines(body []byte, code string) (*QTRealtimeFlow, error) {
	// GBK → UTF-8
	utf8Body, err := gbkToUTF8(body)
	if err != nil {
		utf8Body = body
	}

	raw := string(utf8Body)
	qtCode := toQTCode(code)

	var fields []string
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, qtCode) {
			continue
		}
		start := strings.Index(line, `"`)
		end := strings.LastIndex(line, `"`)
		if start < 0 || end <= start {
			continue
		}
		fields = strings.Split(line[start+1:end], "~")
		break
	}

	if len(fields) < 38 {
		return nil, fmt.Errorf("parseQTFlowFromLines: fields too short (%d) for %s", len(fields), code)
	}

	name := fields[1]
	price := parseF(fields[3])
	outerVol := parseI(fields[7])
	innerVol := parseI(fields[8])
	changeAmt := parseF(fields[31])
	changeRate := parseF(fields[32])
	totalVol := parseI(fields[6])
	amount := parseF(fields[37])
	turnover := parseF(fields[38])

	volRatio := 0.0
	if len(fields) > 49 {
		volRatio = parseF(fields[49])
	}

	outerAmt := float64(outerVol) * 100 * price
	innerAmt := float64(innerVol) * 100 * price
	netVol := outerVol - innerVol
	netAmt := outerAmt - innerAmt

	netPct := 0.0
	if totalVol > 0 {
		netPct = roundFloat(float64(netVol)/float64(totalVol)*100, 2)
	}

	return &QTRealtimeFlow{
		StockCode:   code,
		StockName:   name,
		OuterVol:    outerVol,
		InnerVol:    innerVol,
		NetVol:      netVol,
		NetPct:      netPct,
		OuterAmt:    outerAmt,
		InnerAmt:    innerAmt,
		NetAmt:      netAmt,
		Price:       price,
		Change:      changeAmt,
		ChangeRate:  changeRate,
		Volume:      totalVol,
		Amount:      amount,
		Turnover:    turnover,
		VolumeRatio: volRatio,
	}, nil
}

// parsePKInto 解析盘口大单比例（GBK → UTF-8 后解析）
func parsePKInto(body []byte, flow *QTRealtimeFlow) {
	utf8Body, err := gbkToUTF8(body)
	if err != nil {
		utf8Body = body
	}
	raw := string(utf8Body)
	start := strings.Index(raw, `"`)
	end := strings.LastIndex(raw, `"`)
	if start < 0 || end <= start {
		return
	}
	fields := strings.Split(raw[start+1:end], "~")
	if len(fields) < 4 {
		return
	}
	flow.BigBuyPct  = parseF(fields[0])
	flow.SmlBuyPct  = parseF(fields[1])
	flow.BigSellPct = parseF(fields[2])
	flow.SmlSellPct = parseF(fields[3])
}

func buildFlowDesc(f *QTRealtimeFlow) string {
	total := f.OuterVol + f.InnerVol
	if total == 0 {
		return "暂无成交数据"
	}
	outerPct := float64(f.OuterVol) / float64(total) * 100
	netWan := f.NetAmt / 10000

	direction := "主力偏买"
	if f.NetVol < 0 {
		direction = "主力偏卖"
	}
	if outerPct >= 45 && outerPct <= 55 {
		direction = "买卖均衡"
	}

	desc := fmt.Sprintf("外盘占比 %.1f%%，%s", outerPct, direction)
	if netWan != 0 {
		sign := "+"
		if netWan < 0 {
			sign = ""
		}
		desc += fmt.Sprintf("，净流入 %s%.0f 万", sign, netWan)
	}

	if f.BigBuyPct > 0 || f.BigSellPct > 0 {
		if f.BigBuyPct > f.BigSellPct*1.5 {
			desc += "，大单买盘强势"
		} else if f.BigSellPct > f.BigBuyPct*1.5 {
			desc += "，大单卖压偏重"
		}
	}
	return desc
}

func roundFloat(v float64, decimals int) float64 {
	p := 1.0
	for i := 0; i < decimals; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
