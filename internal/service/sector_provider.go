package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// ═══════════════════════════════════════════════════════════════
// sector_provider.go — 行业板块行情 + 个股-板块关系查询
//
// 东财接口说明：
//
//  1. 获取个股所属行业板块（slist/get）：
//     https://push2.eastmoney.com/api/qt/slist/get
//       ?spt=1&secid={market}.{stock_code}&fields=f12,f14
//     f12 = 板块代码（如 BK0726）
//     f14 = 板块名称（如 印制电路板）
//     ★ spt=1 表示只取行业板块（概念板块用 spt=3）
//
//  2. 获取板块实时行情（stock/get）：
//     https://push2.eastmoney.com/api/qt/stock/get
//       ?secid=90.{sector_code}&fields=f3,f4,f12,f14
//     ★ 板块 market_id 固定为 90（不是1/0）
//     f3 = 涨跌幅（%），f4 = 涨跌额
//     f12 = 代码，f14 = 名称
//
// 背离度（Relative Strength）计算：
//   RS = P_stock(今日涨跌幅%) - P_sector(今日涨跌幅%)
//   RS < -3%  && 触发止损 → "个股显著弱于行业，主力主动流出"
//   RS > +3%  && 触发止损 → "行业整体重挫，个股相对抗跌"
// ═══════════════════════════════════════════════════════════════

const (
	emSlistURL       = "https://push2.eastmoney.com/api/qt/slist/get"
	emSlistUt        = "fa5fd1943c7b386f172d6893dbfba10b"
	emSectorMarketID = 90 // 板块固定 market_id

	// 内存缓存 TTL
	sectorQuoteCacheTTL   = 10 * time.Second // 实时行情短缓存
	sectorMappingCacheTTL = 6 * time.Hour    // 板块归属变动频率低，6h 内存缓存
	sectorMappingMissTTL  = 30 * time.Minute // 远程明确无行业映射时的负缓存，避免日志刷屏
)

// ─────────────────────────────────────────────────────────────────
// 对外数据结构
// ─────────────────────────────────────────────────────────────────

// SectorQuote 板块实时行情
type SectorQuote struct {
	SecID      string    `json:"sec_id"`      // 如 "90.BK0726"
	Code       string    `json:"code"`        // 如 "BK0726"
	Name       string    `json:"name"`        // 如 "印制电路板"
	ChangeRate float64   `json:"change_rate"` // 今日涨跌幅（%）
	ChangeAmt  float64   `json:"change_amt"`  // 今日涨跌额
	UpdatedAt  time.Time `json:"updated_at"`
	FromCache  bool      `json:"from_cache"`
}

// SectorInfo 最终对外暴露的板块信息（嵌入持仓诊断结果）
type SectorInfo struct {
	SectorCode          string  `json:"sector_code"`           // BK0726
	SectorName          string  `json:"sector_name"`           // 印制电路板
	SectorChangePercent float64 `json:"sector_change_percent"` // 板块今日涨跌幅（%）
	RelativeStrength    float64 `json:"relative_strength"`     // RS = 个股涨跌幅 - 板块涨跌幅
	RSLabel             string  `json:"rs_label"`              // 强弱文本描述
	RSLevel             string  `json:"rs_level"`              // "strong" | "normal" | "weak" | "critical"
}

// RelativeStrength 内部计算结构（兼容 position_guardian_service.go 调用）
type RelativeStrength struct {
	StockCode         string  `json:"stock_code"`
	StockChangeToday  float64 `json:"stock_change_today"`  // 个股今日涨跌幅（%）
	SectorChangeToday float64 `json:"sector_change_today"` // 板块今日涨跌幅（%）
	SectorName        string  `json:"sector_name"`
	SectorCode        string  `json:"sector_code"`
	Diff              float64 `json:"diff"`      // RS = 个股 - 板块
	IsWeaker          bool    `json:"is_weaker"` // RS < -weakThreshold
	WeakerThreshold   float64 `json:"weaker_threshold"`
	// 5日兼容字段（供 snapshot 历史字段使用）
	StockChange5D  float64 `json:"stock_change_5d"`
	SectorChange5D float64 `json:"sector_change_5d"`
}

// ─────────────────────────────────────────────────────────────────
// SectorProvider
// ─────────────────────────────────────────────────────────────────

type SectorProvider struct {
	sectorRepo repo.SectorRepo
	memCache   *gocache.Cache
	log        *zap.Logger
}

func NewSectorProvider(sectorRepo repo.SectorRepo, log *zap.Logger) *SectorProvider {
	return &SectorProvider{
		sectorRepo: sectorRepo,
		memCache:   gocache.New(sectorQuoteCacheTTL, 60*time.Second),
		log:        log,
	}
}

// ─────────────────────────────────────────────────────────────────
// SyncSectorMapping 同步个股所属行业板块到 DB（首次添加或每日收盘后调用）
// ─────────────────────────────────────────────────────────────────

func (p *SectorProvider) SyncSectorMapping(ctx context.Context, stockCode string) (*model.StockSectorRelation, error) {
	secid := buildSecID(stockCode)

	// 调用 slist/get 获取行业板块列表
	rawURL := fmt.Sprintf(
		"%s?ut=%s&spt=1&secid=%s&fields=f12,f14&cb=&_=0",
		emSlistURL, emSlistUt, secid,
	)

	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, rawURL, &EMRequestOption{
		Timeout: 8 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("SyncSectorMapping fetch(%s): %w", stockCode, err)
	}

	sectorCode, sectorName, err := parseSlistResp(body)
	if err != nil {
		return nil, fmt.Errorf("SyncSectorMapping parse(%s): %w", stockCode, err)
	}
	if sectorCode == "" {
		return nil, fmt.Errorf("SyncSectorMapping: no sector found for %s", stockCode)
	}

	// 持久化板块基础信息
	sector := &model.Sector{
		Code:     sectorCode,
		Name:     sectorName,
		MarketID: emSectorMarketID,
	}
	if err := p.sectorRepo.UpsertSector(ctx, sector); err != nil {
		p.log.Warn("UpsertSector failed", zap.String("code", sectorCode), zap.Error(err))
	}

	// 持久化个股-板块映射
	rel := &model.StockSectorRelation{
		StockCode:  stockCode,
		SectorCode: sectorCode,
		SectorName: sectorName,
	}
	if err := p.sectorRepo.UpsertRelation(ctx, rel); err != nil {
		return nil, fmt.Errorf("UpsertRelation(%s): %w", stockCode, err)
	}

	// 刷新内存缓存
	p.memCache.Set("rel:"+stockCode, rel, sectorMappingCacheTTL)

	p.log.Info("SyncSectorMapping ok",
		zap.String("stock", stockCode),
		zap.String("sector", sectorCode),
		zap.String("name", sectorName),
	)
	return rel, nil
}

// GetSectorRelation 获取个股所属行业板块（内存 → DB → 远程，逐级降级）
func (p *SectorProvider) GetSectorRelation(ctx context.Context, stockCode string) (*model.StockSectorRelation, error) {
	cacheKey := "rel:" + stockCode
	missKey := "rel-miss:" + stockCode

	// 1. 内存缓存
	if cached, found := p.memCache.Get(cacheKey); found {
		return cached.(*model.StockSectorRelation), nil
	}
	// 1.1 负缓存：最近已确认无行业映射，直接降级
	if _, found := p.memCache.Get(missKey); found {
		return nil, nil
	}

	// 2. DB 缓存
	rel, err := p.sectorRepo.GetRelation(ctx, stockCode)
	if err != nil {
		return nil, fmt.Errorf("GetSectorRelation DB(%s): %w", stockCode, err)
	}
	if rel != nil {
		p.memCache.Set(cacheKey, rel, sectorMappingCacheTTL)
		return rel, nil
	}

	// 3. 远程同步
	rel, err = p.SyncSectorMapping(ctx, stockCode)
	if err != nil && isNoSectorFoundErr(err) {
		p.memCache.Set(missKey, true, sectorMappingMissTTL)
		return nil, nil
	}
	return rel, err
}

// ─────────────────────────────────────────────────────────────────
// GetSectorQuote 获取板块实时行情（内存缓存 → 远程）
// ★ 关键点：
//   1. secid 固定前缀 90.（板块 market_id=90，非个股的 0/1）
//   2. 加 fltt=2 让服务器返回浮点数（否则 f3 返回放大100倍的整数）
//   3. 用 ulist.np 接口替代 stock/get，返回结构更稳定
// ─────────────────────────────────────────────────────────────────

func (p *SectorProvider) GetSectorQuote(ctx context.Context, sectorCode string) (*SectorQuote, error) {
	cacheKey := "sq:" + sectorCode
	if cached, found := p.memCache.Get(cacheKey); found {
		q := *cached.(*SectorQuote)
		q.FromCache = true
		return &q, nil
	}

	// 板块 secid 固定前缀 90.
	secid := fmt.Sprintf("%d.%s", emSectorMarketID, sectorCode)

	// 使用 ulist.np 接口，加 fltt=2 确保返回浮点涨跌幅
	// f3=涨跌幅(%)浮点, f4=涨跌额, f12=代码, f14=名称
	rawURL := fmt.Sprintf(
		"%s?fltt=2&invt=2&fields=f12,f13,f14,f3,f4&secids=%s&ut=%s",
		emUlistNpURL, secid, emUlistUt,
	)

	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, rawURL, &EMRequestOption{
		Timeout: 8 * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("GetSectorQuote fetch(%s): %w", sectorCode, err)
	}

	q, err := parseSectorUlistResp(body, sectorCode)
	if err != nil {
		return nil, err
	}

	p.memCache.Set(cacheKey, q, sectorQuoteCacheTTL)
	return q, nil
}

// ─────────────────────────────────────────────────────────────────
// GetRelativeStrength 并发获取个股+板块行情，计算背离度
// ─────────────────────────────────────────────────────────────────

func (p *SectorProvider) GetRelativeStrength(
	ctx context.Context,
	stockCode string,
	stockChangeToday float64, // 由调用方从行情接口传入，避免重复请求
) (*RelativeStrength, error) {
	const weakThreshold = 3.0 // 背离度阈值（%）

	rel, err := p.GetSectorRelation(ctx, stockCode)
	if err != nil || rel == nil {
		if err != nil {
			if isNoSectorFoundErr(err) {
				p.log.Info("GetRelativeStrength: no sector mapping, fallback to stock-only",
					zap.String("code", stockCode))
			} else {
				p.log.Warn("GetRelativeStrength: no sector relation",
					zap.String("code", stockCode), zap.Error(err))
			}
		}
		return &RelativeStrength{
			StockCode:        stockCode,
			StockChangeToday: stockChangeToday,
			WeakerThreshold:  weakThreshold,
		}, nil
	}

	// 并发获取板块行情（与调用方的其他逻辑并发，此处单独 goroutine 控制）
	type result struct {
		quote *SectorQuote
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		q, e := p.GetSectorQuote(ctx, rel.SectorCode)
		ch <- result{q, e}
	}()

	res := <-ch
	if res.err != nil {
		p.log.Warn("GetRelativeStrength: sector quote failed, RS unavailable",
			zap.String("sector", rel.SectorCode), zap.Error(res.err))
		return &RelativeStrength{
			StockCode:        stockCode,
			StockChangeToday: stockChangeToday,
			SectorName:       rel.SectorName,
			SectorCode:       rel.SectorCode,
			WeakerThreshold:  weakThreshold,
		}, nil
	}

	sectorChange := res.quote.ChangeRate
	diff := stockChangeToday - sectorChange
	isWeaker := diff < -weakThreshold

	return &RelativeStrength{
		StockCode:         stockCode,
		StockChangeToday:  stockChangeToday,
		SectorChangeToday: sectorChange,
		SectorName:        rel.SectorName,
		SectorCode:        rel.SectorCode,
		Diff:              diff,
		IsWeaker:          isWeaker,
		WeakerThreshold:   weakThreshold,
		// 5日兼容字段（当前以今日数据填充，如需 5 日可后续扩展）
		StockChange5D:  stockChangeToday,
		SectorChange5D: sectorChange,
	}, nil
}

func isNoSectorFoundErr(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "no sector found")
}

// BuildSectorInfo 将 RelativeStrength 转换为对外 SectorInfo DTO
func BuildSectorInfo(rs *RelativeStrength) *SectorInfo {
	if rs == nil || rs.SectorCode == "" {
		return nil
	}

	level, label := classifyRS(rs.Diff, rs.WeakerThreshold)
	return &SectorInfo{
		SectorCode:          rs.SectorCode,
		SectorName:          rs.SectorName,
		SectorChangePercent: rs.SectorChangeToday,
		RelativeStrength:    rs.Diff,
		RSLabel:             label,
		RSLevel:             level,
	}
}

// classifyRS 根据背离度返回等级和文字描述
func classifyRS(diff, threshold float64) (level, label string) {
	switch {
	case diff < -threshold*1.67: // < -5%
		return "critical", fmt.Sprintf("极弱 (RS %.1f%%)", diff)
	case diff < -threshold: // < -3%
		return "weak", fmt.Sprintf("偏弱 (RS %.1f%%)", diff)
	case diff > threshold: // > +3%
		return "strong", fmt.Sprintf("偏强 (RS +%.1f%%)", diff)
	default:
		return "normal", fmt.Sprintf("同步 (RS %.1f%%)", diff)
	}
}

// ─────────────────────────────────────────────────────────────────
// FetchSectorInfoBatch 并发批量获取多只个股的板块信息
// 用于 DiagnoseAll 场景，避免串行等待
// ─────────────────────────────────────────────────────────────────

func (p *SectorProvider) FetchSectorInfoBatch(
	ctx context.Context,
	stocks []struct {
		Code        string
		ChangeToday float64
	},
) map[string]*SectorInfo {
	results := make(map[string]*SectorInfo, len(stocks))
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, s := range stocks {
		wg.Add(1)
		go func(code string, change float64) {
			defer wg.Done()
			rs, err := p.GetRelativeStrength(ctx, code, change)
			if err != nil {
				p.log.Warn("FetchSectorInfoBatch: rs failed",
					zap.String("code", code), zap.Error(err))
				return
			}
			info := BuildSectorInfo(rs)
			if info != nil {
				mu.Lock()
				results[code] = info
				mu.Unlock()
			}
		}(s.Code, s.ChangeToday)
	}
	wg.Wait()
	return results
}

// ─────────────────────────────────────────────────────────────────
// JSON 解析函数
// ─────────────────────────────────────────────────────────────────

// parseSlistResp 解析 slist/get 响应，返回第一个行业板块代码和名称
func parseSlistResp(body []byte) (code, name string, err error) {
	var raw struct {
		RC   int `json:"rc"`
		Data *struct {
			Diff []struct {
				F12 string `json:"f12"` // 板块代码
				F14 string `json:"f14"` // 板块名称
			} `json:"diff"`
		} `json:"data"`
	}
	if e := json.Unmarshal(body, &raw); e != nil {
		return "", "", fmt.Errorf("parseSlistResp unmarshal: %w (body: %s)", e, truncateBytes(body, 200))
	}
	if raw.Data == nil || len(raw.Data.Diff) == 0 {
		return "", "", nil // 无板块数据，不报错
	}
	first := raw.Data.Diff[0]
	return first.F12, first.F14, nil
}

// parseSectorUlistResp 解析 ulist.np 接口返回的板块行情
// fltt=2 确保 f3 是浮点涨跌幅（如 -1.23），而非放大100倍的整数（如 -123）
func parseSectorUlistResp(body []byte, sectorCode string) (*SectorQuote, error) {
	var raw ulistNpResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("parseSectorUlistResp unmarshal: %w (body: %s)", err, truncateBytes(body, 200))
	}
	if raw.RC != 0 {
		return nil, fmt.Errorf("parseSectorUlistResp: rc=%d for %s", raw.RC, sectorCode)
	}
	if raw.Data == nil || len(raw.Data.Diff) == 0 {
		return nil, fmt.Errorf("parseSectorUlistResp: no data for sector %s", sectorCode)
	}
	item := &raw.Data.Diff[0]
	name := item.F14
	if name == "" {
		name = sectorCode
	}
	return &SectorQuote{
		SecID:      fmt.Sprintf("%d.%s", emSectorMarketID, sectorCode),
		Code:       sectorCode,
		Name:       name,
		ChangeRate: jnf(item.F3), // fltt=2 保证返回浮点百分比，如 -1.23
		ChangeAmt:  jnf(item.F4),
		UpdatedAt:  time.Now(),
	}, nil
}
