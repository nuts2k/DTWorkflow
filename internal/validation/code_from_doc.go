package validation

import (
	"fmt"
	"strings"
)

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
