package validation

import (
	"fmt"
	"regexp"
	"strings"
)

var safeBranchRefPattern = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// ValidateDocPath 校验 doc_path 参数的合法性。
// 被 API handler / 服务端 CLI / dtw CLI 三处共用。
func ValidateDocPath(docPath string) error {
	if strings.TrimSpace(docPath) == "" {
		return fmt.Errorf("doc_path 不能为空")
	}

	// 归一化反斜杠
	normalized := strings.ReplaceAll(docPath, "\\", "/")

	// 禁止绝对路径
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("doc_path 不能为绝对路径: %s", docPath)
	}

	// 禁止路径遍历
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return fmt.Errorf("doc_path 不能包含路径遍历 (..): %s", docPath)
		}
	}

	return nil
}

// NormalizeDocPath 归一化文档路径（反斜杠转正斜杠）。
// 调用方应先通过 ValidateDocPath 校验。
func NormalizeDocPath(docPath string) string {
	return strings.ReplaceAll(docPath, "\\", "/")
}

// ValidateBranchRef 校验 code_from_doc 可写目标分支。
// 空字符串表示由系统派生 auto-code/{doc-slug}，允许通过。
func ValidateBranchRef(branch string) error {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return nil
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("branch 不能以 '-' 开头: %s", branch)
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return fmt.Errorf("branch 不能以 '/' 开头或结尾: %s", branch)
	}
	if strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return fmt.Errorf("branch 不能以 '.' 或 '.lock' 结尾: %s", branch)
	}
	if !safeBranchRefPattern.MatchString(branch) {
		return fmt.Errorf("branch 只能包含字母、数字、'.'、'_'、'-'、'/': %s", branch)
	}
	if strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.Contains(branch, "@{") {
		return fmt.Errorf("branch 包含非法序列: %s", branch)
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("branch 包含非法路径段: %s", branch)
		}
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("branch 路径段不能以 '.' 开头: %s", branch)
		}
	}
	return nil
}
