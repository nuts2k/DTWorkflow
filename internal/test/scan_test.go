package test

import (
	"context"
	"fmt"
	"testing"
)

type mockScanChecker struct {
	files map[string]bool
	dirs  map[string][]string
	errs  map[string]error
}

func (m *mockScanChecker) HasFile(_ context.Context, _, _, _, module, relPath string) (bool, error) {
	key := module + "/" + relPath
	if module == "" {
		key = relPath
	}
	if e, ok := m.errs[key]; ok {
		return false, e
	}
	return m.files[key], nil
}

func (m *mockScanChecker) ListDir(_ context.Context, _, _, _, dir string) ([]string, error) {
	if e, ok := m.errs["listdir:"+dir]; ok {
		return nil, e
	}
	return m.dirs[dir], nil
}

func TestScanRepoModules_RootSingleFrameworkJava(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{"pom.xml": true},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "owner", "repo", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 1 {
		t.Fatalf("期望 1 个模块，实际 %d", len(modules))
	}
	if modules[0].Path != "" || modules[0].Framework != FrameworkJUnit5 {
		t.Errorf("模块不符合预期: %+v", modules[0])
	}
}

func TestScanRepoModules_RootSingleFrameworkVue(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{"package.json": true},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 1 || modules[0].Framework != FrameworkVitest {
		t.Errorf("期望单条 vitest，实际: %+v", modules)
	}
}

func TestScanRepoModules_RootDualFramework(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{"pom.xml": true, "package.json": true},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 2 {
		t.Fatalf("期望 2 个模块，实际 %d", len(modules))
	}
	if modules[0].Framework != FrameworkJUnit5 || modules[1].Framework != FrameworkVitest {
		t.Errorf("框架不符合预期: %+v", modules)
	}
}

func TestScanRepoModules_Depth1MultiModule(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{
			"backend/pom.xml":       true,
			"frontend/package.json": true,
		},
		dirs: map[string][]string{"": {"backend", "frontend", "docs"}},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 2 {
		t.Fatalf("期望 2 个模块，实际 %d", len(modules))
	}
	if modules[0].Path != "backend" || modules[0].Framework != FrameworkJUnit5 {
		t.Errorf("第一个模块不符合预期: %+v", modules[0])
	}
	if modules[1].Path != "frontend" || modules[1].Framework != FrameworkVitest {
		t.Errorf("第二个模块不符合预期: %+v", modules[1])
	}
}

func TestScanRepoModules_RootAndDepth1Modules(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{
			"pom.xml":               true,
			"backend/pom.xml":       true,
			"frontend/package.json": true,
		},
		dirs: map[string][]string{"": {"backend", "frontend"}},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 3 {
		t.Fatalf("期望根模块 + 2 个子模块，实际 %d: %+v", len(modules), modules)
	}
	want := []DiscoveredModule{
		{Path: "", Framework: FrameworkJUnit5},
		{Path: "backend", Framework: FrameworkJUnit5},
		{Path: "frontend", Framework: FrameworkVitest},
	}
	for i := range want {
		if modules[i] != want[i] {
			t.Errorf("modules[%d] = %+v, want %+v", i, modules[i], want[i])
		}
	}
}

func TestScanRepoModules_NoFrameworkDetected(t *testing.T) {
	checker := &mockScanChecker{
		dirs: map[string][]string{"": {"docs", "scripts"}},
	}
	_, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != ErrNoFrameworkDetected {
		t.Errorf("期望 ErrNoFrameworkDetected，实际: %v", err)
	}
}

func TestScanRepoModules_ListDirError(t *testing.T) {
	checker := &mockScanChecker{
		errs: map[string]error{"listdir:": fmt.Errorf("network error")},
	}
	_, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err == nil {
		t.Fatal("期望返回错误")
	}
}

func TestScanRepoModules_SubdirHasFileError_SkipAndContinue(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{"good/pom.xml": true},
		dirs:  map[string][]string{"": {"bad", "good"}},
		errs:  map[string]error{"bad/pom.xml": fmt.Errorf("timeout")},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 1 || modules[0].Path != "good" {
		t.Errorf("期望跳过 bad 保留 good，实际: %+v", modules)
	}
}

func TestScanRepoModules_ExceedMaxDirs_Truncate(t *testing.T) {
	dirs := make([]string, 40)
	files := map[string]bool{}
	for i := range dirs {
		dirs[i] = fmt.Sprintf("mod%d", i)
		files[fmt.Sprintf("mod%d/pom.xml", i)] = true
	}
	checker := &mockScanChecker{files: files, dirs: map[string][]string{"": dirs}}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != maxScanDirs {
		t.Errorf("期望截断到 %d，实际 %d", maxScanDirs, len(modules))
	}
}

func TestScanRepoModules_SubdirDualFramework(t *testing.T) {
	checker := &mockScanChecker{
		files: map[string]bool{
			"mono/pom.xml":      true,
			"mono/package.json": true,
		},
		dirs: map[string][]string{"": {"mono"}},
	}
	modules, err := ScanRepoModules(context.Background(), checker, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 2 {
		t.Fatalf("期望 2 个模块（同目录双框架），实际 %d", len(modules))
	}
}
