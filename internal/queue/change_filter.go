package queue

import (
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/test"
)

var defaultIgnoredExtensions = map[string]bool{
	".md": true, ".txt": true, ".rst": true,
	".yaml": true, ".yml": true,
	".json": true,
	".toml": true, ".ini": true, ".cfg": true,
	".xml": true,
	".lock": true,
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true,
	".gif": true, ".ico": true,
	".csv": true,
	".license": true,
	".sh": true,
}

var defaultIgnoredFiles = map[string]bool{
	".gitignore": true, ".editorconfig": true,
	".dockerignore": true, ".env": true,
	"Makefile": true, "Dockerfile": true,
	"LICENSE": true,
}

var defaultIgnoredPrefixes = []string{
	"docs/", "doc/", ".github/", ".gitea/",
	"deploy/", ".vscode/", ".idea/",
}

var testFilePatterns = []string{
	"*_test.go", "*Test.java", "*Tests.java",
	"*.test.ts", "*.test.js", "*.spec.ts", "*.spec.js",
	"*.test.tsx", "*.test.jsx", "*.spec.tsx", "*.spec.jsx",
	"test_*.py", "*_test.py",
	"*Test.kt", "*Tests.kt",
}

// extractFilenames 从 ChangedFile 列表中提取文件名，排除 status="deleted" 的文件。
func extractFilenames(files []*gitea.ChangedFile) []string {
	var result []string
	for _, f := range files {
		if f == nil {
			continue
		}
		if strings.EqualFold(f.Status, "deleted") {
			continue
		}
		if f.Filename != "" {
			result = append(result, f.Filename)
		}
	}
	return result
}

// filterSourceFiles 过滤非源码文件，返回通过过滤的文件列表。
func filterSourceFiles(files []string, extraIgnorePaths []string) []string {
	var result []string
	for _, file := range files {
		if shouldIgnoreFile(file, extraIgnorePaths) {
			continue
		}
		result = append(result, file)
	}
	return result
}

func shouldIgnoreFile(file string, extraIgnorePaths []string) bool {
	base := path.Base(file)

	if defaultIgnoredFiles[base] {
		return true
	}

	ext := strings.ToLower(filepath.Ext(file))
	if defaultIgnoredExtensions[ext] {
		return true
	}

	for _, prefix := range defaultIgnoredPrefixes {
		if strings.HasPrefix(file, prefix) {
			return true
		}
	}

	for _, pattern := range testFilePatterns {
		if matched, _ := doublestar.Match(pattern, base); matched {
			return true
		}
	}

	for _, pattern := range extraIgnorePaths {
		if matched, _ := doublestar.Match(pattern, file); matched {
			return true
		}
	}

	return false
}

// moduleFileGroup 将模块与其匹配的变更文件绑定，避免使用 DiscoveredModule 作 map key。
type moduleFileGroup struct {
	Module test.DiscoveredModule
	Files  []string
}

// matchFilesToModules 将过滤后的源码文件按路径前缀归组到模块。
// 返回的切片保持与 modules 参数相同的相对顺序（仅含有匹配文件的模块）。
func matchFilesToModules(files []string, modules []test.DiscoveredModule) []moduleFileGroup {
	collected := make(map[int][]string)

	for _, file := range files {
		for i, mod := range modules {
			if mod.Path == "" {
				belongsToSub := false
				for _, other := range modules {
					if other.Path != "" && strings.HasPrefix(file, other.Path+"/") {
						belongsToSub = true
						break
					}
				}
				if !belongsToSub {
					collected[i] = append(collected[i], file)
				}
			} else if strings.HasPrefix(file, mod.Path+"/") {
				collected[i] = append(collected[i], file)
			}
		}
	}

	var result []moduleFileGroup
	for i, mod := range modules {
		if matched, ok := collected[i]; ok {
			result = append(result, moduleFileGroup{Module: mod, Files: matched})
		}
	}
	return result
}
