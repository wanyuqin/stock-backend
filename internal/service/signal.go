package service

// ═══════════════════════════════════════════════════════════════
// signal.go — 纯函数信号计算，不依赖任何 IO
// ═══════════════════════════════════════════════════════════════

import "stock-backend/internal/model"

// HistoryBar 是信号计算所需的最小 K 线数据结构。
// 与 service.KLine 对齐（kline_service.go），供 CheckSignals 接收。
type HistoryBar struct {
	Date   string  // "2024-01-02"
	Close  float64 // 收盘价
	Volume int64   // 成交量（手）
	PctChg float64 // 当日涨跌幅（%），由调用方从 (close-prevClose)/prevClose*100 计算
}

// SignalResult 是 CheckSignals 的返回值，包含触发的信号列表和辅助指标。
type SignalResult struct {
	Signals     []string // 触发的信号名称，如 ["VOLUME_UP", "MA20_BREAK"]
	VolumeRatio float64  // 量比：今日量 / 5日均量
	MA20        float64  // 20日移动平均收盘价
	MAStatus    string   // 均线状态描述，如 "ABOVE_MA20"
}

// CheckSignals 对一段 K 线历史执行三条信号规则，返回触发结果。
//
// 参数 history：按日期升序排列（最旧在前，最新在后），长度 >= 20 才会计算。
// 如果 len(history) < 20，返回空 SignalResult（Signals 为 nil）。
//
// 规则：
//
//	A — VOLUME_UP ：今日成交量 / 过去 5 日平均成交量 > 2.0
//	B — MA20_BREAK：今日收盘 > MA20 且昨日收盘 < MA20（上穿）
//	C — BIG_RISE  ：今日涨跌幅 > 5.0%
func CheckSignals(history []HistoryBar) SignalResult {
	if len(history) < 20 {
		return SignalResult{} // 数据不足，跳过
	}

	last := history[len(history)-1]     // 今日
	prev := history[len(history)-2]     // 昨日

	// ── 计算 MA20 ─────────────────────────────────────────────────
	ma20 := calcMA20(history)

	// ── 计算 5 日均量（不含今日，取 [n-6, n-2] 共 5 根）────────────
	// 业界惯例：量比 = 今日量 / 过去 N 日（不含今日）的日均量
	avg5Vol := calcAvg5Vol(history)

	// ── 量比 ──────────────────────────────────────────────────────
	volRatio := 0.0
	if avg5Vol > 0 {
		volRatio = float64(last.Volume) / avg5Vol
	}

	// ── 均线状态 ──────────────────────────────────────────────────
	maStatus := "BELOW_MA20"
	if last.Close > ma20 {
		maStatus = "ABOVE_MA20"
	}

	// ── 触发规则 ──────────────────────────────────────────────────
	var signals []string

	// A — 量能放大
	if avg5Vol > 0 && volRatio > 2.0 {
		signals = append(signals, model.SignalVolumeUp)
	}

	// B — 上穿 MA20（今日站上，昨日在下）
	if last.Close > ma20 && prev.Close < ma20 {
		signals = append(signals, model.SignalMA20Break)
	}

	// C — 大涨
	if last.PctChg > 5.0 {
		signals = append(signals, model.SignalBigRise)
	}

	return SignalResult{
		Signals:     signals,
		VolumeRatio: roundF(volRatio, 2),
		MA20:        roundF(ma20, 4),
		MAStatus:    maStatus,
	}
}

// ── 辅助计算函数 ──────────────────────────────────────────────────

// calcMA20 计算最近 20 日收盘均价（含今日）。
func calcMA20(history []HistoryBar) float64 {
	n := len(history)
	start := n - 20
	if start < 0 {
		start = 0
	}
	var sum float64
	cnt := 0
	for i := start; i < n; i++ {
		sum += history[i].Close
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return sum / float64(cnt)
}

// calcAvg5Vol 计算过去 5 个交易日（不含今日）的平均成交量。
// 若历史不足 6 根（无法排除今日取到 5 日），取能取到的最多数量。
func calcAvg5Vol(history []HistoryBar) float64 {
	n := len(history)
	// 取 [n-6, n-1)，即今日之前的 5 根（索引 n-6 ~ n-2）
	end   := n - 1 // 不含今日（今日是 n-1）
	start := end - 5
	if start < 0 {
		start = 0
	}
	if start >= end {
		return 0
	}
	var sum int64
	cnt := 0
	for i := start; i < end; i++ {
		sum += history[i].Volume
		cnt++
	}
	if cnt == 0 {
		return 0
	}
	return float64(sum) / float64(cnt)
}

// roundF 四舍五入到指定小数位。
func roundF(v float64, dec int) float64 {
	p := 1.0
	for i := 0; i < dec; i++ {
		p *= 10
	}
	return float64(int64(v*p+0.5)) / p
}
