package test

import (
	"context"
	"log/slog"
)

// DiscoveredModule 扫描发现的可测试模块。
type DiscoveredModule struct {
	Path      string    // 子目录相对路径（如 "backend"、"frontend"），根级为 ""
	Framework Framework // 检测到的框架
}

const maxScanDirs = 30

// ScanRepoModules 扫描仓库根和 depth=1 子目录，发现可测试模块。
func ScanRepoModules(ctx context.Context, checker RepoFileChecker,
	owner, repo, ref string) ([]DiscoveredModule, error) {

	rootHasPom, errPom := checker.HasFile(ctx, owner, repo, ref, "", "pom.xml")
	if errPom != nil {
		return nil, errPom
	}
	rootHasPkg, errPkg := checker.HasFile(ctx, owner, repo, ref, "", "package.json")
	if errPkg != nil {
		return nil, errPkg
	}

	var result []DiscoveredModule
	if rootHasPom {
		result = append(result, DiscoveredModule{Path: "", Framework: FrameworkJUnit5})
	}
	if rootHasPkg {
		result = append(result, DiscoveredModule{Path: "", Framework: FrameworkVitest})
	}

	// 根级命中即返回，不做 depth=1（设计文档 1.3 节）
	if len(result) > 0 {
		return result, nil
	}

	subdirs, err := checker.ListDir(ctx, owner, repo, ref, "")
	if err != nil {
		return nil, err
	}

	if len(subdirs) > maxScanDirs {
		slog.Warn("ScanRepoModules: 子目录数量超过上限，截断处理",
			"total", len(subdirs), "max", maxScanDirs, "owner", owner, "repo", repo)
		subdirs = subdirs[:maxScanDirs]
	}

	for _, dir := range subdirs {
		hasPom, err := checker.HasFile(ctx, owner, repo, ref, dir, "pom.xml")
		if err != nil {
			slog.Warn("ScanRepoModules: 检查子目录框架标记文件失败，跳过",
				"dir", dir, "file", "pom.xml", "error", err)
			continue
		}
		hasPkg, err := checker.HasFile(ctx, owner, repo, ref, dir, "package.json")
		if err != nil {
			slog.Warn("ScanRepoModules: 检查子目录框架标记文件失败，跳过",
				"dir", dir, "file", "package.json", "error", err)
			continue
		}
		if hasPom {
			result = append(result, DiscoveredModule{Path: dir, Framework: FrameworkJUnit5})
		}
		if hasPkg {
			result = append(result, DiscoveredModule{Path: dir, Framework: FrameworkVitest})
		}
	}

	if len(result) == 0 {
		return nil, ErrNoFrameworkDetected
	}
	return result, nil
}
