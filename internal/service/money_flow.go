package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// 常量 & 类型
// ═══════════════════════════════════════════════════════════════

// ulist.np 支持批量 secids，替换已废弃的 stock/get（TCP RST）
// 字段说明：
//   f12  = 股票代码
//   f13  = 市场(1=SH, 0=SZ)
//   f14  = 股票名称
//   f3   = 涨跌幅(%)
//   f5   = 成交量(手)
//   f62  = 主力净流入(元)
//   f66  = 超大单净流入(元)
//   f72  = 大单净流入(元)
//   f78  = 中单净流入(元)
//   f84  = 小单净流入(元)
//   f184 = 主力净流入占比(%)
const (
	emMoneyFlowURL = "https://push2.eastmoney.com/api/qt/ulist.np/get" +
		"?fltt=2&invt=2" +
		"&fields=f12,f13,f14,f3,f5,f62,f66,f72,f78,f84,f184" +
		"&ut=bd1d9ddb04089700cf9c27f6f7426281" +
		"&secids=%s"

	moneyFlowRequestDelay = 300 * time.Millisecond
)

// MoneyFlow 是一次资金流向抓取的业务结构体。
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

// ── ulist.np 原始 JSON 结构 ───────────────────────────────────────

type emUlistMoneyFlowResp struct {
	RC   int    `json:"rc"`
	Data *struct {
		Total int                    `json:"total"`
		Diff  []emUlistMoneyFlowItem `json:"diff"`
	} `json:"data"`
}

type emUlistMoneyFlowItem struct {
	F12  string      `json:"f12"` // 股票代码
	F13  int         `json:"f13"` // 市场：1=SH, 0=SZ
	F14  string      `json:"f14"` // 股票名称
	F3   json.Number `json:"f3"`  // 涨跌幅
	F5   json.Number `json:"f5"`  // 成交量
	F62  json.Number `json:"f62"` // 主力净流入
	F66  json.Number `json:"f66"` // 超大单净流入
	F72  json.Number `json:"f72"` // 大单净流入
	F78  json.Number `json:"f78"` // 中单净流入
	F84  json.Number `json:"f84"` // 小单净流入
	F184 json.Number `json:"f184"` // 主力净流入占比
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
	results, err := s.fetchBatchRaw([]struct{ Code, Market string }{{code, market}})
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

// FetchBatch 批量抓取。
// 优化：将多只股票合并为一次 ulist.np 请求（最多 50 只），减少网络往返。
// 超过 50 只时自动分批，批次间保留 300ms 间隔防反爬。
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
		batch := stocks[start:end]

		batchMap, err := s.fetchBatchRaw(batch)
		if err != nil {
			s.log.Warn("FetchBatch: batch request failed",
				zap.Int("start", start), zap.Int("end", end), zap.Error(err))
			continue
		}

		for _, st := range batch {
			mf, ok := batchMap[st.Code]
			if !ok {
				s.log.Warn("FetchBatch: single stock not in response",
					zap.String("code", st.Code))
				continue
			}
			mf.Date = time.Now().Truncate(24 * time.Hour)
			if saveErr := s.SaveMoneyFlow(ctx, mf); saveErr != nil {
				s.log.Error("SaveMoneyFlow failed",
					zap.String("code", st.Code), zap.Error(saveErr))
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

// ── 批量抓取原始数据（内部方法）─────────────────────────────────────

func (s *MoneyFlowService) fetchBatchRaw(stocks []struct{ Code, Market string }) (map[string]*MoneyFlow, error) {
	if len(stocks) == 0 {
		return map[string]*MoneyFlow{}, nil
	}

	// 构造 secids 参数："1.603920,0.000858"
	secids := make([]string, 0, len(stocks))
	for _, st := range stocks {
		secids = append(secids, marketToSecPrefix(st.Market)+"."+st.Code)
	}
	url := fmt.Sprintf(emMoneyFlowURL, strings.Join(secids, ","))

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 "+
			"(KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Referer", "https://quote.eastmoney.com/")
	req.Header.Set("Accept", "*/*")

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

	return parseUlistMoneyFlow(body)
}

// ── parseUlistMoneyFlow ───────────────────────────────────────────

func parseUlistMoneyFlow(rawJSON []byte) (map[string]*MoneyFlow, error) {
	var raw emUlistMoneyFlowResp
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		return nil, fmt.Errorf("unmarshal: %w | body: %s", err, truncateBytes(rawJSON, 200))
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("API rc=%d | body: %s", raw.RC, truncateBytes(rawJSON, 200))
	}
	// 非交易时段 diff 为空，返回空 map（不报错）
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

// ── SaveMoneyFlow ─────────────────────────────────────────────────

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

// ═══════════════════════════════════════════════════════════════
// 辅助函数
// ═══════════════════════════════════════════════════════════════

func marketToSecPrefix(market string) string {
	if market == "SH" {
		return "1"
	}
	return "0"
}

// numToDecimal 将 json.Number 转为 decimal.Decimal，"-" 或空值返回 Zero。
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

// numToInt64 将 json.Number 转为 int64，失败返回 0。
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

// truncateBytes 截断 byte slice，用于日志输出。
func truncateBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}

// ParseMoneyFlow 保留供外部测试使用（解析单条 ulist.np 响应）。
func ParseMoneyFlow(rawJSON []byte) (*MoneyFlow, error) {
	results, err := parseUlistMoneyFlow(rawJSON)
	if err != nil {
		return nil, err
	}
	for _, mf := range results {
		return mf, nil // 返回第一条
	}
	return nil, fmt.Errorf("no data in response")
}

// safeDecimal / safeInt64 保留，供其他包引用（如有）。
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
