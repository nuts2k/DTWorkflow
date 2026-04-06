package report

import (
	"context"
	"sort"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// ReviewResultStore 是 StatCollector 对 Store 的最小依赖接口
type ReviewResultStore interface {
	ListReviewResultsByTimeRange(ctx context.Context, start, end time.Time) ([]*model.ReviewRecord, error)
}

// StatCollector 统计收集器接口
type StatCollector interface {
	Collect(ctx context.Context, tr TimeRange) (*DailyStats, error)
}

// ReviewStatCollector 评审统计收集器
type ReviewStatCollector struct {
	store ReviewResultStore
}

// NewReviewStatCollector 创建评审统计收集器
func NewReviewStatCollector(store ReviewResultStore) *ReviewStatCollector {
	return &ReviewStatCollector{store: store}
}

func (c *ReviewStatCollector) Collect(ctx context.Context, tr TimeRange) (*DailyStats, error) {
	records, err := c.store.ListReviewResultsByTimeRange(ctx, tr.Start, tr.End)
	if err != nil {
		return nil, err
	}

	stats := &DailyStats{
		Date: tr.Start.Format("2006-01-02"),
	}
	if len(records) == 0 {
		return stats, nil
	}

	// 按仓库分组
	repoMap := make(map[string][]*model.ReviewRecord)
	for _, r := range records {
		repoMap[r.RepoFullName] = append(repoMap[r.RepoFullName], r)
	}

	// 稳定排序：按仓库名排序
	repoNames := make([]string, 0, len(repoMap))
	for name := range repoMap {
		repoNames = append(repoNames, name)
	}
	sort.Strings(repoNames)

	for _, name := range repoNames {
		repoRecords := repoMap[name]
		repoStat := RepoStats{
			RepoFullName: name,
			Stats:        aggregateRecords(repoRecords),
			TopPRs:       findTopPRs(name, repoRecords),
		}
		stats.RepoStats = append(stats.RepoStats, repoStat)
	}

	stats.Total = aggregateRecords(records)
	return stats, nil
}

func aggregateRecords(records []*model.ReviewRecord) AggregatedStats {
	var s AggregatedStats
	var totalDuration int64
	for _, r := range records {
		s.ReviewCount++
		switch strings.ToLower(r.Verdict) {
		case "approve":
			s.ApproveCount++
		case "request_changes":
			s.RequestChanges++
		case "comment":
			s.CommentCount++
		}
		if r.ParseFailed {
			s.FailedCount++
		}
		s.CriticalCount += r.CriticalCount
		s.ErrorCount += r.ErrorCount
		s.WarningCount += r.WarningCount
		s.InfoCount += r.InfoCount
		totalDuration += r.DurationMs
		s.TotalCostUSD += r.CostUSD
	}
	if s.ReviewCount > 0 {
		s.AvgDurationMs = totalDuration / int64(s.ReviewCount)
	}
	return s
}

// findTopPRs 找出评审次数最多和 issue 数量最多的 PR，去重后最多 2 条
func findTopPRs(repoName string, records []*model.ReviewRecord) []TopPR {
	type prAgg struct {
		ReviewCount   int
		MaxIssueCount int
	}
	m := make(map[int64]*prAgg)
	for _, r := range records {
		agg, ok := m[r.PRNumber]
		if !ok {
			agg = &prAgg{}
			m[r.PRNumber] = agg
		}
		agg.ReviewCount++
		if r.IssueCount > agg.MaxIssueCount {
			agg.MaxIssueCount = r.IssueCount
		}
	}

	// 找评审次数最多的（必须 > 1 才有意义）
	var topByReview *TopPR
	maxReviewCount := 1
	for prNum, agg := range m {
		if agg.ReviewCount > maxReviewCount {
			maxReviewCount = agg.ReviewCount
			topByReview = &TopPR{
				PRNumber:    prNum,
				RepoName:    repoName,
				ReviewCount: agg.ReviewCount,
				Reason:      "review_count",
			}
		}
	}

	// 找 issue 最多的（必须 > 0）
	var topByIssue *TopPR
	maxIssueCount := 0
	for prNum, agg := range m {
		if agg.MaxIssueCount > maxIssueCount {
			maxIssueCount = agg.MaxIssueCount
			topByIssue = &TopPR{
				PRNumber:   prNum,
				RepoName:   repoName,
				IssueCount: agg.MaxIssueCount,
				Reason:     "issue_count",
			}
		}
	}

	var result []TopPR
	if topByReview != nil {
		result = append(result, *topByReview)
	}
	if topByIssue != nil {
		// 去重：如果是同一个 PR，只保留 review_count 那条
		if topByReview == nil || topByIssue.PRNumber != topByReview.PRNumber {
			result = append(result, *topByIssue)
		}
	}
	return result
}
