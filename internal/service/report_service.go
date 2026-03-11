package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// ReportService — 每日复盘简报生成
// ═══════════════════════════════════════════════════════════════

// ReportService 读取当日扫描结果，调用 AI 生成市场复盘简报，写入 daily_reports。
type ReportService struct {
	scanRepo repo.ScanRepo
	aiSvc    *AIAnalysisService
	log      *zap.Logger
}

func NewReportService(scanRepo repo.ScanRepo, aiSvc *AIAnalysisService, log *zap.Logger) *ReportService {
	return &ReportService{
		scanRepo: scanRepo,
		aiSvc:    aiSvc,
		log:      log,
	}
}

// ── 响应结构 ──────────────────────────────────────────────────────

// DailyReportDTO GET /api/v1/reports/daily 返回体。
type DailyReportDTO struct {
	Date        string             `json:"date"`
	Content     string             `json:"content"`     // Markdown 全文
	MarketMood  string             `json:"market_mood"` // 贪婪 / 中性 / 恐惧
	ScanCount   int                `json:"scan_count"`  // 今日命中信号股票数
	Scans       []*model.DailyScan `json:"scans"`       // 今日扫描明细（供信号看板）
	FromCache   bool               `json:"from_cache"`
	GeneratedAt string             `json:"generated_at"`
}

// ── 核心方法 ──────────────────────────────────────────────────────

// GenerateDailyReport 生成今日复盘简报：
//  1. 从 daily_scans 读取今日所有命中记录
//  2. 构建 Prompt 喂给 AI
//  3. 将报告写入 daily_reports（Upsert，当日可多次刷新）
//  4. 返回 DailyReportDTO
func (s *ReportService) GenerateDailyReport(ctx context.Context) (*DailyReportDTO, error) {
	today := time.Now()

	// ── 1. 读取今日扫描记录 ────────────────────────────────────────
	scans, err := s.scanRepo.ListScansByDate(ctx, today)
	if err != nil {
		return nil, fmt.Errorf("读取今日扫描结果失败: %w", err)
	}

	mood := calcMoodFromScans(scans)

	// ── 2. 构建报告内容 ────────────────────────────────────────────
	var content string
	if len(scans) == 0 {
		content = buildNoSignalReport(today)
	} else {
		// 有 API Key → 调 AI；否则用规则降级
		if s.aiSvc.apiKey != "" {
			prompt := buildReportPrompt(today, scans, mood)
			content, err = s.aiSvc.callEino(ctx, prompt)
			if err != nil {
				s.log.Warn("AI report generation failed, fallback to template",
					zap.Error(err))
				content = buildTemplateReport(today, scans, mood)
			}
		} else {
			s.log.Info("No API key, using template report")
			content = buildTemplateReport(today, scans, mood)
		}
	}

	// ── 3. 写入 / 覆盖 daily_reports ──────────────────────────────
	report := &model.DailyReport{
		ReportDate: today,
		Content:    content,
		MarketMood: mood,
		ScanCount:  len(scans),
	}
	if err := s.scanRepo.UpsertReport(ctx, report); err != nil {
		// 写库失败只记日志，不影响返回
		s.log.Error("UpsertReport failed", zap.Error(err))
	} else {
		s.log.Info("daily report saved",
			zap.String("date", today.Format("2006-01-02")),
			zap.String("mood", mood),
			zap.Int("scan_count", len(scans)),
		)
	}

	return &DailyReportDTO{
		Date:        today.Format("2006-01-02"),
		Content:     content,
		MarketMood:  mood,
		ScanCount:   len(scans),
		Scans:       scans,
		FromCache:   false,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// GetTodayReport 优先从 DB 读已有报告，没有则实时生成一份。
func (s *ReportService) GetTodayReport(ctx context.Context) (*DailyReportDTO, error) {
	today := time.Now()

	// 查 DB
	existing, err := s.scanRepo.GetReportByDate(ctx, today)
	if err != nil {
		return nil, fmt.Errorf("查询日报失败: %w", err)
	}

	// 同时也要拿 scans，供信号看板展示
	scans, _ := s.scanRepo.ListScansByDate(ctx, today)

	if existing != nil {
		return &DailyReportDTO{
			Date:        existing.ReportDate.Format("2006-01-02"),
			Content:     existing.Content,
			MarketMood:  existing.MarketMood,
			ScanCount:   existing.ScanCount,
			Scans:       scans,
			FromCache:   true,
			GeneratedAt: existing.CreatedAt.Format(time.RFC3339),
		}, nil
	}

	// DB 无记录 → 实时生成
	return s.GenerateDailyReport(ctx)
}

// GetReportByDate 查询指定日期的报告。
func (s *ReportService) GetReportByDate(ctx context.Context, date time.Time) (*DailyReportDTO, error) {
	report, err := s.scanRepo.GetReportByDate(ctx, date)
	if err != nil {
		return nil, err
	}
	scans, _ := s.scanRepo.ListScansByDate(ctx, date)

	if report == nil {
		// 历史日期无报告 → 返回空占位
		return &DailyReportDTO{
			Date:       date.Format("2006-01-02"),
			Content:    fmt.Sprintf("## 📭 暂无报告\n\n%s 尚未生成复盘简报。", date.Format("2006-01-02")),
			MarketMood: "中性",
			Scans:      scans,
			FromCache:  false,
		}, nil
	}

	return &DailyReportDTO{
		Date:        report.ReportDate.Format("2006-01-02"),
		Content:     report.Content,
		MarketMood:  report.MarketMood,
		ScanCount:   report.ScanCount,
		Scans:       scans,
		FromCache:   true,
		GeneratedAt: report.CreatedAt.Format(time.RFC3339),
	}, nil
}

// ═══════════════════════════════════════════════════════════════
// Prompt 构建
// ═══════════════════════════════════════════════════════════════

// buildReportPrompt 将今日扫描结果组装成结构化 Prompt 送给 Claude。
func buildReportPrompt(date time.Time, scans []*model.DailyScan, mood string) string {
	var sb strings.Builder

	sb.WriteString("你是一位专业的 A 股市场分析师。请根据以下今日扫描数据，生成一份简洁的「每日市场复盘简报」。\n\n")
	sb.WriteString(fmt.Sprintf("**扫描日期**：%s\n", date.Format("2006-01-02")))
	sb.WriteString(fmt.Sprintf("**当前市场情绪**：%s\n", mood))
	sb.WriteString(fmt.Sprintf("**今日异动股票数**：%d 只\n\n", len(scans)))

	sb.WriteString("## 今日异动股票明细\n\n")
	sb.WriteString("| 代码 | 名称 | 信号 | 收盘价 | 涨跌幅 | 量比 | 均线状态 |\n")
	sb.WriteString("|------|------|------|--------|--------|------|----------|\n")
	for _, sc := range scans {
		signals := strings.Join(sc.Signals, "、")
		maStatus := "跌破MA20"
		if sc.MAStatus == "ABOVE_MA20" {
			maStatus = "站上MA20"
		}
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %.2f | %+.2f%% | %.2f | %s |\n",
			sc.StockCode, sc.StockName, signals,
			sc.Price, sc.PctChg, sc.VolumeRatio, maStatus,
		))
	}

	sb.WriteString(`

---

请按以下格式生成简报（使用 Markdown，每节用 ## 开头）：

## 📊 市场情绪总结
分析今日整体市场情绪（强势/弱势/震荡），给出2-3句简洁判断。列出市场情绪评级：**强势** / **中性** / **弱势**。

## 🔍 重点异动个股点评
针对每只触发信号的股票，各给1-2句点评（分析信号含义、潜在走势）。

## ⚠️ 风险提示
列出2-3条当前市场需关注的主要风险，格式为简短的要点。

---
*以上分析基于量化信号，仅供参考，不构成投资建议。*
`)

	return sb.String()
}

// buildTemplateReport 在无 API Key 时按规则生成报告（降级方案）。
func buildTemplateReport(date time.Time, scans []*model.DailyScan, mood string) string {
	var sb strings.Builder

	moodEmoji := map[string]string{"贪婪": "🔥", "中性": "⚖️", "恐惧": "❄️"}
	emoji := moodEmoji[mood]

	sb.WriteString(fmt.Sprintf("# 每日市场复盘简报 · %s\n\n", date.Format("2006-01-02")))

	sb.WriteString(fmt.Sprintf("## 📊 市场情绪总结\n\n"))
	sb.WriteString(fmt.Sprintf("今日市场情绪：%s **%s**\n\n", emoji, mood))
	sb.WriteString(fmt.Sprintf("今日自选股扫描发现 **%d** 只异动股票，", len(scans)))

	riseCount := 0
	for _, sc := range scans {
		if sc.PctChg > 0 {
			riseCount++
		}
	}
	sb.WriteString(fmt.Sprintf("其中上涨 %d 只、下跌或持平 %d 只。\n\n", riseCount, len(scans)-riseCount))

	sb.WriteString("## 🔍 重点异动个股点评\n\n")
	for _, sc := range scans {
		sb.WriteString(fmt.Sprintf("**%s（%s）**\n\n", sc.StockName, sc.StockCode))
		for _, sig := range sc.Signals {
			switch sig {
			case model.SignalVolumeUp:
				sb.WriteString(fmt.Sprintf("- 📈 量能放大（量比 %.2fx），资金关注度显著提升\n", sc.VolumeRatio))
			case model.SignalMA20Break:
				sb.WriteString(fmt.Sprintf("- 🚀 上穿 MA20（收盘 %.2f），技术形态转强\n", sc.Price))
			case model.SignalBigRise:
				sb.WriteString(fmt.Sprintf("- ⚡ 大幅上涨（涨幅 %+.2f%%），强势突破\n", sc.PctChg))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## ⚠️ 风险提示\n\n")
	sb.WriteString("- 以上信号基于量化规则，不代表股票未来走势\n")
	sb.WriteString("- 追高风险：大涨后需关注是否存在回调压力\n")
	sb.WriteString("- 建议结合基本面和更多技术指标综合研判\n")
	sb.WriteString("\n---\n*此报告由系统自动生成（规则模式），仅供参考，不构成投资建议。*\n")

	return sb.String()
}

// buildNoSignalReport 当日无任何信号时的占位报告。
func buildNoSignalReport(date time.Time) string {
	return fmt.Sprintf(`# 每日市场复盘简报 · %s

## 📊 市场情绪总结

今日自选股扫描**未发现**明显异动信号，市场整体处于平静状态。

## 🔍 重点异动个股点评

今日无自选股触发量能放大、均线突破或大涨信号，建议继续观望。

## ⚠️ 风险提示

- 无信号并不代表没有风险，请持续关注持仓股基本面变化
- 市场平静期可能是蓄势阶段，注意突发消息带来的波动

---
*此报告由系统自动生成，仅供参考。*
`, date.Format("2006-01-02"))
}

// ── 辅助 ──────────────────────────────────────────────────────────

// calcMoodFromScans 基于扫描结果计算市场情绪。
func calcMoodFromScans(scans []*model.DailyScan) string {
	if len(scans) == 0 {
		return "中性"
	}
	positiveCount := 0
	for _, sc := range scans {
		for _, sig := range sc.Signals {
			if sig == model.SignalBigRise || sig == model.SignalMA20Break {
				positiveCount++
				break
			}
		}
	}
	ratio := float64(positiveCount) / float64(len(scans))
	switch {
	case ratio >= 0.6:
		return "贪婪"
	case ratio <= 0.25:
		return "恐惧"
	default:
		return "中性"
	}
}
