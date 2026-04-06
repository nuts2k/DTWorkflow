package report

import "testing"

func TestAggregatedStats_IsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		stats AggregatedStats
		want  bool
	}{
		{"zero value", AggregatedStats{}, true},
		{"has reviews", AggregatedStats{ReviewCount: 1}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stats.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDailyStats_IsEmpty(t *testing.T) {
	tests := []struct {
		name  string
		stats DailyStats
		want  bool
	}{
		{"empty", DailyStats{Date: "2026-04-05"}, true},
		{"has total", DailyStats{Date: "2026-04-05", Total: AggregatedStats{ReviewCount: 1}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.stats.IsEmpty(); got != tt.want {
				t.Errorf("IsEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}
