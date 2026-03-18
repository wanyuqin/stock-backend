package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// qq_quote_fetcher.go — 腾讯 qt.gtimg.cn 实时行情 + 资金流向
//
// 接口：https://qt.gtimg.cn/q=sh603920
// 返回：~分隔的字符串，共 80+ 个字段
//
// 资金流向相关字段（下标从 0 开始）：
//   [7]  外盘（主动买入量，手）= 主力流入代理
//   [8]  内盘（主动卖出量，手）= 主力流出代理
//   [3]  当前价格
//   [31] 涨跌额
//   [32] 涨跌幅(%)
//   [36] 总成交量（手）
//   [37] 成交额（万元）
//   [38] 换手率(%)
//   [49] 量比
//
// 盘口大单比例接口：https://qt.gtimg.cn/q=s_pksh603920
//   [0]  买盘大单比例
//   [1]  买盘小单比例
//   [2]  卖盘大单比例
//   [3]  卖盘小单比例
// ═══════════════════════════════════════════════════════════════

const (
	qtBaseURL    = "https://qt.gtimg.cn/q=%s"
	qtPKURL      = "https://qt.gtimg.cn/q=s_pk%s" // 盘口大单比例
	qtReferer    = "https://gu.qq.com/"
	qtUserAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	qtCacheTTL   = 5 * time.Second
	qtTimeout    = 8 * time.Second
)

// QTRealtimeFlow 腾讯实时外盘/内盘资金流向
type QTRealtimeFlow struct {
	StockCode string `json:"stock_code"`
	StockName string `json:"stock_name"`

	// 核心资金流向（外盘/内盘，手）
	OuterVol int64   `json:"outer_vol"`   // 外盘（主动买入，手）
	InnerVol int64   `json:"inner_vol"`   // 内盘（主动卖出，手）
	NetVol   int64   `json:"net_vol"`     // 净流入量（手）= 外盘 - 内盘
	NetPct   float64 `json:"net_pct"`     // 净流入占总成交量比例(%)

	// 换算为金额（元）= 量(手) × 100 × 当前价
	OuterAmt float64 `json:"outer_amt"`   // 外盘金额（元）
	InnerAmt float64 `json:"inner_amt"`   // 内盘金额（元）
	NetAmt   float64 `json:"net_amt"`     // 净流入金额（元）

	// 行情基础数据
	Price      float64 `json:"price"`
	Change     float64 `json:"change"`
	ChangeRate float64 `json:"change_rate"` // 涨跌幅(%)
	Volume     int64   `json:"volume"`      // 总成交量（手）
	Amount     float64 `json:"amount"`      // 成交额（万元）
	Turnover   float64 `json:"turnover"`    // 换手率(%)
	VolumeRatio float64 `json:"volume_ratio"` // 量比

	// 盘口大单比例（来自 s_pk 接口）
	BigBuyPct  float64 `json:"big_buy_pct"`  // 买盘大单比例
	BigSellPct float64 `json:"big_sell_pct"` // 卖盘大单比例
	SmlBuyPct  float64 `json:"sml_buy_pct"`  // 买盘小单比例
	SmlSellPct float64 `json:"sml_sell_pct"` // 卖盘小单比例

	// 流向描述
	FlowDesc string `json:"flow_desc"` // 如 "外盘占比 55.3%，主力偏买"
	UpdatedAt string `json:"updated_at"`
}

// ─────────────────────────────────────────────────────────────────
// FetchQTRealtimeFlow 从腾讯 qt 接口获取实时外盘/内盘资金流向
// ─────────────────────────────────────────────────────────────────

func FetchQTRealtimeFlow(ctx context.Context, code string, log *zap.Logger) (*QTRealtimeFlow, error) {
	// 构建腾讯股票代码格式（sh603920 / sz000858）
	qtCode := toQTCode(code)

	// 并发拉取行情 + 盘口大单比例
	type result struct {
		body []byte
		err  error
	}

	qtCh := make(chan result, 1)
	pkCh := make(chan result, 1)

	go func() {
		url := fmt.Sprintf(qtBaseURL, qtCode)
		body, err := fetchQTRaw(ctx, url)
		qtCh <- result{body, err}
	}()

	go func() {
		url := fmt.Sprintf(qtPKURL, qtCode)
		body, err := fetchQTRaw(ctx, url)
		pkCh <- result{body, err}
	}()

	qtRes := <-qtCh
	pkRes := <-pkCh

	if qtRes.err != nil {
		return nil, fmt.Errorf("FetchQTRealtimeFlow qt(%s): %w", code, qtRes.err)
	}

	flow, err := parseQTQuote(qtRes.body, code)
	if err != nil {
		return nil, fmt.Errorf("FetchQTRealtimeFlow parse(%s): %w", code, err)
	}

	// 盘口大单数据（降级容忍：失败不影响主流程）
	if pkRes.err == nil {
		parsePKInto(pkRes.body, flow)
	} else if log != nil {
		log.Warn("FetchQTRealtimeFlow: s_pk failed, skip",
			zap.String("code", code), zap.Error(pkRes.err))
	}

	// 计算流向描述文案
	flow.FlowDesc = buildFlowDesc(flow)
	flow.UpdatedAt = time.Now().Format("15:04:05")

	return flow, nil
}

// ─────────────────────────────────────────────────────────────────
// parseQTQuote 解析 qt.gtimg.cn 返回的 ~分隔字符串
// ─────────────────────────────────────────────────────────────────

func parseQTQuote(body []byte, code string) (*QTRealtimeFlow, error) {
	raw := string(body)

	// 格式：v_sh603920="1~股票名~603920~price~...~";
	start := strings.Index(raw, `"`)
	end := strings.LastIndex(raw, `"`)
	if start < 0 || end <= start {
		return nil, fmt.Errorf("invalid qt response: %s", truncateBytes(body, 100))
	}
	content := raw[start+1 : end]
	fields := strings.Split(content, "~")

	if len(fields) < 50 {
		return nil, fmt.Errorf("qt response too short: %d fields", len(fields))
	}

	name := fields[1]
	price := parseQTFloat(fields[3])
	outerVol := parseQTInt(fields[7])  // 外盘（手）
	innerVol := parseQTInt(fields[8])  // 内盘（手）
	changeAmt := parseQTFloat(fields[31])
	changeRate := parseQTFloat(fields[32])
	totalVol := parseQTInt(fields[36])
	amount := parseQTFloat(fields[37])  // 万元
	turnover := parseQTFloat(fields[38])

	// 量比在 fields[49]，但有时为空
	volRatio := 0.0
	if len(fields) > 49 {
		volRatio = parseQTFloat(fields[49])
	}

	// 金额换算：量(手) × 100股 × 当前价
	outerAmt := float64(outerVol) * 100 * price
	innerAmt := float64(innerVol) * 100 * price
	netVol := outerVol - innerVol
	netAmt := outerAmt - innerAmt

	netPct := 0.0
	if totalVol > 0 {
		netPct = float64(netVol) / float64(totalVol) * 100
	}

	return &QTRealtimeFlow{
		StockCode:   code,
		StockName:   name,
		OuterVol:    outerVol,
		InnerVol:    innerVol,
		NetVol:      netVol,
		NetPct:      roundFloat(netPct, 2),
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

// parsePKInto 解析盘口大单比例，写入 flow
// 格式：v_s_pksh603920="买大~买小~卖大~卖小";
func parsePKInto(body []byte, flow *QTRealtimeFlow) {
	raw := string(body)
	start := strings.Index(raw, `"`)
	end := strings.LastIndex(raw, `"`)
	if start < 0 || end <= start {
		return
	}
	fields := strings.Split(raw[start+1:end], "~")
	if len(fields) < 4 {
		return
	}
	flow.BigBuyPct  = parseQTFloat(fields[0])
	flow.SmlBuyPct  = parseQTFloat(fields[1])
	flow.BigSellPct = parseQTFloat(fields[2])
	flow.SmlSellPct = parseQTFloat(fields[3])
}

// buildFlowDesc 生成可读的资金流向描述文案
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

	// 大单信号（如果有盘口数据）
	if f.BigBuyPct > 0 || f.BigSellPct > 0 {
		if f.BigBuyPct > f.BigSellPct*1.5 {
			desc += "，大单买盘强势"
		} else if f.BigSellPct > f.BigBuyPct*1.5 {
			desc += "，大单卖压偏重"
		}
	}

	return desc
}

// ─────────────────────────────────────────────────────────────────
// HTTP 工具（独立 http.Client，不走东财 Cookie）
// ─────────────────────────────────────────────────────────────────

func fetchQTRaw(ctx context.Context, url string) ([]byte, error) {
	// 复用 EMHTTPClient，但覆盖 Referer/UA 为腾讯
	client := GetEMHTTPClient()
	return client.FetchBody(ctx, url, &EMRequestOption{
		Timeout: qtTimeout,
		Headers: map[string]string{
			"Referer":    qtReferer,
			"User-Agent": qtUserAgent,
		},
		SkipCookie: true,
	})
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

// toQTCode 把纯数字代码转为腾讯格式（sh603920 / sz000858）
func toQTCode(code string) string {
	if strings.HasPrefix(code, "sh") || strings.HasPrefix(code, "sz") {
		return code
	}
	if strings.HasPrefix(code, "6") {
		return "sh" + code
	}
	return "sz" + code
}

func parseQTFloat(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

func parseQTInt(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return 0
	}
	// 可能是浮点数，先解析为 float 再转 int
	v, _ := strconv.ParseFloat(s, 64)
	return int64(v)
}

func roundFloat(v float64, decimals int) float64 {
	p := 1.0
	for i := 0; i < decimals; i++ {
		p *= 10
	}
	return float64(int(v*p+0.5)) / p
}
