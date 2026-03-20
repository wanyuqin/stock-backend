package service

import (
	"fmt"
	"math"
	"time"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// buy_context_analyzer.go
//
// 基于 K 线数据，客观分析买入时的价格行为。
// 完全不依赖用户填写的理由文字。
//
// 使用方式：
//   ctx, err := AnalyzeBuyContext(klines, buyPrice, buyDate)
//
// 需要的数据：
//   - 至少 25 根 K 线（含买入当日，用于计算 MA20 + 5日均量）
//   - 买入价格
//   - 买入日期（YYYY-MM-DD）
// ═══════════════════════════════════════════════════════════════

const (
	// 追高阈值：买入位置在当日区间上方 80% 以上
	chasingHighPositionThreshold = 0.75
	// 追高阈值：相对 MA20 偏离超过 8%
	chasingHighMA20Threshold = 8.0
	// 低吸阈值：买入位置在当日区间下方 25% 以内
	bottomFishingPositionThreshold = 0.30
	// 放量阈值：当日成交量 > 5日均量 × 1.5
	volumeBreakoutThreshold = 1.5
	// 连续上涨判定：前3日涨幅超过 6% 则认为连续拉升
	priorRallyThreshold3d = 6.0
	// 前10日涨幅超过15%视为强势股追高
	priorRallyThreshold10d = 15.0
)

// AnalyzeBuyContext 基于 K 线数据计算买入上下文。
// klines 必须按日期正序排列（最早在前，最新在后）。
// buyDate 格式为 "2006-01-02"。
func AnalyzeBuyContext(klines []KLine, buyPrice float64, buyDate time.Time) *model.BuyContext {
	ctx := &model.BuyContext{
		BuyDate:    buyDate.Format("2006-01-02"),
		BuyPrice:   buyPrice,
		AnalyzedAt: time.Now().Format(time.RFC3339),
	}

	if len(klines) < 6 {
		ctx.DataSufficient = false
		ctx.BuyLabel = "数据不足"
		return ctx
	}

	// 找到买入当日的 K 线索引
	buyDateStr := buyDate.Format("2006-01-02")
	buyIdx := -1
	for i, k := range klines {
		if k.Date == buyDateStr {
			buyIdx = i
			break
		}
	}
	// 找不到精确日期：找最近的较早日期（容错）
	if buyIdx == -1 {
		for i := len(klines) - 1; i >= 0; i-- {
			if klines[i].Date <= buyDateStr {
				buyIdx = i
				break
			}
		}
	}
	if buyIdx == -1 || buyIdx == 0 {
		ctx.DataSufficient = false
		ctx.BuyLabel = "数据不足（找不到买入当日K线）"
		return ctx
	}

	ctx.DataSufficient = true
	day := klines[buyIdx]
	prevDay := klines[buyIdx-1]

	// ── 1. 买入价在当日区间的位置 ───────────────────────────────
	dayRange := day.High - day.Low
	if dayRange > 0 {
		ctx.BuyPositionInDayRange = (buyPrice - day.Low) / dayRange
	} else {
		ctx.BuyPositionInDayRange = 0.5 // 无振幅日，居中
	}

	// ── 2. 当日基础数据 ─────────────────────────────────────────
	ctx.DayOpen   = day.Open
	ctx.DayHigh   = day.High
	ctx.DayLow    = day.Low
	ctx.DayClose  = day.Close
	ctx.DayVolume = day.Volume

	if prevDay.Close > 0 {
		ctx.DayChangePct    = (day.Close - prevDay.Close) / prevDay.Close * 100
		ctx.DayAmplitudePct = (day.High - day.Low) / prevDay.Close * 100
	}

	// ── 3. MA5（买入当日收盘后） ─────────────────────────────────
	ctx.MA5, ctx.MA5Uptrend = calcMAWithTrend(klines, buyIdx, 5)

	// ── 4. MA20 及趋势（买入当日收盘后） ─────────────────────────
	if buyIdx >= 19 {
		ctx.MA20, ctx.MA20Uptrend = calcMAWithTrend(klines, buyIdx, 20)
	} else {
		// 数据不足 20 根，用已有数据近似
		ctx.MA20, ctx.MA20Uptrend = calcMAWithTrend(klines, buyIdx, buyIdx+1)
	}

	// MA20 偏离度（用买入价对比 MA20）
	if ctx.MA20 > 0 {
		ctx.MA20DeviationPct = (buyPrice - ctx.MA20) / ctx.MA20 * 100
	}

	// ── 5. 5日均量 ───────────────────────────────────────────────
	vol5dSum := int64(0)
	vol5dCount := 0
	// 用买入日之前的5根（不含买入当日，避免自我引用）
	for i := buyIdx - 1; i >= 0 && vol5dCount < 5; i-- {
		vol5dSum += klines[i].Volume
		vol5dCount++
	}
	if vol5dCount > 0 {
		ctx.AvgVol5d = float64(vol5dSum) / float64(vol5dCount)
	}
	if ctx.AvgVol5d > 0 {
		ctx.VolumeRatioVs5d = float64(day.Volume) / ctx.AvgVol5d
	}

	// ── 6. 买入前 N 日涨跌幅 ─────────────────────────────────────
	ctx.Prior3dGainPct  = priorGain(klines, buyIdx, 3)
	ctx.Prior5dGainPct  = priorGain(klines, buyIdx, 5)
	ctx.Prior10dGainPct = priorGain(klines, buyIdx, 10)

	// ── 7. 行为判断标签 ──────────────────────────────────────────
	ctx.IsChasingHigh = ctx.BuyPositionInDayRange >= chasingHighPositionThreshold ||
		ctx.MA20DeviationPct >= chasingHighMA20Threshold ||
		(ctx.Prior3dGainPct >= priorRallyThreshold3d && ctx.BuyPositionInDayRange >= 0.6)

	ctx.IsBottomFishing = ctx.BuyPositionInDayRange <= bottomFishingPositionThreshold &&
		ctx.MA20DeviationPct <= 0

	ctx.IsVolumeBreakout = ctx.VolumeRatioVs5d >= volumeBreakoutThreshold

	ctx.IsTrendAligned = ctx.MA5Uptrend && ctx.MA20Uptrend

	ctx.BuyLabel = buildBuyLabel(ctx)

	return ctx
}

// ── 工具函数 ──────────────────────────────────────────────────────

// calcMAWithTrend 计算指定索引处的 MA 值和趋势方向（5根斜率）
func calcMAWithTrend(klines []KLine, endIdx int, period int) (ma float64, uptrend bool) {
	if endIdx < period-1 {
		period = endIdx + 1
	}
	sum := 0.0
	for i := endIdx - period + 1; i <= endIdx; i++ {
		sum += klines[i].Close
	}
	ma = sum / float64(period)

	// 趋势：比较当前MA和5根前的MA
	lookback := 5
	if endIdx < period-1+lookback {
		lookback = endIdx - period + 1
		if lookback <= 0 {
			return ma, true // 数据不足，默认上升
		}
	}
	prevEndIdx := endIdx - lookback
	if prevEndIdx < period-1 {
		return ma, true
	}
	prevSum := 0.0
	for i := prevEndIdx - period + 1; i <= prevEndIdx; i++ {
		prevSum += klines[i].Close
	}
	prevMA := prevSum / float64(period)
	uptrend = ma >= prevMA
	return ma, uptrend
}

// priorGain 计算买入日前 n 个交易日的涨跌幅（%）
func priorGain(klines []KLine, buyIdx int, n int) float64 {
	startIdx := buyIdx - n
	if startIdx < 0 {
		startIdx = 0
	}
	if startIdx == buyIdx {
		return 0
	}
	startPrice := klines[startIdx].Close
	endPrice := klines[buyIdx-1].Close // 买入日前一日收盘
	if startPrice <= 0 {
		return 0
	}
	return (endPrice - startPrice) / startPrice * 100
}

// buildBuyLabel 根据量化指标生成综合标签
func buildBuyLabel(ctx *model.BuyContext) string {
	// 优先级最高：明确的追高
	if ctx.IsChasingHigh {
		if ctx.Prior3dGainPct >= priorRallyThreshold3d {
			return fmt.Sprintf("连续拉升%.1f%%后追高（位置%.0f%%）", ctx.Prior3dGainPct, ctx.BuyPositionInDayRange*100)
		}
		if ctx.MA20DeviationPct >= chasingHighMA20Threshold {
			return fmt.Sprintf("偏离MA20 +%.1f%% 高位追买", ctx.MA20DeviationPct)
		}
		return fmt.Sprintf("追高买入（日内位置%.0f%%）", ctx.BuyPositionInDayRange*100)
	}

	// 低吸
	if ctx.IsBottomFishing {
		if ctx.IsVolumeBreakout {
			return fmt.Sprintf("放量低吸（量比%.1fx，位置%.0f%%）", ctx.VolumeRatioVs5d, ctx.BuyPositionInDayRange*100)
		}
		return fmt.Sprintf("缩量低吸（位置%.0f%%，MA20偏离%.1f%%）", ctx.BuyPositionInDayRange*100, ctx.MA20DeviationPct)
	}

	// 顺势买入
	if ctx.IsTrendAligned {
		if ctx.IsVolumeBreakout {
			return fmt.Sprintf("顺势放量买入（量比%.1fx）", ctx.VolumeRatioVs5d)
		}
		return "顺势买入（MA5/MA20同向上）"
	}

	// 逆势买入
	if !ctx.MA20Uptrend && !ctx.MA5Uptrend {
		if ctx.IsVolumeBreakout {
			return fmt.Sprintf("逆势放量买入（MA20向下，量比%.1fx）", ctx.VolumeRatioVs5d)
		}
		return "逆势买入（MA5/MA20同向下）"
	}

	// 中间区域
	posLabel := "中位"
	if ctx.BuyPositionInDayRange >= 0.6 {
		posLabel = "偏高位"
	} else if ctx.BuyPositionInDayRange <= 0.4 {
		posLabel = "偏低位"
	}
	return fmt.Sprintf("%s买入（日内%.0f%%，MA20偏离%.1f%%）", posLabel, ctx.BuyPositionInDayRange*100, ctx.MA20DeviationPct)
}

// round2ForContext 保留两位小数
func round2ForContext(v float64) float64 {
	return math.Round(v*100) / 100
}

// ═══════════════════════════════════════════════════════════════
// BuyContextSummary — 用于 AI prompt 和前端展示的文字摘要
// ═══════════════════════════════════════════════════════════════

// BuyContextSummary 生成买入上下文的中文摘要，直接用于 AI 审计 prompt
func BuyContextSummary(ctx *model.BuyContext) string {
	if ctx == nil {
		return "买入上下文：未分析"
	}
	if !ctx.DataSufficient {
		return "买入上下文：K线数据不足，无法分析"
	}

	trendStr := "MA20向上"
	if !ctx.MA20Uptrend {
		trendStr = "MA20向下"
	}
	if ctx.MA5Uptrend != ctx.MA20Uptrend {
		if ctx.MA5Uptrend {
			trendStr += "（MA5短期反弹）"
		} else {
			trendStr += "（MA5短期转弱）"
		}
	}

	volStr := fmt.Sprintf("量比 %.1fx（%s）", ctx.VolumeRatioVs5d, volumeLabel(ctx.VolumeRatioVs5d))

	priorStr := ""
	if math.Abs(ctx.Prior3dGainPct) >= 2 {
		priorStr = fmt.Sprintf("，前3日累涨 %+.1f%%", ctx.Prior3dGainPct)
	}
	if math.Abs(ctx.Prior10dGainPct) >= 5 {
		priorStr += fmt.Sprintf("，前10日累涨 %+.1f%%", ctx.Prior10dGainPct)
	}

	return fmt.Sprintf(
		"买入上下文：%s | 日内位置 %.0f%%（0%%最低 100%%最高）| MA20偏离 %+.1f%% | %s | %s%s",
		ctx.BuyLabel,
		ctx.BuyPositionInDayRange*100,
		ctx.MA20DeviationPct,
		trendStr,
		volStr,
		priorStr,
	)
}

func volumeLabel(ratio float64) string {
	switch {
	case ratio >= 2.0:
		return "大幅放量"
	case ratio >= 1.5:
		return "放量"
	case ratio >= 0.8:
		return "正常"
	case ratio >= 0.5:
		return "缩量"
	default:
		return "极度缩量"
	}
}
