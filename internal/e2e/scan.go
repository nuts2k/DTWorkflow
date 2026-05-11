package e2e

import (
	"context"
	"log/slog"

	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
)

// E2EModuleScanner 扫描仓库 E2E 模块的窄接口。
// giteaRepoFileChecker（internal/cmd/adapter.go）天然满足此接口。
type E2EModuleScanner interface {
	ListDir(ctx context.Context, owner, repo, ref, dir string) ([]string, error)
}

const maxE2EScanDirs = 30

// ScanE2EModules 扫描仓库 e2e/ 目录，发现包含 cases/ 子目录的模块。
// 返回模块名列表（如 ["order", "auth"]）。
func ScanE2EModules(ctx context.Context, scanner E2EModuleScanner,
	owner, repo, ref string) ([]string, error) {

	topDirs, err := scanner.ListDir(ctx, owner, repo, ref, "e2e")
	if err != nil {
		if gitea.IsNotFound(err) {
			return nil, ErrNoE2EModulesFound
		}
		return nil, err
	}

	if len(topDirs) == 0 {
		return nil, ErrNoE2EModulesFound
	}

	if len(topDirs) > maxE2EScanDirs {
		slog.Warn("ScanE2EModules: 子目录数量超过上限，截断处理",
			"total", len(topDirs), "max", maxE2EScanDirs, "owner", owner, "repo", repo)
		topDirs = topDirs[:maxE2EScanDirs]
	}

	var modules []string
	for _, dir := range topDirs {
		subEntries, subErr := scanner.ListDir(ctx, owner, repo, ref, "e2e/"+dir)
		if subErr != nil {
			slog.Warn("ScanE2EModules: 子目录扫描失败，跳过",
				"dir", dir, "error", subErr)
			continue
		}
		for _, entry := range subEntries {
			if entry == "cases" {
				modules = append(modules, dir)
				break
			}
		}
	}

	if len(modules) == 0 {
		return nil, ErrNoE2EModulesFound
	}
	return modules, nil
}
