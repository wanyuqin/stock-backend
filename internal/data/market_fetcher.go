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
	client       *http.Client
	tokenManager *TokenManager
}

func NewMarketFetcher(tm *TokenManager) *MarketFetcher {
	return &MarketFetcher{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
		tokenManager: tm,
	}
}

type MarketStats struct {
	TotalAmount    float64
	UpCount        int
	DownCount      int
	LimitUpCount   int
	LimitDownCount int
	FlatCount      int
}

// FetchAll 获取全市场行情快照（携带 Cookie 绕过东财 CDN 检测）
func (f *MarketFetcher) FetchAll() ([]*model.StockDailySnapshot, *MarketStats, error) {
	url := "https://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=6000&po=1&np=1&ut=bd1d9ddb04089700cf9c27f6f7426281&fltt=2&invt=2&fid=f3&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23&fields=f12,f14,f2,f3,f6,f9,f8,f23,f104,f105"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")

	// 注入 Cookie
	if f.tokenManager != nil {
		if cookie, err := f.tokenManager.GetStockCookie(); err == nil && cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
	}

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

type emStockItem struct {
	Code      string      `json:"f12"`
	Name      string      `json:"f14"`
	Price     interface{} `json:"f2"`
	PctChg    interface{} `json:"f3"`
	Amount    interface{} `json:"f6"`
	Turnover  interface{} `json:"f8"`
	PERatio   interface{} `json:"f23"`
	LimitUp   interface{} `json:"f104"`
	LimitDown interface{} `json:"f105"`
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
		price := parseFloat(item.Price)
		pctChg := parseFloat(item.PctChg)
		amount := parseFloat(item.Amount)
		turnover := parseFloat(item.Turnover)

		if price == 0 {
			continue
		}

		snap := &model.StockDailySnapshot{
			TradeDate:    today,
			Code:         item.Code,
			Name:         item.Name,
			Price:        floatPtr(price),
			PctChg:       floatPtr(pctChg),
			TurnoverRate: floatPtr(turnover),
		}
		snapshots = append(snapshots, snap)

		stats.TotalAmount += amount
		if pctChg > 0 {
			stats.UpCount++
		} else if pctChg < 0 {
			stats.DownCount++
		} else {
			stats.FlatCount++
		}

		isLimitUp := parseBool(item.LimitUp)
		isLimitDown := parseBool(item.LimitDown)
		if isLimitUp {
			stats.LimitUpCount++
		} else if isLimitDown {
			stats.LimitDownCount++
		} else if pctChg >= 9.8 {
			stats.LimitUpCount++
		} else if pctChg <= -9.8 {
			stats.LimitDownCount++
		}
	}

	return snapshots, stats, nil
}

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
		if val >= 9.8 {
			stats.LimitUpCount++
		} else if val <= -9.8 {
			stats.LimitDownCount++
		}
	}
	return stats
}

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
