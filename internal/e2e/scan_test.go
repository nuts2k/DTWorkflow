package e2e

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

type mockE2EScanner struct {
	dirs map[string][]string
	errs map[string]error
}

func (m *mockE2EScanner) ListDir(_ context.Context, _, _, _, dir string) ([]string, error) {
	if err, ok := m.errs[dir]; ok {
		return nil, err
	}
	return m.dirs[dir], nil
}

func TestScanE2EModules_Normal(t *testing.T) {
	scanner := &mockE2EScanner{
		dirs: map[string][]string{
			"e2e":       {"order", "auth", "docs"},
			"e2e/order": {"cases", "fixtures"},
			"e2e/auth":  {"cases"},
			"e2e/docs":  {"readme.md"},
		},
	}
	modules, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 2 {
		t.Fatalf("expected 2 modules, got %d: %v", len(modules), modules)
	}
	if modules[0] != "order" || modules[1] != "auth" {
		t.Errorf("unexpected modules: %v", modules)
	}
}

func TestScanE2EModules_Empty(t *testing.T) {
	scanner := &mockE2EScanner{dirs: map[string][]string{"e2e": {}}}
	_, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if !errors.Is(err, ErrNoE2EModulesFound) {
		t.Fatalf("expected ErrNoE2EModulesFound, got: %v", err)
	}
}

func TestScanE2EModules_NoCasesDir(t *testing.T) {
	scanner := &mockE2EScanner{
		dirs: map[string][]string{
			"e2e":       {"order", "auth"},
			"e2e/order": {"fixtures"},
			"e2e/auth":  {"scripts"},
		},
	}
	_, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if !errors.Is(err, ErrNoE2EModulesFound) {
		t.Fatalf("expected ErrNoE2EModulesFound, got: %v", err)
	}
}

func TestScanE2EModules_TopDirFails(t *testing.T) {
	scanner := &mockE2EScanner{
		errs: map[string]error{"e2e": errors.New("network error")},
	}
	_, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if err == nil || errors.Is(err, ErrNoE2EModulesFound) {
		t.Fatalf("expected non-ErrNoE2EModulesFound error, got: %v", err)
	}
}

func TestScanE2EModules_TopDirNotFound_ReturnsNoModules(t *testing.T) {
	scanner := &mockE2EScanner{
		errs: map[string]error{"e2e": &gitea.ErrorResponse{StatusCode: http.StatusNotFound}},
	}
	_, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if !errors.Is(err, ErrNoE2EModulesFound) {
		t.Fatalf("expected ErrNoE2EModulesFound, got: %v", err)
	}
}

func TestScanE2EModules_SubDirFails_SkipsContinues(t *testing.T) {
	scanner := &mockE2EScanner{
		dirs: map[string][]string{
			"e2e":      {"order", "auth"},
			"e2e/auth": {"cases"},
		},
		errs: map[string]error{"e2e/order": errors.New("timeout")},
	}
	modules, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) != 1 || modules[0] != "auth" {
		t.Fatalf("expected [auth], got: %v", modules)
	}
}

func TestScanE2EModules_Truncation(t *testing.T) {
	dirs := make([]string, 35)
	scanner := &mockE2EScanner{dirs: map[string][]string{}}
	for i := range dirs {
		name := "mod" + string(rune('a'+i%26)) + string(rune('0'+i/26))
		dirs[i] = name
		scanner.dirs["e2e/"+name] = []string{"cases"}
	}
	scanner.dirs["e2e"] = dirs
	modules, err := ScanE2EModules(context.Background(), scanner, "o", "r", "main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(modules) > maxE2EScanDirs {
		t.Fatalf("expected at most %d modules, got %d", maxE2EScanDirs, len(modules))
	}
}
