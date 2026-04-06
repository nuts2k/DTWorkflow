package report

import (
	"context"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// mockReviewStore 仅实现 ListReviewResultsByTimeRange
type mockReviewStore struct {
	results []*model.ReviewRecord
	err     error
}

func (m *mockReviewStore) ListReviewResultsByTimeRange(_ context.Context, _, _ time.Time) ([]*model.ReviewRecord, error) {
	return m.results, m.err
}

// testReviewRecords 创建测试用评审记录（在 generator_test.go 中也会用到）
func testReviewRecords() []*model.ReviewRecord {
	now := time.Now()
	return []*model.ReviewRecord{
		{RepoFullName: "acme/backend", PRNumber: 1, Verdict: "approve", IssueCount: 2, DurationMs: 10000, CostUSD: 0.5, CreatedAt: now.Add(-2 * time.Hour)},
		{RepoFullName: "acme/backend", PRNumber: 2, Verdict: "request_changes", IssueCount: 5, CriticalCount: 2, ErrorCount: 2, WarningCount: 1, DurationMs: 20000, CostUSD: 0.8, CreatedAt: now.Add(-4 * time.Hour)},
	}
}

func TestReviewStatCollector_Collect(t *testing.T) {
	records := []*model.ReviewRecord{
		{RepoFullName: "acme/backend", PRNumber: 1, Verdict: "approve", IssueCount: 2, CriticalCount: 0, ErrorCount: 1, WarningCount: 1, InfoCount: 0, DurationMs: 10000, CostUSD: 0.5},
		{RepoFullName: "acme/backend", PRNumber: 2, Verdict: "request_changes", IssueCount: 5, CriticalCount: 2, ErrorCount: 2, WarningCount: 1, InfoCount: 0, DurationMs: 20000, CostUSD: 0.8},
		{RepoFullName: "acme/frontend", PRNumber: 3, Verdict: "approve", IssueCount: 1, CriticalCount: 0, ErrorCount: 0, WarningCount: 0, InfoCount: 1, DurationMs: 5000, CostUSD: 0.2},
	}

	store := &mockReviewStore{results: records}
	collector := NewReviewStatCollector(store)

	now := time.Now()
	tr := TimeRange{Start: now.Add(-24 * time.Hour), End: now}

	stats, err := collector.Collect(context.Background(), tr)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// 总计
	if stats.Total.ReviewCount != 3 {
		t.Errorf("Total.ReviewCount = %d, want 3", stats.Total.ReviewCount)
	}
	if stats.Total.ApproveCount != 2 {
		t.Errorf("Total.ApproveCount = %d, want 2", stats.Total.ApproveCount)
	}
	if stats.Total.RequestChanges != 1 {
		t.Errorf("Total.RequestChanges = %d, want 1", stats.Total.RequestChanges)
	}
	if stats.Total.CriticalCount != 2 {
		t.Errorf("Total.CriticalCount = %d, want 2", stats.Total.CriticalCount)
	}
	if stats.Total.TotalCostUSD != 1.5 {
		t.Errorf("Total.TotalCostUSD = %f, want 1.5", stats.Total.TotalCostUSD)
	}

	// 仓库分组
	if len(stats.RepoStats) != 2 {
		t.Fatalf("RepoStats count = %d, want 2", len(stats.RepoStats))
	}
}

func TestReviewStatCollector_Collect_Empty(t *testing.T) {
	store := &mockReviewStore{results: nil}
	collector := NewReviewStatCollector(store)

	stats, err := collector.Collect(context.Background(), TimeRange{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if !stats.IsEmpty() {
		t.Error("expected empty stats")
	}
}

func TestReviewStatCollector_TopPRs(t *testing.T) {
	// 同一 PR 多次评审
	records := []*model.ReviewRecord{
		{RepoFullName: "acme/backend", PRNumber: 42, Verdict: "request_changes", IssueCount: 3},
		{RepoFullName: "acme/backend", PRNumber: 42, Verdict: "request_changes", IssueCount: 2},
		{RepoFullName: "acme/backend", PRNumber: 42, Verdict: "approve", IssueCount: 1},
		{RepoFullName: "acme/backend", PRNumber: 10, Verdict: "approve", IssueCount: 12},
	}

	store := &mockReviewStore{results: records}
	collector := NewReviewStatCollector(store)
	stats, err := collector.Collect(context.Background(), TimeRange{})
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	// acme/backend 应有 TopPRs
	if len(stats.RepoStats) != 1 {
		t.Fatalf("RepoStats count = %d, want 1", len(stats.RepoStats))
	}
	repo := stats.RepoStats[0]
	if len(repo.TopPRs) == 0 {
		t.Fatal("expected TopPRs, got none")
	}

	// PR #42 评审次数最多（3 次）
	foundReviewTop := false
	for _, tp := range repo.TopPRs {
		if tp.Reason == "review_count" && tp.PRNumber == 42 && tp.ReviewCount == 3 {
			foundReviewTop = true
		}
	}
	if !foundReviewTop {
		t.Error("expected PR #42 as top by review_count")
	}

	// PR #10 issue 最多（12 个）
	foundIssueTop := false
	for _, tp := range repo.TopPRs {
		if tp.Reason == "issue_count" && tp.PRNumber == 10 && tp.IssueCount == 12 {
			foundIssueTop = true
		}
	}
	if !foundIssueTop {
		t.Error("expected PR #10 as top by issue_count")
	}
}
