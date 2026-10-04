package shared

import (
	"math"
	"testing"
)

func TestFeedbackTimeWeightUsesSevenDayHalfLife(t *testing.T) {
	now := int64(1_800_000_000)
	day := int64(24 * 60 * 60)

	tests := []struct {
		name string
		age  int64
		want float64
	}{
		{name: "current vote", age: 0, want: 1},
		{name: "one half-life", age: 7 * day, want: 0.5},
		{name: "two half-lives", age: 14 * day, want: 0.25},
		{name: "future timestamp", age: -day, want: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := feedbackTimeWeight(now-tt.age, now)
			if math.Abs(got-tt.want) > 1e-12 {
				t.Errorf("feedbackTimeWeight() = %.12f, want %.12f", got, tt.want)
			}
		})
	}
}

func TestPositiveFeedbackMetricsFavorRecentVotes(t *testing.T) {
	ranks := map[string]int{"recent": 9, "older": 1}
	ndcgRecentLow, _, recallRecentLow := positiveFeedbackMetrics([]feedbackRow{
		{ResultPath: "recent", Weight: 1},
		{ResultPath: "older", Weight: 0.5},
	}, ranks, 5)

	ndcgOlderLow, _, recallOlderLow := positiveFeedbackMetrics([]feedbackRow{
		{ResultPath: "recent", Weight: 0.5},
		{ResultPath: "older", Weight: 1},
	}, ranks, 5)

	if ndcgRecentLow >= ndcgOlderLow {
		t.Errorf("NDCG with recent low-ranked vote = %v, want less than %v", ndcgRecentLow, ndcgOlderLow)
	}
	if recallRecentLow >= recallOlderLow {
		t.Errorf("recall with recent low-ranked vote = %v, want less than %v", recallRecentLow, recallOlderLow)
	}
}
