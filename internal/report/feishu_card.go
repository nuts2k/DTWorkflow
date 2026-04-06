package report

import (
	"fmt"
	"strings"
	"time"
)

// FormatDailyReportCard 格式化每日统计报告为飞书交互卡片
func FormatDailyReportCard(stats, prevStats *DailyStats) map[string]any {
	if stats.IsEmpty() {
		return buildEmptyReportCard(stats.Date)
	}
	return buildFullReportCard(stats, prevStats)
}

func buildEmptyReportCard(date string) map[string]any {
	return buildCard(
		fmt.Sprintf("每日评审统计 - %s", formatDateWithWeekday(date)),
		"grey",
		[]any{
			mdElement("昨日无 PR 评审活动"),
		},
	)
}

func buildFullReportCard(stats, prevStats *DailyStats) map[string]any {
	hasPrev := prevStats != nil && !prevStats.IsEmpty()
	var prevTotal AggregatedStats
	if hasPrev {
		prevTotal = prevStats.Total
	}
	s := stats.Total

	var parts []string

	// 总览
	parts = append(parts, "**总览**")
	parts = append(parts, fmt.Sprintf("评审总数：**%d** %s", s.ReviewCount, trendIndicator(s.ReviewCount, prevTotal.ReviewCount, hasPrev)))
	parts = append(parts, fmt.Sprintf("通过：%d　需修改：%d　评论：%d",
		s.ApproveCount, s.RequestChanges, s.CommentCount))
	parts = append(parts, fmt.Sprintf("耗时均值：%s　总费用：$%.2f", formatDuration(s.AvgDurationMs), s.TotalCostUSD))
	parts = append(parts, "")

	// 问题分布
	parts = append(parts, "**问题分布**")
	parts = append(parts, fmt.Sprintf("CRITICAL: %d %s　ERROR: %d %s",
		s.CriticalCount, trendIndicator(s.CriticalCount, prevTotal.CriticalCount, hasPrev),
		s.ErrorCount, trendIndicator(s.ErrorCount, prevTotal.ErrorCount, hasPrev)))
	parts = append(parts, fmt.Sprintf("WARNING: %d %s　INFO: %d %s",
		s.WarningCount, trendIndicator(s.WarningCount, prevTotal.WarningCount, hasPrev),
		s.InfoCount, trendIndicator(s.InfoCount, prevTotal.InfoCount, hasPrev)))

	elements := []any{mdElement(strings.Join(parts, "\n"))}

	// 仓库明细
	if len(stats.RepoStats) > 0 {
		elements = append(elements, divider())

		var repoParts []string
		for _, repo := range stats.RepoStats {
			rs := repo.Stats
			repoParts = append(repoParts, fmt.Sprintf("**%s**（%d 次评审）", repo.RepoFullName, rs.ReviewCount))
			repoParts = append(repoParts, fmt.Sprintf("通过 %d　需修改 %d　评论 %d | CRITICAL: %d　ERROR: %d",
				rs.ApproveCount, rs.RequestChanges, rs.CommentCount, rs.CriticalCount, rs.ErrorCount))
		}
		elements = append(elements, mdElement(strings.Join(repoParts, "\n")))
	}

	// 关键 PR
	var topParts []string
	for _, repo := range stats.RepoStats {
		for _, tp := range repo.TopPRs {
			switch tp.Reason {
			case "review_count":
				topParts = append(topParts, fmt.Sprintf("评审最多：%s #%d（%d 次重新评审）", tp.RepoName, tp.PRNumber, tp.ReviewCount))
			case "issue_count":
				topParts = append(topParts, fmt.Sprintf("问题最多：%s #%d（%d 个 issue）", tp.RepoName, tp.PRNumber, tp.IssueCount))
			}
		}
	}
	if len(topParts) > 0 {
		elements = append(elements, divider())
		elements = append(elements, mdElement("**关键 PR**\n"+strings.Join(topParts, "\n")))
	}

	return buildCard(
		fmt.Sprintf("每日评审统计 - %s", formatDateWithWeekday(stats.Date)),
		"blue",
		elements,
	)
}

func buildCard(title, color string, elements []any) map[string]any {
	return map[string]any{
		"msg_type": "interactive",
		"card": map[string]any{
			"config": map[string]bool{"wide_screen_mode": true},
			"header": map[string]any{
				"title":    map[string]string{"tag": "plain_text", "content": title},
				"template": color,
			},
			"elements": elements,
		},
	}
}

func mdElement(content string) map[string]any {
	return map[string]any{
		"tag":     "markdown",
		"content": content,
	}
}

func divider() map[string]any {
	return map[string]any{"tag": "hr"}
}

// trendIndicator 计算趋势指示符
func trendIndicator(current, previous int, hasPrev bool) string {
	if !hasPrev {
		return ""
	}
	diff := current - previous
	switch {
	case diff > 0:
		return fmt.Sprintf("(+%d)", diff)
	case diff < 0:
		return fmt.Sprintf("(%d)", diff)
	default:
		return "(=)"
	}
}

func formatDuration(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func formatDateWithWeekday(dateStr string) string {
	t, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return dateStr
	}
	weekdays := []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}
	return fmt.Sprintf("%s（%s）", dateStr, weekdays[t.Weekday()])
}
