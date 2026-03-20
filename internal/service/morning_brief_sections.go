package service

import "context"

// ─────────────────────────────────────────────────────────────────
// 各 Section 独立公开方法 — 供各自独立接口调用，互不阻塞
// ─────────────────────────────────────────────────────────────────

// SectionResult 大盘 section 的扩展结果（带情绪字段）
type SectionResult struct {
	Section     MorningBriefSection `json:"section"`
	MarketMood  string              `json:"market_mood"`
	MoodScore   int                 `json:"mood_score"`
	MoodSummary string              `json:"mood_summary"`
}

func (s *MorningBriefService) GetSectionMarket(_ int64) SectionResult {
	ctx := context.Background()
	sec, mood, score, summary := s.buildMarketSection(ctx)
	return SectionResult{
		Section:     sec,
		MarketMood:  mood,
		MoodScore:   score,
		MoodSummary: summary,
	}
}

func (s *MorningBriefService) GetSectionPosition(_ int64) MorningBriefSection {
	return s.buildPositionSection(context.Background())
}

func (s *MorningBriefService) GetSectionBuyPlan(userID int64) MorningBriefSection {
	return s.buildBuyPlanSection(context.Background(), userID)
}

func (s *MorningBriefService) GetSectionReports(_ int64) MorningBriefSection {
	return s.buildReportSection(context.Background())
}

func (s *MorningBriefService) GetSectionValuation(userID int64) MorningBriefSection {
	return s.buildValuationSection(context.Background(), userID)
}

func (s *MorningBriefService) GetSectionNews(userID int64) MorningBriefSection {
	return s.buildNewsSection(context.Background(), userID)
}

func (s *MorningBriefService) GetSectionExternal(userID int64) MorningBriefSection {
	return s.buildExternalSignalSection(context.Background(), userID)
}

func (s *MorningBriefService) GetAIComment(_ int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cached != nil {
		return s.cached.AIComment
	}
	return ""
}
