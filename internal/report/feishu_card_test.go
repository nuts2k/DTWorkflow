package report

import (
	"strings"
	"testing"
)

func TestFormatDailyReportCard_WithData(t *testing.T) {
	stats := &DailyStats{
		Date: "2026-04-05",
		Total: AggregatedStats{
			ReviewCount:    12,
			ApproveCount:   8,
			RequestChanges: 3,
			CommentCount:   1,
			CriticalCount:  2,
			ErrorCount:     5,
			WarningCount:   18,
			InfoCount:      7,
			AvgDurationMs:  222000,
			TotalCostUSD:   1.24,
		},
		RepoStats: []RepoStats{
			{
				RepoFullName: "acme/backend",
				Stats:        AggregatedStats{ReviewCount: 8, ApproveCount: 6, RequestChanges: 2, CriticalCount: 1, ErrorCount: 3},
			},
		},
	}
	prevStats := &DailyStats{
		Date:  "2026-04-04",
		Total: AggregatedStats{ReviewCount: 9, CriticalCount: 1, ErrorCount: 7},
	}

	card := FormatDailyReportCard(stats, prevStats)

	// 验证顶层结构
	if card["msg_type"] != "interactive" {
		t.Errorf("msg_type = %v, want interactive", card["msg_type"])
	}

	cardContent, ok := card["card"].(map[string]any)
	if !ok {
		t.Fatal("missing card content")
	}
	header, ok := cardContent["header"].(map[string]any)
	if !ok {
		t.Fatal("missing header")
	}

	// 蓝色 header
	if header["template"] != "blue" {
		t.Errorf("header template = %v, want blue", header["template"])
	}

	// 标题包含日期
	titleMap, ok := header["title"].(map[string]string)
	if !ok || !strings.Contains(titleMap["content"], "2026-04-05") {
		t.Error("header title should contain date")
	}
}

func TestFormatDailyReportCard_Empty(t *testing.T) {
	stats := &DailyStats{Date: "2026-04-05"}

	card := FormatDailyReportCard(stats, nil)

	cardContent := card["card"].(map[string]any)
	header := cardContent["header"].(map[string]any)

	// 灰色 header
	if header["template"] != "grey" {
		t.Errorf("empty report should have grey header, got %v", header["template"])
	}
}

func TestTrendIndicator(t *testing.T) {
	tests := []struct {
		current, previous int
		hasPrev           bool
		want              string
	}{
		{10, 5, true, "(+5)"},
		{3, 8, true, "(-5)"},
		{5, 5, true, "(=)"},
		{10, 0, false, ""},
	}
	for _, tt := range tests {
		got := trendIndicator(tt.current, tt.previous, tt.hasPrev)
		if got != tt.want {
			t.Errorf("trendIndicator(%d, %d, %v) = %q, want %q", tt.current, tt.previous, tt.hasPrev, got, tt.want)
		}
	}
}
