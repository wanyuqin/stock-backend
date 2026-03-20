package service

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// market_sentinel_service.go
//
// 数据来源（全部腾讯 qt.gtimg.cn，不依赖东财接口）：
//
//   主力：bkqtRank_A_sh + bkqtRank_A_sz
//         板块排行接口，直接给出真实的沪深涨跌家数、涨停数、成交额
//         字段布局：
//           [2]  上涨家数
//           [3]  涨停家数
//           [4]  下跌家数
//           [5]  本市场股票总数
//           [9]  成交量（手）
//           [10] 成交额（万元）
//           [11] 涨幅最大股票代码
//           [12] 涨幅最小股票代码
//
//   备用：sh000001 + sz399001（指数行情，仅用于估算）
//
// 实时性设计：
//   - GetSummary 每次都拉实时数据（腾讯接口极快，<200ms）
//   - 内存缓存 5 秒，避免同一秒内多次请求打穿接口
//   - 数据库 Upsert 保留，每分钟后台写入（供历史均量计算）
// ═══════════════════════════════════════════════════════════════

const (
	// 腾讯 bkqtRank — 全市场板块排行（沪/深分开，数据最准）
	qqBkRankURL = "https://qt.gtimg.cn/q=bkqtRank_A_sh,bkqtRank_A_sz"

	// 腾讯指数行情（备用）
	qqIndexURL = "https://qt.gtimg.cn/q=sh000001,sz399001"

	// GetSummary 实时数据内存缓存 TTL
	summaryCacheTTL = 5 * time.Second
)

// ─────────────────────────────────────────────────────────────────
// 内存缓存（5 秒）
// ─────────────────────────────────────────────────────────────────

type summaryCache struct {
	mu        sync.RWMutex
	data      *MarketSummaryDTO
	fetchedAt time.Time
}

func (c *summaryCache) get() (*MarketSummaryDTO, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.data == nil || time.Since(c.fetchedAt) > summaryCacheTTL {
		return nil, false
	}
	cp := *c.data
	return &cp, true
}

func (c *summaryCache) set(d *MarketSummaryDTO) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *d
	c.data = &cp
	c.fetchedAt = time.Now()
}

// ─────────────────────────────────────────────────────────────────
// 服务结构
// ─────────────────────────────────────────────────────────────────

type MarketSentinelService struct {
	repo                repo.MarketSentimentRepo
	defaultMarketSource string
	cache               summaryCache
	log                 *zap.Logger
}

func NewMarketSentinelService(r repo.MarketSentimentRepo, defaultMarketSource string, log *zap.Logger) *MarketSentinelService {
	return &MarketSentinelService{
		repo:                r,
		defaultMarketSource: normalizeMarketSource(defaultMarketSource),
		log:                 log,
	}
}

func (s *MarketSentinelService) DefaultMarketSource() string { return s.defaultMarketSource }

// ─────────────────────────────────────────────────────────────────
// DTO
// ─────────────────────────────────────────────────────────────────

type MarketSummaryDTO struct {
	SentimentScore int     `json:"sentiment_score"`
	TotalAmount    float64 `json:"total_amount"`
	AlertStatus    string  `json:"alert_status"`
	DailySummary   string  `json:"daily_summary"`
	UpCount        int     `json:"up_count"`
	DownCount      int     `json:"down_count"`
}

// ─────────────────────────────────────────────────────────────────
// Start — 后台定时任务（每分钟写库，供历史均量使用）
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) Start(ctx context.Context) {
	ticker := time.NewTicker(60 * time.Second)
	go func() {
		// 启动时立即写一次
		s.persistToDB(ctx)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				now := time.Now()
				if now.Hour() >= 9 && (now.Hour() < 15 || (now.Hour() == 15 && now.Minute() <= 30)) {
					s.persistToDB(ctx)
				}
			}
		}
	}()
}

// persistToDB 抓取实时数据写入数据库（后台调用，不影响接口响应速度）
func (s *MarketSentinelService) persistToDB(ctx context.Context) {
	ms, err := s.fetchMarketData(ctx)
	if err != nil {
		s.log.Warn("market sentinel: fetchMarketData for DB failed", zap.Error(err))
		return
	}
	avgVol := s.getAverageVolume(ctx, 5)
	if avgVol == 0 {
		avgVol = ms.TotalAmount
	}
	ms.SentimentScore = s.calculateScore(ms, avgVol)
	if err := s.repo.Upsert(ctx, ms); err != nil {
		s.log.Warn("market sentinel: db upsert failed", zap.Error(err))
		return
	}
	s.log.Info("market sentinel: db persisted",
		zap.Int("score", ms.SentimentScore),
		zap.Float64("amount_100m", ms.TotalAmount/1e8),
		zap.Int("up", ms.UpCount),
		zap.Int("down", ms.DownCount),
	)
}

// RunAnalysis / RunAnalysisBySource 保留兼容接口（供 cron/admin 调用）
func (s *MarketSentinelService) RunAnalysis(ctx context.Context) error {
	return s.RunAnalysisBySource(ctx, s.defaultMarketSource)
}

func (s *MarketSentinelService) RunAnalysisBySource(ctx context.Context, _ string) error {
	s.persistToDB(ctx)
	return nil
}

// ─────────────────────────────────────────────────────────────────
// GetSummary — 实时拉取，5 秒内存缓存
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) GetSummaryBySource(ctx context.Context, _ string) (*MarketSummaryDTO, error) {
	return s.GetSummary(ctx)
}

func (s *MarketSentinelService) GetSummary(ctx context.Context) (*MarketSummaryDTO, error) {
	// 命中内存缓存（5s 内）
	if cached, ok := s.cache.get(); ok {
		return cached, nil
	}

	ms, err := s.fetchMarketData(ctx)
	if err != nil {
		// 实时拉取失败，降级读数据库最新一条
		s.log.Warn("market sentinel: live fetch failed, falling back to DB", zap.Error(err))
		return s.buildSummaryFromDB(ctx)
	}

	// 计算热度分（需要历史均量）
	avgVol := s.getAverageVolume(ctx, 5)
	if avgVol == 0 {
		avgVol = ms.TotalAmount
	}
	ms.SentimentScore = s.calculateScore(ms, avgVol)

	dto := s.buildSummaryDTO(ms)
	s.cache.set(dto)
	return dto, nil
}

// buildSummaryFromDB 降级：读数据库最新一条
func (s *MarketSentinelService) buildSummaryFromDB(ctx context.Context) (*MarketSummaryDTO, error) {
	today := time.Now().Truncate(24 * time.Hour)
	m, err := s.repo.GetByDate(ctx, today)
	if err != nil || m == nil {
		m, err = s.repo.GetLatest(ctx)
		if err != nil || m == nil {
			return nil, fmt.Errorf("no market data available")
		}
	}
	return s.buildSummaryDTO(m), nil
}

// buildSummaryDTO model → DTO，统一构建逻辑
func (s *MarketSentinelService) buildSummaryDTO(m *model.MarketSentiment) *MarketSummaryDTO {
	alertStatus := "SAFE"
	switch {
	case m.LimitDownCount > 20 || m.SentimentScore < 30:
		alertStatus = "DANGER"
	case m.SentimentScore < 50:
		alertStatus = "WARNING"
	}

	summary := fmt.Sprintf("今日成交 %.0f 亿，上涨 %d 家，下跌 %d 家，热度 %d。",
		m.TotalAmount/1e8, m.UpCount, m.DownCount, m.SentimentScore)
	switch {
	case alertStatus == "DANGER":
		summary += " 市场极寒，请注意风险！"
	case alertStatus == "SAFE" && m.SentimentScore > 70:
		summary += " 市场火热，进攻！"
	case m.TotalAmount > 0 && m.TotalAmount < 700e9:
		summary += " 存量博弈，赚钱效应较弱。"
	}

	return &MarketSummaryDTO{
		SentimentScore: m.SentimentScore,
		TotalAmount:    m.TotalAmount,
		AlertStatus:    alertStatus,
		DailySummary:   summary,
		UpCount:        m.UpCount,
		DownCount:      m.DownCount,
	}
}

// ─────────────────────────────────────────────────────────────────
// fetchMarketData — 优先 bkqtRank，失败退回指数估算
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) fetchMarketData(ctx context.Context) (*model.MarketSentiment, error) {
	ms, err := s.fetchFromBkRank(ctx)
	if err == nil {
		return ms, nil
	}
	s.log.Warn("market sentinel: bkqtRank failed, fallback to index estimate", zap.Error(err))
	recordDataSourceFallback("market_summary", "qq_bkrank", "qq_index")
	return s.fetchFromQQIndex(ctx)
}

// ─────────────────────────────────────────────────────────────────
// fetchFromBkRank — 腾讯 bkqtRank_A_sh/sz（最准确）
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) fetchFromBkRank(ctx context.Context) (*model.MarketSentiment, error) {
	body, err := fetchQQHTTP(ctx, qqBkRankURL)
	if err != nil {
		return nil, fmt.Errorf("bkqtRank http: %w", err)
	}

	utf8Body, convErr := gbkToUTF8(body)
	if convErr != nil {
		utf8Body = body
	}

	var sh, sz []string
	for _, line := range strings.Split(string(utf8Body), ";") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		start := strings.Index(line, `"`)
		end := strings.LastIndex(line, `"`)
		if start < 0 || end <= start {
			continue
		}
		fields := strings.Split(line[start+1:end], "~")
		if len(fields) < 11 {
			continue
		}
		switch {
		case strings.Contains(line, "bkqtRank_A_sh"):
			sh = fields
		case strings.Contains(line, "bkqtRank_A_sz"):
			sz = fields
		}
	}

	if sh == nil && sz == nil {
		return nil, fmt.Errorf("bkqtRank: both sh and sz missing from response")
	}

	ms := &model.MarketSentiment{
		TradeDate: time.Now().Truncate(24 * time.Hour),
	}
	if sh != nil {
		ms.UpCount      += int(parseF(sh[2]))
		ms.LimitUpCount += int(parseF(sh[3]))
		ms.DownCount    += int(parseF(sh[4]))
		ms.TotalAmount  += parseF(sh[10]) * 10000 // 万元 → 元
	}
	if sz != nil {
		ms.UpCount      += int(parseF(sz[2]))
		ms.LimitUpCount += int(parseF(sz[3]))
		ms.DownCount    += int(parseF(sz[4]))
		ms.TotalAmount  += parseF(sz[10]) * 10000 // 万元 → 元
	}

	if ms.UpCount == 0 && ms.DownCount == 0 {
		return nil, fmt.Errorf("bkqtRank: zero up/down counts (market likely closed or pre-open)")
	}

	s.log.Debug("market sentinel: bkqtRank fetch ok",
		zap.Float64("amount_100m", ms.TotalAmount/1e8),
		zap.Int("up", ms.UpCount),
		zap.Int("down", ms.DownCount),
		zap.Int("limit_up", ms.LimitUpCount),
	)
	return ms, nil
}

// ─────────────────────────────────────────────────────────────────
// fetchFromQQIndex — 指数行情备用（家数为估算）
// ─────────────────────────────────────────────────────────────────

func (s *MarketSentinelService) fetchFromQQIndex(ctx context.Context) (*model.MarketSentiment, error) {
	body, err := fetchQQHTTP(ctx, qqIndexURL)
	if err != nil {
		return nil, fmt.Errorf("qq index http: %w", err)
	}
	quotes, parseErr := parseQQQuoteBatch(body)
	if parseErr != nil {
		return nil, fmt.Errorf("qq index parse: %w", parseErr)
	}

	// 按名称匹配，避免与同代码个股（000001 平安银行）冲突
	var sh, sz *Quote
	for _, q := range quotes {
		if q == nil {
			continue
		}
		if strings.Contains(q.Name, "上证") && sh == nil {
			sh = q
		} else if (strings.Contains(q.Name, "深证成") || strings.Contains(q.Name, "深成")) && sz == nil {
			sz = q
		}
	}

	if sh == nil && sz == nil {
		return nil, fmt.Errorf("qq index: neither SH nor SZ index found")
	}

	totalAmount := 0.0
	weightedChange := 0.0
	weights := 0.0
	for _, q := range []*Quote{sh, sz} {
		if q == nil {
			continue
		}
		amtYuan := q.Amount * 10000
		totalAmount += amtYuan
		w := amtYuan
		if w <= 0 {
			w = 1
		}
		weightedChange += q.ChangeRate * w
		weights += w
	}

	marketChg := 0.0
	if weights > 0 {
		marketChg = weightedChange / weights
	}

	const totalStocks = 5200
	upRatio := 0.5 + marketChg/10.0
	if upRatio < 0.05 {
		upRatio = 0.05
	}
	if upRatio > 0.95 {
		upRatio = 0.95
	}
	upCount   := int(math.Round(upRatio * totalStocks))
	downCount := totalStocks - upCount

	limitUp, limitDown := 0, 0
	switch {
	case marketChg >= 2:
		limitUp = 100
	case marketChg >= 1:
		limitUp = 50
	case marketChg <= -2:
		limitDown = 100
	case marketChg <= -1:
		limitDown = 50
	}

	s.log.Info("market sentinel: qq index fallback ok",
		zap.Float64("market_chg", marketChg),
		zap.Float64("amount_100m", totalAmount/1e8),
	)

	return &model.MarketSentiment{
		TradeDate:      time.Now().Truncate(24 * time.Hour),
		TotalAmount:    totalAmount,
		UpCount:        upCount,
		DownCount:      downCount,
		LimitUpCount:   limitUp,
		LimitDownCount: limitDown,
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// 辅助方法
// ─────────────────────────────────────────────────────────────────

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
	if avgVol > 0 && m.TotalAmount > 0 {
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
