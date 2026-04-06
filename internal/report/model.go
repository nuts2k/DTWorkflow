package report

import "time"

// TimeRange 查询时间窗口
type TimeRange struct {
	Start time.Time // inclusive
	End   time.Time // exclusive
}

// DailyStats 每日统计汇总
type DailyStats struct {
	Date      string          // "2026-04-05"
	RepoStats []RepoStats     // 按仓库分组
	Total     AggregatedStats // 跨仓库汇总
}

// IsEmpty 判断是否无评审数据
func (s *DailyStats) IsEmpty() bool {
	return s.Total.IsEmpty()
}

// RepoStats 单仓库统计
type RepoStats struct {
	RepoFullName string
	Stats        AggregatedStats
	TopPRs       []TopPR
}

// AggregatedStats 聚合统计
type AggregatedStats struct {
	ReviewCount    int     // 评审总数
	ApproveCount   int     // approve 次数
	RequestChanges int     // request_changes 次数
	CommentCount   int     // comment 次数（含降级场景）
	FailedCount    int     // 解析失败次数
	CriticalCount  int     // CRITICAL issue 总数
	ErrorCount     int     // ERROR issue 总数
	WarningCount   int     // WARNING issue 总数
	InfoCount      int     // INFO issue 总数
	AvgDurationMs  int64   // 平均评审耗时
	TotalCostUSD   float64 // 总费用
}

// IsEmpty 判断是否无评审数据
func (s *AggregatedStats) IsEmpty() bool {
	return s.ReviewCount == 0
}

// TopPR 关键 PR
type TopPR struct {
	PRNumber    int64
	RepoName    string
	ReviewCount int    // 该 PR 被评审次数
	IssueCount  int    // 该 PR 的 issue 总数
	Reason      string // "review_count" 或 "issue_count"
}
