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

// ═══════════════════════════════════════════════════════════════
// 请求 / 响应数据结构
// ═══════════════════════════════════════════════════════════════

type AddTradeRequest struct {
	StockCode string  `json:"stock_code" binding:"required"`
	Action    string  `json:"action"     binding:"required"`
	Price     float64 `json:"price"      binding:"required"`
	Volume    int64   `json:"volume"     binding:"required"`
	TradedAt  string  `json:"traded_at"`
	Reason    string  `json:"reason"`
}

type UpdateTradeRequest struct {
	Action   string  `json:"action"`
	Price    float64 `json:"price"`
	Volume   int64   `json:"volume"`
	TradedAt string  `json:"traded_at"`
	Reason   string  `json:"reason"`
}

type TradeLogDTO struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	StockCode string  `json:"stock_code"`
	Action    string  `json:"action"`
	Price     float64 `json:"price"`
	Volume    int64   `json:"volume"`
	Amount    float64 `json:"amount"`
	TradedAt  string  `json:"traded_at"`
	Reason    string  `json:"reason"`
	CreatedAt string  `json:"created_at"`
}

type PositionSummary struct {
	StockCode      string  `json:"stock_code"`
	HoldVolume     int64   `json:"hold_volume"`
	AvgCostPrice   float64 `json:"avg_cost_price"`
	TotalCost      float64 `json:"total_cost"`
	RealizedPnL    float64 `json:"realized_pnl"`
	RealizedTrades int     `json:"realized_trades"`
	CurrentPrice   float64 `json:"current_price"`
	UnrealizedPnL  float64 `json:"unrealized_pnl"`
	UnrealizedPct  float64 `json:"unrealized_pct"`
	TotalPnL       float64 `json:"total_pnl"`
}

type PerformanceReport struct {
	TotalRealizedPnL   float64            `json:"total_realized_pnl"`
	TotalUnrealizedPnL float64            `json:"total_unrealized_pnl"`
	TotalPnL           float64            `json:"total_pnl"`
	Positions          []*PositionSummary `json:"positions"`
	TotalTrades        int                `json:"total_trades"`
	WinPositions       int                `json:"win_positions"`
	LosePositions      int                `json:"lose_positions"`
	CalculatedAt       string             `json:"calculated_at"`
	Note               string             `json:"note"`
}

// ═══════════════════════════════════════════════════════════════
// TradeService
// ═══════════════════════════════════════════════════════════════

type TradeService struct {
	repo   repo.TradeLogRepo
	market *MarketProvider
	log    *zap.Logger
}

func NewTradeService(r repo.TradeLogRepo, stockSvc *StockService, log *zap.Logger) *TradeService {
	return &TradeService{repo: r, market: stockSvc.market, log: log}
}

// ─────────────────────────────────────────────────────────────────
// AddTradeLog — POST /api/v1/trades
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) AddTradeLog(ctx context.Context, userID int64, req *AddTradeRequest) (*TradeLogDTO, error) {
	if err := validateTradeRequest(req); err != nil {
		return nil, err
	}
	tradedAt, err := parseTradedAt(req.TradedAt)
	if err != nil {
		return nil, fmt.Errorf("traded_at 格式错误（请用 RFC3339 或 2006-01-02）: %w", err)
	}
	action := model.TradeAction(strings.ToUpper(req.Action))
	t := &model.TradeLog{
		UserID:    userID,
		StockCode: strings.ToUpper(strings.TrimSpace(req.StockCode)),
		Action:    action,
		Price:     req.Price,
		Volume:    req.Volume,
		TradedAt:  tradedAt,
		Reason:    strings.TrimSpace(req.Reason),
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, fmt.Errorf("保存交易记录失败: %w", err)
	}
	s.log.Sugar().Infow("trade log created",
		"id", t.ID, "code", t.StockCode,
		"action", t.Action, "price", t.Price, "volume", t.Volume,
	)
	return toDTO(t), nil
}

// ─────────────────────────────────────────────────────────────────
// UpdateTradeLog — PUT /api/v1/trades/:id
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) UpdateTradeLog(ctx context.Context, userID, id int64, req *UpdateTradeRequest) (*TradeLogDTO, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("交易记录不存在")
	}
	if t.UserID != userID {
		return nil, fmt.Errorf("无权限操作此记录")
	}

	// 更新可修改的字段
	if req.Action != "" {
		action := strings.ToUpper(req.Action)
		if action != "BUY" && action != "SELL" {
			return nil, fmt.Errorf("action 只能是 BUY 或 SELL")
		}
		t.Action = model.TradeAction(action)
	}
	if req.Price > 0 {
		if req.Price > 1_000_000 {
			return nil, fmt.Errorf("price 超出合理范围")
		}
		t.Price = req.Price
	}
	if req.Volume > 0 {
		if req.Volume > 10_000_000 {
			return nil, fmt.Errorf("volume 超出合理范围")
		}
		t.Volume = req.Volume
	}
	if req.TradedAt != "" {
		tradedAt, err := parseTradedAt(req.TradedAt)
		if err != nil {
			return nil, fmt.Errorf("traded_at 格式错误: %w", err)
		}
		t.TradedAt = tradedAt
	}
	// reason 允许改为空字符串
	t.Reason = strings.TrimSpace(req.Reason)

	if err := s.repo.Update(ctx, t); err != nil {
		return nil, fmt.Errorf("更新交易记录失败: %w", err)
	}
	s.log.Sugar().Infow("trade log updated", "id", id, "user_id", userID)
	return toDTO(t), nil
}

// ─────────────────────────────────────────────────────────────────
// DeleteTradeLog — DELETE /api/v1/trades/:id
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) DeleteTradeLog(ctx context.Context, userID, id int64) error {
	if err := s.repo.Delete(ctx, userID, id); err != nil {
		return fmt.Errorf("删除交易记录失败: %w", err)
	}
	s.log.Sugar().Infow("trade log deleted", "id", id, "user_id", userID)
	return nil
}

// ─────────────────────────────────────────────────────────────────
// ListAll — GET /api/v1/trades
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) ListAll(ctx context.Context, userID int64, limit, offset int) ([]*TradeLogDTO, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	logs, err := s.repo.ListByUser(ctx, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("查询全量交易记录失败: %w", err)
	}
	dtos := make([]*TradeLogDTO, 0, len(logs))
	for _, l := range logs {
		dtos = append(dtos, toDTO(l))
	}
	return dtos, nil
}

// ─────────────────────────────────────────────────────────────────
// ListByCode — GET /api/v1/trades/:code
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) ListByCode(ctx context.Context, userID int64, code string) ([]*TradeLogDTO, error) {
	logs, err := s.repo.ListByCode(ctx, userID, strings.ToUpper(code))
	if err != nil {
		return nil, fmt.Errorf("查询交易记录失败: %w", err)
	}
	dtos := make([]*TradeLogDTO, 0, len(logs))
	for _, l := range logs {
		dtos = append(dtos, toDTO(l))
	}
	return dtos, nil
}

// ─────────────────────────────────────────────────────────────────
// GetPerformance — GET /api/v1/stats/performance
// ─────────────────────────────────────────────────────────────────

func (s *TradeService) GetPerformance(ctx context.Context, userID int64) (*PerformanceReport, error) {
	allLogs, err := s.repo.ListAllByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("读取交易记录失败: %w", err)
	}
	if len(allLogs) == 0 {
		return &PerformanceReport{
			CalculatedAt: time.Now().Format(time.RFC3339),
			Note:         "暂无交易记录",
			Positions:    []*PositionSummary{},
		}, nil
	}

	grouped := make(map[string][]*model.TradeLog)
	for _, l := range allLogs {
		grouped[l.StockCode] = append(grouped[l.StockCode], l)
	}

	positions := make([]*PositionSummary, 0, len(grouped))
	holdCodes := make([]string, 0)
	for code, logs := range grouped {
		pos := calcPositionFIFO(code, logs)
		positions = append(positions, pos)
		if pos.HoldVolume > 0 {
			holdCodes = append(holdCodes, code)
		}
	}

	noteMsg := "浮动盈亏基于实时行情"
	if len(holdCodes) > 0 {
		quotes, _ := s.market.FetchMultipleQuotes(holdCodes)
		for _, pos := range positions {
			if pos.HoldVolume <= 0 {
				continue
			}
			if q, ok := quotes[pos.StockCode]; ok {
				pos.CurrentPrice = q.Price
				pos.UnrealizedPnL = round2((q.Price - pos.AvgCostPrice) * float64(pos.HoldVolume))
				if pos.TotalCost > 0 {
					pos.UnrealizedPct = round2(pos.UnrealizedPnL / pos.TotalCost * 100)
				}
			} else {
				noteMsg = "部分行情获取失败，浮动盈亏可能不完整"
			}
			pos.TotalPnL = round2(pos.RealizedPnL + pos.UnrealizedPnL)
		}
	}

	sort.Slice(positions, func(i, j int) bool {
		return positions[i].TotalPnL > positions[j].TotalPnL
	})

	var totalRealized, totalUnrealized float64
	var winPos, losePos int
	for _, pos := range positions {
		totalRealized += pos.RealizedPnL
		totalUnrealized += pos.UnrealizedPnL
		if pos.TotalPnL > 0 {
			winPos++
		} else if pos.TotalPnL < 0 {
			losePos++
		}
	}

	return &PerformanceReport{
		TotalRealizedPnL:   round2(totalRealized),
		TotalUnrealizedPnL: round2(totalUnrealized),
		TotalPnL:           round2(totalRealized + totalUnrealized),
		Positions:          positions,
		TotalTrades:        len(allLogs),
		WinPositions:       winPos,
		LosePositions:      losePos,
		CalculatedAt:       time.Now().Format(time.RFC3339),
		Note:               noteMsg,
	}, nil
}

// ═══════════════════════════════════════════════════════════════
// 内部工具
// ═══════════════════════════════════════════════════════════════

func validateTradeRequest(req *AddTradeRequest) error {
	code := strings.TrimSpace(req.StockCode)
	if code == "" {
		return fmt.Errorf("stock_code 不能为空")
	}
	if len(code) != 6 {
		return fmt.Errorf("stock_code 格式错误（应为 6 位数字，如 600519）")
	}
	action := strings.ToUpper(req.Action)
	if action != "BUY" && action != "SELL" {
		return fmt.Errorf("action 只能是 BUY 或 SELL，收到：%q", req.Action)
	}
	if req.Price <= 0 {
		return fmt.Errorf("price 必须大于 0，收到：%v", req.Price)
	}
	if req.Price > 1_000_000 {
		return fmt.Errorf("price 超出合理范围（最大 1,000,000），收到：%v", req.Price)
	}
	if req.Volume <= 0 {
		return fmt.Errorf("volume 必须大于 0，收到：%v", req.Volume)
	}
	if req.Volume > 10_000_000 {
		return fmt.Errorf("volume 超出合理范围（最大 10,000,000），收到：%v", req.Volume)
	}
	return nil
}

func parseTradedAt(s string) (time.Time, error) {
	if s == "" {
		return time.Now(), nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	cst := time.FixedZone("CST", 8*3600)
	if t, err := time.ParseInLocation("2006-01-02", s, cst); err == nil {
		return t.Add(9*time.Hour + 30*time.Minute), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("无法解析日期 %q", s)
}

func toDTO(t *model.TradeLog) *TradeLogDTO {
	return &TradeLogDTO{
		ID:        t.ID,
		UserID:    t.UserID,
		StockCode: t.StockCode,
		Action:    string(t.Action),
		Price:     t.Price,
		Volume:    t.Volume,
		Amount:    round2(t.Price * float64(t.Volume)),
		TradedAt:  t.TradedAt.Format(time.RFC3339),
		Reason:    t.Reason,
		CreatedAt: t.CreatedAt.Format(time.RFC3339),
	}
}

type costLot struct {
	price     float64
	remaining int64
}

func calcPositionFIFO(code string, logs []*model.TradeLog) *PositionSummary {
	pos := &PositionSummary{StockCode: code}
	queue := make([]costLot, 0, 8)

	for _, l := range logs {
		switch l.Action {
		case model.TradeActionBuy:
			queue = append(queue, costLot{price: l.Price, remaining: l.Volume})
		case model.TradeActionSell:
			sellVolume := l.Volume
			for sellVolume > 0 && len(queue) > 0 {
				lot := &queue[0]
				match := sellVolume
				if match > lot.remaining {
					match = lot.remaining
				}
				pos.RealizedPnL += (l.Price - lot.price) * float64(match)
				pos.RealizedTrades++
				lot.remaining -= match
				sellVolume -= match
				if lot.remaining == 0 {
					queue = queue[1:]
				}
			}
		}
	}

	var holdVolume int64
	var totalCost float64
	for _, lot := range queue {
		holdVolume += lot.remaining
		totalCost += lot.price * float64(lot.remaining)
	}
	pos.HoldVolume = holdVolume
	pos.TotalCost = round2(totalCost)
	if holdVolume > 0 {
		pos.AvgCostPrice = round4(totalCost / float64(holdVolume))
	}
	pos.RealizedPnL = round2(pos.RealizedPnL)
	return pos
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
