package service

import "encoding/json"

// ═══════════════════════════════════════════════════════════════
// em_types.go — 东财接口专用数据结构
//
// 这些类型仅用于仍保留东财接口的模块：
//   - sector_provider.go    (板块归属 + 板块行情)
//   - sector_heatmap_service.go (板块热力图)
//   - crawler_service.go    (全市场行情同步，用于机会雷达打分)
//
// 行情（market_provider.go）和 K 线（kline_service.go）已切换腾讯接口，
// 不再使用这里的类型。
// ═══════════════════════════════════════════════════════════════

// 东财 ulist.np 接口常量（板块行情专用）
const (
	emUlistNpURL  = "https://push2.eastmoney.com/api/qt/ulist.np/get"
	emUlistUt     = "bd1d9ddb04089700cf9c27f6f7426281"
	emUlistFields = "f12,f13,f14,f2,f3,f4,f5,f6,f8,f10,f15,f16,f17,f18,f20,f21"
)

// ulistNpResp 东财 ulist.np 接口统一响应结构
type ulistNpResp struct {
	RC   int    `json:"rc"`
	Data *struct {
		Total int           `json:"total"`
		Diff  []ulistNpItem `json:"diff"`
	} `json:"data"`
}

type ulistNpItem struct {
	F12 string      `json:"f12"` // 代码
	F13 int         `json:"f13"` // 市场：1=SH, 0=SZ
	F14 string      `json:"f14"` // 名称
	F2  json.Number `json:"f2"`  // 价格
	F3  json.Number `json:"f3"`  // 涨跌幅(%)
	F4  json.Number `json:"f4"`  // 涨跌额
	F5  json.Number `json:"f5"`  // 成交量
	F6  json.Number `json:"f6"`  // 成交额
	F8  json.Number `json:"f8"`  // 换手率
	F10 json.Number `json:"f10"` // 量比
	F15 json.Number `json:"f15"` // 最高
	F16 json.Number `json:"f16"` // 最低
	F17 json.Number `json:"f17"` // 今开
	F18 json.Number `json:"f18"` // 昨收
	F20 json.Number `json:"f20"` // 总市值
	F21 json.Number `json:"f21"` // 流通市值
}
