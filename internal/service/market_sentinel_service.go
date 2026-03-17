package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

const (
	emUlistURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2" +
		"&fields=f12,f14,f3,f5,f6,f104,f105,f106,f134" +
		"&secids=1.000001,0.399001" +
		"&ut=bd1d9ddb04089700cf9c27f6f7426281"

	emDatacenterURL = "https://datacenter-web.eastmoney.com/api/data/v1/get" +
		"?reportName=RPT_STOCK_CHANGE_STATISTICS" +
		"&columns=TRADE_DATE,RISE_NUM,DOWN_NUM" +
		"&pageNumber=1&pageSize=1" +
		"&sortColumns=TRADE_DATE&sortTypes=-1"
)

type ulistResponse struct {
	RC   int    `json:"rc"`
	Data *struct {
		Total int         `json:"total"`
		Diff  []ulistItem `json:"diff"`
	} `json:"data"`
}

type ulistItem struct {
	Code    string  `json:"f12"`
	Name    string  `json:"f14"`
	ChgPct  float64 `json:"f3"`
	Volume  float64 `json:"f5"`
	Amount  float64 `json:"f6"`
	UpCount int     `json:"f104"`
	DnCount int     `json:"f105"`
	FlCount int     `json:"f106"`
	LimitUp int     `json:"f134"`
}

type datacenterResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Result  *struct {
		Pages int `json:"pages"`
		Count int `json:"count"`
		Data  []struct {
			TradeDate string `json:"TRADE_DATE"`
			RiseNum   int    `json:"RISE_NUM"`
			DownNum   int    `json:"DOWN_NUM"`
		} `json:"data"`
	} `json:"result"`
}

type MarketSentinelService struct {
	repo       repo.MarketSentimentRepo
	httpClient *http.Client
	log        *zap.Logger
}

func NewMarketSentinelService(r repo.MarketSentimentRepo, log *zap.Logger) *MarketSentinelService {
	return &MarketSentinelService{
		repo:       r,
		httpClient: newEMClient(15 * time.Second),
		log:        log,
	}
}

type MarketSummaryDTO struct {
	SentimentScore int     `json:"sentiment_score"`
	TotalAmount    float64 `json:"total_amount"`
	AlertStatus    string  `json:"alert_status"`
	DailySummary   string  `json:"daily_summary"`
	UpCount        int     `json:"up_count"`
	DownCount      int     `json:"down_count"`
}

func (s *MarketSentinelService) Start(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		if err := s.RunAnalysis(ctx); err != nil {
			s.log.Error("market sentinel: initial run failed", zap.Error(err))
		}
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				now := time.Now()
				if now.Hour() >= 9 && (now.Hour() < 15 || (now.Hour() == 15 && now.Minute() <= 30)) {
					if err := s.RunAnalysis(ctx); err != nil {
						s.log.Error("market sentinel: run failed", zap.Error(err))
					}
				}
			}
		}
	}()
}

func (s *MarketSentinelService) RunAnalysis(ctx context.Context) error {
	marketData, err := s.fetchMarketData()
	if err != nil {
		return fmt.Errorf("fetch market data: %w", err)
	}
	avgVol := s.getAverageVolume(ctx, 5)
	if avgVol == 0 {
		avgVol = marketData.TotalAmount
	}
	marketData.SentimentScore = s.calculateScore(marketData, avgVol)
	if err := s.repo.Upsert(ctx, marketData); err != nil {
		return fmt.Errorf("save market sentiment: %w", err)
	}
	s.log.Info("market sentinel: analysis completed",
		zap.Int("score", marketData.SentimentScore),
		zap.Float64("amount_bn", marketData.TotalAmount/1e9),
		zap.Int("up", marketData.UpCount),
		zap.Int("down", marketData.DownCount),
	)
	return nil
}

func (s *MarketSentinelService) GetSummary(ctx context.Context) (*MarketSummaryDTO, error) {
	today := time.Now().Truncate(24 * time.Hour)
	m, err := s.repo.GetByDate(ctx, today)
	if err != nil {
		s.log.Info("market data missing for today, triggering on-demand analysis")
		if runErr := s.RunAnalysis(ctx); runErr == nil {
			m, err = s.repo.GetByDate(ctx, today)
		} else {
			s.log.Warn("on-demand analysis failed", zap.Error(runErr))
		}
	}
	if err != nil || m == nil {
		m, err = s.repo.GetLatest(ctx)
		if err != nil {
			return nil, fmt.Errorf("no market data available: %w", err)
		}
	}
	if m == nil {
		return nil, fmt.Errorf("no market data available")
	}

	alertStatus := "SAFE"
	if m.LimitDownCount > 20 || m.SentimentScore < 30 {
		alertStatus = "DANGER"
	} else if m.SentimentScore < 50 {
		alertStatus = "WARNING"
	}

	summary := fmt.Sprintf("今日成交 %.0f 亿，上涨 %d 家，下跌 %d 家，热度 %d。",
		m.TotalAmount/1e8, m.UpCount, m.DownCount, m.SentimentScore)
	switch {
	case alertStatus == "DANGER":
		summary += " 市场极寒，请注意风险！"
	case alertStatus == "SAFE" && m.SentimentScore > 70:
		summary += " 市场火热，进攻！"
	case m.TotalAmount < 700e9:
		summary += " 存量博弈，赚钱效应较弱。"
	}

	return &MarketSummaryDTO{
		SentimentScore: m.SentimentScore,
		TotalAmount:    m.TotalAmount,
		AlertStatus:    alertStatus,
		DailySummary:   summary,
		UpCount:        m.UpCount,
		DownCount:      m.DownCount,
	}, nil
}

func (s *MarketSentinelService) fetchMarketData() (*model.MarketSentiment, error) {
	ms, err := s.fetchFromUlist()
	if err != nil {
		s.log.Warn("market sentinel: ulist fetch failed, falling back to datacenter", zap.Error(err))
		return s.fetchFromDatacenter()
	}
	return ms, nil
}

func (s *MarketSentinelService) fetchFromUlist() (*model.MarketSentiment, error) {
	body, err := s.get(emUlistURL)
	if err != nil {
		return nil, fmt.Errorf("ulist http: %w", err)
	}
	var resp ulistResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("ulist unmarshal: %w | body: %.200s", err, body)
	}
	if resp.RC != 0 {
		return nil, fmt.Errorf("ulist rc=%d", resp.RC)
	}
	if resp.Data == nil || len(resp.Data.Diff) == 0 {
		return nil, fmt.Errorf("ulist: empty diff")
	}

	ms := &model.MarketSentiment{TradeDate: time.Now().Truncate(24 * time.Hour)}
	for _, item := range resp.Data.Diff {
		ms.TotalAmount += item.Amount
		ms.UpCount += item.UpCount
		ms.DownCount += item.DnCount
		ms.LimitUpCount += item.LimitUp
	}
	if ms.TotalAmount == 0 || (ms.UpCount == 0 && ms.DownCount == 0) {
		return nil, fmt.Errorf("ulist: zero data (market may be closed)")
	}
	s.log.Info("market sentinel: ulist fetch ok",
		zap.Float64("amount_bn", ms.TotalAmount/1e9),
		zap.Int("up", ms.UpCount),
		zap.Int("down", ms.DownCount),
	)
	return ms, nil
}

func (s *MarketSentinelService) fetchFromDatacenter() (*model.MarketSentiment, error) {
	body, err := s.get(emDatacenterURL)
	if err != nil {
		return nil, fmt.Errorf("datacenter http: %w", err)
	}
	var resp datacenterResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("datacenter unmarshal: %w | body: %.200s", err, body)
	}
	if !resp.Success || resp.Result == nil || len(resp.Result.Data) == 0 {
		return nil, fmt.Errorf("datacenter: %s", resp.Message)
	}

	row := resp.Result.Data[0]
	ms := &model.MarketSentiment{
		TradeDate: time.Now().Truncate(24 * time.Hour),
		UpCount:   row.RiseNum,
		DownCount: row.DownNum,
	}
	if amount, err := s.fetchTotalAmount(); err == nil {
		ms.TotalAmount = amount
	}
	if ms.UpCount == 0 && ms.DownCount == 0 {
		return nil, fmt.Errorf("datacenter: zero up/down counts")
	}
	return ms, nil
}

func (s *MarketSentinelService) fetchTotalAmount() (float64, error) {
	const amtURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2&fields=f12,f6" +
		"&secids=1.000001,0.399001" +
		"&ut=bd1d9ddb04089700cf9c27f6f7426281"
	body, err := s.get(amtURL)
	if err != nil {
		return 0, err
	}
	var resp ulistResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, err
	}
	if resp.RC != 0 || resp.Data == nil {
		return 0, fmt.Errorf("rc=%d", resp.RC)
	}
	total := 0.0
	for _, item := range resp.Data.Diff {
		total += item.Amount
	}
	return total, nil
}

// get 通用 HTTP GET，带 Cookie + Chrome 请求头
func (s *MarketSentinelService) get(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")
	injectCookie(req) // ← 注入 Cookie

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func (s *MarketSentinelService) getAverageVolume(ctx context.Context, days int) float64 {
	sum := 0.0
	count := 0
	for i := 1; i <= days*3 && count < days; i++ {
		date := time.Now().AddDate(0, 0, -i).Truncate(24 * time.Hour)
		m, err := s.repo.GetByDate(ctx, date)
		if err == nil && m != nil && m.TotalAmount > 0 {
			sum += m.TotalAmount
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

func (s *MarketSentinelService) calculateScore(m *model.MarketSentiment, avgVol float64) int {
	total := float64(m.UpCount + m.DownCount)
	if total == 0 {
		return 50
	}
	upRatio := float64(m.UpCount) / total
	limitUpRatio := 0.0
	totalLimit := float64(m.LimitUpCount + m.LimitDownCount)
	if totalLimit > 0 {
		limitUpRatio = float64(m.LimitUpCount) / totalLimit
	} else if m.LimitUpCount > 0 {
		limitUpRatio = 1.0
	}
	volRatio := 1.0
	if avgVol > 0 {
		volRatio = m.TotalAmount / avgVol
		if volRatio > 2.0 {
			volRatio = 2.0
		}
		if volRatio < 0.2 {
			volRatio = 0.2
		}
	}
	rawScore := 0.45*upRatio + 0.25*limitUpRatio + 0.30*(volRatio/2.0)
	score := int(math.Round(rawScore * 100))
	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}
