package service

import (
	"context"
	"fmt"
	"strings"
)

type globalQuoteSnapshot struct {
	Name       string
	Code       string
	Price      float64
	ChangeRate float64
}

func (s *MorningBriefService) buildExternalSignalSection(ctx context.Context, userID int64) MorningBriefSection {
	sec := MorningBriefSection{Title: "外部信号", Level: "normal"}
	items := make([]string, 0, 10)

	newsResult, newsErr := s.newsAnalyzer.Analyze(ctx, userID)
	policyLine, policyScore, policyLevel := buildPolicySignal(newsResult, newsErr)
	items = append(items, policyLine)
	sec.Level = maxBriefLevel(sec.Level, policyLevel)

	globalLine, globalScore, globalLevel := s.buildGlobalSignal(ctx)
	items = append(items, globalLine)
	sec.Level = maxBriefLevel(sec.Level, globalLevel)

	// 互动易/巨潮公告：为自选股和买入计划股抓取最新公告
	var corpAnnouncements []*CorporateAnnouncement
	if s.interactiveSvc != nil {
		codes := s.getWatchAndPlanCodes(ctx, userID)
		if len(codes) > 0 {
			corpAnnouncements, _ = s.interactiveSvc.FetchAnnouncements(ctx, codes)
		}
	}

	alphaLine, alphaLevel := buildAlphaSignal(newsResult, newsErr, corpAnnouncements)
	items = append(items, alphaLine)
	sec.Level = maxBriefLevel(sec.Level, alphaLevel)

	supplyLine, supplyLevel := buildSupplyChainSignal(newsResult, newsErr)
	items = append(items, supplyLine)
	sec.Level = maxBriefLevel(sec.Level, supplyLevel)

	totalDelta := policyScore + globalScore
	switch {
	case totalDelta >= 8:
		items = append(items, fmt.Sprintf("综合修正：外部信号偏暖，建议将大盘情绪上修 %d 分，强势计划可适度提前观察。", totalDelta))
		sec.Level = maxBriefLevel(sec.Level, "info")
	case totalDelta <= -8:
		items = append(items, fmt.Sprintf("综合修正：外部信号偏冷，建议将大盘情绪下修 %d 分，智能建仓买入区间下移 0.5%%-1.0%%。", -totalDelta))
		sec.Level = maxBriefLevel(sec.Level, "warning")
	default:
		items = append(items, "综合修正：外部信号中性，维持原始计划，盘前重点看竞价强弱与量能确认。")
	}

	sec.Items = items
	return sec
}

func buildPolicySignal(result *NewsAnalysisResult, err error) (string, int, string) {
	if err != nil || result == nil {
		return "政策风向：暂未完成关键词扫描，先按中性处理。", 0, "normal"
	}
	positiveKeywords := []string{
		"降准", "降息", "全面降准", "限制减持", "活跃资本市场", "新质生产力",
		"补贴", "稳增长", "支持民营", "专项债", "扩大内需", "产业支持",
		"央行释放", "证监会支持", "财政刺激", "消费刺激",
	}
	negativeKeywords := []string{
		"从严监管", "过度投机", "严查配资", "窗口指导", "重点监控",
		"减持新规从严", "打击炒作", "严厉查处", "违规减持", "暂停上市",
		"叫停", "清查", "严禁", "禁止炒作",
	}
	positiveHit := firstKeywordHit(result.Items, positiveKeywords)
	negativeHit := firstKeywordHit(result.Items, negativeKeywords)
	switch {
	case positiveHit != "":
		return fmt.Sprintf("政策风向：检测到关键词「%s」，情绪偏多，建议大盘情绪上修 8 分。", positiveHit), 8, "info"
	case negativeHit != "":
		return fmt.Sprintf("政策风向：检测到关键词「%s」，监管偏紧，建议仓位与追高意愿同步降温。", negativeHit), -7, "warning"
	default:
		return "政策风向：未捕捉到强政策催化或明显监管降温表态，先按中性处理。", 0, "normal"
	}
}

func (s *MorningBriefService) buildGlobalSignal(ctx context.Context) (string, int, string) {
	// 请求纳指、道指、恒指、A50期货、人民币汇率、英伟达、特斯拉
	codes := []string{"usIXIC", "usDJI", "hkHSI", "hkCNA50", "FX_USDCNY", "usNVDA", "usTSLA"}
	quotes, err := s.stockSvc.GetMultipleQuotesBySource(codes, "qq")
	if err != nil || len(quotes) == 0 {
		return "外围环境：全球指数快照暂不可用，开盘前请手动确认纳指、恒指与 A50 期货。", 0, "normal"
	}

	nasdaq := pickGlobalQuote(quotes, "IXIC", "usIXIC")
	hangSeng := pickGlobalQuote(quotes, "HSI", "hkHSI")
	a50 := pickGlobalQuote(quotes, "CNA50", "hkCNA50")
	usdcny := pickGlobalQuote(quotes, "USDCNY", "FX_USDCNY")
	nvda := pickGlobalQuote(quotes, "NVDA", "usNVDA")
	tsla := pickGlobalQuote(quotes, "TSLA", "usTSLA")

	if nasdaq == nil && hangSeng == nil && a50 == nil {
		return "外围环境：暂未解析到有效全球指数，先按中性处理。", 0, "normal"
	}

	parts := make([]string, 0, 6)
	score := 0
	level := "normal"
	buyRangeShift := false // 是否触发买入区间下移

	// 纳斯达克
	if nasdaq != nil {
		parts = append(parts, fmt.Sprintf("纳指 %+.2f%%", nasdaq.ChangeRate))
		if nasdaq.ChangeRate <= -1 {
			score -= 5
			level = maxBriefLevel(level, "warning")
		} else if nasdaq.ChangeRate >= 1 {
			score += 4
			level = maxBriefLevel(level, "info")
		}
	}

	// 恒生指数
	if hangSeng != nil {
		parts = append(parts, fmt.Sprintf("恒指 %+.2f%%", hangSeng.ChangeRate))
		if hangSeng.ChangeRate <= -0.8 {
			score -= 3
			level = maxBriefLevel(level, "warning")
		} else if hangSeng.ChangeRate >= 0.8 {
			score += 2
			level = maxBriefLevel(level, "info")
		}
	}

	// A50 期货（核心信号：跌幅超 0.5% → 买入区间下移）
	if a50 != nil {
		parts = append(parts, fmt.Sprintf("A50 %+.2f%%", a50.ChangeRate))
		if a50.ChangeRate <= -0.5 {
			score -= 6
			buyRangeShift = true
			level = maxBriefLevel(level, "warning")
		} else if a50.ChangeRate <= -1.5 {
			score -= 4 // 已在上面减了 6，累计 -10
			level = maxBriefLevel(level, "warning")
		} else if a50.ChangeRate >= 0.5 {
			score += 3
			level = maxBriefLevel(level, "info")
		}
	}

	// 人民币汇率（USD/CNY 上涨=人民币贬值，外资情绪偏冷）
	if usdcny != nil && usdcny.ChangeRate != 0 {
		if usdcny.ChangeRate >= 0.3 {
			parts = append(parts, fmt.Sprintf("人民币 %+.2f%%（贬值）", usdcny.ChangeRate))
			score -= 3
			level = maxBriefLevel(level, "warning")
		} else if usdcny.ChangeRate <= -0.3 {
			parts = append(parts, fmt.Sprintf("人民币 %+.2f%%（升值）", usdcny.ChangeRate))
			score += 2
			level = maxBriefLevel(level, "info")
		}
	}

	// 英伟达 / 特斯拉（科技情绪风向标）
	techBearish := false
	techBullish := false
	techParts := make([]string, 0, 2)
	if nvda != nil {
		if nvda.ChangeRate <= -3 {
			techBearish = true
			techParts = append(techParts, fmt.Sprintf("英伟达 %+.2f%%", nvda.ChangeRate))
		} else if nvda.ChangeRate >= 3 {
			techBullish = true
			techParts = append(techParts, fmt.Sprintf("英伟达 %+.2f%%", nvda.ChangeRate))
		}
	}
	if tsla != nil {
		if tsla.ChangeRate <= -3 {
			techBearish = true
			techParts = append(techParts, fmt.Sprintf("特斯拉 %+.2f%%", tsla.ChangeRate))
		} else if tsla.ChangeRate >= 3 {
			techBullish = true
			techParts = append(techParts, fmt.Sprintf("特斯拉 %+.2f%%", tsla.ChangeRate))
		}
	}
	if techBearish {
		score -= 4
		level = maxBriefLevel(level, "warning")
	} else if techBullish {
		score += 3
		level = maxBriefLevel(level, "info")
	}

	line := "外围环境：" + strings.Join(parts, "，")

	// 科技个股补充说明
	if len(techParts) > 0 {
		direction := "大跌"
		if techBullish {
			direction = "大涨"
		}
		line += fmt.Sprintf("（%s %s）", strings.Join(techParts, "、"), direction)
	}

	// 综合结论
	switch {
	case buyRangeShift:
		line += "。【重要】A50 期货跌幅超 0.5%，智能建仓买入区间建议下移 0.5%-1.0%，暂缓追高。"
	case score <= -8:
		line += "。外围全面承压，科技与高弹性方向开盘压力大，买入计划建议延后确认。"
	case score <= -4:
		line += "。科技与情绪板块开盘承压，智能建仓买入区间建议下移 0.5%。"
	case score >= 6:
		line += "。外围偏暖，科技链与高弹性方向更容易获得竞价溢价，可适度积极。"
	case score >= 3:
		line += "。外围略偏暖，关注竞价量能是否同步放大。"
	default:
		line += "。外围影响中性，重点观察竞价是否顺势放大。"
	}

	return line, score, level
}

func buildAlphaSignal(result *NewsAnalysisResult, err error, announcements []*CorporateAnnouncement) (string, string) {
	positiveKeywords := []string{"供货", "订单", "中标", "合作", "华为", "回购", "增持", "重组", "预增", "战略合作", "独家"}
	negativeKeywords := []string{"减持", "问询", "终止", "诉讼", "预亏", "风险提示", "立案", "处罚", "违规"}

	// 优先检查互动易/巨潮公告（更权威的一手信息源）
	for _, ann := range announcements {
		hitPos := containsAnyKeyword(ann.Content, positiveKeywords)
		hitNeg := containsAnyKeyword(ann.Content, negativeKeywords)
		switch {
		case hitPos != "":
			return fmt.Sprintf("公告/互动：【%s】%s（%s）出现关键词「%s」，建议开盘后优先观察量价确认。",
				ann.Source, ann.StockName, ann.StockCode, hitPos), "info"
		case hitNeg != "":
			return fmt.Sprintf("公告/互动：【%s】%s（%s）出现关键词「%s」，建议降低预期，相关买入计划延后确认。",
				ann.Source, ann.StockName, ann.StockCode, hitNeg), "warning"
		}
	}
	if len(announcements) > 0 {
		first := announcements[0]
		return fmt.Sprintf("公告/互动：%s（%s）有最新【%s】公告，建议优先查阅原文。",
			first.StockName, first.StockCode, first.Source), "info"
	}

	// 回退：财联社关联新闻
	if err != nil || result == nil || len(result.LinkedItems) == 0 {
		return "公告/互动：暂未捕捉到与你持仓或买入计划直接关联的强催化新闻。", "normal"
	}
	for _, item := range result.LinkedItems {
		hitPos := containsAnyKeyword(item.Content, positiveKeywords)
		hitNeg := containsAnyKeyword(item.Content, negativeKeywords)
		target := strings.Join(item.AffectedCodes, "、")
		switch {
		case hitPos != "":
			return fmt.Sprintf("公告/互动：%s 出现关键词「%s」，建议开盘后优先观察量价是否确认，再决定是否推进计划。", target, hitPos), "info"
		case hitNeg != "":
			return fmt.Sprintf("公告/互动：%s 出现关键词「%s」，建议降低预期，相关买入计划延后确认。", target, hitNeg), "warning"
		}
	}
	first := result.LinkedItems[0]
	target := strings.Join(first.AffectedCodes, "、")
	if target == "" {
		target = "关联标的"
	}
	return fmt.Sprintf("公告/互动：%s 有最新电报关联，建议优先查看消息原文与竞价反馈。", target), "info"
}

func buildSupplyChainSignal(result *NewsAnalysisResult, err error) (string, string) {
	if err != nil || result == nil {
		return "产业链成本：暂未完成上游价格关键词扫描，先保持行业中性判断。", "normal"
	}
	upKeywords := []string{
		"铜价", "电解铜", "覆铜板", "CCL", "铜期货", "铜价上涨",
		"碳酸锂", "硅料", "稀土", "锂价", "钴价",
		"涨价", "上行", "走高", "创新高", "大涨", "价格上涨",
	}
	downKeywords := []string{"降价", "下行", "回落", "跌价", "价格下跌", "供应过剩"}
	for _, item := range result.Items {
		upHit := containsAnyKeyword(item.Content, upKeywords)
		downHit := containsAnyKeyword(item.Content, downKeywords)
		switch {
		case upHit != "":
			return fmt.Sprintf("产业链成本：检测到「%s」相关上行表述，上游成本大幅上行，PCB/电子链估值面临承压，建议买入计划延后。", upHit), "warning"
		case downHit != "":
			return fmt.Sprintf("产业链成本：检测到「%s」相关回落表述，成本压力阶段性缓解，PCB/电子链可继续跟踪计划。", downHit), "info"
		}
	}
	return "产业链成本：未检测到明显原材料异动关键词，维持原行业判断。", "normal"
}

func firstKeywordHit(items []*NewsSentiment, keywords []string) string {
	for _, item := range items {
		if hit := containsAnyKeyword(item.Content, keywords); hit != "" {
			return hit
		}
	}
	return ""
}

func containsAnyKeyword(content string, keywords []string) string {
	for _, kw := range keywords {
		if strings.Contains(content, kw) {
			return kw
		}
	}
	return ""
}

func pickGlobalQuote(quotes map[string]*Quote, codeSuffix string, fallback string) *globalQuoteSnapshot {
	for _, q := range quotes {
		if q == nil {
			continue
		}
		if strings.EqualFold(q.Code, codeSuffix) || strings.EqualFold(q.Code, fallback) {
			return &globalQuoteSnapshot{Name: q.Name, Code: q.Code, Price: q.Price, ChangeRate: q.ChangeRate}
		}
	}
	return nil
}

func maxBriefLevel(a, b string) string {
	order := map[string]int{"normal": 0, "info": 1, "warning": 2, "danger": 3}
	if order[b] > order[a] {
		return b
	}
	return a
}
