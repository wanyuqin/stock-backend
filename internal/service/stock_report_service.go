package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 常量与过滤规则
// ═══════════════════════════════════════════════════════════════

const (
	// 全市场研报列表（无 code 参数）
	emReportListURL = "https://reportapi.eastmoney.com/report/list" +
		"?pageSize=50&qType=0&pageNo=%d&beginTime=%s&endTime=%s"

	// 个股研报列表（带 code 参数，精准命中）
	emReportByCodeURL = "https://reportapi.eastmoney.com/report/list" +
		"?pageSize=50&qType=0&pageNo=%d&beginTime=%s&endTime=%s&code=%s"

	emReportDetailURLFmt = "https://data.eastmoney.com/report/zw_stock.jshtml?encodeUrl=%s"
	emReportReferer      = "https://data.eastmoney.com/report/stock.jshtml"

	reportFetchHTTPTimeout = 15 * time.Second
	aiWorkerBatchSize      = 10
)

var allowedRatings = map[string]bool{
	"买入":   true,
	"增持":   true,
	"强烈推荐": true,
	"买入-A": true,
	"推荐":   true,
}

var excludeTitleKeywords = []string{
	"新股", "可转债", "双周报", "周报", "月报", "季报",
}

const aiSummaryPromptTpl = `请作为高级分析师，从以下研报标题中提炼其核心看涨逻辑，不超过 100 字。如果标题显示为评级或简短信息，则保持简练。标题：%s`

// ═══════════════════════════════════════════════════════════════
// 东方财富原始响应结构
// ═══════════════════════════════════════════════════════════════

type emReportListResp struct {
	Hits int            `json:"hits"`
	Size int            `json:"size"`
	Data []emReportItem `json:"data"`
}

type emReportItem struct {
	InfoCode     string `json:"infoCode"`
	StockCode    string `json:"stockCode"`
	StockName    string `json:"stockName"`
	Title        string `json:"title"`
	OrgName      string `json:"orgName"`
	OrgSName     string `json:"orgSName"`
	EmRatingName string `json:"emRatingName"`
	PublishDate  string `json:"publishDate"` // "2026-03-16 00:00:00.000"
	EncodeUrl    string `json:"encodeUrl"`
}

// ═══════════════════════════════════════════════════════════════
// StockReportService
// ═══════════════════════════════════════════════════════════════

type StockReportService struct {
	repo   repo.StockReportRepo
	aiSvc  *AIAnalysisService
	client *http.Client
	log    *zap.Logger
}

func NewStockReportService(
	repo repo.StockReportRepo,
	aiSvc *AIAnalysisService,
	log *zap.Logger,
) *StockReportService {
	return &StockReportService{
		repo:  repo,
		aiSvc: aiSvc,
		client: &http.Client{Timeout: reportFetchHTTPTimeout},
		log:   log,
	}
}

// ─────────────────────────────────────────────────────────────────
// SyncResult 同步结果摘要
// ─────────────────────────────────────────────────────────────────

type SyncResult struct {
	Fetched  int `json:"fetched"`
	Filtered int `json:"filtered"`
	Saved    int `json:"saved"`
}

// ─────────────────────────────────────────────────────────────────
// SyncReports — 全市场采集（定时任务用）
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) SyncReports(ctx context.Context, days int) (*SyncResult, error) {
	if days < 0 {
		days = 0
	}
	now := time.Now()
	endDate := now.Format("2006-01-02")
	startDate := now.AddDate(0, 0, -days).Format("2006-01-02")

	s.log.Info("stock_report: sync market started",
		zap.String("from", startDate), zap.String("to", endDate))

	allItems, err := s.fetchAllPages(ctx, startDate, endDate, "")
	if err != nil {
		return nil, fmt.Errorf("fetchAllPages: %w", err)
	}

	return s.saveFiltered(ctx, allItems)
}

// ─────────────────────────────────────────────────────────────────
// SyncByCode — 个股按需采集（查询时触发，精准命中东财个股接口）
//
// 东财接口区分：
//   全市场：?beginTime=...&endTime=...           （无 code 参数）
//   个  股：?beginTime=...&endTime=...&code=xxx  （有 code 参数，返回指定股票的全量研报）
//
// 使用场景：用户通过 GET /reports/intel?stock_code=xxx 查询时，
// handler 先调此方法从东财拉取最新数据入库，再走数据库分页返回。
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) SyncByCode(ctx context.Context, stockCode string, days int) (*SyncResult, error) {
	if stockCode == "" {
		return nil, fmt.Errorf("stockCode is required")
	}
	if days <= 0 {
		days = 365 // 个股查询默认拉最近 1 年
	}

	now := time.Now()
	endDate := now.Format("2006-01-02")
	startDate := now.AddDate(0, 0, -days).Format("2006-01-02")

	s.log.Info("stock_report: sync by code started",
		zap.String("code", stockCode),
		zap.String("from", startDate),
		zap.String("to", endDate),
	)

	// 使用个股专属接口（带 code 参数）
	allItems, err := s.fetchAllPages(ctx, startDate, endDate, stockCode)
	if err != nil {
		return nil, fmt.Errorf("fetchAllPages(%s): %w", stockCode, err)
	}

	return s.saveFiltered(ctx, allItems)
}

// ─────────────────────────────────────────────────────────────────
// ProcessAISummaries — AI 摘要 Worker
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) ProcessAISummaries(ctx context.Context) (int, error) {
	pending, err := s.repo.ListPending(ctx, aiWorkerBatchSize)
	if err != nil {
		return 0, fmt.Errorf("ListPending: %w", err)
	}
	if len(pending) == 0 {
		s.log.Debug("stock_report: no pending AI summaries")
		return 0, nil
	}

	s.log.Info("stock_report: processing AI summaries", zap.Int("count", len(pending)))

	done := 0
	for _, report := range pending {
		select {
		case <-ctx.Done():
			return done, ctx.Err()
		default:
		}

		summary, err := s.generateSummary(ctx, report.Title)
		if err != nil {
			s.log.Warn("stock_report: AI summary failed",
				zap.Int64("id", report.ID),
				zap.String("title", report.Title),
				zap.Error(err),
			)
			continue
		}

		if err := s.repo.UpdateAISummary(ctx, report.ID, summary); err != nil {
			s.log.Warn("stock_report: UpdateAISummary failed",
				zap.Int64("id", report.ID), zap.Error(err))
			continue
		}
		done++
	}

	s.log.Info("stock_report: AI batch completed", zap.Int("done", done))
	return done, nil
}

// ─────────────────────────────────────────────────────────────────
// GetReports — 分页查询
// 若指定了 stock_code，先触发个股同步，保证数据最新
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) GetReports(ctx context.Context, q repo.StockReportQuery) (*repo.StockReportPage, error) {
	// 有个股筛选时，先从东财拉取该股最新研报（按需同步）
	if q.StockCode != "" {
		result, err := s.SyncByCode(ctx, q.StockCode, 365)
		if err != nil {
			// 同步失败不阻断查询，降级到已有数据
			s.log.Warn("stock_report: SyncByCode failed, falling back to cache",
				zap.String("code", q.StockCode), zap.Error(err))
		} else {
			s.log.Info("stock_report: on-demand sync done",
				zap.String("code", q.StockCode),
				zap.Int("saved", result.Saved),
			)
		}
	}

	return s.repo.List(ctx, q)
}

// ─────────────────────────────────────────────────────────────────
// 内部：分页抓取
// stockCode="" 时走全市场接口；非空时走个股接口（带 code 参数）
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) fetchAllPages(
	ctx context.Context,
	startDate, endDate, stockCode string,
) ([]emReportItem, error) {
	var all []emReportItem
	page := 1
	const maxPages = 20

	for page <= maxPages {
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		default:
		}

		items, total, err := s.fetchPage(ctx, page, startDate, endDate, stockCode)
		if err != nil {
			return nil, fmt.Errorf("page %d: %w", page, err)
		}
		all = append(all, items...)

		s.log.Debug("stock_report: fetched page",
			zap.String("code", stockCode),
			zap.Int("page", page),
			zap.Int("items", len(items)),
			zap.Int("total", total),
		)

		if len(all) >= total || len(items) == 0 {
			break
		}
		page++

		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}

	return all, nil
}

func (s *StockReportService) fetchPage(
	ctx context.Context,
	page int,
	startDate, endDate, stockCode string,
) ([]emReportItem, int, error) {
	// 根据是否有个股代码选择不同 URL 模板
	var url string
	if stockCode != "" {
		url = fmt.Sprintf(emReportByCodeURL, page, startDate, endDate, stockCode)
	} else {
		url = fmt.Sprintf(emReportListURL, page, startDate, endDate)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Referer", emReportReferer)
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, */*")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, 0, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}

	var parsed emReportListResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("unmarshal: %w | body: %.200s", err, body)
	}

	return parsed.Data, parsed.Hits, nil
}

// ─────────────────────────────────────────────────────────────────
// 内部：过滤 + 写入（全市场和个股复用）
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) saveFiltered(ctx context.Context, allItems []emReportItem) (*SyncResult, error) {
	result := &SyncResult{Fetched: len(allItems)}

	filtered := filterReports(allItems)
	result.Filtered = len(filtered)

	if len(filtered) == 0 {
		return result, nil
	}

	reports := make([]*model.StockReport, 0, len(filtered))
	for _, item := range filtered {
		r, err := toStockReport(item)
		if err != nil {
			s.log.Warn("stock_report: convert item failed",
				zap.String("infoCode", item.InfoCode), zap.Error(err))
			continue
		}
		reports = append(reports, r)
	}

	saved, err := s.repo.BulkUpsert(ctx, reports)
	if err != nil {
		return nil, fmt.Errorf("BulkUpsert: %w", err)
	}
	result.Saved = int(saved)

	s.log.Info("stock_report: save done",
		zap.Int("fetched", result.Fetched),
		zap.Int("filtered", result.Filtered),
		zap.Int("saved", result.Saved),
	)
	return result, nil
}

// generateSummary 调用 AI 生成研报摘要。
func (s *StockReportService) generateSummary(ctx context.Context, title string) (string, error) {
	prompt := fmt.Sprintf(aiSummaryPromptTpl, title)
	summary, err := s.aiSvc.callEino(ctx, prompt)
	if err != nil {
		return "", fmt.Errorf("callEino: %w", err)
	}
	summary = strings.TrimSpace(summary)
	if len([]rune(summary)) > 150 {
		runes := []rune(summary)
		summary = string(runes[:150]) + "…"
	}
	return summary, nil
}

// ─────────────────────────────────────────────────────────────────
// 过滤与转换
// ─────────────────────────────────────────────────────────────────

func filterReports(items []emReportItem) []emReportItem {
	result := make([]emReportItem, 0, len(items))
	for _, item := range items {
		if !allowedRatings[item.EmRatingName] {
			continue
		}
		titleLower := strings.ToLower(item.Title)
		excluded := false
		for _, kw := range excludeTitleKeywords {
			if strings.Contains(titleLower, strings.ToLower(kw)) {
				excluded = true
				break
			}
		}
		if excluded {
			continue
		}
		if item.InfoCode == "" {
			continue
		}
		result = append(result, item)
	}
	return result
}

func toStockReport(item emReportItem) (*model.StockReport, error) {
	publishDate, err := time.ParseInLocation("2006-01-02 15:04:05.000", item.PublishDate, time.Local)
	if err != nil {
		publishDate, err = time.ParseInLocation("2006-01-02 15:04:05", item.PublishDate, time.Local)
		if err != nil {
			if len(item.PublishDate) >= 10 {
				publishDate, err = time.ParseInLocation("2006-01-02", item.PublishDate[:10], time.Local)
			}
			if err != nil {
				return nil, fmt.Errorf("parse publishDate %q: %w", item.PublishDate, err)
			}
		}
	}

	detailURL := ""
	if item.EncodeUrl != "" {
		detailURL = fmt.Sprintf(emReportDetailURLFmt, item.EncodeUrl)
	}

	return &model.StockReport{
		InfoCode:    item.InfoCode,
		StockCode:   item.StockCode,
		StockName:   item.StockName,
		Title:       item.Title,
		OrgName:     item.OrgName,
		OrgSName:    item.OrgSName,
		RatingName:  item.EmRatingName,
		PublishDate: publishDate,
		DetailURL:   detailURL,
		IsProcessed: false,
	}, nil
}
