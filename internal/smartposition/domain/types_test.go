package domain

import "testing"

func TestSmartPositionRequestNormalize(t *testing.T) {
	req, err := (SmartPositionRequest{
		StockCode:    "603920",
		TotalCapital: 500000,
	}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() unexpected error: %v", err)
	}
	if req.MaxRiskRatio != DefaultMaxRiskRatio {
		t.Fatalf("MaxRiskRatio = %v, want %v", req.MaxRiskRatio, DefaultMaxRiskRatio)
	}
	if req.ATRMultiplier != DefaultATRMultiple {
		t.Fatalf("ATRMultiplier = %v, want %v", req.ATRMultiplier, DefaultATRMultiple)
	}
	if req.AnalysisWindow != DefaultAnalysisDays {
		t.Fatalf("AnalysisWindow = %v, want %v", req.AnalysisWindow, DefaultAnalysisDays)
	}
}

func TestMergeStatesDedup(t *testing.T) {
	merged, err := MergeStates([]*GraphState{
		{Warnings: []string{"a"}, DegradedModules: []string{"x"}, Confidence: 0.9},
		{Warnings: []string{"a", "b"}, DegradedModules: []string{"x", "y"}, Confidence: 0.6},
	})
	if err != nil {
		t.Fatalf("MergeStates() unexpected error: %v", err)
	}
	if got, want := len(merged.Warnings), 2; got != want {
		t.Fatalf("len(Warnings) = %d, want %d", got, want)
	}
	if got, want := len(merged.DegradedModules), 2; got != want {
		t.Fatalf("len(DegradedModules) = %d, want %d", got, want)
	}
	if merged.Confidence != 0.6 {
		t.Fatalf("Confidence = %v, want 0.6", merged.Confidence)
	}
}
