package code

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// FailureCategory 失败分类枚举。
type FailureCategory string

const (
	FailureCategoryNone             FailureCategory = "none"
	FailureCategoryInfoInsufficient FailureCategory = "info_insufficient"
	FailureCategoryTestFailure      FailureCategory = "test_failure"
	FailureCategoryInfrastructure   FailureCategory = "infrastructure"
)

// ModifiedFile 记录单个文件的变更。
type ModifiedFile struct {
	Path        string `json:"path"`
	Action      string `json:"action"`
	Description string `json:"description"`
}

// TestRunResults 测试运行结果统计。
type TestRunResults struct {
	Passed    int  `json:"passed"`
	Failed    int  `json:"failed"`
	Skipped   int  `json:"skipped"`
	AllPassed bool `json:"all_passed"`
}

// CodeFromDocOutput Claude 容器执行输出。
type CodeFromDocOutput struct {
	Success         bool            `json:"success"`
	InfoSufficient  bool            `json:"info_sufficient"`
	MissingInfo     []string        `json:"missing_info,omitempty"`
	BranchName      string          `json:"branch_name"`
	CommitSHA       string          `json:"commit_sha"`
	ModifiedFiles   []ModifiedFile  `json:"modified_files"`
	TestResults     TestRunResults  `json:"test_results"`
	Analysis        string          `json:"analysis"`
	Implementation  string          `json:"implementation"`
	FailureCategory FailureCategory `json:"failure_category"`
	FailureReason   string          `json:"failure_reason,omitempty"`
}

// CodeFromDocResult 包装 Execute 的完整结果，对齐 review.ReviewResult / fix.FixResult 模式。
type CodeFromDocResult struct {
	Output    *CodeFromDocOutput
	RawOutput string
	ExitCode  int
	PRNumber  int64
	PRURL     string
}

// ParseCodeFromDocOutput 从容器输出中解析 CodeFromDocOutput。
// 支持双层 JSON（Claude 有时会在外层包一个 result 字段）。
func ParseCodeFromDocOutput(raw string) (*CodeFromDocOutput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: 输出为空", ErrCodeFromDocParseFailure)
	}

	// 尝试直接解析：只有包含 CodeFromDocOutput 核心字段（success 为 true，或
	// failure_category 非空）时才认为是有效的直接解析结果；否则继续尝试双层解析。
	var output CodeFromDocOutput
	if err := json.Unmarshal([]byte(raw), &output); err == nil {
		if output.Success || output.FailureCategory != "" || output.InfoSufficient {
			if err := validateOutput(&output); err != nil {
				return &output, err
			}
			return &output, nil
		}
	}

	// 双层 JSON：尝试解析外层 {"result": "..."} 或 {"content": "..."}
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &wrapper); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCodeFromDocParseFailure, err)
	}

	for _, key := range []string{"result", "content", "output"} {
		inner, ok := wrapper[key]
		if !ok {
			continue
		}
		// inner 可能是字符串（转义 JSON）或直接对象
		var innerStr string
		if err := json.Unmarshal(inner, &innerStr); err == nil {
			if err2 := json.Unmarshal([]byte(innerStr), &output); err2 == nil {
				if err3 := validateOutput(&output); err3 != nil {
					return &output, err3
				}
				return &output, nil
			}
		}
		if err := json.Unmarshal(inner, &output); err == nil {
			if err2 := validateOutput(&output); err2 != nil {
				return &output, err2
			}
			return &output, nil
		}
	}

	return nil, fmt.Errorf("%w: 无法从输出中提取有效 JSON", ErrCodeFromDocParseFailure)
}

func validateOutput(o *CodeFromDocOutput) error {
	if o.Success {
		if o.BranchName == "" || o.CommitSHA == "" {
			return fmt.Errorf("%w: success=true 但 branch_name 或 commit_sha 为空", ErrCodeFromDocParseFailure)
		}
		if o.FailureCategory != FailureCategoryNone {
			return fmt.Errorf("%w: success=true 但 failure_category 非 none", ErrCodeFromDocParseFailure)
		}
	}
	return nil
}

// SanitizeCodeFromDocOutput 脱敏自由文本字段，防止 prompt injection 内容泄露到通知。
func SanitizeCodeFromDocOutput(o *CodeFromDocOutput) {
	if o == nil {
		return
	}
	o.Analysis = sanitizeText(o.Analysis, 500)
	o.Implementation = sanitizeText(o.Implementation, 1000)
	o.FailureReason = sanitizeText(o.FailureReason, 500)
	for i := range o.MissingInfo {
		o.MissingInfo[i] = sanitizeText(o.MissingInfo[i], 200)
	}
	for i := range o.ModifiedFiles {
		o.ModifiedFiles[i].Description = sanitizeText(o.ModifiedFiles[i].Description, 200)
	}
}

func sanitizeText(s string, maxLen int) string {
	s = strings.ReplaceAll(s, "\x00", "")
	// 移除控制字符（保留换行和 tab）
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || r >= 32 {
			b.WriteRune(r)
		}
	}
	s = b.String()
	if utf8.RuneCountInString(s) > maxLen {
		runes := []rune(s)
		s = string(runes[:maxLen]) + "..."
	}
	return s
}
