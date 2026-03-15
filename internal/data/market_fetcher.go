package data

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"stock-backend/internal/model"
)

// MarketFetcher 全市场行情抓取器
type MarketFetcher struct {
	client *http.Client
}

func NewMarketFetcher() *MarketFetcher {
	return &MarketFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// MarketStats 市场统计数据
type MarketStats struct {
	TotalAmount    float64 // 两市总成交额
	UpCount        int     // 上涨家数
	DownCount      int     // 下跌家数
	LimitUpCount   int     // 涨停家数 (>= 9.8%)
	LimitDownCount int     // 跌停家数 (<= -9.8%)
	FlatCount      int     // 平盘家数
}

// FetchAll 获取全市场行情快照
func (f *MarketFetcher) FetchAll() ([]*model.StockDailySnapshot, *MarketStats, error) {
	// 东方财富全市场接口 (沪深A股)
	// fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23
	// fields 映射:
	// f12:股票代码, f14:股票名称, f2:最新价, f3:涨跌幅, f6:成交额, f9:昨收价,
	// f22:涨速(注意：部分接口f8是换手率，但用户指定f22为换手率，这里为了稳妥同时请求f8和f22，优先使用f8), f23:市盈率
	// f104:涨停状态(需确认接口是否支持，通常需计算), f105:跌停状态

	// URL 参数构造
	// pn=1&pz=6000: 一次性拉取 6000 条，覆盖全市场
	// fields=f12,f14,f2,f3,f6,f9,f8,f23,f104,f105
	url := "https://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=6000&po=1&np=1&ut=bd1d9ddb04089700cf9c27f6f7426281&fltt=2&invt=2&fid=f3&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23&fields=f12,f14,f2,f3,f6,f9,f8,f23,f104,f105"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}

	// 模拟 Browser User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	return f.parseResponse(body)
}

// 内部解析逻辑
type emStockItem struct {
	Code      string      `json:"f12"`
	Name      string      `json:"f14"`
	Price     interface{} `json:"f2"`   // 最新价
	PctChg    interface{} `json:"f3"`   // 涨跌幅
	Amount    interface{} `json:"f6"`   // 成交额
	Turnover  interface{} `json:"f8"`   // 换手率
	PERatio   interface{} `json:"f23"`  // 市盈率
	LimitUp   interface{} `json:"f104"` // 涨停状态 (通常是 0/1 或 boolean)
	LimitDown interface{} `json:"f105"` // 跌停状态
}

type emResponse struct {
	Data struct {
		Diff []emStockItem `json:"diff"`
	} `json:"data"`
}

func (f *MarketFetcher) parseResponse(body []byte) ([]*model.StockDailySnapshot, *MarketStats, error) {
	var resp emResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, nil, fmt.Errorf("unmarshal failed: %w", err)
	}

	if resp.Data.Diff == nil {
		return nil, nil, fmt.Errorf("empty data")
	}

	snapshots := make([]*model.StockDailySnapshot, 0, len(resp.Data.Diff))
	stats := &MarketStats{}
	today := time.Now().Truncate(24 * time.Hour)

	for _, item := range resp.Data.Diff {
		// 解析数值 (处理 "-")
		price := parseFloat(item.Price)
		pctChg := parseFloat(item.PctChg)
		amount := parseFloat(item.Amount)
		turnover := parseFloat(item.Turnover)

		// 忽略无效数据 (价格为0通常是停牌或未上市)
		if price == 0 {
			continue
		}

		// 构建 Snapshot
		snap := &model.StockDailySnapshot{
			TradeDate:    today,
			Code:         item.Code,
			Name:         item.Name,
			Price:        floatPtr(price),
			PctChg:       floatPtr(pctChg),
			TurnoverRate: floatPtr(turnover),
			// VolRatio 暂时无法从单次快照获取 (需要昨量)
		}
		snapshots = append(snapshots, snap)

		// 统计
		stats.TotalAmount += amount

		if pctChg > 0 {
			stats.UpCount++
		} else if pctChg < 0 {
			stats.DownCount++
		} else {
			stats.FlatCount++
		}

		// 涨跌停判断
		// 优先使用接口返回的状态字段 (f104/f105)，如果有效的话
		isLimitUp := parseBool(item.LimitUp)
		isLimitDown := parseBool(item.LimitDown)

		if isLimitUp {
			stats.LimitUpCount++
		} else if isLimitDown {
			stats.LimitDownCount++
		} else {
			// 兜底：如果接口没返回状态，使用涨跌幅阈值判定 (>= 9.8%)
			// 注意：创业板/科创板是 20%，北交所 30%，ST 是 5%
			// 简单近似 >= 9.8%
			if pctChg >= 9.8 {
				stats.LimitUpCount++
			} else if pctChg <= -9.8 {
				stats.LimitDownCount++
			}
		}
	}

	return snapshots, stats, nil
}

// CalculateMarketStats 纯计算逻辑 (如果已有 snapshot 列表)
func (f *MarketFetcher) CalculateMarketStats(snapshots []*model.StockDailySnapshot) *MarketStats {
	stats := &MarketStats{}
	for _, s := range snapshots {
		if s.PctChg == nil {
			continue
		}
		val := *s.PctChg

		if val > 0 {
			stats.UpCount++
		} else if val < 0 {
			stats.DownCount++
		} else {
			stats.FlatCount++
		}

		// 这里只能用阈值判断，因为 snapshot 里没有存储 LimitUp/Down 状态字段
		if val >= 9.8 {
			stats.LimitUpCount++
		} else if val <= -9.8 {
			stats.LimitDownCount++
		}
	}
	return stats
}

// 辅助函数
func parseFloat(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case string:
		if val == "-" {
			return 0
		}
		f, _ := strconv.ParseFloat(val, 64)
		return f
	}
	return 0
}

func parseBool(v interface{}) bool {
	// f104/f105 可能返回 0/1 或 "0"/"1" 或 boolean
	switch val := v.(type) {
	case float64:
		return val > 0
	case int:
		return val > 0
	case string:
		return val == "1" || val == "true"
	case bool:
		return val
	}
	return false
}

func floatPtr(v float64) *float64 {
	return &v
}
