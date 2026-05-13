package iterate

import "strings"

// ReviewIssueSummary 评审问题的精简表示，用于过滤判断。
type ReviewIssueSummary struct {
	Severity string
}

// ContainsLabel 检查标签列表中是否包含指定标签。
func ContainsLabel(labels []string, target string) bool {
	for _, l := range labels {
		if l == target {
			return true
		}
	}
	return false
}

// FilterBySeverity 按严重等级阈值过滤问题。
// threshold 为 "error" 时，返回 CRITICAL + ERROR 等级的问题。
func FilterBySeverity(issues []ReviewIssueSummary, threshold string) []ReviewIssueSummary {
	minRank := SeverityRank(strings.ToUpper(threshold))
	var result []ReviewIssueSummary
	for _, issue := range issues {
		if SeverityRank(strings.ToUpper(issue.Severity)) >= minRank {
			result = append(result, issue)
		}
	}
	return result
}

// SeverityRank 返回严重等级的数值排名（越大越严重）。
func SeverityRank(severity string) int {
	switch severity {
	case "CRITICAL":
		return 4
	case "ERROR":
		return 3
	case "WARNING":
		return 2
	case "INFO":
		return 1
	default:
		return 0
	}
}
