package review

import (
	"testing"
)

func TestParseSeverity(t *testing.T) {
	tests := []struct {
		input    string
		expected SeverityLevel
	}{
		{"critical", SeverityCritical},
		{"CRITICAL", SeverityCritical},
		{"Critical", SeverityCritical},
		{"error", SeverityError},
		{"ERROR", SeverityError},
		{"Error", SeverityError},
		{"warning", SeverityWarning},
		{"WARNING", SeverityWarning},
		{"Warning", SeverityWarning},
		{"info", SeverityInfo},
		{"INFO", SeverityInfo},
		{"Info", SeverityInfo},
		{"", SeverityInfo},
		{"unknown", SeverityInfo},
		{"  warning  ", SeverityWarning},
		{"WARN", SeverityInfo}, // 无效值降级为 info
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := ParseSeverity(tt.input)
			if got != tt.expected {
				t.Errorf("ParseSeverity(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSeverityLevelString(t *testing.T) {
	tests := []struct {
		level    SeverityLevel
		expected string
	}{
		{SeverityInfo, "info"},
		{SeverityWarning, "warning"},
		{SeverityError, "error"},
		{SeverityCritical, "critical"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			got := tt.level.String()
			if got != tt.expected {
				t.Errorf("SeverityLevel(%d).String() = %q, want %q", tt.level, got, tt.expected)
			}
		})
	}
}

func TestAtLeast(t *testing.T) {
	tests := []struct {
		level     SeverityLevel
		threshold SeverityLevel
		expected  bool
	}{
		{SeverityInfo, SeverityInfo, true},
		{SeverityInfo, SeverityWarning, false},
		{SeverityInfo, SeverityError, false},
		{SeverityInfo, SeverityCritical, false},
		{SeverityWarning, SeverityInfo, true},
		{SeverityWarning, SeverityWarning, true},
		{SeverityWarning, SeverityError, false},
		{SeverityWarning, SeverityCritical, false},
		{SeverityError, SeverityInfo, true},
		{SeverityError, SeverityWarning, true},
		{SeverityError, SeverityError, true},
		{SeverityError, SeverityCritical, false},
		{SeverityCritical, SeverityInfo, true},
		{SeverityCritical, SeverityWarning, true},
		{SeverityCritical, SeverityError, true},
		{SeverityCritical, SeverityCritical, true},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := tt.level.AtLeast(tt.threshold)
			if got != tt.expected {
				t.Errorf("%v.AtLeast(%v) = %v, want %v", tt.level, tt.threshold, got, tt.expected)
			}
		})
	}
}

func TestMatchesIgnorePattern(t *testing.T) {
	tests := []struct {
		name     string
		filePath string
		patterns []string
		expected bool
	}{
		{
			name:     "基础 *.md 匹配",
			filePath: "README.md",
			patterns: []string{"*.md"},
			expected: true,
		},
		{
			name:     "*.md 不匹配非 md 文件",
			filePath: "main.go",
			patterns: []string{"*.md"},
			expected: false,
		},
		{
			name:     "docs/** 匹配子目录文件",
			filePath: "docs/api.md",
			patterns: []string{"docs/**"},
			expected: true,
		},
		{
			name:     "docs/** 匹配深层子目录",
			filePath: "docs/zh/guide/index.md",
			patterns: []string{"docs/**"},
			expected: true,
		},
		{
			name:     "docs/** 不匹配其他目录",
			filePath: "internal/service.go",
			patterns: []string{"docs/**"},
			expected: false,
		},
		{
			name:     "**/vendor/** 递归匹配 vendor",
			filePath: "vendor/pkg/file.go",
			patterns: []string{"**/vendor/**"},
			expected: true,
		},
		{
			name:     "**/vendor/** 匹配深层 vendor",
			filePath: "a/b/vendor/pkg/file.go",
			patterns: []string{"**/vendor/**"},
			expected: true,
		},
		{
			name:     "*.pb.go 匹配 protobuf 生成文件",
			filePath: "api/service.pb.go",
			patterns: []string{"*.pb.go"},
			expected: false, // 带目录前缀时不匹配（glob 不跨目录）
		},
		{
			name:     "**/*.pb.go 匹配任意目录下的 pb 文件",
			filePath: "api/service.pb.go",
			patterns: []string{"**/*.pb.go"},
			expected: true,
		},
		{
			name:     "**/*.generated.go 递归匹配生成文件",
			filePath: "internal/gen/model.generated.go",
			patterns: []string{"**/*.generated.go"},
			expected: true,
		},
		{
			name:     "**/*.generated.go 匹配根目录生成文件",
			filePath: "schema.generated.go",
			patterns: []string{"**/*.generated.go"},
			expected: true,
		},
		{
			name:     "空 patterns 列表 → false",
			filePath: "README.md",
			patterns: []string{},
			expected: false,
		},
		{
			name:     "nil patterns → false",
			filePath: "README.md",
			patterns: nil,
			expected: false,
		},
		{
			name:     "空 filePath → false",
			filePath: "",
			patterns: []string{"*.md", "docs/**"},
			expected: false,
		},
		{
			name:     "多 pattern 任一匹配即返回 true",
			filePath: "docs/api.md",
			patterns: []string{"*.go", "docs/**", "*.pb.go"},
			expected: true,
		},
		{
			name:     "多 pattern 全不匹配返回 false",
			filePath: "internal/service.go",
			patterns: []string{"*.md", "docs/**", "vendor/**"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesIgnorePattern(tt.filePath, tt.patterns)
			if got != tt.expected {
				t.Errorf("MatchesIgnorePattern(%q, %v) = %v, want %v", tt.filePath, tt.patterns, got, tt.expected)
			}
		})
	}
}

func TestFilterIssues(t *testing.T) {
	makeIssue := func(file, severity string) ReviewIssue {
		return ReviewIssue{
			File:     file,
			Severity: severity,
			Message:  "test issue",
		}
	}

	tests := []struct {
		name              string
		issues            []ReviewIssue
		severityThreshold string
		ignorePatterns    []string
		wantVisibleCount  int
		wantFiltered      int
		wantBySeverity    int
		wantByFile        int
	}{
		{
			name: "纯 severity 过滤：阈值 warning，过滤 INFO",
			issues: []ReviewIssue{
				makeIssue("main.go", "info"),
				makeIssue("main.go", "warning"),
				makeIssue("main.go", "error"),
				makeIssue("main.go", "critical"),
			},
			severityThreshold: "warning",
			ignorePatterns:    nil,
			wantVisibleCount:  3,
			wantFiltered:      1,
			wantBySeverity:    1,
			wantByFile:        0,
		},
		{
			name: "纯 severity 过滤：阈值 error",
			issues: []ReviewIssue{
				makeIssue("main.go", "info"),
				makeIssue("main.go", "warning"),
				makeIssue("main.go", "error"),
				makeIssue("main.go", "critical"),
			},
			severityThreshold: "error",
			ignorePatterns:    nil,
			wantVisibleCount:  2,
			wantFiltered:      2,
			wantBySeverity:    2,
			wantByFile:        0,
		},
		{
			name: "纯文件过滤：忽略 docs/**",
			issues: []ReviewIssue{
				makeIssue("docs/api.md", "critical"),
				makeIssue("docs/guide/index.md", "error"),
				makeIssue("internal/service.go", "warning"),
			},
			severityThreshold: "info",
			ignorePatterns:    []string{"docs/**"},
			wantVisibleCount:  1,
			wantFiltered:      2,
			wantBySeverity:    0,
			wantByFile:        2,
		},
		{
			name: "双重过滤：同时命中 severity 和文件，计入 ByFile",
			issues: []ReviewIssue{
				makeIssue("docs/api.md", "info"),     // 同时命中文件和 severity，计入 ByFile
				makeIssue("internal/main.go", "info"), // 仅命中 severity，计入 BySeverity
				makeIssue("internal/svc.go", "error"), // 均不命中，visible
			},
			severityThreshold: "warning",
			ignorePatterns:    []string{"docs/**"},
			wantVisibleCount:  1,
			wantFiltered:      2,
			wantBySeverity:    1,
			wantByFile:        1,
		},
		{
			name: "全部被过滤：返回空 Visible、正确计数",
			issues: []ReviewIssue{
				makeIssue("docs/api.md", "info"),
				makeIssue("vendor/pkg.go", "warning"),
			},
			severityThreshold: "error",
			ignorePatterns:    []string{"docs/**", "**/vendor/**"},
			wantVisibleCount:  0,
			wantFiltered:      2,
			wantBySeverity:    0,
			wantByFile:        2,
		},
		{
			name: "无过滤条件：severity='' + patterns=nil → 全部 visible",
			issues: []ReviewIssue{
				makeIssue("main.go", "info"),
				makeIssue("service.go", "warning"),
				makeIssue("handler.go", "critical"),
			},
			severityThreshold: "",
			ignorePatterns:    nil,
			wantVisibleCount:  3,
			wantFiltered:      0,
			wantBySeverity:    0,
			wantByFile:        0,
		},
		{
			name:              "空 issues 列表",
			issues:            []ReviewIssue{},
			severityThreshold: "warning",
			ignorePatterns:    []string{"docs/**"},
			wantVisibleCount:  0,
			wantFiltered:      0,
			wantBySeverity:    0,
			wantByFile:        0,
		},
		{
			name: "项目级 issue（空 file）不被文件 pattern 过滤",
			issues: []ReviewIssue{
				makeIssue("", "info"),    // 空 file，不被文件 pattern 过滤，但被 severity 过滤
				makeIssue("", "warning"), // 空 file，visible
			},
			severityThreshold: "warning",
			ignorePatterns:    []string{"**/*"},
			wantVisibleCount:  1,
			wantFiltered:      1,
			wantBySeverity:    1,
			wantByFile:        0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterIssues(tt.issues, tt.severityThreshold, tt.ignorePatterns)

			if len(result.Visible) != tt.wantVisibleCount {
				t.Errorf("Visible count = %d, want %d", len(result.Visible), tt.wantVisibleCount)
			}
			if result.Filtered != tt.wantFiltered {
				t.Errorf("Filtered = %d, want %d", result.Filtered, tt.wantFiltered)
			}
			if result.BySeverity != tt.wantBySeverity {
				t.Errorf("BySeverity = %d, want %d", result.BySeverity, tt.wantBySeverity)
			}
			if result.ByFile != tt.wantByFile {
				t.Errorf("ByFile = %d, want %d", result.ByFile, tt.wantByFile)
			}
		})
	}
}
