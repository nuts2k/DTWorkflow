package config

// RepoConfig 仓库级配置覆盖。
type RepoConfig struct {
	Name   string          `mapstructure:"name"`
	Review *ReviewOverride `mapstructure:"review"`
	Notify *NotifyOverride `mapstructure:"notify"`
}

// NotifyOverride 仓库级通知配置覆盖。
type NotifyOverride struct {
	Routes []RouteConfig `mapstructure:"routes"`
}

// ReviewOverride 评审配置覆盖（结构预留；本轮仅实现最小合并规则）。
//
// 约定：使用指针字段表达"是否覆盖"。
type ReviewOverride struct {
	Enabled        *bool    `mapstructure:"enabled"`
	IgnorePatterns []string `mapstructure:"ignore_patterns"`
	Severity       string   `mapstructure:"severity"`

	// --- M2.1 新增字段 ---
	Instructions     string   `mapstructure:"instructions"`       // 评审指令文本（全局）
	RepoInstructions string   `mapstructure:"-"`                  // 仓库级追加指令（由 ResolveReviewConfig 填充，不直接来自 YAML）
	Dimensions       []string `mapstructure:"dimensions"`         // 启用的评审维度
	LargePRThreshold int      `mapstructure:"large_pr_threshold"` // 大 PR 警告阈值（变更行数）
}

// ResolveNotifyRoutes 解析指定仓库的最终通知路由规则。
//
// 优先使用仓库级覆盖，无覆盖时回退到全局配置。
// 覆盖策略：仓库级 NotifyOverride.Routes 非空时，完全替换全局路由（不合并）。
func (c *Config) ResolveNotifyRoutes(repoFullName string) []RouteConfig {
	if c == nil {
		return nil
	}
	for _, repo := range c.Repos {
		if repo.Name != repoFullName || repo.Notify == nil {
			continue
		}

		// 语义：nil 表示"未覆盖"；空切片表示"显式清空"。
		if repo.Notify.Routes != nil {
			return repo.Notify.Routes
		}
		break
	}
	return c.Notify.Routes
}

// ResolveReviewConfig 解析指定仓库的最终评审配置（全局 + 仓库覆盖合并）。
//
// 说明：本轮仅做结构预留与最小合并逻辑，后续再扩展字段语义与校验。
func (c *Config) ResolveReviewConfig(repoFullName string) ReviewOverride {
	if c == nil {
		return ReviewOverride{}
	}

	// 基础：使用全局 Review 作为默认值。
	merged := c.Review

	for _, repo := range c.Repos {
		if repo.Name != repoFullName || repo.Review == nil {
			continue
		}

		if repo.Review.Enabled != nil {
			merged.Enabled = repo.Review.Enabled
		}
		if repo.Review.Severity != "" {
			merged.Severity = repo.Review.Severity
		}
		// 语义：nil 表示"未覆盖"；空切片表示"显式清空"。
		if repo.Review.IgnorePatterns != nil {
			merged.IgnorePatterns = repo.Review.IgnorePatterns
		}
		// M2.1 新增字段合并
		// 仓库级指令采用追加模式：全局 Instructions 保持不变，仓库级存入 RepoInstructions
		if repo.Review.Instructions != "" {
			merged.RepoInstructions = repo.Review.Instructions
		}
		if repo.Review.Dimensions != nil {
			merged.Dimensions = repo.Review.Dimensions
		}
		if repo.Review.LargePRThreshold > 0 {
			merged.LargePRThreshold = repo.Review.LargePRThreshold
		}
		break
	}

	return merged
}
