package validation

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
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
	if strings.TrimSpace(normalized) != normalized {
		return fmt.Errorf("doc_path 不能包含首尾空白: %s", docPath)
	}
	if strings.Contains(normalized, "//") {
		return fmt.Errorf("doc_path 不能包含连续斜杠: %s", docPath)
	}

	// 禁止绝对路径
	if strings.HasPrefix(normalized, "/") {
		return fmt.Errorf("doc_path 不能为绝对路径: %s", docPath)
	}

	// 禁止路径遍历
	for _, part := range strings.Split(normalized, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("doc_path 包含非法路径段: %s", docPath)
		}
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				continue
			}
			switch r {
			case '.', '_', '-':
				continue
			default:
				return fmt.Errorf("doc_path 只能包含字母、数字、'.'、'_'、'-'、'/': %s", docPath)
			}
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
	return validateSafeGitRef("branch", branch)
}

// ValidateBaseRef 校验 code_from_doc 的基础 ref。
// 空字符串表示调用方自行回落仓库默认分支，允许通过。
func ValidateBaseRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	return validateSafeGitRef("ref", ref)
}

func validateSafeGitRef(field, ref string) error {
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%s 不能以 '-' 开头: %s", field, ref)
	}
	if ref == "HEAD" || strings.HasPrefix(ref, "refs/") {
		return fmt.Errorf("%s 不能使用保留引用名: %s", field, ref)
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return fmt.Errorf("%s 不能以 '/' 开头或结尾: %s", field, ref)
	}
	if strings.HasSuffix(ref, ".") || strings.HasSuffix(ref, ".lock") {
		return fmt.Errorf("%s 不能以 '.' 或 '.lock' 结尾: %s", field, ref)
	}
	if !safeBranchRefPattern.MatchString(ref) {
		return fmt.Errorf("%s 只能包含字母、数字、'.'、'_'、'-'、'/': %s", field, ref)
	}
	if strings.Contains(ref, "..") || strings.Contains(ref, "//") || strings.Contains(ref, "@{") {
		return fmt.Errorf("%s 包含非法序列: %s", field, ref)
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("%s 包含非法路径段: %s", field, ref)
		}
		if strings.HasPrefix(part, ".") {
			return fmt.Errorf("%s 路径段不能以 '.' 开头: %s", field, ref)
		}
	}
	return nil
}
