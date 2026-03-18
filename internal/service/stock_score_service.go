package service

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// StockScoreService — 个股综合建仓评分
//
// 评分维度（满分 100）：
//   大盘情绪   20 分  — 市场不能极寒，否则扣分
//   趋势结构   25 分  — MA20 上方 + 斜率向上
//   板块强度   20 分  — RS 个股 vs 行业
//   主力资金   20 分  — 量比动能
//   估值分位   15 分  — PE 分位 < 50% 加分
// ═══════════════════════════════════════════════════════════════

// ScoreItem 单维度得分明细
type ScoreItem struct {
	Name     string `json:"name"`
	Score    int    `json:"score"`  // 实际得分
	MaxScore int    `json:"max"`    // 满分
	Level    string `json:"level"`  // "good" | "normal" | "bad"
	Desc     string `json:"desc"`   // 一句话说明
}

// StockScoreDTO 完整评分结果
type StockScoreDTO struct {
	Code         string      `json:"code"`
	Name         string      `json:"name"`
	TotalScore   int         `json:"total_score"`   // 0-100
	Verdict      string      `json:"verdict"`       // "建议建仓" | "谨慎观望" | "不建议"
	VerdictLevel string      `json:"verdict_level"` // "go" | "caution" | "no"
	Items        []ScoreItem `json:"items"`
	Summary      string      `json:"summary"`       // 一句话综合结论
	// 关键价位（供 K 线图标注）
	CurrentPrice float64 `json:"current_price"`
	MA20         float64 `json:"ma20"`
	Support      float64 `json:"support"`
	Resistance   float64 `json:"resistance"`
	ATR          float64 `json:"atr"`
}

type StockScoreService struct {
	guardianSvc   *PositionGuardianService
	marketSvc     *MarketSentinelService
	valRepo       repo.ValuationRepo
	watchlistRepo repo.WatchlistRepo
	stockSvc      *StockService
	log           *zap.Logger
}

func NewStockScoreService(
	guardianSvc *PositionGuardianService,
	marketSvc *MarketSentinelService,
	valRepo repo.ValuationRepo,
	watchlistRepo repo.WatchlistRepo,
	stockSvc *StockService,
	log *zap.Logger,
) *StockScoreService {
	return &StockScoreService{
		guardianSvc:   guardianSvc,
		marketSvc:     marketSvc,
		valRepo:       valRepo,
		watchlistRepo: watchlistRepo,
		stockSvc:      stockSvc,
		log:           log,
	}
}

// Score 计算指定股票的综合建仓评分
func (s *StockScoreService) Score(ctx context.Context, code string) (*StockScoreDTO, error) {
	type klineResult struct {
		klines []KLine
		name   string
		err    error
	}
	type quoteResult struct {
		q   *Quote
		err error
	}
	type marketResult struct {
		summary *MarketSummaryDTO
		err     error
	}
	type valResult struct {
		pePercentile *float64
		err          error
	}

	klineCh  := make(chan klineResult, 1)
	quoteCh  := make(chan quoteResult, 1)
	marketCh := make(chan marketResult, 1)
	valCh    := make(chan valResult, 1)

	go func() {
		resp, err := s.stockSvc.GetKLine(code, 30)
		if err != nil {
			klineCh <- klineResult{err: err}
			return
		}
		klineCh <- klineResult{klines: resp.KLines, name: resp.Name}
	}()
	go func() {
		q, err := s.stockSvc.GetRealtimeQuote(code)
		quoteCh <- quoteResult{q: q, err: err}
	}()
	go func() {
		summary, err := s.marketSvc.GetSummary(ctx)
		marketCh <- marketResult{summary: summary, err: err}
	}()
	go func() {
		snap, err := s.valRepo.GetSnapshot(ctx, code)
		if err != nil || snap == nil {
			valCh <- valResult{}
			return
		}
		valCh <- valResult{pePercentile: snap.PEPercentile}
	}()

	kr := <-klineCh
	qr := <-quoteCh
	mr := <-marketCh
	vr := <-valCh

	if kr.err != nil {
		return nil, fmt.Errorf("获取 K 线失败: %w", kr.err)
	}
	if qr.err != nil {
		return nil, fmt.Errorf("获取行情失败: %w", qr.err)
	}
	klines := kr.klines
	quote  := qr.q

	// ── 计算技术指标 ─────────────────────────────────────────────
	var (
		ma20, ma20Slope     float64
		support, resistance float64
		atr                 float64
	)
	if len(klines) >= 20 {
		ma20, ma20Slope = calcMA20WithSlope(klines)
		support, resistance = calcSupportResistance(klines, 20)
		atr = calcATRFromKLines(klines, 20)
	}

	// 板块 RS — RelativeStrength.Diff = 个股涨跌幅 - 板块涨跌幅
	var rsVal float64
	rs, rsErr := s.guardianSvc.sectorProvider.GetRelativeStrength(ctx, code, quote.ChangeRate)
	if rsErr == nil && rs != nil {
		rsVal = rs.Diff // ← 修复：正确字段名为 Diff，不是 RS
	}

	// ── 逐维度打分 ────────────────────────────────────────────────
	items := make([]ScoreItem, 0, 5)
	total := 0

	// 1. 大盘情绪（20分）
	{
		maxScore := 20
		var item ScoreItem
		item.Name     = "大盘情绪"
		item.MaxScore = maxScore
		if mr.err != nil || mr.summary == nil {
			item.Score = 10
			item.Level = "normal"
			item.Desc  = "大盘数据暂缺，中性评分"
		} else {
			score := mr.summary.SentimentScore
			switch {
			case mr.summary.AlertStatus == "DANGER":
				item.Score = 0
				item.Level = "bad"
				item.Desc  = fmt.Sprintf("市场极寒（热度%d），强烈不建议建仓", score)
			case mr.summary.AlertStatus == "WARNING":
				item.Score = 8
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("市场偏弱（热度%d），谨慎操作", score)
			case score >= 70:
				item.Score = maxScore
				item.Level = "good"
				item.Desc  = fmt.Sprintf("市场火热（热度%d），进攻时机", score)
			case score >= 50:
				item.Score = 15
				item.Level = "good"
				item.Desc  = fmt.Sprintf("市场平稳（热度%d），正常操作", score)
			default:
				item.Score = 10
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("市场偏冷（热度%d），保守仓位", score)
			}
		}
		total += item.Score
		items = append(items, item)
	}

	// 2. 趋势结构（25分）
	{
		maxScore := 25
		item := ScoreItem{Name: "趋势结构", MaxScore: maxScore}
		price := quote.Price
		if ma20 == 0 {
			item.Score = 10
			item.Level = "normal"
			item.Desc  = "K 线数据不足，无法计算 MA20"
		} else {
			aboveMA20 := price > ma20
			slopeUp   := ma20Slope > 0
			switch {
			case aboveMA20 && slopeUp:
				item.Score = maxScore
				item.Level = "good"
				item.Desc  = fmt.Sprintf("价格在 MA20(%.2f) 上方，均线向上，趋势健康", ma20)
			case aboveMA20 && !slopeUp:
				item.Score = 15
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("价格在 MA20(%.2f) 上方，但均线趋平，趋势减弱", ma20)
			case !aboveMA20 && slopeUp:
				item.Score = 8
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("价格跌破 MA20(%.2f)，均线仍向上，等待回踩确认", ma20)
			default:
				item.Score = 0
				item.Level = "bad"
				item.Desc  = fmt.Sprintf("价格跌破 MA20(%.2f) 且均线向下，下跌趋势中，不建议建仓", ma20)
			}
		}
		total += item.Score
		items = append(items, item)
	}

	// 3. 板块强度（20分）
	{
		maxScore := 20
		item := ScoreItem{Name: "板块强度", MaxScore: maxScore}
		if rsErr != nil || rs == nil {
			item.Score = 10
			item.Level = "normal"
			item.Desc  = "板块数据暂缺，中性评分"
		} else {
			switch {
			case rsVal >= 3:
				item.Score = maxScore
				item.Level = "good"
				item.Desc  = fmt.Sprintf("强于板块 %.1f%%，个股资金主动流入", rsVal)
			case rsVal >= 0:
				item.Score = 14
				item.Level = "good"
				item.Desc  = fmt.Sprintf("与板块持平（RS +%.1f%%），随势运行", rsVal)
			case rsVal >= -3:
				item.Score = 8
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("略弱于板块（RS %.1f%%），观察主力动向", rsVal)
			case rsVal >= -5:
				item.Score = 3
				item.Level = "bad"
				item.Desc  = fmt.Sprintf("明显弱于板块（RS %.1f%%），主力有流出迹象", rsVal)
			default:
				item.Score = 0
				item.Level = "bad"
				item.Desc  = fmt.Sprintf("严重弱于板块（RS %.1f%%），主力主动出货，强烈不建议建仓", rsVal)
			}
		}
		total += item.Score
		items = append(items, item)
	}

	// 4. 资金动能（20分）：量比
	{
		maxScore := 20
		item := ScoreItem{Name: "资金动能", MaxScore: maxScore}
		volRatio := quote.VolumeRatio
		switch {
		case volRatio >= 2.5:
			item.Score = maxScore
			item.Level = "good"
			item.Desc  = fmt.Sprintf("量比 %.2f，资金高度活跃，有主力参与", volRatio)
		case volRatio >= 1.5:
			item.Score = 15
			item.Level = "good"
			item.Desc  = fmt.Sprintf("量比 %.2f，资金较为活跃，关注度高", volRatio)
		case volRatio >= 1.0:
			item.Score = 10
			item.Level = "normal"
			item.Desc  = fmt.Sprintf("量比 %.2f，资金正常流动", volRatio)
		case volRatio >= 0.5:
			item.Score = 5
			item.Level = "normal"
			item.Desc  = fmt.Sprintf("量比 %.2f，成交清淡，市场关注度低", volRatio)
		default:
			item.Score = 0
			item.Level = "bad"
			item.Desc  = fmt.Sprintf("量比 %.2f，极度萎缩，无人问津", volRatio)
		}
		total += item.Score
		items = append(items, item)
	}

	// 5. 估值分位（15分）
	{
		maxScore := 15
		item := ScoreItem{Name: "估值分位", MaxScore: maxScore}
		if vr.pePercentile == nil {
			item.Score = 7
			item.Level = "normal"
			item.Desc  = "历史数据积累中，暂无估值分位"
		} else {
			pct := *vr.pePercentile
			switch {
			case pct < 20:
				item.Score = maxScore
				item.Level = "good"
				item.Desc  = fmt.Sprintf("PE 分位 %.0f%%，历史极低估值，安全边际充足", pct)
			case pct < 40:
				item.Score = 12
				item.Level = "good"
				item.Desc  = fmt.Sprintf("PE 分位 %.0f%%，估值偏低，有上行空间", pct)
			case pct < 60:
				item.Score = 8
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("PE 分位 %.0f%%，估值合理，随势操作", pct)
			case pct < 80:
				item.Score = 4
				item.Level = "normal"
				item.Desc  = fmt.Sprintf("PE 分位 %.0f%%，估值偏高，上方压力较大", pct)
			default:
				item.Score = 0
				item.Level = "bad"
				item.Desc  = fmt.Sprintf("PE 分位 %.0f%%，历史高估值区间，建仓风险较大", pct)
			}
		}
		total += item.Score
		items = append(items, item)
	}

	// ── 综合结论 ──────────────────────────────────────────────────
	verdict, verdictLevel, summary := buildVerdict(total, items)

	s.log.Info("stock score calculated",
		zap.String("code", code),
		zap.Int("score", total),
		zap.String("verdict", verdict),
	)

	return &StockScoreDTO{
		Code:         code,
		Name:         kr.name,
		TotalScore:   total,
		Verdict:      verdict,
		VerdictLevel: verdictLevel,
		Items:        items,
		Summary:      summary,
		CurrentPrice: quote.Price,
		MA20:         ma20,
		Support:      support,
		Resistance:   resistance,
		ATR:          atr,
	}, nil
}

func buildVerdict(total int, items []ScoreItem) (verdict, level, summary string) {
	hasHardVeto := false
	for _, item := range items {
		if (item.Name == "大盘情绪" || item.Name == "趋势结构" || item.Name == "板块强度") &&
			item.Score == 0 {
			hasHardVeto = true
			break
		}
	}

	var badItems, goodItems []string
	for _, item := range items {
		switch item.Level {
		case "bad":
			badItems = append(badItems, item.Name)
		case "good":
			goodItems = append(goodItems, item.Name)
		}
	}

	switch {
	case hasHardVeto || total < 30:
		verdict = "不建议建仓"
		level   = "no"
		summary = fmt.Sprintf("综合评分 %d/100，%s 存在硬性风险，当前不适合建仓。", total, strings.Join(badItems, "、"))
	case total < 55:
		verdict = "谨慎观望"
		level   = "caution"
		summary = fmt.Sprintf("综合评分 %d/100，条件不够充分，可以列入观察但暂缓建仓。", total)
	case total < 75:
		verdict = "可以考虑"
		level   = "caution"
		summary = fmt.Sprintf("综合评分 %d/100，%s 表现良好，可小仓位试探，控制在计划的 40%%。", total, strings.Join(goodItems, "、"))
	default:
		verdict = "建议建仓"
		level   = "go"
		summary = fmt.Sprintf("综合评分 %d/100，%s 全面向好，可按计划建仓。", total, strings.Join(goodItems, "、"))
	}
	return
}
