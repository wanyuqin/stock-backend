package service

import (
	"context"
	"math"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// DTO
// ═══════════════════════════════════════════════════════════════

// EquityPoint 净值曲线中的单个数据点
type EquityPoint struct {
	Date          string  `json:"date"`           // YYYY-MM-DD
	Equity        float64 `json:"equity"`         // 账户净值（元）
	RealizedPnL   float64 `json:"realized_pnl"`
	UnrealizedPnL float64 `json:"unrealized_pnl"`
	DailyReturn   float64 `json:"daily_return"`   // 当日收益率（%）
	Drawdown      float64 `json:"drawdown"`       // 当日回撤（相对峰值，%）
}

// EquityMetrics 净值曲线汇总指标
type EquityMetrics struct {
	TotalReturn     float64 `json:"total_return"`      // 总收益率（%）
	AnnualReturn    float64 `json:"annual_return"`     // 年化收益率（%）
	MaxDrawdown     float64 `json:"max_drawdown"`      // 最大回撤（%）
	SharpeRatio     float64 `json:"sharpe_ratio"`      // 夏普比率
	WinDays         int     `json:"win_days"`          // 盈利天数
	LoseDays        int     `json:"lose_days"`         // 亏损天数
	TradingDays     int     `json:"trading_days"`      // 有记录的交易天数
	CurrentEquity   float64 `json:"current_equity"`    // 最新账户净值
	PeakEquity      float64 `json:"peak_equity"`       // 历史峰值
	InitialEquity   float64 `json:"initial_equity"`    // 起始净值
}

// EquityCurveDTO 完整净值曲线响应
type EquityCurveDTO struct {
	Points  []*EquityPoint `json:"points"`
	Metrics EquityMetrics  `json:"metrics"`
}

// ═══════════════════════════════════════════════════════════════
// EquityService
// ═══════════════════════════════════════════════════════════════

type EquityService struct {
	snapshotRepo  repo.AccountSnapshotRepo
	tradeSvc      *TradeService
	log           *zap.Logger
}

func NewEquityService(
	snapshotRepo repo.AccountSnapshotRepo,
	tradeSvc *TradeService,
	log *zap.Logger,
) *EquityService {
	return &EquityService{
		snapshotRepo: snapshotRepo,
		tradeSvc:     tradeSvc,
		log:          log,
	}
}

// TakeSnapshot 计算当前账户净值并写入快照
func (s *EquityService) TakeSnapshot(ctx context.Context, userID int64) error {
	report, err := s.tradeSvc.GetPerformance(ctx, userID)
	if err != nil {
		return err
	}

	cst := time.FixedZone("CST", 8*3600)
	today := time.Now().In(cst).Truncate(24 * time.Hour)

	// 基准净值：已实现 + 浮动（简单模型；若有初始资金可在此叠加）
	equity := report.TotalRealizedPnL + report.TotalUnrealizedPnL

	snap := &model.AccountSnapshot{
		SnapshotDate:  today,
		Equity:        round2(equity),
		RealizedPnL:   round2(report.TotalRealizedPnL),
		UnrealizedPnL: round2(report.TotalUnrealizedPnL),
	}

	if err := s.snapshotRepo.Upsert(ctx, snap); err != nil {
		return err
	}
	s.log.Info("equity snapshot saved",
		zap.String("date", today.Format("2006-01-02")),
		zap.Float64("equity", equity),
	)
	return nil
}

// GetCurve 返回最近 days 天的净值曲线 + 指标
func (s *EquityService) GetCurve(ctx context.Context, days int) (*EquityCurveDTO, error) {
	if days <= 0 || days > 1825 {
		days = 365
	}
	to := time.Now()
	from := to.AddDate(0, 0, -days)

	snaps, err := s.snapshotRepo.ListByDateRange(ctx, from, to)
	if err != nil {
		return nil, err
	}
	if len(snaps) == 0 {
		return &EquityCurveDTO{
			Points:  []*EquityPoint{},
			Metrics: EquityMetrics{},
		}, nil
	}

	points, metrics := buildCurve(snaps)
	return &EquityCurveDTO{
		Points:  points,
		Metrics: metrics,
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// 内部计算
// ─────────────────────────────────────────────────────────────────

func buildCurve(snaps []*model.AccountSnapshot) ([]*EquityPoint, EquityMetrics) {
	n := len(snaps)
	points := make([]*EquityPoint, n)

	peak := snaps[0].Equity
	if peak == 0 {
		peak = 1 // 避免除零
	}

	var maxDrawdown float64
	dailyReturns := make([]float64, 0, n)
	winDays, loseDays := 0, 0

	for i, s := range snaps {
		eq := s.Equity
		if eq == 0 {
			eq = 0.0001 // 避免除零
		}

		// 日收益率
		dailyRet := 0.0
		if i > 0 {
			prev := snaps[i-1].Equity
			if prev != 0 {
				dailyRet = (eq - prev) / math.Abs(prev) * 100
			}
		}

		// 最大回撤
		if eq > peak {
			peak = eq
		}
		drawdown := 0.0
		if peak != 0 {
			drawdown = (peak - eq) / math.Abs(peak) * 100
		}
		if drawdown > maxDrawdown {
			maxDrawdown = drawdown
		}

		if dailyRet > 0 {
			winDays++
		} else if dailyRet < 0 {
			loseDays++
		}
		if i > 0 {
			dailyReturns = append(dailyReturns, dailyRet)
		}

		points[i] = &EquityPoint{
			Date:          s.SnapshotDate.Format("2006-01-02"),
			Equity:        round2(eq),
			RealizedPnL:   round2(s.RealizedPnL),
			UnrealizedPnL: round2(s.UnrealizedPnL),
			DailyReturn:   round2(dailyRet),
			Drawdown:      round2(drawdown),
		}
	}

	initial := snaps[0].Equity
	current := snaps[n-1].Equity
	totalReturn := 0.0
	if initial != 0 {
		totalReturn = (current - initial) / math.Abs(initial) * 100
	}

	// 年化收益率 = (1 + totalReturn/100)^(365/days) - 1
	tradingDays := n
	annualReturn := 0.0
	if tradingDays > 1 && initial != 0 {
		growthFactor := current / math.Abs(initial)
		if growthFactor > 0 {
			annualReturn = (math.Pow(growthFactor, 365.0/float64(tradingDays)) - 1) * 100
		}
	}

	// 夏普比率 = (mean - riskFree) / stddev * sqrt(252)
	// 无风险利率 2.5%/年 → 日化 ≈ 0.00993%
	riskFreeDaily := 2.5 / 252.0
	sharpe := calcSharpe(dailyReturns, riskFreeDaily)

	metrics := EquityMetrics{
		TotalReturn:   round2(totalReturn),
		AnnualReturn:  round2(annualReturn),
		MaxDrawdown:   round2(maxDrawdown),
		SharpeRatio:   round2(sharpe),
		WinDays:       winDays,
		LoseDays:      loseDays,
		TradingDays:   tradingDays,
		CurrentEquity: round2(current),
		PeakEquity:    round2(peak),
		InitialEquity: round2(initial),
	}
	return points, metrics
}

func calcSharpe(returns []float64, riskFreeDaily float64) float64 {
	if len(returns) < 2 {
		return 0
	}
	// mean
	sum := 0.0
	for _, r := range returns {
		sum += r
	}
	mean := sum / float64(len(returns))
	// std
	variance := 0.0
	for _, r := range returns {
		d := r - mean
		variance += d * d
	}
	std := math.Sqrt(variance / float64(len(returns)-1))
	if std == 0 {
		return 0
	}
	return (mean - riskFreeDaily) / std * math.Sqrt(252)
}
