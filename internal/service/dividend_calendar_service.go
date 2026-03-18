package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// DividendEvent 单只股票的除权除息事件
type DividendEvent struct {
	StockCode    string  `json:"stock_code"`
	StockName    string  `json:"stock_name"`
	ExRightDate  string  `json:"ex_right_date"`  // 除权除息日 YYYY-MM-DD
	DividendAmt  float64 `json:"dividend_amt"`   // 每股分红（元）
	BonusRatio   float64 `json:"bonus_ratio"`    // 送股比例（每10股送X股）
	PlanDesc     string  `json:"plan_desc"`      // 方案描述
	DaysUntil    int     `json:"days_until"`     // 距今天数（<0=已过）
}

// DividendCalendarService 分红除权日历服务
type DividendCalendarService struct {
	memCache *gocache.Cache
	log      *zap.Logger
}

func NewDividendCalendarService(log *zap.Logger) *DividendCalendarService {
	return &DividendCalendarService{
		memCache: gocache.New(6*time.Hour, 30*time.Minute),
		log:      log,
	}
}

// GetUpcomingDividends 批量查询多只股票未来 14 天内的除权事件
func (s *DividendCalendarService) GetUpcomingDividends(ctx context.Context, codes []string) ([]*DividendEvent, error) {
	results := make([]*DividendEvent, 0)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, code := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			events, err := s.fetchDividend(ctx, code)
			if err != nil {
				s.log.Debug("dividend fetch failed", zap.String("code", code), zap.Error(err))
				return
			}
			mu.Lock()
			results = append(results, events...)
			mu.Unlock()
		}(code)
	}
	wg.Wait()

	// 只保留未来 14 天内（含今日）的事件
	upcoming := make([]*DividendEvent, 0, len(results))
	for _, e := range results {
		if e.DaysUntil >= 0 && e.DaysUntil <= 14 {
			upcoming = append(upcoming, e)
		}
	}
	return upcoming, nil
}

// fetchDividend 从东财 F10 接口获取个股分红信息
// 东财接口：https://datacenter-web.eastmoney.com/api/data/v1/get
//   ?reportName=RPT_F10_FINANCE_BONUS&columns=SECUCODE,SECURITY_CODE,SECURITY_NAME_ABBR,
//     EX_DIVIDEND_DATE,PER_CASH_DIV,BONUS_SHARE_RATIO&quoteColumns=&filter=(SECUCODE="%s.SH")
//     &pageNumber=1&pageSize=3&sortTypes=-1&sortColumns=EX_DIVIDEND_DATE
func (s *DividendCalendarService) fetchDividend(ctx context.Context, code string) ([]*DividendEvent, error) {
	cacheKey := "div:" + code
	if cached, found := s.memCache.Get(cacheKey); found {
		return cached.([]*DividendEvent), nil
	}

	suffix := "SH"
	if strings.HasPrefix(code, "0") || strings.HasPrefix(code, "3") {
		suffix = "SZ"
	}
	secucode := fmt.Sprintf("%s.%s", code, suffix)

	rawURL := fmt.Sprintf(
		"https://datacenter-web.eastmoney.com/api/data/v1/get"+
			"?reportName=RPT_F10_FINANCE_BONUS"+
			"&columns=SECUCODE,SECURITY_CODE,SECURITY_NAME_ABBR,EX_DIVIDEND_DATE,PER_CASH_DIV,BONUS_SHARE_RATIO,PLAN_NOTICE_DATE"+
			"&filter=(SECUCODE=%q)"+
			"&pageNumber=1&pageSize=3"+
			"&sortTypes=-1&sortColumns=EX_DIVIDEND_DATE",
		secucode,
	)

	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, rawURL, &EMRequestOption{Timeout: 8 * time.Second})
	if err != nil {
		return nil, err
	}

	events, err := parseDividendResp(body, code)
	if err != nil {
		return nil, err
	}

	s.memCache.Set(cacheKey, events, 6*time.Hour)
	return events, nil
}

func parseDividendResp(body []byte, code string) ([]*DividendEvent, error) {
	var raw struct {
		Result *struct {
			Data []struct {
				SecurityNameAbbr string   `json:"SECURITY_NAME_ABBR"`
				ExDividendDate   string   `json:"EX_DIVIDEND_DATE"` // "2026-03-20 00:00:00" 或 null
				PerCashDiv       *float64 `json:"PER_CASH_DIV"`
				BonusShareRatio  *float64 `json:"BONUS_SHARE_RATIO"`
			} `json:"data"`
		} `json:"result"`
		Success bool `json:"success"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	if raw.Result == nil || len(raw.Result.Data) == 0 {
		return nil, nil
	}

	cst := time.FixedZone("CST", 8*3600)
	today := time.Now().In(cst).Truncate(24 * time.Hour)
	events := make([]*DividendEvent, 0)

	for _, d := range raw.Result.Data {
		if d.ExDividendDate == "" {
			continue
		}
		// 解析日期（格式可能是 "2026-03-20 00:00:00" 或 "2026-03-20"）
		dateStr := d.ExDividendDate
		if len(dateStr) > 10 {
			dateStr = dateStr[:10]
		}
		exDate, err := time.ParseInLocation("2006-01-02", dateStr, cst)
		if err != nil {
			continue
		}
		daysUntil := int(exDate.Sub(today).Hours() / 24)

		cashAmt := 0.0
		if d.PerCashDiv != nil {
			cashAmt = *d.PerCashDiv
		}
		bonusRatio := 0.0
		if d.BonusShareRatio != nil {
			bonusRatio = *d.BonusShareRatio
		}

		// 拼方案描述
		parts := make([]string, 0)
		if cashAmt > 0 {
			parts = append(parts, fmt.Sprintf("每股派 %.2f 元", cashAmt))
		}
		if bonusRatio > 0 {
			parts = append(parts, fmt.Sprintf("每10股送 %.0f 股", bonusRatio))
		}
		planDesc := strings.Join(parts, "，")
		if planDesc == "" {
			planDesc = "见公告"
		}

		events = append(events, &DividendEvent{
			StockCode:   code,
			StockName:   d.SecurityNameAbbr,
			ExRightDate: dateStr,
			DividendAmt: cashAmt,
			BonusRatio:  bonusRatio,
			PlanDesc:    planDesc,
			DaysUntil:   daysUntil,
		})
	}
	return events, nil
}
