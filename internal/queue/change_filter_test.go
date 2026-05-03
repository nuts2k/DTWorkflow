package queue

import (
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

func TestExtractFilenames(t *testing.T) {
	tests := []struct {
		name  string
		input []*gitea.ChangedFile
		want  []string
	}{
		{
			name: "normal files",
			input: []*gitea.ChangedFile{
				{Filename: "src/main.go", Status: "modified"},
				{Filename: "src/util.go", Status: "added"},
			},
			want: []string{"src/main.go", "src/util.go"},
		},
		{
			name: "nil entry skipped",
			input: []*gitea.ChangedFile{
				nil,
				{Filename: "src/main.go", Status: "added"},
			},
			want: []string{"src/main.go"},
		},
		{
			name: "deleted excluded",
			input: []*gitea.ChangedFile{
				{Filename: "old.go", Status: "deleted"},
				{Filename: "new.go", Status: "added"},
			},
			want: []string{"new.go"},
		},
		{
			name: "deleted case insensitive",
			input: []*gitea.ChangedFile{
				{Filename: "old.go", Status: "Deleted"},
			},
			want: nil,
		},
		{
			name: "empty filename skipped",
			input: []*gitea.ChangedFile{
				{Filename: "", Status: "added"},
				{Filename: "real.go", Status: "added"},
			},
			want: []string{"real.go"},
		},
		{
			name: "nil slice",
			input: nil,
			want:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFilenames(tt.input)
			assertStringSliceEqual(t, tt.want, got)
		})
	}
}

func TestFilterSourceFiles_Extensions(t *testing.T) {
	files := []string{
		"src/main.go",
		"README.md",
		"config.yaml",
		"data.json",
		"image.png",
		"src/service.java",
		"frontend/App.vue",
		"frontend/index.ts",
	}
	got := filterSourceFiles(files, nil)
	want := []string{"src/main.go", "src/service.java", "frontend/App.vue", "frontend/index.ts"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_DotFiles(t *testing.T) {
	files := []string{
		".gitignore",
		".editorconfig",
		".env",
		"src/main.go",
	}
	got := filterSourceFiles(files, nil)
	want := []string{"src/main.go"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_Prefixes(t *testing.T) {
	files := []string{
		"docs/guide.go",
		".github/workflows/ci.go",
		"deploy/script.sh",
		"src/main.go",
	}
	got := filterSourceFiles(files, nil)
	want := []string{"src/main.go"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_ShellScripts(t *testing.T) {
	files := []string{
		"src/main.go",
		"scripts/build.sh",
		"tools/deploy.sh",
	}
	got := filterSourceFiles(files, nil)
	want := []string{"src/main.go"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_TestFiles(t *testing.T) {
	files := []string{
		"service_test.go",
		"UserTest.java",
		"UserTests.java",
		"app.spec.ts",
		"util.test.js",
		"Component.test.tsx",
		"helper.spec.jsx",
		"test_handler.py",
		"service_test.py",
		"UserTest.kt",
		"UserTests.kt",
		"src/main.go",
	}
	got := filterSourceFiles(files, nil)
	want := []string{"src/main.go"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_ExtraIgnorePaths(t *testing.T) {
	files := []string{
		"src/main.go",
		"generated/api.go",
		"internal/vendor/lib.go",
	}
	extra := []string{"generated/**", "internal/vendor/**"}
	got := filterSourceFiles(files, extra)
	want := []string{"src/main.go"}
	assertStringSliceEqual(t, want, got)
}

func TestFilterSourceFiles_Empty(t *testing.T) {
	files := []string{
		"README.md",
		".gitignore",
		"docs/guide.txt",
	}
	got := filterSourceFiles(files, nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

func TestFilterSourceFiles_AllPass(t *testing.T) {
	files := []string{
		"src/main.go",
		"internal/service.go",
		"cmd/app.go",
	}
	got := filterSourceFiles(files, nil)
	assertStringSliceEqual(t, files, got)
}

func TestMatchFilesToModules_RootModule(t *testing.T) {
	modules := []test.DiscoveredModule{
		{Path: "", Framework: test.FrameworkJUnit5},
	}
	files := []string{"src/main.go", "internal/service.go"}

	got := matchFilesToModules(files, modules)
	if len(got) != 1 {
		t.Fatalf("expected 1 group, got %d", len(got))
	}
	want := []string{"src/main.go", "internal/service.go"}
	assertStringSliceEqual(t, want, got[0].Files)
}

func TestMatchFilesToModules_SubModules(t *testing.T) {
	modules := []test.DiscoveredModule{
		{Path: "backend", Framework: test.FrameworkJUnit5},
		{Path: "frontend", Framework: test.FrameworkVitest},
	}
	files := []string{
		"backend/src/Main.java",
		"backend/src/Service.java",
		"frontend/src/App.vue",
	}

	got := matchFilesToModules(files, modules)
	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}

	assertStringSliceEqual(t, []string{"backend/src/Main.java", "backend/src/Service.java"}, got[0].Files)
	assertStringSliceEqual(t, []string{"frontend/src/App.vue"}, got[1].Files)
}

func TestMatchFilesToModules_DualFramework(t *testing.T) {
	modules := []test.DiscoveredModule{
		{Path: "mono", Framework: test.FrameworkJUnit5},
		{Path: "mono", Framework: test.FrameworkVitest},
	}
	files := []string{"mono/src/App.java", "mono/src/index.ts"}

	got := matchFilesToModules(files, modules)
	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}

	assertStringSliceEqual(t, []string{"mono/src/App.java", "mono/src/index.ts"}, got[0].Files)
	assertStringSliceEqual(t, []string{"mono/src/App.java", "mono/src/index.ts"}, got[1].Files)
}

func TestMatchFilesToModules_NoMatch(t *testing.T) {
	modules := []test.DiscoveredModule{
		{Path: "backend", Framework: test.FrameworkJUnit5},
	}
	files := []string{"frontend/src/App.vue", "other/util.go"}

	got := matchFilesToModules(files, modules)
	if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestMatchFilesToModules_Mixed(t *testing.T) {
	modules := []test.DiscoveredModule{
		{Path: "backend", Framework: test.FrameworkJUnit5},
		{Path: "frontend", Framework: test.FrameworkVitest},
	}
	files := []string{
		"backend/src/Main.java",
		"unrelated/util.go",
		"frontend/src/App.vue",
		"random.txt",
	}

	got := matchFilesToModules(files, modules)
	if len(got) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(got))
	}

	assertStringSliceEqual(t, []string{"backend/src/Main.java"}, got[0].Files)
	assertStringSliceEqual(t, []string{"frontend/src/App.vue"}, got[1].Files)
}

func assertStringSliceEqual(t *testing.T, want, got []string) {
	t.Helper()
	if len(want) != len(got) {
		t.Errorf("length mismatch: want %d, got %d\nwant: %v\ngot:  %v", len(want), len(got), want, got)
		return
	}
	for i := range want {
		if want[i] != got[i] {
			t.Errorf("index %d: want %q, got %q", i, want[i], got[i])
		}
	}
}
