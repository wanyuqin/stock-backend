package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 常量 & 类型
// ═══════════════════════════════════════════════════════════════

const (
	emMoneyFlowURL        = "https://push2.eastmoney.com/api/qt/stock/get?fltt=2&fields=f62,f66,f72,f78,f84,f184,f3,f5&secid=%s.%s"
	moneyFlowRequestDelay = 300 * time.Millisecond
)

// MoneyFlow 是一次资金流向抓取的业务结构体。
// 金额字段使用 decimal.Decimal 保证计算精度，写入 DB 时转 float64。
type MoneyFlow struct {
	StockCode        string
	StockName        string
	Market           string
	MainNetInflow    decimal.Decimal // f62 主力净流入（元）
	SuperLargeInflow decimal.Decimal // f66 超大单净流入（元）
	LargeInflow      decimal.Decimal // f72 大单净流入（元）
	MediumInflow     decimal.Decimal // f78 中单净流入（元）
	SmallInflow      decimal.Decimal // f84 小单净流入（元）
	MainInflowPct    decimal.Decimal // f184 主力净流入占比（%）
	PctChg           decimal.Decimal // f3  涨跌幅（%）
	Volume           int64           // f5  成交量（手）
	Date             time.Time
}

// ── 东方财富原始 JSON 结构 ────────────────────────────────────────

type emMoneyFlowResp struct {
	Data struct {
		F62  interface{} `json:"f62"`
		F66  interface{} `json:"f66"`
		F72  interface{} `json:"f72"`
		F78  interface{} `json:"f78"`
		F84  interface{} `json:"f84"`
		F184 interface{} `json:"f184"`
		F3   interface{} `json:"f3"`
		F5   interface{} `json:"f5"`
	} `json:"data"`
	RC  int    `json:"rc"`
	Msg string `json:"message,omitempty"`
}

// ═══════════════════════════════════════════════════════════════
// MoneyFlowService
// ═══════════════════════════════════════════════════════════════

type MoneyFlowService struct {
	mfRepo    repo.MoneyFlowRepo
	stockRepo repo.StockRepo
	client    *http.Client
	log       *zap.Logger
}

func NewMoneyFlowService(
	mfRepo repo.MoneyFlowRepo,
	stockRepo repo.StockRepo,
	log *zap.Logger,
) *MoneyFlowService {
	return &MoneyFlowService{
		mfRepo:    mfRepo,
		stockRepo: stockRepo,
		client:    &http.Client{Timeout: 10 * time.Second},
		log:       log,
	}
}

// FetchAndSave 抓取单只股票的实时资金流向，持久化到 DB，并更新 stocks 表。
func (s *MoneyFlowService) FetchAndSave(ctx context.Context, code, market string) (*MoneyFlow, error) {
	rawBytes, err := s.fetchRaw(code, market)
	if err != nil {
		return nil, fmt.Errorf("fetch money flow %s: %w", code, err)
	}

	mf, err := ParseMoneyFlow(rawBytes)
	if err != nil {
		return nil, fmt.Errorf("parse money flow %s: %w", code, err)
	}
	mf.StockCode = code
	mf.Market    = market
	mf.Date      = time.Now().Truncate(24 * time.Hour)

	if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
		s.log.Error("SaveMoneyFlow failed", zap.String("code", code), zap.Error(saveErr))
	}

	return mf, nil
}

// FetchBatch 批量抓取，每只之间加 300ms 间隔防反爬。
func (s *MoneyFlowService) FetchBatch(ctx context.Context, stocks []struct{ Code, Market string }) ([]*MoneyFlow, error) {
	results := make([]*MoneyFlow, 0, len(stocks))
	for i, st := range stocks {
		if i > 0 {
			select {
			case <-ctx.Done():
				return results, ctx.Err()
			case <-time.After(moneyFlowRequestDelay):
			}
		}
		mf, err := s.FetchAndSave(ctx, st.Code, st.Market)
		if err != nil {
			s.log.Warn("FetchBatch: single stock failed",
				zap.String("code", st.Code), zap.Error(err))
			continue
		}
		results = append(results, mf)
	}
	return results, nil
}

func (s *MoneyFlowService) GetLatest(ctx context.Context, code string) (*model.MoneyFlowLog, error) {
	return s.mfRepo.LatestByCode(ctx, code)
}

func (s *MoneyFlowService) ListHistory(ctx context.Context, code string, limit int) ([]*model.MoneyFlowLog, error) {
	return s.mfRepo.ListByCode(ctx, code, limit)
}

// ── 抓取原始 JSON ─────────────────────────────────────────────────

func (s *MoneyFlowService) fetchRaw(code, market string) ([]byte, error) {
	secPrefix := marketToSecPrefix(market)
	url := fmt.Sprintf(emMoneyFlowURL, secPrefix, code)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")
	req.Header.Set("Referer", "https://finance.eastmoney.com/")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// ── ParseMoneyFlow ────────────────────────────────────────────────

func ParseMoneyFlow(rawJSON []byte) (*MoneyFlow, error) {
	var raw emMoneyFlowResp
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w", err)
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("API error rc=%d msg=%s", raw.RC, raw.Msg)
	}

	d := raw.Data
	mf := &MoneyFlow{
		MainNetInflow:    safeDecimal(d.F62),
		SuperLargeInflow: safeDecimal(d.F66),
		LargeInflow:      safeDecimal(d.F72),
		MediumInflow:     safeDecimal(d.F78),
		SmallInflow:      safeDecimal(d.F84),
		MainInflowPct:    safeDecimal(d.F184),
		PctChg:           safeDecimal(d.F3),
		Volume:           safeInt64(d.F5),
	}
	return mf, nil
}

// ── SaveMoneyFlow ─────────────────────────────────────────────────
// DB 列是 NUMERIC(15,2)，pgx driver Scan 到 Go 时返回字符串，
// GORM 能把字符串自动转换为 float64，但不能转换为 int64。
// 因此写入时统一用 float64（InexactFloat64），读取时也用 float64。

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

	// 同步更新 stocks.latest_money_flow
	return s.stockRepo.UpdateMoneyFlow(ctx, mf.StockCode, log.MainNetInflow)
}

// ═══════════════════════════════════════════════════════════════
// 辅助函数
// ═══════════════════════════════════════════════════════════════

func marketToSecPrefix(market string) string {
	if market == "SH" {
		return "1"
	}
	return "0"
}

func safeDecimal(v interface{}) decimal.Decimal {
	if v == nil {
		return decimal.Zero
	}
	switch val := v.(type) {
	case float64:
		return decimal.NewFromFloat(val)
	case string:
		if val == "-" || val == "" {
			return decimal.Zero
		}
		d, err := decimal.NewFromString(val)
		if err != nil {
			return decimal.Zero
		}
		return d
	case json.Number:
		d, err := decimal.NewFromString(val.String())
		if err != nil {
			return decimal.Zero
		}
		return d
	default:
		return decimal.Zero
	}
}

func safeInt64(v interface{}) int64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return int64(val)
	case int64:
		return val
	case json.Number:
		n, err := val.Int64()
		if err != nil {
			f, _ := val.Float64()
			return int64(f)
		}
		return n
	default:
		return 0
	}
}
