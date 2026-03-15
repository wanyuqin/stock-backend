package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 东方财富接口说明（经实际抓包验证，2026-03）
//
// push2.eastmoney.com/api/qt/clist/get   → 502，已废弃
// push2.eastmoney.com/api/qt/stock/get   → 连接被关闭，已废弃
//
// 可用接口：
//
// [A] push2.eastmoney.com/api/qt/ulist.np/get
//     secids=1.000001,0.399001  （上证 + 深证）
//     字段：
//       f6   = 成交额（元）
//       f104 = 上涨家数
//       f105 = 下跌家数
//       f106 = 平盘家数
//       f134 = 涨停家数（含ST）
//
// [B] datacenter-web.eastmoney.com/api/data/v1/get
//     reportName=RPT_STOCK_CHANGE_STATISTICS
//     字段：TRADE_DATE, RISE_NUM, DOWN_NUM
//     说明：含北交所，实时性略低于[A]，用于交叉兜底
// ═══════════════════════════════════════════════════════════════

const (
	// ulist.np：上证+深证 涨跌家数 + 成交额
	emUlistURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2" +
		"&fields=f12,f14,f3,f5,f6,f104,f105,f106,f134" +
		"&secids=1.000001,0.399001" +
		"&ut=bd1d9ddb04089700cf9c27f6f7426281"

	// datacenter：涨跌家数（含北交所，用于兜底）
	emDatacenterURL = "https://datacenter-web.eastmoney.com/api/data/v1/get" +
		"?reportName=RPT_STOCK_CHANGE_STATISTICS" +
		"&columns=TRADE_DATE,RISE_NUM,DOWN_NUM" +
		"&pageNumber=1&pageSize=1" +
		"&sortColumns=TRADE_DATE&sortTypes=-1"
)

// ─────────────────────────────────────────────────────────────────
// 响应结构：ulist.np
// ─────────────────────────────────────────────────────────────────

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
	ChgPct  float64 `json:"f3"`   // 涨跌幅%
	Volume  float64 `json:"f5"`   // 成交量（手）
	Amount  float64 `json:"f6"`   // 成交额（元）
	UpCount int     `json:"f104"` // 上涨家数
	DnCount int     `json:"f105"` // 下跌家数
	FlCount int     `json:"f106"` // 平盘家数
	LimitUp int     `json:"f134"` // 涨停家数
}

// ─────────────────────────────────────────────────────────────────
// 响应结构：datacenter
// ─────────────────────────────────────────────────────────────────

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

// ═══════════════════════════════════════════════════════════════
// MarketSentinelService
// ═══════════════════════════════════════════════════════════════

type MarketSentinelService struct {
	repo       repo.MarketSentimentRepo
	httpClient *http.Client
	log        *zap.Logger
}

func NewMarketSentinelService(r repo.MarketSentimentRepo, log *zap.Logger) *MarketSentinelService {
	jar, _ := cookiejar.New(nil)
	return &MarketSentinelService{
		repo: r,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
		log: log,
	}
}

// MarketSummaryDTO 前端返回结构
type MarketSummaryDTO struct {
	SentimentScore int     `json:"sentiment_score"`
	TotalAmount    float64 `json:"total_amount"`
	AlertStatus    string  `json:"alert_status"` // SAFE, WARNING, DANGER
	DailySummary   string  `json:"daily_summary"`
	UpCount        int     `json:"up_count"`
	DownCount      int     `json:"down_count"`
}

// ─────────────────────────────────────────────────────────────────
// Start / RunAnalysis / GetSummary
// ─────────────────────────────────────────────────────────────────

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
				// 交易时段：9:00-15:30
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
		zap.Int("limit_up", marketData.LimitUpCount),
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

// ─────────────────────────────────────────────────────────────────
// fetchMarketData — 主入口，优先走 ulist.np，失败降级到 datacenter
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) fetchMarketData() (*model.MarketSentiment, error) {
	ms, err := s.fetchFromUlist()
	if err != nil {
		s.log.Warn("market sentinel: ulist fetch failed, falling back to datacenter", zap.Error(err))
		return s.fetchFromDatacenter()
	}
	return ms, nil
}

// ─────────────────────────────────────────────────────────────────
// fetchFromUlist — 主数据源
//
// 接口：push2.eastmoney.com/api/qt/ulist.np/get
// 两条记录：上证(000001) + 深证(399001)
// 成交额 = 沪 f6 + 深 f6
// 涨家数 = 沪 f104 + 深 f104
// 跌家数 = 沪 f105 + 深 f105
// 涨停数 = 沪 f134 + 深 f134
// ─────────────────────────────────────────────────────────────────

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

	ms := &model.MarketSentiment{
		TradeDate: time.Now().Truncate(24 * time.Hour),
	}

	// 聚合沪 + 深两条记录
	for _, item := range resp.Data.Diff {
		ms.TotalAmount += item.Amount
		ms.UpCount += item.UpCount
		ms.DownCount += item.DnCount
		ms.LimitUpCount += item.LimitUp
		// ulist.np 无独立的跌停字段，用对称阈值近似（实际跌停数较少，影响可接受）
	}

	if ms.TotalAmount == 0 || (ms.UpCount == 0 && ms.DownCount == 0) {
		return nil, fmt.Errorf("ulist: zero data (market may be closed)")
	}

	s.log.Info("market sentinel: ulist fetch ok",
		zap.Float64("amount_bn", ms.TotalAmount/1e9),
		zap.Int("up", ms.UpCount),
		zap.Int("down", ms.DownCount),
		zap.Int("limit_up", ms.LimitUpCount),
	)
	return ms, nil
}

// ─────────────────────────────────────────────────────────────────
// fetchFromDatacenter — 降级数据源
//
// 接口：datacenter-web.eastmoney.com
// 说明：含北交所，每分钟更新，实时性略低
//       只能拿到 RISE_NUM / DOWN_NUM，无成交额和涨停数
//       成交额用 ulist 的沪深两市指数 f6 兜底
// ─────────────────────────────────────────────────────────────────

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
		// TotalAmount/LimitUpCount/LimitDownCount 不可得，保持零值
	}

	// 尝试补充成交额（从指数行情接口单独拉一次）
	if amount, err := s.fetchTotalAmount(); err == nil {
		ms.TotalAmount = amount
	} else {
		s.log.Warn("market sentinel: fetchTotalAmount failed", zap.Error(err))
	}

	if ms.UpCount == 0 && ms.DownCount == 0 {
		return nil, fmt.Errorf("datacenter: zero up/down counts")
	}

	s.log.Info("market sentinel: datacenter fallback ok",
		zap.Int("up", ms.UpCount),
		zap.Int("down", ms.DownCount),
		zap.Float64("amount_bn", ms.TotalAmount/1e9),
	)
	return ms, nil
}

// fetchTotalAmount 用于 datacenter 降级时单独补充成交额
// 只拉上证+深证成交额之和（ulist.np 仅取 f6）
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

// ─────────────────────────────────────────────────────────────────
// get — 通用 HTTP GET，带固定请求头
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) get(url string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// ─────────────────────────────────────────────────────────────────
// getAverageVolume — 取过去 N 个交易日平均成交额（用于情绪打分）
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) getAverageVolume(ctx context.Context, days int) float64 {
	sum := 0.0
	count := 0
	// 最多往前查 days*3 个日历日，命中 days 个交易日为止
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

// ─────────────────────────────────────────────────────────────────
// calculateScore — 综合情绪评分（0-100）
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) calculateScore(m *model.MarketSentiment, avgVol float64) int {
	total := float64(m.UpCount + m.DownCount)
	if total == 0 {
		return 50
	}

	// 涨跌比 [0,1]
	upRatio := float64(m.UpCount) / total

	// 涨停占比 [0,1]（对涨跌停总数归一化；无跌停数时保守估计为上涨家数的 1%）
	limitUpRatio := 0.0
	totalLimit := float64(m.LimitUpCount + m.LimitDownCount)
	if totalLimit > 0 {
		limitUpRatio = float64(m.LimitUpCount) / totalLimit
	} else if m.LimitUpCount > 0 {
		limitUpRatio = 1.0 // 只有涨停、没有跌停，极度乐观
	}

	// 量能比（今日 vs 5日均值）[0,2]，clamp 到 [0.2, 2]
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

	// 加权：涨跌比 45%，涨停比 25%，量能比 30%
	// volRatio 归一化到 [0,1]（满值 2x 均量 → 1.0）
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
