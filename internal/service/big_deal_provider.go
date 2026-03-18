package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ═══════════════════════════════════════════════════════════════
// big_deal_provider.go — 大单逐笔数据抓取与分析
//
// 数据源：腾讯证券 getDadan CGI
//   https://gu.qq.com/proxy/cgi/cgi-bin/yidong/getDadan
//     ?code=sh603920&need=&start=&v=<随机数>
//
// 设计原则：
//   1. 通过 BigDealFetcher 接口隔离数据源，方便后续切换为东财等其他源
//   2. 腾讯接口独立使用 http.Client（不走东财的 Cookie 体系）
//   3. 按需拉取，严格限速，不自动轮询
//
// 字段映射（summary.data.cje100，逗号分隔）：
//   [0] 大单档位代码（10=所有大单）
//   [1] 大单成交总手数
//   [2] 大单买入金额（万元）
//   [3] 大单卖出金额（万元）
//   [4] 大单净买入金额（万元）= [2]-[3]
//   [5] 大单买入金额（另一口径）
//   [6] 大单卖出金额（另一口径）
//
// detail 每行格式：[时间, 价格, 成交量(手), 方向(B/S)]
//   大单门槛（腾讯定义）：成交量 ≥ 6万股 或 成交额 ≥ 20万元
// ═══════════════════════════════════════════════════════════════

// ─────────────────────────────────────────────────────────────────
// 分类阈值（以成交额元为单位，与腾讯 dadan 页面口径一致）
// ─────────────────────────────────────────────────────────────────

const (
	threshSmall  = 200_000  // < 20万 = 小单
	threshMedium = 500_000  // 20万~50万 = 中单
	threshLarge  = 2_000_000 // 50万~200万 = 大单
	// >= 200万 = 特大单（超大单）

	bigDealCacheTTL    = 30 * time.Second // 大单数据缓存30秒
	bigDealHTTPTimeout = 10 * time.Second

	qqDadanURL = "https://gu.qq.com/proxy/cgi/cgi-bin/yidong/getDadan"
	qqReferer  = "https://gu.qq.com/"
)

// ─────────────────────────────────────────────────────────────────
// 核心数据类型
// ─────────────────────────────────────────────────────────────────

// TickSize 单笔成交量级别
type TickSize string

const (
	TickSizeSmall  TickSize = "small"   // 小单  < 20万
	TickSizeMedium TickSize = "medium"  // 中单  20万~50万
	TickSizeLarge  TickSize = "large"   // 大单  50万~200万
	TickSizeSuper  TickSize = "super"   // 特大单 >= 200万
)

// TickData 单笔逐笔成交记录
type TickData struct {
	Time      string   `json:"time"`       // 成交时间，如 "14:56:48"
	Price     float64  `json:"price"`      // 成交价格
	Volume    int64    `json:"volume"`     // 成交量（手）
	Amount    float64  `json:"amount"`     // 成交额（元）= price * volume * 100
	Direction string   `json:"direction"`  // B=主买 S=主卖 （腾讯原始字段）
	Size      TickSize `json:"size"`       // 分类：small/medium/large/super
}

// BigDealSummary 大单统计汇总
type BigDealSummary struct {
	Date          string  `json:"date"`           // 交易日期 20060317
	Time          string  `json:"time"`           // 最新数据时间 161500
	Desc          string  `json:"desc"`           // 大单定义说明
	TotalVolume   float64 `json:"total_volume"`   // 当日总成交量（手）

	// 逐笔明细（已按量级分类）
	Ticks []TickData `json:"ticks"`

	// 主力统计（大单+特大单合并）
	MainBuyAmount  float64 `json:"main_buy_amount"`  // 主力买入金额（元）
	MainSellAmount float64 `json:"main_sell_amount"` // 主力卖出金额（元）
	MainNetFlow    float64 `json:"main_net_flow"`    // 主力净流入（元）= 买入-卖出

	// 按量级分组统计
	Stats map[TickSize]*TickSizeStat `json:"stats"`

	// 散户/主力成交额占比（用于饼图）
	MainFlowPct   float64 `json:"main_flow_pct"`   // 主力成交额占比 0~100
	RetailFlowPct float64 `json:"retail_flow_pct"` // 散户成交额占比 0~100

	// ── AnalysisEngine 三维度扩展 ────────────────────────────────

	// 1. 主力今日建仓成本（特大单+大单的加权平均成交价）
	MainAvgCost     float64 `json:"main_avg_cost"`      // 元/股（0 = 无大单数据）
	MainAvgCostDesc string  `json:"main_avg_cost_desc"` // 文案，如 "主力今日均价 ¥52.85"

	// 2. 大单频率异动检测（近1小时 vs 全天均频）
	SurgeSignal     bool    `json:"surge_signal"`      // 频率翻倍 = true
	SurgeMultiplier float64 `json:"surge_multiplier"`  // 倍数，如 2.3
	SurgeDesc       string  `json:"surge_desc"`        // 文案，如 "主力扫货显著加速 2.3×"

	// 3. 特大单净流入+价格集中区间 综合结论
	InsightDesc string `json:"insight_desc"` // 综合多维度文案

	// 风险雷达信号
	WashingSignal     bool   `json:"washing_signal"`      // 疑似主力洗盘吸筹
	WashingSignalDesc string `json:"washing_signal_desc"` // 信号说明文案
}

// TickSizeStat 单个量级的统计
type TickSizeStat struct {
	Count      int     `json:"count"`       // 笔数
	BuyCount   int     `json:"buy_count"`
	SellCount  int     `json:"sell_count"`
	BuyAmount  float64 `json:"buy_amount"`  // 元
	SellAmount float64 `json:"sell_amount"` // 元
	NetFlow    float64 `json:"net_flow"`    // 净流入
}

// ─────────────────────────────────────────────────────────────────
// BigDealFetcher 接口（数据源隔离）
// ─────────────────────────────────────────────────────────────────

// BigDealFetcher 定义大单数据获取的抽象接口
// 当前实现：腾讯证券 getDadan
// 未来可替换为：东财逐笔接口、自建 Level-2 数据等
type BigDealFetcher interface {
	// FetchBigDeal 按需拉取指定股票的大单数据
	// code 格式：sh603920 / sz000858
	FetchBigDeal(ctx context.Context, code string) (*BigDealSummary, error)
}

// ─────────────────────────────────────────────────────────────────
// QQDadanFetcher — 腾讯证券大单数据源实现
// ─────────────────────────────────────────────────────────────────

type QQDadanFetcher struct {
	client *http.Client
	cache  *gocache.Cache
	log    *zap.Logger
}

// NewQQDadanFetcher 创建腾讯大单数据源（独立 http.Client，不依赖东财 Cookie）
func NewQQDadanFetcher(log *zap.Logger) BigDealFetcher {
	return &QQDadanFetcher{
		client: &http.Client{
			Timeout: bigDealHTTPTimeout,
		},
		cache: gocache.New(bigDealCacheTTL, 2*time.Minute),
		log:   log,
	}
}

func (f *QQDadanFetcher) FetchBigDeal(ctx context.Context, code string) (*BigDealSummary, error) {
	cacheKey := "bd:" + code
	if cached, found := f.cache.Get(cacheKey); found {
		return cached.(*BigDealSummary), nil
	}

	body, err := f.fetch(ctx, code)
	if err != nil {
		return nil, err
	}

	summary, err := parseQQDadan(body)
	if err != nil {
		return nil, fmt.Errorf("parse getDadan(%s): %w", code, err)
	}

	// 计算分类统计和信号
	enrichSummary(summary)

	f.cache.Set(cacheKey, summary, bigDealCacheTTL)
	return summary, nil
}

// fetch 向腾讯 CGI 发起请求，必须带 Referer 头
func (f *QQDadanFetcher) fetch(ctx context.Context, code string) ([]byte, error) {
	// v 参数：腾讯要求传一个随机字符串，用时间戳即可
	v := fmt.Sprintf("%d", time.Now().UnixNano()%1e15)
	url := fmt.Sprintf("%s?code=%s&need=&start=&v=%s", qqDadanURL, code, v)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Referer", qqReferer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get getDadan(%s): %w", code, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getDadan(%s) status %d", code, resp.StatusCode)
	}

	var buf strings.Builder
	buf.Grow(64 * 1024)
	tmp := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
		}
		if readErr != nil {
			break
		}
	}
	return []byte(buf.String()), nil
}

// ─────────────────────────────────────────────────────────────────
// JSON 解析
// ─────────────────────────────────────────────────────────────────

// qqDadanResp 腾讯 getDadan 接口原始响应
type qqDadanResp struct {
	Code int `json:"code"`
	Data *struct {
		Summary *struct {
			Date   string `json:"date"`
			Time   string `json:"time"`
			Volume string `json:"volume"`
			Desc   string `json:"desc"`
			Data   *struct {
				Cje100 string `json:"cje100"` // 大单汇总字段
			} `json:"data"`
		} `json:"summary"`
		Detail [][]string `json:"detail"` // [时间, 价格, 成交量, 方向]
	} `json:"data"`
}

func parseQQDadan(raw []byte) (*BigDealSummary, error) {
	var resp qqDadanResp
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if resp.Code != 0 {
		return nil, fmt.Errorf("api code=%d", resp.Code)
	}
	if resp.Data == nil || resp.Data.Summary == nil {
		return nil, fmt.Errorf("empty data")
	}

	s := resp.Data.Summary
	totalVol, _ := strconv.ParseFloat(strings.TrimSpace(s.Volume), 64)

	summary := &BigDealSummary{
		Date:        s.Date,
		Time:        s.Time,
		Desc:        s.Desc,
		TotalVolume: totalVol,
	}

	// 解析逐笔明细
	ticks := make([]TickData, 0, len(resp.Data.Detail))
	for _, row := range resp.Data.Detail {
		if len(row) < 4 {
			continue
		}
		price, err1 := strconv.ParseFloat(strings.TrimSpace(row[1]), 64)
		vol, err2 := strconv.ParseInt(strings.TrimSpace(row[2]), 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		dir := strings.TrimSpace(row[3]) // B or S
		amount := price * float64(vol) * 100 // 1手=100股

		ticks = append(ticks, TickData{
			Time:      strings.TrimSpace(row[0]),
			Price:     price,
			Volume:    vol,
			Amount:    amount,
			Direction: dir,
			Size:      classifyTick(amount),
		})
	}
	summary.Ticks = ticks
	return summary, nil
}

// classifyTick 按成交额划分量级
func classifyTick(amount float64) TickSize {
	switch {
	case amount >= threshLarge:
		return TickSizeSuper
	case amount >= threshMedium:
		return TickSizeLarge
	case amount >= threshSmall:
		return TickSizeMedium
	default:
		return TickSizeSmall
	}
}

// ─────────────────────────────────────────────────────────────────
// MainForceNetFlow — 计算主力净流入（大单+特大单）
// ─────────────────────────────────────────────────────────────────

// enrichSummary 填充统计字段、占比、洗盘信号以及 Analysis Engine 三维度分析
func enrichSummary(s *BigDealSummary) {
	// 初始化各量级统计
	stats := map[TickSize]*TickSizeStat{
		TickSizeSmall:  {},
		TickSizeMedium: {},
		TickSizeLarge:  {},
		TickSizeSuper:  {},
	}

	var totalAmount float64
	for i := range s.Ticks {
		t := &s.Ticks[i]
		st := stats[t.Size]
		st.Count++
		totalAmount += t.Amount
		if t.Direction == "B" {
			st.BuyCount++
			st.BuyAmount += t.Amount
		} else if t.Direction == "S" {
			st.SellCount++
			st.SellAmount += t.Amount
		}
		st.NetFlow = st.BuyAmount - st.SellAmount
	}
	s.Stats = stats

	// 主力 = 大单 + 特大单
	mainBuy := stats[TickSizeLarge].BuyAmount + stats[TickSizeSuper].BuyAmount
	mainSell := stats[TickSizeLarge].SellAmount + stats[TickSizeSuper].SellAmount
	s.MainBuyAmount = mainBuy
	s.MainSellAmount = mainSell
	s.MainNetFlow = mainBuy - mainSell

	// 散户/主力成交额占比（用于饼图）
	mainTotal := mainBuy + mainSell
	retailTotal := totalAmount - mainTotal
	if totalAmount > 0 {
		s.MainFlowPct = math.Round(mainTotal/totalAmount*100*10) / 10
		s.RetailFlowPct = math.Round(retailTotal/totalAmount*100*10) / 10
	}

	// 洗盘信号
	mainBuyPct := 0.0
	if mainTotal > 0 {
		mainBuyPct = mainBuy / mainTotal * 100
	}
	s.WashingSignal = mainBuyPct > 60 && s.MainNetFlow > 0
	if s.WashingSignal {
		s.WashingSignalDesc = fmt.Sprintf(
			"主力买入占比 %.1f%%，净买入 %.0f 万元，结合价格走势判断是否洗盘吸筹",
			mainBuyPct, s.MainNetFlow/10000,
		)
	}

	// ─────────────────────────────────────────────────────────────────
	// AnalysisEngine: 维度 1 —— 主力建仓加权平均成本
	// 公式：加权均价 = sum(price_i * amount_i) / sum(amount_i)
	// 只统计特大单+大单中主动买入（Direction==B）的成交，代表主力建仓成本
	// ─────────────────────────────────────────────────────────────────
	calcMainAvgCost(s)

	// ─────────────────────────────────────────────────────────────────
	// AnalysisEngine: 维度 2 —— 大单频率异动检测
	// 过去 1 小时 vs 全天均频，频率翻倍 = 奇异动
	// ─────────────────────────────────────────────────────────────────
	calcSurgeSignal(s)

	// ─────────────────────────────────────────────────────────────────
	// AnalysisEngine: 维度 3 —— 特大单净流入 + 价格集中区间综合结论
	// ─────────────────────────────────────────────────────────────────
	calcInsightDesc(s)
}

// calcMainAvgCost 计算主力建仓加权平均成本
func calcMainAvgCost(s *BigDealSummary) {
	// 只取特大单+大单中的主动买入（B）一方作为建仓依据
	// 加权 = 成交额（amount），公式：均价 = sum(price*amount) / sum(amount)
	var weightedSum, totalAmt float64
	for _, t := range s.Ticks {
		if (t.Size == TickSizeLarge || t.Size == TickSizeSuper) && t.Direction == "B" {
			weightedSum += t.Price * t.Amount
			totalAmt += t.Amount
		}
	}
	if totalAmt == 0 {
		s.MainAvgCost = 0
		s.MainAvgCostDesc = "今日暂无主力买入记录"
		return
	}
	avg := weightedSum / totalAmt
	s.MainAvgCost = math.Round(avg*100) / 100
	s.MainAvgCostDesc = fmt.Sprintf("主力今日均价 ¥%.2f（大单+特大单买入加权）", s.MainAvgCost)
}

// calcSurgeSignal 计算大单频率异动检测
func calcSurgeSignal(s *BigDealSummary) {
	if len(s.Ticks) == 0 || s.Time == "" {
		return
	}

	// 解析当前数据时间 —— time 格式 HHmmss
	endHour := 0
	if len(s.Time) >= 2 {
		endHour, _ = strconv.Atoi(s.Time[:2])
	}

	// 全天总笔数 ÷ 进行了多少小时 = 平均每小时笔数
	// 交易时间 9:30-15:00，共 5.5h。简化：小时数 = endHour - 9（不足则用全天）
	elapsedHours := float64(endHour - 9)
	if elapsedHours < 0.5 {
		elapsedHours = 0.5 // 最少按 0.5 小时计算，避免阻塔期过大
	}
	if elapsedHours > 5.5 {
		elapsedHours = 5.5
	}
	totalTicks := float64(len(s.Ticks))
	avgPerHour := totalTicks / elapsedHours

	// 计算这 1 小时内大单笔数
	// 时间格式 HH:MM:SS，取小时部分与 endHour 对比
	recentCount := 0
	for _, t := range s.Ticks {
		tickHour := 0
		if len(t.Time) >= 2 {
			tickHour, _ = strconv.Atoi(t.Time[:2])
		}
		if tickHour >= endHour-1 {
			recentCount++
		}
	}
	recentPerHour := float64(recentCount)

	// 频率翻倍检测：近 1h 频率 ≥ 全天均频 × 2.0
	const surgeThreshold = 2.0
	if avgPerHour <= 0 {
		return
	}
	multiplier := recentPerHour / avgPerHour
	if multiplier >= surgeThreshold {
		s.SurgeSignal = true
		s.SurgeMultiplier = math.Round(multiplier*10) / 10
		s.SurgeDesc = fmt.Sprintf(
			"主力扫货显著加速：近 1h 大单 %d 笔，是全天均频的 %.1f×",
			recentCount, s.SurgeMultiplier,
		)
	}
}

// calcInsightDesc 生成特大单净流入+价格集中区间综合结论
func calcInsightDesc(s *BigDealSummary) {
	superNetFlow := s.Stats[TickSizeSuper].NetFlow

	// 特大单不足时不输出结论
	if superNetFlow == 0 && s.Stats[TickSizeSuper].Count == 0 {
		s.InsightDesc = "今日暂无特大单数据"
		return
	}

	// 对特大单主动买入的价格做分位数，取 25%~75% 分位区间
	var superBuyPrices []float64
	for _, t := range s.Ticks {
		if t.Size == TickSizeSuper && t.Direction == "B" {
			superBuyPrices = append(superBuyPrices, t.Price)
		}
	}

	var priceRange string
	if len(superBuyPrices) >= 2 {
		minP, maxP := superBuyPrices[0], superBuyPrices[0]
		for _, p := range superBuyPrices[1:] {
			if p < minP {
				minP = p
			}
			if p > maxP {
				maxP = p
			}
		}
		// 取 25%~75% 分位区间（去掉尾部异常成交）
		q25 := minP + (maxP-minP)*0.25
		q75 := minP + (maxP-minP)*0.75
		priceRange = fmt.Sprintf("¥%.2f-%.2f", q25, q75)
	} else if len(superBuyPrices) == 1 {
		priceRange = fmt.Sprintf("¥%.2f附近", superBuyPrices[0])
	}

	// 生成结论文案
	netWan := superNetFlow / 10000
	if superNetFlow > 0 {
		if priceRange != "" {
			s.InsightDesc = fmt.Sprintf(
				"特大单净流入 %.0f 万，集中买入区间 %s，支撑力强劲",
				netWan, priceRange,
			)
		} else {
			s.InsightDesc = fmt.Sprintf("特大单净流入 %.0f 万，主力拉指意愁明显", netWan)
		}
	} else if superNetFlow < 0 {
		if priceRange != "" {
			s.InsightDesc = fmt.Sprintf(
				"特大单净流出 %.0f 万，集中卖出区间 %s，拉升动划谨慎",
				-netWan, priceRange,
			)
		} else {
			s.InsightDesc = fmt.Sprintf("特大单净流出 %.0f 万，主力出货意愿强烈", -netWan)
		}
	} else {
		s.InsightDesc = "特大单买卖均衡，方向不明确"
	}
}

// ─────────────────────────────────────────────────────────────────
// BigDealService — 对外服务层
// ─────────────────────────────────────────────────────────────────

// BigDealService 封装大单分析逻辑，持有可替换的数据源
type BigDealService struct {
	fetcher BigDealFetcher
	log     *zap.Logger
}

// NewBigDealService 创建大单服务，注入数据源
func NewBigDealService(fetcher BigDealFetcher, log *zap.Logger) *BigDealService {
	return &BigDealService{fetcher: fetcher, log: log}
}

// GetBigDeal 获取大单分析数据，附带股价涨跌用于洗盘判断
func (s *BigDealService) GetBigDeal(ctx context.Context, code string, priceChangeRate float64) (*BigDealSummary, error) {
	summary, err := s.fetcher.FetchBigDeal(ctx, code)
	if err != nil {
		s.log.Warn("BigDeal fetch failed", zap.String("code", code), zap.Error(err))
		return nil, fmt.Errorf("big deal fetch: %w", err)
	}

	// 结合股价涨跌覆盖洗盘信号
	// 经典洗盘特征：主力大量买入（买入占比>60%）但股价下跌
	if priceChangeRate != 0 {
		mainBuyPct := 0.0
		if s := summary.Stats[TickSizeLarge]; s != nil {
			mainBuyPct += s.BuyAmount
		}
		if s := summary.Stats[TickSizeSuper]; s != nil {
			mainBuyPct += s.BuyAmount
		}
		mainTotal := summary.MainBuyAmount + summary.MainSellAmount
		if mainTotal > 0 {
			mainBuyPct = mainBuyPct / mainTotal * 100
		}

		isWashing := mainBuyPct > 60 && priceChangeRate < 0
		summary.WashingSignal = isWashing
		if isWashing {
			summary.WashingSignalDesc = fmt.Sprintf(
				"疑似主力洗盘吸筹：主力买入占比 %.1f%%，但今日股价 %.2f%%",
				mainBuyPct, priceChangeRate,
			)
		} else {
			summary.WashingSignal = false
			summary.WashingSignalDesc = ""
		}
	}

	return summary, nil
}
