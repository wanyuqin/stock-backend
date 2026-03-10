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

// AddTradeRequest POST /api/v1/trades 的请求体。
type AddTradeRequest struct {
	StockCode string  `json:"stock_code" binding:"required"`
	Action    string  `json:"action"     binding:"required"` // "BUY" | "SELL"
	Price     float64 `json:"price"      binding:"required"`
	Volume    int64   `json:"volume"     binding:"required"`
	// TradedAt 交易日期（ISO 8601）。不传时默认为当前时间。
	TradedAt string `json:"traded_at"`
	Reason   string `json:"reason"`
}

// TradeLogDTO 返回给前端的交易记录，含格式化后的 Amount 字段。
type TradeLogDTO struct {
	ID        int64   `json:"id"`
	UserID    int64   `json:"user_id"`
	StockCode string  `json:"stock_code"`
	Action    string  `json:"action"`
	Price     float64 `json:"price"`
	Volume    int64   `json:"volume"`
	Amount    float64 `json:"amount"` // 计算值：price × volume
	TradedAt  string  `json:"traded_at"`
	Reason    string  `json:"reason"`
	CreatedAt string  `json:"created_at"`
}

// ── 盈亏相关结构 ──────────────────────────────────────────────────

// PositionSummary 单只股票的持仓 & 盈亏摘要。
type PositionSummary struct {
	StockCode string `json:"stock_code"`

	// ── 持仓信息 ──────────────────────────────────────────────────
	// HoldVolume > 0 表示仍有持仓
	HoldVolume    int64   `json:"hold_volume"`    // 当前持仓手数
	AvgCostPrice  float64 `json:"avg_cost_price"` // 持仓均价（FIFO）
	TotalCost     float64 `json:"total_cost"`     // 持仓总成本

	// ── 已实现盈亏（平仓盈亏，FIFO）──────────────────────────────
	RealizedPnL   float64 `json:"realized_pnl"`
	RealizedTrades int    `json:"realized_trades"` // 已完成平仓次数

	// ── 浮动盈亏（持仓盈亏，需要实时行情）────────────────────────
	// 由调用方填入，service 层计算前先置 0
	CurrentPrice  float64 `json:"current_price"`  // 调用方注入
	UnrealizedPnL float64 `json:"unrealized_pnl"` // 计算值
	UnrealizedPct float64 `json:"unrealized_pct"` // 浮动盈亏 %

	// ── 汇总 ──────────────────────────────────────────────────────
	TotalPnL      float64 `json:"total_pnl"` // realized + unrealized
}

// PerformanceReport GET /api/v1/stats/performance 的返回体。
type PerformanceReport struct {
	// 总体汇总
	TotalRealizedPnL   float64 `json:"total_realized_pnl"`
	TotalUnrealizedPnL float64 `json:"total_unrealized_pnl"`
	TotalPnL           float64 `json:"total_pnl"`

	// 各股票明细
	Positions []*PositionSummary `json:"positions"`

	// 统计数字
	TotalTrades   int `json:"total_trades"`    // 总交易笔数
	WinPositions  int `json:"win_positions"`   // 盈利持仓数
	LosePositions int `json:"lose_positions"`  // 亏损持仓数

	// 元信息
	CalculatedAt string `json:"calculated_at"`
	Note         string `json:"note"` // 如：浮动盈亏来自实时行情
}

// ═══════════════════════════════════════════════════════════════
// TradeService
// ═══════════════════════════════════════════════════════════════

type TradeService struct {
	repo   repo.TradeLogRepo
	market *MarketProvider // 用于拉取实时行情计算浮动盈亏
	log    *zap.Logger
}

// NewTradeService 创建 TradeService，注入 repo 和 stockSvc（提取其 market provider）。
func NewTradeService(r repo.TradeLogRepo, stockSvc *StockService, log *zap.Logger) *TradeService {
	return &TradeService{
		repo:   r,
		market: stockSvc.market,
		log:    log,
	}
}

// ─────────────────────────────────────────────────────────────────
// AddTradeLog — POST /api/v1/trades
// ─────────────────────────────────────────────────────────────────

// AddTradeLog 校验请求并将交易记录写入数据库，返回完整的 TradeLogDTO。
func (s *TradeService) AddTradeLog(ctx context.Context, userID int64, req *AddTradeRequest) (*TradeLogDTO, error) {
	// ── 1. 基础校验 ────────────────────────────────────────────────
	if err := validateTradeRequest(req); err != nil {
		return nil, err
	}

	// ── 2. 解析交易时间 ────────────────────────────────────────────
	tradedAt, err := parseTradedAt(req.TradedAt)
	if err != nil {
		return nil, fmt.Errorf("traded_at 格式错误（请用 RFC3339 或 2006-01-02）: %w", err)
	}

	// ── 3. 构建 model ──────────────────────────────────────────────
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

	// ── 4. 写库 ────────────────────────────────────────────────────
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
// ListByCode — GET /api/v1/trades/:code
// ─────────────────────────────────────────────────────────────────

// ListByCode 返回某只股票的全部交易记录（traded_at 倒序）。
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

// GetPerformance 计算用户的整体盈亏表现。
//
// 计算逻辑（FIFO 成本法）：
//  1. 按股票分组，用买入队列跟踪成本
//  2. 卖出时从最早买入的仓位开始配对，计算已实现盈亏
//  3. 剩余持仓拉取实时价格，计算浮动盈亏
//  4. 汇总得到 PerformanceReport
func (s *TradeService) GetPerformance(ctx context.Context, userID int64) (*PerformanceReport, error) {
	// ── 1. 读取全部交易记录（traded_at 升序，便于 FIFO）──────────
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

	// ── 2. 按股票分组 ──────────────────────────────────────────────
	grouped := make(map[string][]*model.TradeLog)
	for _, l := range allLogs {
		grouped[l.StockCode] = append(grouped[l.StockCode], l)
	}

	// ── 3. 收集需要拉取行情的股票（有持仓的）────────────────────
	positions := make([]*PositionSummary, 0, len(grouped))
	holdCodes  := make([]string, 0)

	for code, logs := range grouped {
		pos := calcPositionFIFO(code, logs)
		positions = append(positions, pos)
		if pos.HoldVolume > 0 {
			holdCodes = append(holdCodes, code)
		}
	}

	// ── 4. 批量拉取实时行情，注入浮动盈亏 ────────────────────────
	noteMsg := "浮动盈亏基于实时行情"
	if len(holdCodes) > 0 {
		quotes, _ := s.market.FetchMultipleQuotes(holdCodes)
		for _, pos := range positions {
			if pos.HoldVolume <= 0 {
				continue
			}
			if q, ok := quotes[pos.StockCode]; ok {
				pos.CurrentPrice  = q.Price
				pos.UnrealizedPnL = (q.Price - pos.AvgCostPrice) * float64(pos.HoldVolume)
				pos.UnrealizedPnL = round2(pos.UnrealizedPnL)
				if pos.TotalCost > 0 {
					pos.UnrealizedPct = round2(pos.UnrealizedPnL / pos.TotalCost * 100)
				}
			} else {
				noteMsg = "部分行情获取失败，浮动盈亏可能不完整"
			}
			pos.TotalPnL = round2(pos.RealizedPnL + pos.UnrealizedPnL)
		}
	}

	// ── 5. 按 TotalPnL 倒序排列，亏损最大的放最后 ────────────────
	sort.Slice(positions, func(i, j int) bool {
		return positions[i].TotalPnL > positions[j].TotalPnL
	})

	// ── 6. 汇总 ───────────────────────────────────────────────────
	var (
		totalRealized   float64
		totalUnrealized float64
		winPos, losePos int
	)
	for _, pos := range positions {
		totalRealized   += pos.RealizedPnL
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
// 内部工具函数
// ═══════════════════════════════════════════════════════════════

// validateTradeRequest 对请求体做业务层校验，返回带中文描述的 error。
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

// parseTradedAt 解析 traded_at 字段，支持：
//   - 空字符串 → 当前时间
//   - RFC3339（如 "2024-01-02T15:04:05Z"）
//   - 纯日期（如 "2024-01-02"）→ 设为当天 09:30:00 CST
func parseTradedAt(s string) (time.Time, error) {
	if s == "" {
		return time.Now(), nil
	}
	// 尝试 RFC3339
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	// 尝试纯日期
	cst := time.FixedZone("CST", 8*3600)
	if t, err := time.ParseInLocation("2006-01-02", s, cst); err == nil {
		return t.Add(9*time.Hour + 30*time.Minute), nil
	}
	// 尝试带时区偏移的日期时间
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("无法解析日期 %q", s)
}

// toDTO 将 model.TradeLog 转换为返回给前端的 DTO。
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

// ── FIFO 成本计算 ──────────────────────────────────────────────────

// costLot 代表一批买入仓位（用于 FIFO 队列）。
type costLot struct {
	price     float64
	remaining int64 // 尚未卖出的手数
}

// calcPositionFIFO 对单只股票按 FIFO 计算持仓成本与已实现盈亏。
// logs 必须已按 traded_at ASC 排序。
func calcPositionFIFO(code string, logs []*model.TradeLog) *PositionSummary {
	pos := &PositionSummary{StockCode: code}
	queue := make([]costLot, 0, 8) // FIFO 买入队列

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
				// 已实现盈亏 = (卖出价 - 成本价) × 配对手数
				pos.RealizedPnL += (l.Price - lot.price) * float64(match)
				pos.RealizedTrades++
				lot.remaining -= match
				sellVolume    -= match
				if lot.remaining == 0 {
					queue = queue[1:] // 该批已全部卖出，出队
				}
			}
			// 若卖出量超过买入量（数据异常），忽略超出部分
		}
	}

	// ── 计算剩余持仓的成本 ────────────────────────────────────────
	var holdVolume int64
	var totalCost  float64
	for _, lot := range queue {
		holdVolume += lot.remaining
		totalCost  += lot.price * float64(lot.remaining)
	}

	pos.HoldVolume = holdVolume
	pos.TotalCost  = round2(totalCost)
	if holdVolume > 0 {
		pos.AvgCostPrice = round4(totalCost / float64(holdVolume))
	}
	pos.RealizedPnL = round2(pos.RealizedPnL)

	return pos
}

// round2 四舍五入到 2 位小数。
func round2(v float64) float64 { return math.Round(v*100) / 100 }

// round4 四舍五入到 4 位小数（成本均价）。
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }
