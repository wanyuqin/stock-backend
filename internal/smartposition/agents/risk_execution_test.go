package agents

import (
	"math"
	"testing"

	"stock-backend/internal/service"
)

func TestCalcATR(t *testing.T) {
	klines := []service.KLine{
		{Date: "2026-03-24", High: 10.5, Low: 9.8, Close: 10.1},
		{Date: "2026-03-25", High: 10.8, Low: 10.0, Close: 10.6},
		{Date: "2026-03-26", High: 11.0, Low: 10.2, Close: 10.9},
		{Date: "2026-03-27", High: 11.2, Low: 10.5, Close: 11.1},
	}

	got := calcATR(klines, 3)
	want := (0.8 + 0.8 + 0.7) / 3
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("calcATR() = %.4f, want %.4f", got, want)
	}
}
