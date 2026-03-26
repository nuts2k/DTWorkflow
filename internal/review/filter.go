package review

import (
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// SeverityLevel 表示严重程度的有序类型
type SeverityLevel int

const (
	SeverityInfo     SeverityLevel = iota // 最低
	SeverityWarning
	SeverityError
	SeverityCritical                      // 最高
)

// ParseSeverity 将字符串转为 SeverityLevel。
// 无效值降级为 SeverityInfo（不过滤），fail-open。
func ParseSeverity(s string) SeverityLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return SeverityCritical
	case "error":
		return SeverityError
	case "warning":
		return SeverityWarning
	case "info":
		return SeverityInfo
	default:
		return SeverityInfo
	}
}

// String 返回 SeverityLevel 的字符串表示
func (l SeverityLevel) String() string {
	switch l {
	case SeverityCritical:
		return "critical"
	case SeverityError:
		return "error"
	case SeverityWarning:
		return "warning"
	case SeverityInfo:
		return "info"
	default:
		return "info"
	}
}

// AtLeast 判断当前级别是否 >= 阈值
func (l SeverityLevel) AtLeast(threshold SeverityLevel) bool {
	return l >= threshold
}

// MatchesIgnorePattern 检查文件路径是否匹配任一 ignore pattern。
// 使用 doublestar.Match，支持 ** 递归匹配。
// 路径使用 / 分隔（Gitea API 返回的 PR 文件路径格式）。
// 空 patterns 列表 → 不匹配任何文件（不过滤）。
// filePath 为空字符串（项目级 issue，无关联文件）→ 不匹配任何 pattern。
func MatchesIgnorePattern(filePath string, patterns []string) bool {
	if filePath == "" || len(patterns) == 0 {
		return false
	}
	for _, pattern := range patterns {
		// pattern 已在 Validate 阶段校验，忽略 err
		matched, _ := doublestar.Match(pattern, filePath)
		if matched {
			return true
		}
	}
	return false
}

// FilterResult 封装过滤结果
type FilterResult struct {
	Visible    []ReviewIssue // 过滤后可见的 issue
	Filtered   int           // 被过滤掉的总数
	BySeverity int           // 因 severity 阈值过滤的数量
	ByFile     int           // 因文件 glob 匹配过滤的数量
}

// FilterIssues 对 issue 列表应用 severity + 文件双重过滤。
// 过滤优先级：先检查文件 glob（命中计入 ByFile），再检查 severity（命中计入 BySeverity）。
// 同时命中两个条件时计入 ByFile（文件忽略优先）。
func FilterIssues(issues []ReviewIssue, severityThreshold string, ignorePatterns []string) FilterResult {
	result := FilterResult{
		Visible: make([]ReviewIssue, 0, len(issues)),
	}

	threshold := ParseSeverity(severityThreshold)

	for _, issue := range issues {
		// 先检查文件 glob
		if MatchesIgnorePattern(issue.File, ignorePatterns) {
			result.Filtered++
			result.ByFile++
			continue
		}

		// 再检查 severity
		issueSeverity := ParseSeverity(issue.Severity)
		if !issueSeverity.AtLeast(threshold) {
			result.Filtered++
			result.BySeverity++
			continue
		}

		result.Visible = append(result.Visible, issue)
	}

	return result
}
