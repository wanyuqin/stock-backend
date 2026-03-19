package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// money_flow.go — 资金流向服务（腾讯 qt.gtimg.cn 版）
//
// 数据源：腾讯 qt.gtimg.cn（无需 Cookie，不会 EOF）
//
// 东财 → 腾讯字段映射：
//   MainNetInflow    = (外盘vol - 内盘vol) × 100 × 现价  （元）
//   MainInflowPct    = (外盘vol - 内盘vol) / (外盘vol + 内盘vol) × 100（%）
//   SuperLargeInflow / LargeInflow / MediumInflow / SmallInflow = 0
//   PctChg           = fields[32]（涨跌幅%）
//   Volume           = fields[6]（成交量，手）
// ═══════════════════════════════════════════════════════════════

const (
	moneyFlowRequestDelay   = 300 * time.Millisecond
	moneyFlowRequestTimeout = 8 * time.Second
	moneyFlowBatchSize      = 50
)

// MoneyFlow 资金流向（字段语义与旧东财版保持一致，上层无感知）
type MoneyFlow struct {
	StockCode        string
	StockName        string
	Market           string
	MainNetInflow    decimal.Decimal // 主力净流入（元）= 外盘金额 - 内盘金额
	SuperLargeInflow decimal.Decimal // 腾讯无此数据，恒为 0
	LargeInflow      decimal.Decimal // 腾讯无此数据，恒为 0
	MediumInflow     decimal.Decimal // 腾讯无此数据，恒为 0
	SmallInflow      decimal.Decimal // 腾讯无此数据，恒为 0
	MainInflowPct    decimal.Decimal // 主力净占比(%) = (外-内)/(外+内)*100
	PctChg           decimal.Decimal // 涨跌幅(%)
	Volume           int64           // 成交量（手）
	Date             time.Time
}

// ─────────────────────────────────────────────────────────────────
// MoneyFlowService
// ─────────────────────────────────────────────────────────────────

type MoneyFlowService struct {
	mfRepo    repo.MoneyFlowRepo
	stockRepo repo.StockRepo
	log       *zap.Logger
}

func NewMoneyFlowService(mfRepo repo.MoneyFlowRepo, stockRepo repo.StockRepo, log *zap.Logger) *MoneyFlowService {
	return &MoneyFlowService{
		mfRepo:    mfRepo,
		stockRepo: stockRepo,
		log:       log,
	}
}

// ─────────────────────────────────────────────────────────────────
// 公开方法
// ─────────────────────────────────────────────────────────────────

func (s *MoneyFlowService) FetchAndSave(ctx context.Context, code, market string) (*MoneyFlow, error) {
	results, err := s.fetchBatchFromQQ(ctx, []string{code})
	if err != nil {
		return nil, fmt.Errorf("fetch money flow %s: %w", code, err)
	}
	mf, ok := results[strings.ToUpper(code)]
	if !ok {
		return nil, fmt.Errorf("fetch money flow %s: not found in response", code)
	}
	mf.Date = time.Now().Truncate(24 * time.Hour)
	if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
		s.log.Error("SaveMoneyFlow failed", zap.String("code", code), zap.Error(saveErr))
	}
	return mf, nil
}

func (s *MoneyFlowService) FetchBatch(ctx context.Context, stocks []struct{ Code, Market string }) ([]*MoneyFlow, error) {
	codes := make([]string, 0, len(stocks))
	for _, st := range stocks {
		codes = append(codes, st.Code)
	}

	results := make([]*MoneyFlow, 0, len(codes))
	for start := 0; start < len(codes); start += moneyFlowBatchSize {
		if start > 0 {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(moneyFlowRequestDelay):
			}
		}
		end := start + moneyFlowBatchSize
		if end > len(codes) {
			end = len(codes)
		}
		batchMap, err := s.fetchBatchFromQQ(ctx, codes[start:end])
		if err != nil {
			s.log.Warn("FetchBatch: batch request failed", zap.Error(err))
			continue
		}
		for _, code := range codes[start:end] {
			mf, ok := batchMap[strings.ToUpper(code)]
			if !ok {
				continue
			}
			mf.Date = time.Now().Truncate(24 * time.Hour)
			if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
				s.log.Error("SaveMoneyFlow failed", zap.String("code", code), zap.Error(saveErr))
			}
			results = append(results, mf)
		}
	}
	return results, nil
}

func (s *MoneyFlowService) GetLatest(ctx context.Context, code string) (*model.MoneyFlowLog, error) {
	return s.mfRepo.LatestByCode(ctx, code)
}

func (s *MoneyFlowService) ListHistory(ctx context.Context, code string, limit int) ([]*model.MoneyFlowLog, error) {
	return s.mfRepo.ListByCode(ctx, code, limit)
}

// ─────────────────────────────────────────────────────────────────
// 腾讯行情批量抓取
// ─────────────────────────────────────────────────────────────────

func (s *MoneyFlowService) fetchBatchFromQQ(ctx context.Context, codes []string) (map[string]*MoneyFlow, error) {
	if len(codes) == 0 {
		return map[string]*MoneyFlow{}, nil
	}

	qtCodes := make([]string, 0, len(codes))
	for _, code := range codes {
		qtCodes = append(qtCodes, toQTCode(code))
	}

	url := fmt.Sprintf(qqQuoteURL, strings.Join(qtCodes, ","))
	body, err := fetchQQHTTP(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("fetchBatchFromQQ: %w", err)
	}

	// GBK → UTF-8
	if utf8, e := gbkToUTF8(body); e == nil {
		body = utf8
	}

	return parseQQMoneyFlow(body)
}

// parseQQMoneyFlow 解析腾讯批量响应，提取资金流向
func parseQQMoneyFlow(body []byte) (map[string]*MoneyFlow, error) {
	results := make(map[string]*MoneyFlow)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "v_") {
			continue
		}
		mf, err := parseQQMoneyFlowLine(line)
		if err != nil || mf == nil {
			continue
		}
		results[mf.StockCode] = mf
	}
	return results, nil
}

// parseQQMoneyFlowLine 从单行腾讯数据提取资金流向
// 格式：v_sh603920="1~名~代码~价~昨收~开~量~外盘~内盘~...~涨跌幅~...";
func parseQQMoneyFlowLine(line string) (*MoneyFlow, error) {
	eqIdx := strings.Index(line, "=")
	if eqIdx < 0 {
		return nil, fmt.Errorf("no = in line")
	}
	varName := line[:eqIdx]

	start := strings.Index(line, `"`)
	end := strings.LastIndex(line, `"`)
	if start < 0 || end <= start {
		return nil, fmt.Errorf("no quotes in line")
	}
	fields := strings.Split(line[start+1:end], "~")
	if len(fields) < 38 {
		return nil, fmt.Errorf("too few fields: %d", len(fields))
	}

	market, pureCode := extractMarketCode(varName)
	if pureCode == "" {
		return nil, fmt.Errorf("empty code")
	}

	name      := fields[1]
	price     := parseF(fields[3])
	outerVol  := parseI(fields[7])  // 外盘（主动买入，手）
	innerVol  := parseI(fields[8])  // 内盘（主动卖出，手）
	pctChg    := parseF(fields[32])
	totalVol  := parseI(fields[6])

	// 金额换算：量(手) × 100股/手 × 现价(元/股)
	outerAmt := float64(outerVol) * 100 * price
	innerAmt := float64(innerVol) * 100 * price
	netAmt   := outerAmt - innerAmt

	// 净占比
	netPct := 0.0
	if totalV2 := outerVol + innerVol; totalV2 > 0 {
		netPct = float64(outerVol-innerVol) / float64(totalV2) * 100
	}

	return &MoneyFlow{
		StockCode:        pureCode,
		StockName:        name,
		Market:           market,
		MainNetInflow:    decimal.NewFromFloat(netAmt),
		SuperLargeInflow: decimal.Zero,
		LargeInflow:      decimal.Zero,
		MediumInflow:     decimal.Zero,
		SmallInflow:      decimal.Zero,
		MainInflowPct:    decimal.NewFromFloat(netPct),
		PctChg:           decimal.NewFromFloat(pctChg),
		Volume:           totalVol,
	}, nil
}

// ─────────────────────────────────────────────────────────────────
// SaveMoneyFlow — 持久化到数据库
// ─────────────────────────────────────────────────────────────────

func (s *MoneyFlowService) SaveMoneyFlow(ctx context.Context, mf *MoneyFlow) error {
	entry := &model.MoneyFlowLog{
		StockCode:        mf.StockCode,
		Date:             mf.Date,
		MainNetInflow:    mf.MainNetInflow.InexactFloat64(),
		SuperLargeInflow: mf.SuperLargeInflow.InexactFloat64(),
		LargeInflow:      mf.LargeInflow.InexactFloat64(),
		MediumInflow:     mf.MediumInflow.InexactFloat64(),
		SmallInflow:      mf.SmallInflow.InexactFloat64(),
		MainInflowPct:    mf.MainInflowPct.InexactFloat64(),
		PctChg:           mf.PctChg.InexactFloat64(),
		Volume:           mf.Volume,
	}
	if err := s.mfRepo.Insert(ctx, entry); err != nil {
		return err
	}
	return s.stockRepo.UpdateMoneyFlow(ctx, mf.StockCode, entry.MainNetInflow)
}

// ─────────────────────────────────────────────────────────────────
// 兼容保留
// ─────────────────────────────────────────────────────────────────

// marketToSecPrefix 东财 secid 前缀，保留供其他东财接口引用
func marketToSecPrefix(market string) string {
	if market == "SH" {
		return "1"
	}
	return "0"
}

// ParseMoneyFlow 兼容旧调用方
func ParseMoneyFlow(rawBody []byte) (*MoneyFlow, error) {
	results, err := parseQQMoneyFlow(rawBody)
	if err != nil {
		return nil, err
	}
	for _, mf := range results {
		return mf, nil
	}
	return nil, fmt.Errorf("no data in response")
}
