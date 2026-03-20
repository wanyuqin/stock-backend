package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

type RiskService struct {
	repo            repo.RiskRepo
	tradeRepo       repo.TradeLogRepo
	posRepo         repo.PositionGuardianRepo
	tradeSvc        *TradeService
	guardianSvc     *PositionGuardianService
	buyPlanRepo     repo.BuyPlanRepo
	stockRepo       repo.StockRepo
	stockReportRepo repo.StockReportRepo
	dividendSvc     *DividendCalendarService
	log             *zap.Logger
}

func NewRiskService(
	r repo.RiskRepo,
	tradeRepo repo.TradeLogRepo,
	posRepo repo.PositionGuardianRepo,
	tradeSvc *TradeService,
	guardianSvc *PositionGuardianService,
	buyPlanRepo repo.BuyPlanRepo,
	stockRepo repo.StockRepo,
	stockReportRepo repo.StockReportRepo,
	log *zap.Logger,
) *RiskService {
	return &RiskService{
		repo:            r,
		tradeRepo:       tradeRepo,
		posRepo:         posRepo,
		tradeSvc:        tradeSvc,
		guardianSvc:     guardianSvc,
		buyPlanRepo:     buyPlanRepo,
		stockRepo:       stockRepo,
		stockReportRepo: stockReportRepo,
		dividendSvc:     NewDividendCalendarService(log),
		log:             log,
	}
}

type UpdateRiskProfileRequest struct {
	RiskPerTradePct float64 `json:"risk_per_trade_pct" binding:"required"`
	MaxPositionPct  float64 `json:"max_position_pct" binding:"required"`
	AccountSize     float64 `json:"account_size" binding:"required"`
}

type TradePrecheckRequest struct {
	StockCode     string   `json:"stock_code"`
	BuyPrice      float64  `json:"buy_price"`
	StopLossPrice float64  `json:"stop_loss_price"`
	TargetPrice   *float64 `json:"target_price"`
	PlannedAmount float64  `json:"planned_amount"`
	Reason        string   `json:"reason"`
}

type TradePrecheckChecklist struct {
	HasStopLoss        bool   `json:"has_stop_loss"`
	HasTargetPrice     bool   `json:"has_target_price"`
	HasRiskBudget      bool   `json:"has_risk_budget"`
	HasReason          bool   `json:"has_reason"`
	PositionInBounds   bool   `json:"position_in_bounds"`
	CanOpenNewPosition bool   `json:"can_open_new_position"`
	Failure            string `json:"failure"`
}

type DailyRiskState struct {
	Status               string   `json:"status"`
	TodayRealizedPnL     float64  `json:"today_realized_pnl"`
	DailyLossAmount      float64  `json:"daily_loss_amount"`
	DailyLossPct         float64  `json:"daily_loss_pct"`
	LossLimitPct         float64  `json:"loss_limit_pct"`
	LossLimitAmount      float64  `json:"loss_limit_amount"`
	RemainingLossAmount  float64  `json:"remaining_loss_amount"`
	TodayBuyOpenCount    int      `json:"today_buy_open_count"`
	MaxBuyOpenPerDay     int      `json:"max_buy_open_per_day"`
	ConsecutiveLossDays  int      `json:"consecutive_loss_days"`
	ConsecutiveLossLimit int      `json:"consecutive_loss_limit"`
	GuardrailsTriggered  []string `json:"guardrails_triggered"`
	CanOpenNewPosition   bool     `json:"can_open_new_position"`
	Message              string   `json:"message"`
}

type TradePrecheckResult struct {
	Pass                  bool                   `json:"pass"`
	Checklist             TradePrecheckChecklist `json:"checklist"`
	EstimatedVolume       int64                  `json:"estimated_volume"`
	EstimatedPositionPct  float64                `json:"estimated_position_pct"`
	WorstLossAmount       float64                `json:"worst_loss_amount"`
	WorstLossPct          float64                `json:"worst_loss_pct"`
	AllowedLossAmount     float64                `json:"allowed_loss_amount"`
	MaxPositionAmount     float64                `json:"max_position_amount"`
	MaxPositionVolume     int64                  `json:"max_position_volume"`
	SuggestedAdjustVolume int64                  `json:"suggested_adjust_volume"`
	SuggestedAdjustAmount float64                `json:"suggested_adjust_amount"`
	DailyRiskState        DailyRiskState         `json:"daily_risk_state"`
	Advice                string                 `json:"advice"`
}

type PositionSizeSuggestion struct {
	BuyPrice             float64 `json:"buy_price"`
	StopLossPrice        float64 `json:"stop_loss_price"`
	RiskPerShare         float64 `json:"risk_per_share"`
	AllowedLossAmount    float64 `json:"allowed_loss_amount"`
	RawSuggestedVolume   int64   `json:"raw_suggested_volume"`
	SuggestedVolume      int64   `json:"suggested_volume"`
	SuggestedAmount      float64 `json:"suggested_amount"`
	SuggestedPositionPct float64 `json:"suggested_position_pct"`
	MaxPositionAmount    float64 `json:"max_position_amount"`
	MaxPositionVolume    int64   `json:"max_position_volume"`
	Advice               string  `json:"advice"`
}

type SectorExposureItem struct {
	Sector         string   `json:"sector"`
	ExposureAmount float64  `json:"exposure_amount"`
	ExposurePct    float64  `json:"exposure_pct"`
	PositionCount  int      `json:"position_count"`
	StockCodes     []string `json:"stock_codes"`
	OverLimit      bool     `json:"over_limit"`
}

type PortfolioExposureResult struct {
	TotalExposureAmount float64              `json:"total_exposure_amount"`
	SectorLimitPct      float64              `json:"sector_limit_pct"`
	HasOverLimit        bool                 `json:"has_over_limit"`
	Items               []SectorExposureItem `json:"items"`
}

type RiskEventItem struct {
	Date       string `json:"date"`
	StockCode  string `json:"stock_code"`
	StockName  string `json:"stock_name"`
	EventType  string `json:"event_type"`
	RiskLevel  string `json:"risk_level"`
	Title      string `json:"title"`
	ActionHint string `json:"action_hint"`
	Source     string `json:"source"`
}

type EventCalendarResult struct {
	FromDate    string          `json:"from_date"`
	ToDate      string          `json:"to_date"`
	Days        int             `json:"days"`
	HighCount   int             `json:"high_count"`
	MediumCount int             `json:"medium_count"`
	LowCount    int             `json:"low_count"`
	Items       []RiskEventItem `json:"items"`
}

type TodayRiskTodoItem struct {
	ID         string `json:"id"`
	Date       string `json:"date"`
	StockCode  string `json:"stock_code"`
	StockName  string `json:"stock_name"`
	Priority   string `json:"priority"`
	Title      string `json:"title"`
	ActionHint string `json:"action_hint"`
	EventType  string `json:"event_type"`
	Done       bool   `json:"done"`
}

type TodayRiskTodoResult struct {
	Date        string              `json:"date"`
	Total       int                 `json:"total"`
	HighCount   int                 `json:"high_count"`
	MediumCount int                 `json:"medium_count"`
	DoneCount   int                 `json:"done_count"`
	Pending     int                 `json:"pending"`
	Items       []TodayRiskTodoItem `json:"items"`
}

type UpdateTodayRiskTodoRequest struct {
	TodoDate string `json:"todo_date"`
	TodoID   string `json:"todo_id" binding:"required"`
	Done     bool   `json:"done"`
}

type GenerateLowHealthTodoRequest struct {
	Limit int `json:"limit"`
}

type GenerateLowHealthTodoItem struct {
	TodoID      string `json:"todo_id"`
	StockCode   string `json:"stock_code"`
	StockName   string `json:"stock_name"`
	HealthScore int    `json:"health_score"`
	Priority    string `json:"priority"`
}

type GenerateLowHealthTodoResult struct {
	Date      string                      `json:"date"`
	Limit     int                         `json:"limit"`
	Generated int                         `json:"generated"`
	Items     []GenerateLowHealthTodoItem `json:"items"`
}

type WeeklyReviewIssue struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type WeeklyReviewResult struct {
	FromDate       string              `json:"from_date"`
	ToDate         string              `json:"to_date"`
	Days           int                 `json:"days"`
	TotalTrades    int                 `json:"total_trades"`
	BuyCount       int                 `json:"buy_count"`
	SellCount      int                 `json:"sell_count"`
	WinRate        float64             `json:"win_rate"`
	RealizedPnL    float64             `json:"realized_pnl"`
	MaxDrawdownPct float64             `json:"max_drawdown_pct"`
	ProfitDays     int                 `json:"profit_days"`
	LossDays       int                 `json:"loss_days"`
	TopIssues      []WeeklyReviewIssue `json:"top_issues"`
	Summary        string              `json:"summary"`
	Suggestions    []string            `json:"suggestions"`
}

type HealthTrendPoint struct {
	Date  string `json:"date"`
	Score int    `json:"score"`
}

type HealthTrendItem struct {
	StockCode    string             `json:"stock_code"`
	StockName    string             `json:"stock_name"`
	CurrentScore int                `json:"current_score"`
	CurrentLevel string             `json:"current_level"`
	Trend        []HealthTrendPoint `json:"trend"`
}

type HealthTrendResult struct {
	FromDate    string            `json:"from_date"`
	ToDate      string            `json:"to_date"`
	Days        int               `json:"days"`
	GeneratedAt string            `json:"generated_at"`
	Items       []HealthTrendItem `json:"items"`
}

func (s *RiskService) GetProfile(ctx context.Context, userID int64) (*model.UserRiskProfile, error) {
	return s.repo.GetOrInitProfile(ctx, userID)
}

func (s *RiskService) UpdateProfile(ctx context.Context, userID int64, req *UpdateRiskProfileRequest) (*model.UserRiskProfile, error) {
	if req.RiskPerTradePct <= 0 || req.RiskPerTradePct > 10 {
		return nil, fmt.Errorf("risk_per_trade_pct 需在 0~10 之间")
	}
	if req.MaxPositionPct <= 0 || req.MaxPositionPct > 100 {
		return nil, fmt.Errorf("max_position_pct 需在 0~100 之间")
	}
	if req.AccountSize <= 0 {
		return nil, fmt.Errorf("account_size 必须大于 0")
	}
	p := &model.UserRiskProfile{
		UserID:          userID,
		RiskPerTradePct: round2Risk(req.RiskPerTradePct),
		MaxPositionPct:  round2Risk(req.MaxPositionPct),
		AccountSize:     round2Risk(req.AccountSize),
	}
	if err := s.repo.UpsertProfile(ctx, p); err != nil {
		return nil, err
	}
	return s.repo.GetOrInitProfile(ctx, userID)
}

func (s *RiskService) PrecheckTrade(ctx context.Context, userID int64, req *TradePrecheckRequest) (*TradePrecheckResult, error) {
	if req.BuyPrice <= 0 {
		return nil, fmt.Errorf("buy_price 必须大于 0")
	}
	if req.StopLossPrice <= 0 {
		return nil, fmt.Errorf("stop_loss_price 必须大于 0")
	}
	if req.PlannedAmount <= 0 {
		return nil, fmt.Errorf("planned_amount 必须大于 0")
	}

	profile, err := s.repo.GetOrInitProfile(ctx, userID)
	if err != nil {
		return nil, err
	}

	estimatedVolume := int64(math.Floor(req.PlannedAmount/req.BuyPrice/100.0) * 100)
	if estimatedVolume < 0 {
		estimatedVolume = 0
	}
	positionAmount := float64(estimatedVolume) * req.BuyPrice
	riskPerShare := req.BuyPrice - req.StopLossPrice
	worstLossAmount := float64(estimatedVolume) * riskPerShare
	if worstLossAmount < 0 {
		worstLossAmount = 0
	}
	allowedLossAmount := profile.AccountSize * profile.RiskPerTradePct / 100.0
	maxPositionAmount := profile.AccountSize * profile.MaxPositionPct / 100.0
	maxPositionVolume := int64(math.Floor(maxPositionAmount/req.BuyPrice/100.0) * 100)
	if maxPositionVolume < 0 {
		maxPositionVolume = 0
	}
	riskLimitedVolume := int64(0)
	if riskPerShare > 0 {
		riskLimitedVolume = int64(math.Floor(allowedLossAmount/riskPerShare/100.0) * 100)
		if riskLimitedVolume < 0 {
			riskLimitedVolume = 0
		}
	}
	suggestedAdjustVolume := minInt64(riskLimitedVolume, maxPositionVolume)
	if suggestedAdjustVolume < 0 {
		suggestedAdjustVolume = 0
	}
	suggestedAdjustAmount := float64(suggestedAdjustVolume) * req.BuyPrice
	worstLossPct := 0.0
	estPosPct := 0.0
	if profile.AccountSize > 0 {
		worstLossPct = worstLossAmount / profile.AccountSize * 100.0
		estPosPct = positionAmount / profile.AccountSize * 100.0
	}

	dailyState, err := s.GetDailyRiskState(ctx, userID)
	if err != nil {
		return nil, err
	}

	checklist := TradePrecheckChecklist{
		HasStopLoss:        req.StopLossPrice > 0 && req.StopLossPrice < req.BuyPrice,
		HasTargetPrice:     req.TargetPrice != nil && *req.TargetPrice > req.BuyPrice,
		HasRiskBudget:      worstLossAmount > 0 && worstLossAmount <= allowedLossAmount,
		HasReason:          strings.TrimSpace(req.Reason) != "",
		PositionInBounds:   estPosPct > 0 && estPosPct <= profile.MaxPositionPct,
		CanOpenNewPosition: dailyState.CanOpenNewPosition,
	}

	failures := make([]string, 0, 4)
	if !checklist.HasStopLoss {
		failures = append(failures, "止损价必须小于买入价")
	}
	if !checklist.HasTargetPrice {
		failures = append(failures, "请设置高于买入价的目标价")
	}
	if !checklist.HasRiskBudget {
		failures = append(failures, "本单最坏亏损超出单笔风险预算")
	}
	if !checklist.HasReason {
		failures = append(failures, "请填写买入理由")
	}
	if !checklist.PositionInBounds {
		failures = append(failures, "本单仓位超出单票仓位上限")
	}
	if !checklist.CanOpenNewPosition {
		failures = append(failures, "当日亏损已触发熔断，仅限制买入开仓（卖出/减仓不受影响）")
	}

	checklist.Failure = strings.Join(failures, "；")
	pass := checklist.Failure == ""
	advice := "检查通过，可执行计划"
	if !pass {
		advice = "检查未通过，请先修正红色项后再提交"
		if suggestedAdjustVolume > 0 {
			advice = fmt.Sprintf("%s；可先调整为 %d 股（约 %.0f 元）", advice, suggestedAdjustVolume, suggestedAdjustAmount)
		}
	}

	res := &TradePrecheckResult{
		Pass:                  pass,
		Checklist:             checklist,
		EstimatedVolume:       estimatedVolume,
		EstimatedPositionPct:  round2Risk(estPosPct),
		WorstLossAmount:       round2Risk(worstLossAmount),
		WorstLossPct:          round2Risk(worstLossPct),
		AllowedLossAmount:     round2Risk(allowedLossAmount),
		MaxPositionAmount:     round2Risk(maxPositionAmount),
		MaxPositionVolume:     maxPositionVolume,
		SuggestedAdjustVolume: suggestedAdjustVolume,
		SuggestedAdjustAmount: round2Risk(suggestedAdjustAmount),
		DailyRiskState:        *dailyState,
		Advice:                advice,
	}

	_ = s.repo.CreatePrecheckLog(ctx, &model.TradePrecheckLog{
		UserID:          userID,
		StockCode:       strings.ToUpper(strings.TrimSpace(req.StockCode)),
		BuyPrice:        req.BuyPrice,
		StopLossPrice:   req.StopLossPrice,
		TargetPrice:     req.TargetPrice,
		PlannedAmount:   req.PlannedAmount,
		Reason:          req.Reason,
		EstimatedVolume: estimatedVolume,
		WorstLossAmount: res.WorstLossAmount,
		WorstLossPct:    res.WorstLossPct,
		Pass:            res.Pass,
		FailReason:      checklist.Failure,
	})

	return res, nil
}

func (s *RiskService) SuggestPositionSize(ctx context.Context, userID int64, buyPrice, stopLossPrice float64) (*PositionSizeSuggestion, error) {
	if buyPrice <= 0 {
		return nil, fmt.Errorf("buy_price 必须大于 0")
	}
	if stopLossPrice <= 0 || stopLossPrice >= buyPrice {
		return nil, fmt.Errorf("stop_loss_price 必须大于 0 且小于 buy_price")
	}

	profile, err := s.repo.GetOrInitProfile(ctx, userID)
	if err != nil {
		return nil, err
	}

	allowedLossAmount := profile.AccountSize * profile.RiskPerTradePct / 100.0
	riskPerShare := buyPrice - stopLossPrice
	rawVol := int64(math.Floor(allowedLossAmount / riskPerShare))
	if rawVol < 0 {
		rawVol = 0
	}
	suggestedVol := int64(math.Floor(float64(rawVol)/100.0) * 100)
	if suggestedVol < 0 {
		suggestedVol = 0
	}

	maxPositionAmount := profile.AccountSize * profile.MaxPositionPct / 100.0
	maxPositionVol := int64(math.Floor(maxPositionAmount/buyPrice/100.0) * 100)
	if maxPositionVol < 0 {
		maxPositionVol = 0
	}
	if suggestedVol > maxPositionVol {
		suggestedVol = maxPositionVol
	}

	suggestedAmount := float64(suggestedVol) * buyPrice
	suggestedPosPct := 0.0
	if profile.AccountSize > 0 {
		suggestedPosPct = suggestedAmount / profile.AccountSize * 100
	}

	advice := "建议按该股数分批建仓"
	if suggestedVol == 0 {
		advice = "当前风险参数下建议不建仓，请提高止损距离或降低买入价"
	}

	return &PositionSizeSuggestion{
		BuyPrice:             round2Risk(buyPrice),
		StopLossPrice:        round2Risk(stopLossPrice),
		RiskPerShare:         round2Risk(riskPerShare),
		AllowedLossAmount:    round2Risk(allowedLossAmount),
		RawSuggestedVolume:   rawVol,
		SuggestedVolume:      suggestedVol,
		SuggestedAmount:      round2Risk(suggestedAmount),
		SuggestedPositionPct: round2Risk(suggestedPosPct),
		MaxPositionAmount:    round2Risk(maxPositionAmount),
		MaxPositionVolume:    maxPositionVol,
		Advice:               advice,
	}, nil
}

func (s *RiskService) GetPortfolioExposure(ctx context.Context, userID int64) (*PortfolioExposureResult, error) {
	report, err := s.tradeSvc.GetPerformance(ctx, userID)
	if err != nil {
		return nil, err
	}
	sectorLimitPct := 30.0
	if len(report.Positions) == 0 {
		return &PortfolioExposureResult{
			TotalExposureAmount: 0,
			SectorLimitPct:      sectorLimitPct,
			HasOverLimit:        false,
			Items:               []SectorExposureItem{},
		}, nil
	}

	type agg struct {
		amount float64
		codes  []string
	}
	sectorMap := map[string]*agg{}
	total := 0.0
	for _, p := range report.Positions {
		if p.HoldVolume <= 0 || p.CurrentPrice <= 0 {
			continue
		}
		amount := float64(p.HoldVolume) * p.CurrentPrice
		total += amount
		sector := "其他"
		if stock, e := s.stockRepo.GetByCode(ctx, p.StockCode); e == nil && strings.TrimSpace(stock.Sector) != "" {
			sector = strings.TrimSpace(stock.Sector)
		}
		if sectorMap[sector] == nil {
			sectorMap[sector] = &agg{amount: 0, codes: []string{}}
		}
		sectorMap[sector].amount += amount
		sectorMap[sector].codes = append(sectorMap[sector].codes, p.StockCode)
	}

	items := make([]SectorExposureItem, 0, len(sectorMap))
	hasOverLimit := false
	for sector, a := range sectorMap {
		pct := 0.0
		if total > 0 {
			pct = a.amount / total * 100
		}
		over := pct > sectorLimitPct
		if over {
			hasOverLimit = true
		}
		items = append(items, SectorExposureItem{
			Sector:         sector,
			ExposureAmount: round2Risk(a.amount),
			ExposurePct:    round2Risk(pct),
			PositionCount:  len(a.codes),
			StockCodes:     a.codes,
			OverLimit:      over,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ExposureAmount > items[j].ExposureAmount
	})
	return &PortfolioExposureResult{
		TotalExposureAmount: round2Risk(total),
		SectorLimitPct:      sectorLimitPct,
		HasOverLimit:        hasOverLimit,
		Items:               items,
	}, nil
}

func (s *RiskService) GetDailyRiskState(ctx context.Context, userID int64) (*DailyRiskState, error) {
	profile, err := s.repo.GetOrInitProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	logs, err := s.tradeRepo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	today := time.Now()
	lossLimitPct := 2.0
	maxBuyOpenPerDay := 2
	consecutiveLossLimit := 2
	lossLimitAmount := profile.AccountSize * lossLimitPct / 100.0
	todayRealized := calcTodayRealizedPnL(logs, today)
	todayBuyOpenCount := countTodayBuyOpens(logs, today)
	consecutiveLossDays := calcConsecutiveLossDays(logs, today)
	dailyLossAmount := 0.0
	if todayRealized < 0 {
		dailyLossAmount = -todayRealized
	}
	dailyLossPct := 0.0
	if profile.AccountSize > 0 {
		dailyLossPct = dailyLossAmount / profile.AccountSize * 100
	}

	status := "SAFE"
	canOpen := true
	msg := "今日风险状态安全，可正常开仓"
	guardrails := make([]string, 0, 4)
	if dailyLossAmount >= lossLimitAmount {
		status = "BLOCK"
		canOpen = false
		guardrails = append(guardrails, "当日亏损达到熔断阈值，仅限制买入开仓（卖出/减仓不受影响）")
	} else if dailyLossAmount >= lossLimitAmount*0.7 {
		status = "WARN"
		msg = "当日亏损接近熔断阈值，请谨慎新开仓（卖出/减仓不受影响）"
	}

	if todayBuyOpenCount >= maxBuyOpenPerDay {
		canOpen = false
		status = "BLOCK"
		guardrails = append(guardrails, fmt.Sprintf("今日新开仓次数已达上限（%d/%d）", todayBuyOpenCount, maxBuyOpenPerDay))
	}
	if consecutiveLossDays >= consecutiveLossLimit {
		canOpen = false
		status = "BLOCK"
		guardrails = append(guardrails, fmt.Sprintf("连续亏损天数已达限制（%d/%d），今日禁止新开仓", consecutiveLossDays, consecutiveLossLimit))
	}
	if len(guardrails) > 0 {
		msg = strings.Join(guardrails, "；")
	}
	remaining := lossLimitAmount - dailyLossAmount
	if remaining < 0 {
		remaining = 0
	}
	return &DailyRiskState{
		Status:               status,
		TodayRealizedPnL:     round2Risk(todayRealized),
		DailyLossAmount:      round2Risk(dailyLossAmount),
		DailyLossPct:         round2Risk(dailyLossPct),
		LossLimitPct:         lossLimitPct,
		LossLimitAmount:      round2Risk(lossLimitAmount),
		RemainingLossAmount:  round2Risk(remaining),
		TodayBuyOpenCount:    todayBuyOpenCount,
		MaxBuyOpenPerDay:     maxBuyOpenPerDay,
		ConsecutiveLossDays:  consecutiveLossDays,
		ConsecutiveLossLimit: consecutiveLossLimit,
		GuardrailsTriggered:  guardrails,
		CanOpenNewPosition:   canOpen,
		Message:              msg,
	}, nil
}

func (s *RiskService) GetEventCalendar(ctx context.Context, userID int64, days int) (*EventCalendarResult, error) {
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}
	now := time.Now()
	fromDate := now.Format("2006-01-02")
	toDate := now.AddDate(0, 0, days).Format("2006-01-02")
	endTime := now.AddDate(0, 0, days+1)

	report, err := s.tradeSvc.GetPerformance(ctx, userID)
	if err != nil {
		return nil, err
	}

	codes := make([]string, 0, len(report.Positions))
	codeName := map[string]string{}
	for _, p := range report.Positions {
		if p.HoldVolume <= 0 {
			continue
		}
		codes = append(codes, p.StockCode)
		if stock, e := s.stockRepo.GetByCode(ctx, p.StockCode); e == nil {
			codeName[p.StockCode] = stock.Name
		}
	}

	items := make([]RiskEventItem, 0, 32)
	if len(codes) > 0 {
		divs, _ := s.dividendSvc.GetUpcomingDividends(ctx, codes)
		for _, d := range divs {
			level := "LOW"
			hint := "关注除权后价格波动，避免误判为突发下跌"
			if d.DaysUntil <= 1 {
				level = "HIGH"
				hint = "临近除权日，优先评估是否减仓或规避跳空"
			} else if d.DaysUntil <= 3 {
				level = "MEDIUM"
				hint = "未来几天有除权事件，谨慎追涨加仓"
			}
			items = append(items, RiskEventItem{
				Date:       d.ExRightDate,
				StockCode:  d.StockCode,
				StockName:  coalesceName(d.StockName, codeName[d.StockCode], d.StockCode),
				EventType:  "DIVIDEND_EX_DATE",
				RiskLevel:  level,
				Title:      d.PlanDesc,
				ActionHint: hint,
				Source:     "dividend",
			})
		}

		for _, code := range codes {
			page, e := s.stockReportRepo.List(ctx, repo.StockReportQuery{
				StockCode: code,
				Page:      1,
				Limit:     3,
			})
			if e != nil || page == nil {
				continue
			}
			for _, rp := range page.Items {
				if rp.PublishDate.Before(now.AddDate(0, 0, -3)) || rp.PublishDate.After(endTime) {
					continue
				}
				items = append(items, RiskEventItem{
					Date:       rp.PublishDate.Format("2006-01-02"),
					StockCode:  rp.StockCode,
					StockName:  coalesceName(rp.StockName, codeName[rp.StockCode], rp.StockCode),
					EventType:  "RESEARCH_REPORT",
					RiskLevel:  "MEDIUM",
					Title:      fmt.Sprintf("%s：%s", rp.OrgSName, truncateText(rp.Title, 40)),
					ActionHint: "研报催化可能带来波动，关注次日高开低走风险",
					Source:     "stock_report",
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Date == items[j].Date {
			return riskLevelWeight(items[i].RiskLevel) > riskLevelWeight(items[j].RiskLevel)
		}
		return items[i].Date < items[j].Date
	})

	high, medium, low := 0, 0, 0
	for _, it := range items {
		switch it.RiskLevel {
		case "HIGH":
			high++
		case "MEDIUM":
			medium++
		default:
			low++
		}
	}

	return &EventCalendarResult{
		FromDate:    fromDate,
		ToDate:      toDate,
		Days:        days,
		HighCount:   high,
		MediumCount: medium,
		LowCount:    low,
		Items:       items,
	}, nil
}

func (s *RiskService) GetTodayRiskTodo(ctx context.Context, userID int64) (*TodayRiskTodoResult, error) {
	today := time.Now().Format("2006-01-02")
	calendar, err := s.GetEventCalendar(ctx, userID, 1)
	if err != nil {
		return nil, err
	}
	daily, err := s.GetDailyRiskState(ctx, userID)
	if err != nil {
		return nil, err
	}
	doneMap, err := s.repo.ListTodoStatusByDate(ctx, userID, today)
	if err != nil {
		return nil, err
	}
	openVolByCode, openVolErr := s.getOpenVolumeByCode(ctx, userID)
	if openVolErr != nil {
		s.log.Warn("GetTodayRiskTodo: compute open volume failed", zap.Error(openVolErr))
	}

	items := make([]TodayRiskTodoItem, 0, 16)
	if daily.Status == "BLOCK" || daily.Status == "WARN" {
		items = append(items, TodayRiskTodoItem{
			ID:         "SYS|DAILY_RISK",
			Date:       today,
			StockCode:  "SYSTEM",
			StockName:  "组合风控",
			Priority:   mapDailyPriority(daily.Status),
			Title:      fmt.Sprintf("当日风险状态：%s", daily.Status),
			ActionHint: daily.Message,
			EventType:  "DAILY_RISK_STATE",
			Done:       doneMap["SYS|DAILY_RISK"],
		})
	}
	for _, ev := range calendar.Items {
		if ev.RiskLevel != "HIGH" && ev.RiskLevel != "MEDIUM" {
			continue
		}
		items = append(items, TodayRiskTodoItem{
			ID:         fmt.Sprintf("%s|%s|%s", ev.Date, ev.StockCode, ev.EventType),
			Date:       ev.Date,
			StockCode:  ev.StockCode,
			StockName:  ev.StockName,
			Priority:   ev.RiskLevel,
			Title:      ev.Title,
			ActionHint: ev.ActionHint,
			EventType:  ev.EventType,
			Done:       doneMap[fmt.Sprintf("%s|%s|%s", ev.Date, ev.StockCode, ev.EventType)],
		})
	}

	// 注入持仓守护高风险触发器：止损/减仓信号 + 接近目标价提示
	if s.guardianSvc != nil {
		diag, diagErr := s.guardianSvc.DiagnoseAll(ctx)
		if diagErr != nil {
			s.log.Warn("GetTodayRiskTodo: guardian diagnose failed", zap.Error(diagErr))
		} else {
			for _, d := range diag {
				if d == nil || d.Position == nil || d.Position.Quantity <= 0 {
					continue
				}
				code := strings.ToUpper(strings.TrimSpace(d.StockCode))
				if vol, ok := openVolByCode[code]; ok && vol <= 0 {
					// 交易日志显示该票已无净持仓，跳过守护类待办（避免卖出后误提醒）
					continue
				}

				switch d.Signal {
				case model.SignalStopLoss, model.SignalSell, model.SignalSellT:
					todoID := fmt.Sprintf("PG|%s|%s", d.StockCode, d.Signal)
					title := fmt.Sprintf("持仓风控信号：%s", d.Signal)
					hint := d.ActionDirective
					if strings.TrimSpace(hint) == "" {
						hint = d.Snapshot.ActionSummary
					}
					items = append(items, TodayRiskTodoItem{
						ID:         todoID,
						Date:       today,
						StockCode:  d.StockCode,
						StockName:  coalesceName(d.StockName, d.StockCode),
						Priority:   mapGuardianPriority(d.Signal),
						Title:      title,
						ActionHint: hint,
						EventType:  "POSITION_SIGNAL",
						Done:       doneMap[todoID],
					})
				}

				if d.Snapshot.NearTargetNotice {
					todoID := fmt.Sprintf("PG|%s|NEAR_TARGET", d.StockCode)
					targetHint := "接近目标价，考虑先分批止盈"
					if d.Snapshot.SuggestQty > 0 {
						targetHint = fmt.Sprintf("接近目标价，建议先止盈 %d 股", d.Snapshot.SuggestQty)
					}
					items = append(items, TodayRiskTodoItem{
						ID:         todoID,
						Date:       today,
						StockCode:  d.StockCode,
						StockName:  coalesceName(d.StockName, d.StockCode),
						Priority:   "MEDIUM",
						Title:      "接近目标价提醒",
						ActionHint: targetHint,
						EventType:  "NEAR_TARGET",
						Done:       doneMap[todoID],
					})
				}
			}

			// 注入低健康分优先处理清单（默认取最低分前3）
			for _, low := range pickLowHealthTodos(diag, 3) {
				items = append(items, TodayRiskTodoItem{
					ID:         low.TodoID,
					Date:       today,
					StockCode:  low.StockCode,
					StockName:  low.StockName,
					Priority:   low.Priority,
					Title:      fmt.Sprintf("持仓健康分偏低：%d 分", low.HealthScore),
					ActionHint: buildLowHealthActionHint(low.HealthScore),
					EventType:  "LOW_HEALTH",
					Done:       doneMap[low.TodoID],
				})
			}
		}
	}

	items = normalizeTodayTodoItems(items)

	// 注入买入计划有效期提醒：临期/到期计划进入今日待办
	if s.buyPlanRepo != nil {
		plans, planErr := s.buyPlanRepo.ListByUser(ctx, userID, []model.BuyPlanStatus{
			model.BuyPlanStatusWatching,
			model.BuyPlanStatusReady,
		})
		if planErr != nil {
			s.log.Warn("GetTodayRiskTodo: list buy plans failed", zap.Error(planErr))
		} else {
			now := time.Now()
			startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			for _, p := range plans {
				if p == nil || p.ValidUntil == nil {
					continue
				}
				validDate := time.Date(p.ValidUntil.Year(), p.ValidUntil.Month(), p.ValidUntil.Day(), 0, 0, 0, 0, startToday.Location())
				daysLeft := int(validDate.Sub(startToday).Hours() / 24)
				if daysLeft > 7 {
					continue
				}

				priority := "MEDIUM"
				title := "买入计划临近到期，请复盘去留"
				actionHint := fmt.Sprintf("计划有效期剩余 %d 天，建议复盘：继续观察 / 调整价位 / 放弃", daysLeft)
				if daysLeft <= 1 {
					priority = "HIGH"
					title = "买入计划即将到期（1天内）"
					actionHint = "计划即将到期，请今天完成复盘并明确：执行、延期或放弃"
				}
				if daysLeft < 0 {
					priority = "HIGH"
					title = "买入计划已到期"
					actionHint = "该计划已过有效期，请立即复盘并处理：延期、执行或放弃"
				}

				stockName := p.StockName
				if strings.TrimSpace(stockName) == "" {
					stockName = p.StockCode
				}
				todoID := fmt.Sprintf("BP|%d|EXPIRY", p.ID)
				items = append(items, TodayRiskTodoItem{
					ID:         todoID,
					Date:       today,
					StockCode:  p.StockCode,
					StockName:  stockName,
					Priority:   priority,
					Title:      title,
					ActionHint: actionHint,
					EventType:  "PLAN_EXPIRY",
					Done:       doneMap[todoID],
				})
			}
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].Date == items[j].Date {
			return riskLevelWeight(items[i].Priority) > riskLevelWeight(items[j].Priority)
		}
		return items[i].Date < items[j].Date
	})

	high, medium, doneCount := 0, 0, 0
	for _, it := range items {
		if it.Priority == "HIGH" {
			high++
		} else if it.Priority == "MEDIUM" {
			medium++
		}
		if it.Done {
			doneCount++
		}
	}
	return &TodayRiskTodoResult{
		Date:        today,
		Total:       len(items),
		HighCount:   high,
		MediumCount: medium,
		DoneCount:   doneCount,
		Pending:     len(items) - doneCount,
		Items:       items,
	}, nil
}

func (s *RiskService) UpdateTodayRiskTodoStatus(ctx context.Context, userID int64, req *UpdateTodayRiskTodoRequest) error {
	if req == nil {
		return fmt.Errorf("request is nil")
	}
	todoID := strings.TrimSpace(req.TodoID)
	if todoID == "" {
		return fmt.Errorf("todo_id 不能为空")
	}
	todoDate := strings.TrimSpace(req.TodoDate)
	if todoDate == "" {
		todoDate = time.Now().Format("2006-01-02")
	}
	if len(todoDate) != len("2006-01-02") {
		return fmt.Errorf("todo_date 格式错误")
	}
	if _, err := time.Parse("2006-01-02", todoDate); err != nil {
		return fmt.Errorf("todo_date 格式错误")
	}
	return s.repo.UpsertTodoStatus(ctx, userID, todoDate, todoID, req.Done)
}

func (s *RiskService) GenerateLowHealthTodo(ctx context.Context, userID int64, req *GenerateLowHealthTodoRequest) (*GenerateLowHealthTodoResult, error) {
	if s.guardianSvc == nil {
		return nil, fmt.Errorf("guardian service not initialized")
	}
	limit := 3
	if req != nil && req.Limit > 0 {
		limit = req.Limit
	}
	if limit > 10 {
		limit = 10
	}

	diag, err := s.guardianSvc.DiagnoseAll(ctx)
	if err != nil {
		return nil, err
	}
	openVolByCode, openVolErr := s.getOpenVolumeByCode(ctx, userID)
	if openVolErr != nil {
		s.log.Warn("GenerateLowHealthTodo: compute open volume failed", zap.Error(openVolErr))
	}
	items := pickLowHealthTodos(diag, limit)
	if len(openVolByCode) > 0 {
		filtered := make([]GenerateLowHealthTodoItem, 0, len(items))
		for _, it := range items {
			code := strings.ToUpper(strings.TrimSpace(it.StockCode))
			if vol, ok := openVolByCode[code]; ok && vol <= 0 {
				continue
			}
			filtered = append(filtered, it)
		}
		items = filtered
	}
	today := time.Now().Format("2006-01-02")
	for _, it := range items {
		if e := s.repo.UpsertTodoStatus(ctx, userID, today, it.TodoID, false); e != nil {
			return nil, e
		}
	}
	return &GenerateLowHealthTodoResult{
		Date:      today,
		Limit:     limit,
		Generated: len(items),
		Items:     items,
	}, nil
}

func (s *RiskService) GetHealthTrend(ctx context.Context, userID int64, days int) (*HealthTrendResult, error) {
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}
	if s.guardianSvc == nil {
		return &HealthTrendResult{
			FromDate:    time.Now().Format("2006-01-02"),
			ToDate:      time.Now().Format("2006-01-02"),
			Days:        days,
			GeneratedAt: time.Now().Format(time.RFC3339),
			Items:       []HealthTrendItem{},
		}, nil
	}

	current, err := s.guardianSvc.DiagnoseAll(ctx)
	if err != nil {
		return nil, err
	}
	if len(current) == 0 {
		return &HealthTrendResult{
			FromDate:    time.Now().Format("2006-01-02"),
			ToDate:      time.Now().Format("2006-01-02"),
			Days:        days,
			GeneratedAt: time.Now().Format(time.RFC3339),
			Items:       []HealthTrendItem{},
		}, nil
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	codes := make([]string, 0, len(current))
	curByCode := make(map[string]*PositionDiagnosisResult, len(current))
	for _, d := range current {
		if d == nil {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(d.StockCode))
		if code == "" {
			continue
		}
		codes = append(codes, code)
		curByCode[code] = d
	}

	historyByCode := make(map[string]map[string]int, len(codes))
	if s.posRepo != nil && len(codes) > 0 {
		rows, listErr := s.posRepo.ListDiagnosticsByCodes(ctx, codes, start)
		if listErr != nil {
			s.log.Warn("GetHealthTrend: list diagnostics failed", zap.Error(listErr))
		} else {
			for _, row := range rows {
				if row == nil {
					continue
				}
				code := strings.ToUpper(strings.TrimSpace(row.StockCode))
				if code == "" {
					continue
				}
				score, _ := calcHealthScore(row.SignalType, &row.DataSnapshot)
				day := row.CreatedAt.In(now.Location()).Format("2006-01-02")
				if historyByCode[code] == nil {
					historyByCode[code] = map[string]int{}
				}
				historyByCode[code][day] = score
			}
		}
	}

	items := make([]HealthTrendItem, 0, len(curByCode))
	for _, code := range codes {
		cur := curByCode[code]
		if cur == nil {
			continue
		}
		trend := make([]HealthTrendPoint, 0, days)
		lastScore := cur.HealthScore
		dayScores := historyByCode[code]
		for i := 0; i < days; i++ {
			d := start.AddDate(0, 0, i)
			dayKey := d.Format("2006-01-02")
			if dayScores != nil {
				if v, ok := dayScores[dayKey]; ok {
					lastScore = v
				}
			}
			trend = append(trend, HealthTrendPoint{
				Date:  dayKey,
				Score: lastScore,
			})
		}
		if len(trend) > 0 {
			trend[len(trend)-1].Score = cur.HealthScore
		}
		items = append(items, HealthTrendItem{
			StockCode:    code,
			StockName:    coalesceName(cur.StockName, code),
			CurrentScore: cur.HealthScore,
			CurrentLevel: resolveHealthLevel(cur.HealthScore, cur.HealthLevel),
			Trend:        trend,
		})
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].CurrentScore < items[j].CurrentScore
	})

	return &HealthTrendResult{
		FromDate:    start.Format("2006-01-02"),
		ToDate:      now.Format("2006-01-02"),
		Days:        days,
		GeneratedAt: now.Format(time.RFC3339),
		Items:       items,
	}, nil
}

func pickLowHealthTodos(diag []*PositionDiagnosisResult, limit int) []GenerateLowHealthTodoItem {
	if limit <= 0 {
		limit = 3
	}
	candidates := make([]GenerateLowHealthTodoItem, 0, limit)
	for _, d := range diag {
		if d == nil || d.Position == nil || d.Position.Quantity <= 0 {
			continue
		}
		if d.HealthScore >= 70 {
			continue
		}
		priority := "MEDIUM"
		if d.HealthScore < 45 || d.Signal == model.SignalStopLoss {
			priority = "HIGH"
		}
		candidates = append(candidates, GenerateLowHealthTodoItem{
			TodoID:      fmt.Sprintf("PG|%s|LOW_HEALTH", d.StockCode),
			StockCode:   d.StockCode,
			StockName:   coalesceName(d.StockName, d.StockCode),
			HealthScore: d.HealthScore,
			Priority:    priority,
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].HealthScore == candidates[j].HealthScore {
			return riskLevelWeight(candidates[i].Priority) > riskLevelWeight(candidates[j].Priority)
		}
		return candidates[i].HealthScore < candidates[j].HealthScore
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func (s *RiskService) getOpenVolumeByCode(ctx context.Context, userID int64) (map[string]int64, error) {
	if s.tradeRepo == nil {
		return map[string]int64{}, nil
	}
	logs, err := s.tradeRepo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	volByCode := make(map[string]int64, 64)
	for _, l := range logs {
		if l == nil {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(l.StockCode))
		if code == "" {
			continue
		}
		switch l.Action {
		case model.TradeActionBuy:
			volByCode[code] += l.Volume
		case model.TradeActionSell:
			volByCode[code] -= l.Volume
			if volByCode[code] < 0 {
				volByCode[code] = 0
			}
		}
	}
	return volByCode, nil
}

func buildLowHealthActionHint(score int) string {
	if score < 45 {
		return "健康分危险，建议今天优先检查止损位并先执行减仓/止损动作"
	}
	return "健康分偏低，建议今天复核买入逻辑、止损位和仓位，优先处理弱势仓位"
}

func resolveHealthLevel(score int, fallback string) string {
	if fallback == "GOOD" || fallback == "WARN" || fallback == "DANGER" {
		return fallback
	}
	if score < 45 {
		return "DANGER"
	}
	if score < 70 {
		return "WARN"
	}
	return "GOOD"
}

func normalizeTodayTodoItems(items []TodayRiskTodoItem) []TodayRiskTodoItem {
	if len(items) == 0 {
		return items
	}

	byID := make(map[string]TodayRiskTodoItem, len(items))
	for _, it := range items {
		old, ok := byID[it.ID]
		if !ok {
			byID[it.ID] = it
			continue
		}
		if riskLevelWeight(it.Priority) > riskLevelWeight(old.Priority) {
			old.Priority = it.Priority
			old.Title = it.Title
			old.ActionHint = it.ActionHint
		}
		old.Done = old.Done || it.Done
		byID[it.ID] = old
	}

	dedup := make([]TodayRiskTodoItem, 0, len(byID))
	for _, it := range byID {
		dedup = append(dedup, it)
	}

	stockSignalPriority := map[string]int{}
	stockHasStopLossSignal := map[string]bool{}
	for _, it := range dedup {
		if it.EventType != "POSITION_SIGNAL" {
			continue
		}
		code := strings.ToUpper(strings.TrimSpace(it.StockCode))
		if code == "" {
			continue
		}
		p := riskLevelWeight(it.Priority)
		if p > stockSignalPriority[code] {
			stockSignalPriority[code] = p
		}
		if strings.Contains(strings.ToUpper(it.Title), "STOP_LOSS") || p >= riskLevelWeight("HIGH") {
			stockHasStopLossSignal[code] = true
		}
	}

	out := make([]TodayRiskTodoItem, 0, len(dedup))
	for _, it := range dedup {
		code := strings.ToUpper(strings.TrimSpace(it.StockCode))
		if code != "" {
			if it.EventType == "LOW_HEALTH" && stockSignalPriority[code] > 0 {
				continue
			}
			if it.EventType == "NEAR_TARGET" && stockHasStopLossSignal[code] {
				continue
			}
		}
		out = append(out, it)
	}
	return out
}

func (s *RiskService) GetWeeklyReview(ctx context.Context, userID int64, days int) (*WeeklyReviewResult, error) {
	if days <= 0 {
		days = 7
	}
	if days > 30 {
		days = 30
	}

	profile, err := s.repo.GetOrInitProfile(ctx, userID)
	if err != nil {
		return nil, err
	}
	logs, err := s.tradeRepo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(days - 1))
	end := start.AddDate(0, 0, days)

	buyCount, sellCount := 0, 0
	for _, l := range logs {
		if l.TradedAt.Before(start) || !l.TradedAt.Before(end) {
			continue
		}
		if l.Action == model.TradeActionBuy {
			buyCount++
		}
		if l.Action == model.TradeActionSell {
			sellCount++
		}
	}

	periodSellPnL := calcPeriodSellPnL(logs, start, end, now.Location())
	realizedPnL := 0.0
	winSell := 0
	dailyPnL := map[string]float64{}
	for _, v := range periodSellPnL {
		realizedPnL += v.pnl
		if v.pnl > 0 {
			winSell++
		}
		dateKey := v.tradedAt.In(now.Location()).Format("2006-01-02")
		dailyPnL[dateKey] += v.pnl
	}

	winRate := 0.0
	if sellCount > 0 {
		winRate = float64(winSell) / float64(sellCount) * 100
	}

	// 周期内最大回撤（以账户规模作为分母，便于小白理解）
	maxDDPct := 0.0
	cum := 0.0
	peak := 0.0
	profitDays, lossDays := 0, 0
	for i := 0; i < days; i++ {
		d := start.AddDate(0, 0, i).Format("2006-01-02")
		v := dailyPnL[d]
		if v > 0 {
			profitDays++
		} else if v < 0 {
			lossDays++
		}
		cum += v
		if cum > peak {
			peak = cum
		}
		dd := peak - cum
		if profile.AccountSize > 0 {
			ddPct := dd / profile.AccountSize * 100
			if ddPct > maxDDPct {
				maxDDPct = ddPct
			}
		}
	}

	issues := detectWeeklyIssues(logs, start, end)
	suggestions := buildWeeklySuggestions(winRate, realizedPnL, maxDDPct, issues)
	summary := fmt.Sprintf("近%d天成交%d笔（买%d/卖%d），已实现盈亏%s，卖出胜率%.1f%%，最大回撤%.2f%%。",
		days, buyCount+sellCount, buyCount, sellCount, formatSignedAmount(realizedPnL), winRate, maxDDPct)

	return &WeeklyReviewResult{
		FromDate:       start.Format("2006-01-02"),
		ToDate:         end.Add(-24 * time.Hour).Format("2006-01-02"),
		Days:           days,
		TotalTrades:    buyCount + sellCount,
		BuyCount:       buyCount,
		SellCount:      sellCount,
		WinRate:        round2Risk(winRate),
		RealizedPnL:    round2Risk(realizedPnL),
		MaxDrawdownPct: round2Risk(maxDDPct),
		ProfitDays:     profitDays,
		LossDays:       lossDays,
		TopIssues:      issues,
		Summary:        summary,
		Suggestions:    suggestions,
	}, nil
}

func round2Risk(v float64) float64 {
	return math.Round(v*100) / 100
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

type fifoLot struct {
	price  float64
	volume int64
}

func calcTodayRealizedPnL(logs []*model.TradeLog, today time.Time) float64 {
	year, month, day := today.Date()
	loc := today.Location()
	start := time.Date(year, month, day, 0, 0, 0, 0, loc)
	end := start.Add(24 * time.Hour)

	queues := map[string][]fifoLot{}
	realized := 0.0
	for _, l := range logs {
		code := strings.ToUpper(strings.TrimSpace(l.StockCode))
		switch l.Action {
		case model.TradeActionBuy:
			queues[code] = append(queues[code], fifoLot{price: l.Price, volume: l.Volume})
		case model.TradeActionSell:
			remain := l.Volume
			q := queues[code]
			for remain > 0 && len(q) > 0 {
				lot := &q[0]
				match := remain
				if match > lot.volume {
					match = lot.volume
				}
				if !l.TradedAt.Before(start) && l.TradedAt.Before(end) {
					realized += (l.Price - lot.price) * float64(match)
				}
				lot.volume -= match
				remain -= match
				if lot.volume == 0 {
					q = q[1:]
				}
			}
			queues[code] = q
		}
	}
	return realized
}

func countTodayBuyOpens(logs []*model.TradeLog, today time.Time) int {
	year, month, day := today.Date()
	loc := today.Location()
	start := time.Date(year, month, day, 0, 0, 0, 0, loc)
	end := start.Add(24 * time.Hour)
	count := 0
	for _, l := range logs {
		if l.Action != model.TradeActionBuy {
			continue
		}
		if !l.TradedAt.Before(start) && l.TradedAt.Before(end) {
			count++
		}
	}
	return count
}

func calcConsecutiveLossDays(logs []*model.TradeLog, today time.Time) int {
	realizedByDate := calcRealizedPnLByDate(logs, today.Location())
	if len(realizedByDate) == 0 {
		return 0
	}

	dates := make([]string, 0, len(realizedByDate))
	for d := range realizedByDate {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	streak := 0
	// 从最近交易日开始回溯，直到遇到非亏损日
	for i := len(dates) - 1; i >= 0; i-- {
		if realizedByDate[dates[i]] < 0 {
			streak++
			continue
		}
		break
	}
	return streak
}

func calcRealizedPnLByDate(logs []*model.TradeLog, loc *time.Location) map[string]float64 {
	if loc == nil {
		loc = time.Local
	}
	cp := append([]*model.TradeLog(nil), logs...)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].TradedAt.Before(cp[j].TradedAt)
	})

	queues := map[string][]fifoLot{}
	realizedByDate := map[string]float64{}
	for _, l := range cp {
		code := strings.ToUpper(strings.TrimSpace(l.StockCode))
		switch l.Action {
		case model.TradeActionBuy:
			queues[code] = append(queues[code], fifoLot{price: l.Price, volume: l.Volume})
		case model.TradeActionSell:
			remain := l.Volume
			q := queues[code]
			for remain > 0 && len(q) > 0 {
				lot := &q[0]
				match := remain
				if match > lot.volume {
					match = lot.volume
				}
				dateKey := l.TradedAt.In(loc).Format("2006-01-02")
				realizedByDate[dateKey] += (l.Price - lot.price) * float64(match)
				lot.volume -= match
				remain -= match
				if lot.volume == 0 {
					q = q[1:]
				}
			}
			queues[code] = q
		}
	}
	return realizedByDate
}

func coalesceName(names ...string) string {
	for _, n := range names {
		if strings.TrimSpace(n) != "" {
			return n
		}
	}
	return ""
}

func truncateText(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max]) + "..."
}

func riskLevelWeight(level string) int {
	switch level {
	case "HIGH":
		return 3
	case "MEDIUM":
		return 2
	default:
		return 1
	}
}

func mapDailyPriority(status string) string {
	if status == "BLOCK" {
		return "HIGH"
	}
	if status == "WARN" {
		return "MEDIUM"
	}
	return "LOW"
}

func mapGuardianPriority(signal model.SignalType) string {
	switch signal {
	case model.SignalStopLoss:
		return "HIGH"
	case model.SignalSell, model.SignalSellT:
		return "MEDIUM"
	default:
		return "LOW"
	}
}

type periodSellPnlItem struct {
	tradedAt time.Time
	pnl      float64
}

func calcPeriodSellPnL(logs []*model.TradeLog, start, end time.Time, loc *time.Location) []periodSellPnlItem {
	cp := append([]*model.TradeLog(nil), logs...)
	sort.Slice(cp, func(i, j int) bool {
		return cp[i].TradedAt.Before(cp[j].TradedAt)
	})

	queues := map[string][]fifoLot{}
	items := make([]periodSellPnlItem, 0, 32)
	for _, l := range cp {
		code := strings.ToUpper(strings.TrimSpace(l.StockCode))
		switch l.Action {
		case model.TradeActionBuy:
			queues[code] = append(queues[code], fifoLot{price: l.Price, volume: l.Volume})
		case model.TradeActionSell:
			remain := l.Volume
			q := queues[code]
			sellPnL := 0.0
			for remain > 0 && len(q) > 0 {
				lot := &q[0]
				match := remain
				if match > lot.volume {
					match = lot.volume
				}
				if !l.TradedAt.Before(start) && l.TradedAt.Before(end) {
					sellPnL += (l.Price - lot.price) * float64(match)
				}
				lot.volume -= match
				remain -= match
				if lot.volume == 0 {
					q = q[1:]
				}
			}
			queues[code] = q
			if !l.TradedAt.Before(start) && l.TradedAt.Before(end) {
				items = append(items, periodSellPnlItem{tradedAt: l.TradedAt.In(loc), pnl: sellPnL})
			}
		}
	}
	return items
}

func detectWeeklyIssues(logs []*model.TradeLog, start, end time.Time) []WeeklyReviewIssue {
	type rule struct {
		key   string
		label string
		words []string
	}
	rules := []rule{
		{key: "CHASE_HIGH", label: "追高冲动", words: []string{"追高", "冲动", "fomo"}},
		{key: "NO_PLAN", label: "无计划交易", words: []string{"临时", "随便", "感觉", "没计划"}},
		{key: "NO_STOP", label: "止损纪律不足", words: []string{"扛", "不止损", "死扛"}},
	}
	counter := map[string]int{}
	for _, l := range logs {
		if l.TradedAt.Before(start) || !l.TradedAt.Before(end) {
			continue
		}
		reason := strings.ToLower(strings.TrimSpace(l.Reason))
		for _, r := range rules {
			for _, w := range r.words {
				if strings.Contains(reason, w) {
					counter[r.key]++
					break
				}
			}
		}
	}
	items := make([]WeeklyReviewIssue, 0, len(counter))
	for _, r := range rules {
		if counter[r.key] <= 0 {
			continue
		}
		items = append(items, WeeklyReviewIssue{
			Key:   r.key,
			Label: r.label,
			Count: counter[r.key],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Count > items[j].Count
	})
	if len(items) > 3 {
		items = items[:3]
	}
	return items
}

func buildWeeklySuggestions(winRate, realizedPnL, maxDDPct float64, issues []WeeklyReviewIssue) []string {
	res := make([]string, 0, 4)
	if winRate < 45 {
		res = append(res, "下周先降低仓位，单票不超过上限的 70%，优先做确定性更高的计划单。")
	}
	if realizedPnL < 0 {
		res = append(res, "本周为亏损周，下周前两笔交易执行更严格预检，未通过不下单。")
	}
	if maxDDPct > 1.5 {
		res = append(res, "本周回撤偏大，建议把单日新开仓次数控制在 1-2 笔。")
	}
	for _, it := range issues {
		switch it.Key {
		case "CHASE_HIGH":
			res = append(res, "减少追高：只在回踩关键位且风控通过时入场。")
		case "NO_PLAN":
			res = append(res, "减少临时交易：先写买入计划，再执行。")
		case "NO_STOP":
			res = append(res, "强化止损纪律：触发止损信号优先执行减仓/离场。")
		}
	}
	if len(res) == 0 {
		res = append(res, "本周执行较稳，继续保持“先计划、后下单、再复盘”的节奏。")
	}
	if len(res) > 4 {
		res = res[:4]
	}
	return res
}

func formatSignedAmount(v float64) string {
	sign := ""
	if v > 0 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.2f", sign, v)
}
