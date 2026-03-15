package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/data"
	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

type MarketSentinelService struct {
	repo          repo.MarketSentimentRepo
	httpClient    *http.Client
	tokenManager  *data.TokenManager
	marketDataURL string
	log           *zap.Logger
}

func NewMarketSentinelService(repo repo.MarketSentimentRepo, log *zap.Logger) *MarketSentinelService {
	jar, _ := cookiejar.New(nil)
	return &MarketSentinelService{
		repo: repo,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
			Jar:     jar,
		},
		tokenManager:  data.NewTokenManager(log),
		marketDataURL: "http://push2.eastmoney.com/api/qt/clist/get?pn=1&pz=6000&po=1&np=1&ut=bd1d9ddb04089700cf9c27f6f7426281&fltt=2&invt=2&fid=f3&fs=m:0+t:6,m:0+t:80,m:1+t:2,m:1+t:23&fields=f170,f48",
		log:           log,
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

// Start 启动定时任务
func (s *MarketSentinelService) Start(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		// 立即运行一次
		if err := s.RunAnalysis(ctx); err != nil {
			s.log.Error("market sentinel: initial run failed", zap.Error(err))
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// 仅在交易时间运行 (9:00 - 15:30)
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

// RunAnalysis 执行一次全市场分析
func (s *MarketSentinelService) RunAnalysis(ctx context.Context) error {
	// 1. 抓取全市场数据
	data, err := s.fetchMarketData()
	if err != nil {
		return fmt.Errorf("fetch market data: %w", err)
	}

	// 2. 获取历史平均成交额 (用于计算 VolRatio)
	// 取最近 5 个交易日的平均值
	avgVol := s.getAverageVolume(ctx, 5)
	if avgVol == 0 {
		avgVol = data.TotalAmount // 无历史数据时，使用当前值作为基准
	}

	// 3. 计算得分
	score := s.calculateScore(data, avgVol)
	data.SentimentScore = score

	// 4. 保存 (Upsert)
	// 注意：fetchMarketData 返回的 TradeDate 是 time.Now()，需要确保是当天的日期 (00:00:00)
	// 以便 Upsert 正确覆盖
	if err := s.repo.Upsert(ctx, data); err != nil {
		return fmt.Errorf("save market sentiment: %w", err)
	}

	s.log.Info("market sentinel: analysis completed",
		zap.Int("score", score),
		zap.Float64("amount", data.TotalAmount),
		zap.Int("up", data.UpCount),
		zap.Int("down", data.DownCount),
	)
	return nil
}

// GetSummary 获取最新市场概况
func (s *MarketSentinelService) GetSummary(ctx context.Context) (*MarketSummaryDTO, error) {
	// 优先获取今日数据
	today := time.Now().Truncate(24 * time.Hour)
	m, err := s.repo.GetByDate(ctx, today)

	// 如果今日数据不存在（err不为空），尝试立即抓取
	if err != nil {
		s.log.Info("market data missing for today, triggering on-demand analysis", zap.Time("date", today))
		if runErr := s.RunAnalysis(ctx); runErr == nil {
			// 抓取成功后再次查询
			m, err = s.repo.GetByDate(ctx, today)
		} else {
			s.log.Warn("on-demand analysis failed", zap.Error(runErr))
		}
	}

	// 如果还是没有今日数据（抓取失败或非交易日），兜底使用最新一条
	if err != nil || m == nil {
		// 如果今日未生成，尝试获取最新一条
		m, err = s.repo.GetLatest(ctx)
		if err != nil {
			return nil, fmt.Errorf("no market data available: %w", err)
		}
	}
	if m == nil {
		return nil, fmt.Errorf("no market data available")
	}

	// 判定风险状态
	alertStatus := "SAFE"
	// 规则：跌停 > 20 或 分数 < 30 -> DANGER
	if m.LimitDownCount > 20 || m.SentimentScore < 30 {
		alertStatus = "DANGER"
	} else if m.SentimentScore < 50 {
		alertStatus = "WARNING"
	}

	// 生成简评
	summary := fmt.Sprintf("今日成交 %.0f 亿，上涨 %d 家，下跌 %d 家，热度 %d。",
		m.TotalAmount/100000000, m.UpCount, m.DownCount, m.SentimentScore)

	if alertStatus == "DANGER" {
		summary += " 市场极寒，请注意风险！"
	} else if alertStatus == "SAFE" && m.SentimentScore > 70 {
		summary += " 市场火热，进攻！"
	} else if m.TotalAmount < 700000000000 { // 7000亿
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
// 内部逻辑
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) fetchMarketData() (*model.MarketSentiment, error) {
	// Attempt 1: 使用当前 Token 请求
	token, _ := s.tokenManager.GetToken()
	if token != "" {
		s.updateCookie(token)
	}

	ms, err := s.doFetch()
	// Check: 如果返回 502/403 或内容为空 (ms == nil)
	if err != nil || ms == nil {
		s.log.Warn("fetchMarketData: attempt 1 failed or empty, updating token...", zap.Error(err))

		// 触发重新获取 Token
		if updateErr := s.tokenManager.UpdateToken(); updateErr != nil {
			return nil, fmt.Errorf("token update failed: %w", updateErr)
		}

		// 获取新 Token 并注入 Cookie
		newToken, _ := s.tokenManager.GetToken()
		if newToken != "" {
			s.updateCookie(newToken)
		}

		// Attempt 2: 延迟 2 秒后，携带新 Token 再次重试
		s.log.Info("fetchMarketData: retrying attempt 2 after 2s delay...")
		time.Sleep(2 * time.Second)
		return s.doFetch()
	}

	return ms, nil
}

func (s *MarketSentinelService) updateCookie(token string) {
	u, _ := url.Parse("http://push2.eastmoney.com")
	s.httpClient.Jar.SetCookies(u, []*http.Cookie{
		{
			Name:  "qgssid",
			Value: token,
			Path:  "/",
		},
	})
}

func (s *MarketSentinelService) doFetch() (*model.MarketSentiment, error) {
	req, _ := http.NewRequest("GET", s.marketDataURL, nil)

	// Header 伪装
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "http://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("http error: %d", resp.StatusCode)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// 定义响应结构
	type StockItem struct {
		ChangePct float64 `json:"f170"`
		Amount    float64 `json:"f48"`
	}
	type ResponseData struct {
		Diff []StockItem `json:"diff"`
	}
	type Response struct {
		Data *ResponseData `json:"data"`
	}

	var r Response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w, body: %s", err, string(body))
	}
	if r.Data == nil || len(r.Data.Diff) == 0 {
		return nil, fmt.Errorf("empty data from eastmoney")
	}

	ms := &model.MarketSentiment{
		TradeDate: time.Now().Truncate(24 * time.Hour),
	}

	for _, item := range r.Data.Diff {
		ms.TotalAmount += item.Amount

		if item.ChangePct > 0 {
			ms.UpCount++
		} else if item.ChangePct < 0 {
			ms.DownCount++
		}

		// 简单判定涨跌停：>= 9.8%
		if item.ChangePct >= 9.8 {
			ms.LimitUpCount++
		} else if item.ChangePct <= -9.8 {
			ms.LimitDownCount++
		}
	}

	return ms, nil
}

func (s *MarketSentinelService) getAverageVolume(ctx context.Context, days int) float64 {
	sum := 0.0
	count := 0
	// 简单的回溯查找，假设数据连续
	// 如果需要更严谨，应在 repo 实现 GetRecent(limit int)
	for i := 1; i <= days*2; i++ { //以此类推，多查几天防止休市
		if count >= days {
			break
		}
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
	// 公式: Floor( 0.4 * (Up / (Up+Down)) + 0.3 * (LimitUp / (LimitUp+LimitDown)) + 0.3 * (CurrentVol / AvgVol) ) * 100

	totalStocks := float64(m.UpCount + m.DownCount)
	if totalStocks == 0 {
		return 50 // 无数据
	}

	// 1. 上涨占比
	upRatio := float64(m.UpCount) / totalStocks

	// 2. 涨停占比 (分母是涨停+跌停)
	totalLimit := float64(m.LimitUpCount + m.LimitDownCount)
	limitUpRatio := 0.0
	if totalLimit > 0 {
		limitUpRatio = float64(m.LimitUpCount) / totalLimit
	}

	// 3. 量比 (当前成交额 / 均量)
	volRatio := 1.0
	if avgVol > 0 {
		volRatio = m.TotalAmount / avgVol
	}
	// 限制量比对分数的贡献，防止爆量导致分数溢出
	// 假设标准是 1.0，如果达到 2.0 就算非常热
	// 这里的公式是直接乘 100，所以：
	// 0.3 * volRatio * 100
	// 如果 volRatio = 1, 贡献 30 分
	// 如果 volRatio = 2, 贡献 60 分
	// 看起来没问题。但如果 volRatio 过大（如 3.0），分数会超。

	rawScore := (0.4 * upRatio) + (0.3 * limitUpRatio) + (0.3 * volRatio)
	score := int(math.Floor(rawScore * 100))

	if score > 100 {
		score = 100
	}
	if score < 0 {
		score = 0
	}
	return score
}
