package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// StockReportService — 研报服务（优化版）
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 自动重试与 Cookie 刷新
// ═══════════════════════════════════════════════════════════════

const (
	emReportListURL = "https://reportapi.eastmoney.com/report/list" +
		"?pageSize=50&qType=0&pageNo=%d&beginTime=%s&endTime=%s"

	emReportByCodeURL = "https://reportapi.eastmoney.com/report/list" +
		"?pageSize=50&qType=0&pageNo=%d&beginTime=%s&endTime=%s&code=%s"

	emReportDetailURLFmt = "https://data.eastmoney.com/report/zw_stock.jshtml?encodeUrl=%s"
	emReportReferer      = "https://data.eastmoney.com/report/stock.jshtml"

	reportReqTimeout      = 15 * time.Second
	reportPageDelay       = 300 * time.Millisecond
	aiWorkerBatchSize     = 10
)

var allowedRatings = map[string]bool{
	"买入": true, "增持": true, "强烈推荐": true, "买入-A": true, "推荐": true,
}

var excludeTitleKeywords = []string{"新股", "可转债", "双周报", "周报", "月报", "季报"}

const aiSummaryPromptTpl = `请作为高级分析师，从以下研报标题中提炼其核心看涨逻辑，不超过 100 字。如果标题显示为评级或简短信息，则保持简练。标题：%s`

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
	PublishDate  string `json:"publishDate"`
	EncodeUrl    string `json:"encodeUrl"`
}

type StockReportService struct {
	repo  repo.StockReportRepo
	aiSvc *AIAnalysisService
	log   *zap.Logger
}

func NewStockReportService(repo repo.StockReportRepo, aiSvc *AIAnalysisService, log *zap.Logger) *StockReportService {
	return &StockReportService{
		repo:  repo,
		aiSvc: aiSvc,
		log:   log,
	}
}

type SyncResult struct {
	Fetched  int `json:"fetched"`
	Filtered int `json:"filtered"`
	Saved    int `json:"saved"`
}

func (s *StockReportService) SyncReports(ctx context.Context, days int) (*SyncResult, error) {
	if days < 0 {
		days = 0
	}
	now := time.Now()
	endDate := now.Format("2006-01-02")
	startDate := now.AddDate(0, 0, -days).Format("2006-01-02")
	s.log.Info("stock_report: sync market started", zap.String("from", startDate), zap.String("to", endDate))
	allItems, err := s.fetchAllPages(ctx, startDate, endDate, "")
	if err != nil {
		return nil, fmt.Errorf("fetchAllPages: %w", err)
	}
	return s.saveFiltered(ctx, allItems)
}

func (s *StockReportService) SyncByCode(ctx context.Context, stockCode string, days int) (*SyncResult, error) {
	if stockCode == "" {
		return nil, fmt.Errorf("stockCode is required")
	}
	if days <= 0 {
		days = 365
	}
	now := time.Now()
	endDate := now.Format("2006-01-02")
	startDate := now.AddDate(0, 0, -days).Format("2006-01-02")
	s.log.Info("stock_report: sync by code started",
		zap.String("code", stockCode), zap.String("from", startDate), zap.String("to", endDate))
	allItems, err := s.fetchAllPages(ctx, startDate, endDate, stockCode)
	if err != nil {
		return nil, fmt.Errorf("fetchAllPages(%s): %w", stockCode, err)
	}
	return s.saveFiltered(ctx, allItems)
}

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
				zap.Int64("id", report.ID), zap.String("title", report.Title), zap.Error(err))
			continue
		}
		if err := s.repo.UpdateAISummary(ctx, report.ID, summary); err != nil {
			s.log.Warn("stock_report: UpdateAISummary failed", zap.Int64("id", report.ID), zap.Error(err))
			continue
		}
		done++
	}
	s.log.Info("stock_report: AI batch completed", zap.Int("done", done))
	return done, nil
}

func (s *StockReportService) GetReports(ctx context.Context, q repo.StockReportQuery) (*repo.StockReportPage, error) {
	if q.StockCode != "" {
		result, err := s.SyncByCode(ctx, q.StockCode, 365)
		if err != nil {
			s.log.Warn("stock_report: SyncByCode failed, falling back to cache",
				zap.String("code", q.StockCode), zap.Error(err))
		} else {
			s.log.Info("stock_report: on-demand sync done",
				zap.String("code", q.StockCode), zap.Int("saved", result.Saved))
		}
	}
	return s.repo.List(ctx, q)
}

// ─────────────────────────────────────────────────────────────────
// HTTP 抓取（使用统一客户端）
// ─────────────────────────────────────────────────────────────────

func (s *StockReportService) fetchAllPages(ctx context.Context, startDate, endDate, stockCode string) ([]emReportItem, error) {
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
			zap.String("code", stockCode), zap.Int("page", page),
			zap.Int("items", len(items)), zap.Int("total", total))
		if len(all) >= total || len(items) == 0 {
			break
		}
		page++
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(reportPageDelay):
		}
	}
	return all, nil
}

func (s *StockReportService) fetchPage(ctx context.Context, page int, startDate, endDate, stockCode string) ([]emReportItem, int, error) {
	var url string
	if stockCode != "" {
		url = fmt.Sprintf(emReportByCodeURL, page, startDate, endDate, stockCode)
	} else {
		url = fmt.Sprintf(emReportListURL, page, startDate, endDate)
	}

	// 使用统一的 HTTP 客户端
	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, url, &EMRequestOption{
		Timeout:    reportReqTimeout,
		MaxRetries: 3,
		Headers: map[string]string{
			"Referer": emReportReferer,
			"Accept":  "application/json, */*",
		},
	})
	if err != nil {
		return nil, 0, err
	}

	var parsed emReportListResp
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, 0, fmt.Errorf("unmarshal: %w | body: %.200s", err, body)
	}
	return parsed.Data, parsed.Hits, nil
}

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
			s.log.Warn("stock_report: convert item failed", zap.String("infoCode", item.InfoCode), zap.Error(err))
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
		zap.Int("fetched", result.Fetched), zap.Int("filtered", result.Filtered), zap.Int("saved", result.Saved))
	return result, nil
}

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
		if excluded || item.InfoCode == "" {
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
