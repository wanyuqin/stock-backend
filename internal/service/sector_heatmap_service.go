package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"go.uber.org/zap"
)

// ─────────────────────────────────────────────────────────────────
// 主要行业板块列表（东财 BK 代码）
// ─────────────────────────────────────────────────────────────────

var mainSectorCodes = []string{
	"BK0481", // 半导体
	"BK0726", // 印制电路板
	"BK0736", // 消费电子
	"BK0732", // 新能源车
	"BK0537", // 光伏设备
	"BK0528", // 储能
	"BK0493", // 人工智能
	"BK0996", // 机器人
	"BK0490", // 云计算
	"BK0735", // 医疗器械
	"BK0465", // 创新药
	"BK0451", // 生物制品
	"BK0739", // 医疗服务
	"BK0438", // 银行
	"BK0452", // 保险
	"BK0451", // 证券
	"BK0477", // 房地产
	"BK0736", // 建材
	"BK0525", // 化工
	"BK0433", // 煤炭
	"BK0478", // 钢铁
	"BK0443", // 有色金属
	"BK0476", // 石油石化
	"BK0529", // 电力
	"BK0432", // 农业
	"BK0444", // 食品饮料
	"BK0539", // 白酒
	"BK0474", // 零售
	"BK0523", // 传媒
	"BK0493", // 通信
}

// SectorHeatItem 单个板块的热力图数据点
type SectorHeatItem struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	ChangeRate  float64 `json:"change_rate"`  // 今日涨跌幅（%）
	Strength    string  `json:"strength"`     // "strong_up" | "up" | "flat" | "down" | "strong_down"
}

// SectorHeatmapDTO 板块热力图完整响应
type SectorHeatmapDTO struct {
	Items     []*SectorHeatItem `json:"items"`
	UpdatedAt string            `json:"updated_at"`
	FromCache bool              `json:"from_cache"`
}

// ─────────────────────────────────────────────────────────────────
// SectorHeatmapService
// ─────────────────────────────────────────────────────────────────

type SectorHeatmapService struct {
	memCache *gocache.Cache
	log      *zap.Logger
}

func NewSectorHeatmapService(log *zap.Logger) *SectorHeatmapService {
	return &SectorHeatmapService{
		memCache: gocache.New(30*time.Second, 60*time.Second),
		log:      log,
	}
}

const heatmapCacheKey = "sector_heatmap"

func (s *SectorHeatmapService) GetHeatmap(ctx context.Context) (*SectorHeatmapDTO, error) {
	if cached, found := s.memCache.Get(heatmapCacheKey); found {
		dto := *cached.(*SectorHeatmapDTO)
		dto.FromCache = true
		return &dto, nil
	}

	// 去重：mainSectorCodes 里有重复 BK，先去重
	seen := make(map[string]bool)
	unique := make([]string, 0, len(mainSectorCodes))
	for _, c := range mainSectorCodes {
		if !seen[c] {
			seen[c] = true
			unique = append(unique, c)
		}
	}

	items, err := s.fetchSectorBatch(ctx, unique)
	if err != nil {
		return nil, err
	}

	// 按涨跌幅降序排列
	sort.Slice(items, func(i, j int) bool {
		return items[i].ChangeRate > items[j].ChangeRate
	})

	dto := &SectorHeatmapDTO{
		Items:     items,
		UpdatedAt: time.Now().Format("15:04:05"),
		FromCache: false,
	}
	s.memCache.Set(heatmapCacheKey, dto, 30*time.Second)
	return dto, nil
}

// fetchSectorBatch 并发批量拉取板块行情（每批 10 个）
func (s *SectorHeatmapService) fetchSectorBatch(ctx context.Context, codes []string) ([]*SectorHeatItem, error) {
	const batchSize = 10
	results := make([]*SectorHeatItem, 0, len(codes))
	var mu sync.Mutex
	var wg sync.WaitGroup
	var firstErr error

	for i := 0; i < len(codes); i += batchSize {
		end := i + batchSize
		if end > len(codes) {
			end = len(codes)
		}
		batch := codes[i:end]
		wg.Add(1)
		go func(batch []string) {
			defer wg.Done()
			items, err := s.fetchBatch(ctx, batch)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			results = append(results, items...)
		}(batch)
	}
	wg.Wait()
	if firstErr != nil {
		s.log.Warn("sector heatmap: partial fetch error", zap.Error(firstErr))
	}
	return results, nil
}

func (s *SectorHeatmapService) fetchBatch(ctx context.Context, codes []string) ([]*SectorHeatItem, error) {
	// 构造 secids 参数，板块 market_id 固定 90
	secids := ""
	for i, c := range codes {
		if i > 0 {
			secids += ","
		}
		secids += fmt.Sprintf("90.%s", c)
	}
	rawURL := fmt.Sprintf(
		"%s?fltt=2&invt=2&fields=f12,f13,f14,f3&secids=%s&ut=%s",
		emUlistNpURL, secids, emUlistUt,
	)

	client := GetEMHTTPClient()
	body, err := client.FetchBody(ctx, rawURL, &EMRequestOption{Timeout: 8 * time.Second})
	if err != nil {
		return nil, err
	}

	var raw ulistNpResp
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("sector heatmap unmarshal: %w", err)
	}
	if raw.RC != 0 || raw.Data == nil {
		return nil, nil
	}

	items := make([]*SectorHeatItem, 0, len(raw.Data.Diff))
	for _, d := range raw.Data.Diff {
		rate := jnf(d.F3)
		items = append(items, &SectorHeatItem{
			Code:       d.F12,
			Name:       d.F14,
			ChangeRate: rate,
			Strength:   classifyStrength(rate),
		})
	}
	return items, nil
}

func classifyStrength(rate float64) string {
	switch {
	case rate >= 3:
		return "strong_up"
	case rate >= 0.5:
		return "up"
	case rate > -0.5:
		return "flat"
	case rate > -3:
		return "down"
	default:
		return "strong_down"
	}
}
