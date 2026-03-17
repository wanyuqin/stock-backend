package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// MoneyFlowService — 资金流向服务（优化版）
//
// 优化点：
// 1. 使用统一的 EMHTTPClient，共享连接池
// 2. 自动重试与 Cookie 刷新
// ═══════════════════════════════════════════════════════════════

const (
	emMoneyFlowURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2" +
		"&fields=f12,f13,f14,f3,f5,f62,f66,f72,f78,f84,f184" +
		"&ut=bd1d9ddb04089700cf9c27f6f7426281" +
		"&secids=%s"

	moneyFlowRequestDelay   = 300 * time.Millisecond
	moneyFlowRequestTimeout = 15 * time.Second
)

type MoneyFlow struct {
	StockCode        string
	StockName        string
	Market           string
	MainNetInflow    decimal.Decimal
	SuperLargeInflow decimal.Decimal
	LargeInflow      decimal.Decimal
	MediumInflow     decimal.Decimal
	SmallInflow      decimal.Decimal
	MainInflowPct    decimal.Decimal
	PctChg           decimal.Decimal
	Volume           int64
	Date             time.Time
}

type emUlistMoneyFlowResp struct {
	RC   int    `json:"rc"`
	Data *struct {
		Total int                    `json:"total"`
		Diff  []emUlistMoneyFlowItem `json:"diff"`
	} `json:"data"`
}

type emUlistMoneyFlowItem struct {
	F12  string      `json:"f12"`
	F13  int         `json:"f13"`
	F14  string      `json:"f14"`
	F3   json.Number `json:"f3"`
	F5   json.Number `json:"f5"`
	F62  json.Number `json:"f62"`
	F66  json.Number `json:"f66"`
	F72  json.Number `json:"f72"`
	F78  json.Number `json:"f78"`
	F84  json.Number `json:"f84"`
	F184 json.Number `json:"f184"`
}

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

func (s *MoneyFlowService) FetchAndSave(ctx context.Context, code, market string) (*MoneyFlow, error) {
	results, err := s.fetchBatchRaw(ctx, []struct{ Code, Market string }{{code, market}})
	if err != nil {
		return nil, fmt.Errorf("fetch money flow %s: %w", code, err)
	}
	mf, ok := results[code]
	if !ok {
		return nil, fmt.Errorf("fetch money flow %s: code not found in response", code)
	}
	mf.Date = time.Now().Truncate(24 * time.Hour)
	if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
		s.log.Error("SaveMoneyFlow failed", zap.String("code", code), zap.Error(saveErr))
	}
	return mf, nil
}

func (s *MoneyFlowService) FetchBatch(ctx context.Context, stocks []struct{ Code, Market string }) ([]*MoneyFlow, error) {
	const maxPerBatch = 50
	results := make([]*MoneyFlow, 0, len(stocks))

	for start := 0; start < len(stocks); start += maxPerBatch {
		if start > 0 {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(moneyFlowRequestDelay):
			}
		}
		end := start + maxPerBatch
		if end > len(stocks) {
			end = len(stocks)
		}
		batchMap, err := s.fetchBatchRaw(ctx, stocks[start:end])
		if err != nil {
			s.log.Warn("FetchBatch: batch request failed", zap.Error(err))
			continue
		}
		for _, st := range stocks[start:end] {
			mf, ok := batchMap[st.Code]
			if !ok {
				continue
			}
			mf.Date = time.Now().Truncate(24 * time.Hour)
			if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
				s.log.Error("SaveMoneyFlow failed", zap.String("code", st.Code), zap.Error(saveErr))
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
// HTTP 抓取（使用统一客户端）
// ─────────────────────────────────────────────────────────────────

func (s *MoneyFlowService) fetchBatchRaw(ctx context.Context, stocks []struct{ Code, Market string }) (map[string]*MoneyFlow, error) {
	if len(stocks) == 0 {
		return map[string]*MoneyFlow{}, nil
	}
	secids := make([]string, 0, len(stocks))
	for _, st := range stocks {
		secids = append(secids, marketToSecPrefix(st.Market)+"."+st.Code)
	}
	url := fmt.Sprintf(emMoneyFlowURL, strings.Join(secids, ","))

	// 使用统一的 HTTP 客户端
	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, url, &EMRequestOption{
		Timeout:    moneyFlowRequestTimeout,
		MaxRetries: 3,
	})
	if err != nil {
		return nil, err
	}

	return parseUlistMoneyFlow(body)
}

func parseUlistMoneyFlow(rawJSON []byte) (map[string]*MoneyFlow, error) {
	var raw emUlistMoneyFlowResp
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w | body: %s", err, truncateBytes(rawJSON, 200))
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("API rc=%d | body: %s", raw.RC, truncateBytes(rawJSON, 200))
	}
	if raw.Data == nil || len(raw.Data.Diff) == 0 {
		return map[string]*MoneyFlow{}, nil
	}
	results := make(map[string]*MoneyFlow, len(raw.Data.Diff))
	for i := range raw.Data.Diff {
		item := &raw.Data.Diff[i]
		if item.F12 == "" {
			continue
		}
		market := "SZ"
		if item.F13 == 1 {
			market = "SH"
		}
		results[item.F12] = &MoneyFlow{
			StockCode:        item.F12,
			StockName:        item.F14,
			Market:           market,
			MainNetInflow:    numToDecimal(item.F62),
			SuperLargeInflow: numToDecimal(item.F66),
			LargeInflow:      numToDecimal(item.F72),
			MediumInflow:     numToDecimal(item.F78),
			SmallInflow:      numToDecimal(item.F84),
			MainInflowPct:    numToDecimal(item.F184),
			PctChg:           numToDecimal(item.F3),
			Volume:           numToInt64(item.F5),
		}
	}
	return results, nil
}

func (s *MoneyFlowService) SaveMoneyFlow(ctx context.Context, mf *MoneyFlow) error {
	log := &model.MoneyFlowLog{
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
	if err := s.mfRepo.Insert(ctx, log); err != nil {
		return err
	}
	return s.stockRepo.UpdateMoneyFlow(ctx, mf.StockCode, log.MainNetInflow)
}

// ─────────────────────────────────────────────────────────────────
// 工具函数
// ─────────────────────────────────────────────────────────────────

func marketToSecPrefix(market string) string {
	if market == "SH" {
		return "1"
	}
	return "0"
}

func numToDecimal(n json.Number) decimal.Decimal {
	if n == "" || n == "-" {
		return decimal.Zero
	}
	d, err := decimal.NewFromString(n.String())
	if err != nil {
		return decimal.Zero
	}
	return d
}

func numToInt64(n json.Number) int64 {
	if n == "" || n == "-" {
		return 0
	}
	i, err := n.Int64()
	if err != nil {
		f, _ := n.Float64()
		return int64(f)
	}
	return i
}

func ParseMoneyFlow(rawJSON []byte) (*MoneyFlow, error) {
	results, err := parseUlistMoneyFlow(rawJSON)
	if err != nil {
		return nil, err
	}
	for _, mf := range results {
		return mf, nil
	}
	return nil, fmt.Errorf("no data in response")
}
