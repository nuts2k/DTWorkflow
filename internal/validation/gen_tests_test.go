package validation

import (
	"strings"
	"testing"
)

func TestGenTestsFramework_AllowedValues(t *testing.T) {
	cases := []string{"", "junit5", "vitest"}
	for _, v := range cases {
		if err := GenTestsFramework(v); err != nil {
			t.Errorf("GenTestsFramework(%q) = %v，期望 nil", v, err)
		}
	}
}

func TestGenTestsFramework_RejectsOthers(t *testing.T) {
	cases := []string{"jest", "mocha", "junit", "JUNIT5", "Vitest", "pytest", " ", " junit5 "}
	for _, v := range cases {
		if err := GenTestsFramework(v); err == nil {
			t.Errorf("GenTestsFramework(%q) = nil，期望错误", v)
		}
	}
}

func TestGenTestsFramework_ErrorMessageHasAllowedList(t *testing.T) {
	err := GenTestsFramework("rspec")
	if err == nil {
		t.Fatal("应返回错误")
	}
	// 错误消息应列出合法值，便于用户排障
	msg := err.Error()
	if !strings.Contains(msg, `"junit5"`) || !strings.Contains(msg, `"vitest"`) {
		t.Errorf("错误消息应列出合法框架，实际: %q", msg)
	}
}

func TestGenTestsModule_EmptyIsAllowed(t *testing.T) {
	if err := GenTestsModule(""); err != nil {
		t.Errorf("空 module 应放行，得 %v", err)
	}
}

func TestGenTestsModule_AllowsLegitPaths(t *testing.T) {
	ok := []string{
		"service",
		"services/api",
		"packages/web/ui",
		"pkg/subpkg.v2",
		"a/b/c",
	}
	for _, m := range ok {
		if err := GenTestsModule(m); err != nil {
			t.Errorf("GenTestsModule(%q) = %v，期望 nil", m, err)
		}
	}
}

func TestGenTestsModule_RejectsAbsolute(t *testing.T) {
	cases := []string{"/abs/path", "/etc/passwd", "/"}
	for _, m := range cases {
		err := GenTestsModule(m)
		if err == nil {
			t.Errorf("GenTestsModule(%q) 期望错误，得 nil", m)
			continue
		}
		if !strings.Contains(err.Error(), "绝对路径") {
			t.Errorf("GenTestsModule(%q) 错误消息应含 \"绝对路径\"，实际: %v", m, err)
		}
	}
}

func TestGenTestsModule_RejectsDotDot(t *testing.T) {
	cases := []string{
		"..",
		"../escape",
		"a/../b",
		"a/b/..",
		`..\escape`,    // Windows 风格反斜杠
		`a\..\b`,       // 反斜杠中段
	}
	for _, m := range cases {
		err := GenTestsModule(m)
		if err == nil {
			t.Errorf("GenTestsModule(%q) 期望错误，得 nil", m)
			continue
		}
		if !strings.Contains(err.Error(), "..") {
			t.Errorf("GenTestsModule(%q) 错误消息应含 \"..\"，实际: %v", m, err)
		}
	}
}
