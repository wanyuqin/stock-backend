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
	tradeSvc        *TradeService
	stockRepo       repo.StockRepo
	stockReportRepo repo.StockReportRepo
	dividendSvc     *DividendCalendarService
	log             *zap.Logger
}

func NewRiskService(
	r repo.RiskRepo,
	tradeRepo repo.TradeLogRepo,
	tradeSvc *TradeService,
	stockRepo repo.StockRepo,
	stockReportRepo repo.StockReportRepo,
	log *zap.Logger,
) *RiskService {
	return &RiskService{
		repo:            r,
		tradeRepo:       tradeRepo,
		tradeSvc:        tradeSvc,
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
	Status              string  `json:"status"`
	TodayRealizedPnL    float64 `json:"today_realized_pnl"`
	DailyLossAmount     float64 `json:"daily_loss_amount"`
	DailyLossPct        float64 `json:"daily_loss_pct"`
	LossLimitPct        float64 `json:"loss_limit_pct"`
	LossLimitAmount     float64 `json:"loss_limit_amount"`
	RemainingLossAmount float64 `json:"remaining_loss_amount"`
	CanOpenNewPosition  bool    `json:"can_open_new_position"`
	Message             string  `json:"message"`
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
	lossLimitAmount := profile.AccountSize * lossLimitPct / 100.0
	todayRealized := calcTodayRealizedPnL(logs, today)
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
	if dailyLossAmount >= lossLimitAmount {
		status = "BLOCK"
		canOpen = false
		msg = "当日亏损达到熔断阈值，仅限制买入开仓（卖出/减仓不受影响）"
	} else if dailyLossAmount >= lossLimitAmount*0.7 {
		status = "WARN"
		msg = "当日亏损接近熔断阈值，请谨慎新开仓（卖出/减仓不受影响）"
	}
	remaining := lossLimitAmount - dailyLossAmount
	if remaining < 0 {
		remaining = 0
	}
	return &DailyRiskState{
		Status:              status,
		TodayRealizedPnL:    round2Risk(todayRealized),
		DailyLossAmount:     round2Risk(dailyLossAmount),
		DailyLossPct:        round2Risk(dailyLossPct),
		LossLimitPct:        lossLimitPct,
		LossLimitAmount:     round2Risk(lossLimitAmount),
		RemainingLossAmount: round2Risk(remaining),
		CanOpenNewPosition:  canOpen,
		Message:             msg,
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
