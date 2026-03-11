package service

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 打分规则常量
// ═══════════════════════════════════════════════════════════════

const (
	// 资金面（满分 40）
	scoreFundHigh   = 40
	scoreFundMedium = 20
	threshFundHigh  = 15.0
	threshFundMed   = 10.0

	// 趋势面（满分 30，可惩罚 -20）
	scoreTrendBull = 30
	scoreTrendBear = -20

	// 动能面（满分 30）
	scoreMomentum = 30
	threshVolLow  = 1.5
	threshVolHigh = 2.5
)

const (
	TagFundStrong   = "资金强势"
	TagFundModerate = "资金温和"
	TagBullAligned  = "多头排列"
	TagBreakMA20    = "跌破MA20"
	TagVolActive    = "量能活跃"
)

// ═══════════════════════════════════════════════════════════════
// ScreenerRequest / ScreenerResult
// ═══════════════════════════════════════════════════════════════

type ScreenerRequest struct {
	MinScore int    `json:"min_score"` // 最低总分，0 = 不限
	Limit    int    `json:"limit"`     // 返回数量，0 = 默认 50
	Date     string `json:"date"`      // YYYY-MM-DD，空 = 今日
}

type ScreenerResult struct {
	Date      string         `json:"date"`
	Total     int            `json:"total"`      // 本日快照总数
	Matched   int            `json:"matched"`    // 达到 min_score 的数量
	Items     []*ScoredStock `json:"items"`
	ElapsedMs int64          `json:"elapsed_ms"`
}

type ScoredStock struct {
	Code           string   `json:"code"`
	Name           string   `json:"name"`
	Score          int      `json:"score"`
	Tags           []string `json:"tags"`
	Price          float64  `json:"price"`
	PctChg         float64  `json:"pct_chg"`
	VolRatio       float64  `json:"vol_ratio"`
	MainInflowPct  float64  `json:"main_inflow_pct"`
	MainInflow     float64  `json:"main_inflow"`
	IsMultiAligned bool     `json:"is_multi_aligned"`
}

// ═══════════════════════════════════════════════════════════════
// ScreenerService
// ═══════════════════════════════════════════════════════════════

type ScreenerService struct {
	snapshotRepo repo.SnapshotRepo
	log          *zap.Logger
}

func NewScreenerService(snapshotRepo repo.SnapshotRepo, log *zap.Logger) *ScreenerService {
	return &ScreenerService{snapshotRepo: snapshotRepo, log: log}
}

// Execute 从 DB 读取当日快照 → 内存并行打分 → 排序 → 返回 TopN。
// 性能目标：5000 只股票打分 < 500ms。
func (s *ScreenerService) Execute(ctx context.Context, req ScreenerRequest) (*ScreenerResult, error) {
	start := time.Now()

	tradeDate, err := parseScreenerDate(req.Date)
	if err != nil {
		return nil, err
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	// 1. 单次全量读取
	snapshots, err := s.snapshotRepo.ListByDate(ctx, tradeDate)
	if err != nil {
		return nil, err
	}
	total := len(snapshots)

	s.log.Debug("screener: loaded snapshots",
		zap.Int("count", total),
		zap.String("date", tradeDate.Format("2006-01-02")),
	)

	// 2. 并行打分（纯 CPU，无 I/O）
	scored := s.parallelScore(snapshots)

	// 3. 过滤 min_score
	minScore := req.MinScore
	var filtered []*model.SnapshotScore
	for _, sc := range scored {
		if sc != nil && sc.Score >= minScore {
			filtered = append(filtered, sc)
		}
	}
	matched := len(filtered)

	// 4. 排序：分数倒序，同分按主力占比倒序
	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].Score != filtered[j].Score {
			return filtered[i].Score > filtered[j].Score
		}
		return fval(filtered[i].Snapshot.MainInflowPct) > fval(filtered[j].Snapshot.MainInflowPct)
	})

	// 5. 截取 TopN
	if len(filtered) > limit {
		filtered = filtered[:limit]
	}

	// 6. DTO 转换
	items := make([]*ScoredStock, 0, len(filtered))
	for _, sc := range filtered {
		items = append(items, toScoredStock(sc))
	}

	elapsed := time.Since(start).Milliseconds()
	s.log.Info("screener: execute done",
		zap.Int("total", total),
		zap.Int("matched", matched),
		zap.Int("returned", len(items)),
		zap.Int64("elapsed_ms", elapsed),
	)

	return &ScreenerResult{
		Date:      tradeDate.Format("2006-01-02"),
		Total:     total,
		Matched:   matched,
		Items:     items,
		ElapsedMs: elapsed,
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// CalculateScore — 多因子打分
// ─────────────────────────────────────────────────────────────────

func CalculateScore(snap *model.StockDailySnapshot) (int, []string) {
	score := 0
	var tags []string

	// ── 资金面（40分）────────────────────────────────────────────
	mip := fval(snap.MainInflowPct)
	switch {
	case mip > threshFundHigh:
		score += scoreFundHigh
		tags = append(tags, TagFundStrong)
	case mip > threshFundMed:
		score += scoreFundMedium
		tags = append(tags, TagFundModerate)
	}

	// ── 趋势面（30分，可惩罚 -20）────────────────────────────────
	price := fval(snap.Price)
	ma5   := fval(snap.MA5)
	ma20  := fval(snap.MA20)

	if ma20 > 0 {
		if price < ma20 {
			score += scoreTrendBear
			tags = append(tags, TagBreakMA20)
		} else if snap.IsMultiAligned != nil && *snap.IsMultiAligned {
			score += scoreTrendBull
			tags = append(tags, TagBullAligned)
		} else if ma5 > 0 && price > ma5 && ma5 > ma20 {
			// 兜底判断（IsMultiAligned 字段为 nil 时）
			score += scoreTrendBull
			tags = append(tags, TagBullAligned)
		}
	}

	// ── 动能面（30分）────────────────────────────────────────────
	vr := fval(snap.VolRatio)
	if vr >= threshVolLow && vr <= threshVolHigh {
		score += scoreMomentum
		tags = append(tags, TagVolActive)
	}

	return score, tags
}

// ── 并行打分 Worker Pool ──────────────────────────────────────────

func (s *ScreenerService) parallelScore(snapshots []*model.StockDailySnapshot) []*model.SnapshotScore {
	total := len(snapshots)
	if total == 0 {
		return nil
	}

	workers := runtime.NumCPU()
	if workers > total {
		workers = total
	}

	results := make([]*model.SnapshotScore, total)
	chunkSize := (total + workers - 1) / workers

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w * chunkSize
		hi := lo + chunkSize
		if hi > total {
			hi = total
		}
		if lo >= total {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				sc, tags := CalculateScore(snapshots[i])
				results[i] = &model.SnapshotScore{
					Snapshot: snapshots[i],
					Score:    sc,
					Tags:     tags,
				}
			}
		}(lo, hi)
	}
	wg.Wait()
	return results
}

// ── 辅助函数 ──────────────────────────────────────────────────────

func fval(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}

func parseScreenerDate(s string) (time.Time, error) {
	if s == "" {
		return todayDate(), nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, fmt.Errorf("date format error, expected YYYY-MM-DD: %w", err)
	}
	return t, nil
}

func toScoredStock(sc *model.SnapshotScore) *ScoredStock {
	snap := sc.Snapshot
	aligned := false
	if snap.IsMultiAligned != nil {
		aligned = *snap.IsMultiAligned
	}
	return &ScoredStock{
		Code:           snap.Code,
		Name:           snap.Name,
		Score:          sc.Score,
		Tags:           sc.Tags,
		Price:          fval(snap.Price),
		PctChg:         fval(snap.PctChg),
		VolRatio:       fval(snap.VolRatio),
		MainInflowPct:  fval(snap.MainInflowPct),
		MainInflow:     fval(snap.MainInflow),
		IsMultiAligned: aligned,
	}
}
