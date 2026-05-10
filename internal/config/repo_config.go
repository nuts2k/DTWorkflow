package config

// RepoConfig 仓库级配置覆盖。
type RepoConfig struct {
	Name    string           `mapstructure:"name"`
	Review  *ReviewOverride  `mapstructure:"review"`
	Notify  *NotifyOverride  `mapstructure:"notify"`
	TestGen *TestGenOverride `mapstructure:"test_gen"` // M4.1
	E2E     *E2EOverride     `mapstructure:"e2e"`      // M5.1
}

// TestGenOverride 测试生成配置（全局或仓库级覆盖）。
//
// Enabled 使用指针语义，与 ReviewOverride.Enabled 一致：
// nil = 未覆盖（回退到全局，默认启用）；*false = 显式关闭；*true = 显式启用。
// 普通 bool 类型会让 RepoConfig 的零值 false 错误覆盖全局 true。
type TestGenOverride struct {
	Enabled *bool `mapstructure:"enabled"`
	// ModuleScope：仓库级"允许做 gen_tests 的模块前缀白名单"（护栏）。
	// 语义：CLI `--module` 或 API `module` 参数必须以此字符串为前缀，
	// 否则校验拒绝。留空 = 无白名单限制。
	// 不作默认模块：CLI 未传 `--module` 时仍按"整仓生成"处理（module=""），
	// 而非自动套用 ModuleScope 值。
	ModuleScope string `mapstructure:"module_scope"`
	// MaxRetryRounds 容器内 Claude 自主修正测试失败的最大轮次，默认 3，合法范围 [1, 10]。
	MaxRetryRounds int `mapstructure:"max_retry_rounds"`
	// TestFramework 显式指定测试框架：空串 / "junit5" / "vitest"。
	TestFramework string `mapstructure:"test_framework"`
	// ReviewOnFailure M4.2 新增：失败时是否也自动触发 review（D5）。
	//
	// 指针语义，与 Enabled 一致：
	//   - nil    = 未覆盖（回退到全局；全局未设时读取方按 false 处理）
	//   - *false = 显式关闭（仅 Success=true 时入队 review）
	//   - *true  = 显式启用（Success=false 且 PRNumber>0 时也入队 review）
	ReviewOnFailure *bool `mapstructure:"review_on_failure"`
	// ChangeDriven M4.3 新增：变更驱动测试生成配置。
	ChangeDriven *ChangeDrivenConfig `mapstructure:"change_driven"`
}

// NotifyOverride 仓库级通知配置覆盖。
type NotifyOverride struct {
	Routes []RouteConfig  `mapstructure:"routes"`
	Feishu *FeishuOverride `mapstructure:"feishu"`
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

	// --- M2.2 新增 ---
	TechStack          []string `mapstructure:"tech_stack"`           // 技术栈显式指定：["java", "vue"]
	CodeStandardsPaths []string `mapstructure:"code_standards_paths"` // 自定义规范文件路径

	// --- 模型配置 ---
	Model  string `mapstructure:"model"`  // 覆盖全局 claude.model
	Effort string `mapstructure:"effort"` // 覆盖全局 claude.effort
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

// ResolveFeishuOverride 返回指定仓库的飞书配置覆盖。
// 返回 nil 表示使用全局配置。
func (c *Config) ResolveFeishuOverride(repoFullName string) *FeishuOverride {
	if c == nil {
		return nil
	}
	for _, repo := range c.Repos {
		if repo.Name == repoFullName && repo.Notify != nil && repo.Notify.Feishu != nil {
			return repo.Notify.Feishu
		}
	}
	return nil
}

// ResolveReviewConfig 解析指定仓库的最终评审配置（全局 + 仓库覆盖合并）。
//
// 说明：本轮仅做结构预留与最小合并逻辑，后续再扩展字段语义与校验。
func (c *Config) ResolveReviewConfig(repoFullName string) ReviewOverride {
	if c == nil {
		return ReviewOverride{}
	}

	// 基础：使用全局 Review 作为默认值，model/effort 从全局 Claude 配置填充。
	merged := c.Review
	if merged.Model == "" {
		merged.Model = c.Claude.Model
	}
	if merged.Effort == "" {
		merged.Effort = c.Claude.Effort
	}

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
		if repo.Review.TechStack != nil {
			merged.TechStack = repo.Review.TechStack
		}
		if repo.Review.CodeStandardsPaths != nil {
			merged.CodeStandardsPaths = repo.Review.CodeStandardsPaths
		}
		if repo.Review.Model != "" {
			merged.Model = repo.Review.Model
		}
		if repo.Review.Effort != "" {
			merged.Effort = repo.Review.Effort
		}
		break
	}

	return merged
}

// ResolveTestGenConfig 解析指定仓库的最终 test_gen 配置。
//
// 全局 Config.TestGen 作为基础，若 repos[*].test_gen 非 nil，
// 用其非零字段逐项覆盖。返回值可安全修改（按值返回）。
//
// 合并规则：
//   - Enabled: repo.Enabled 非 nil 时覆盖（nil 表示未覆盖，保留全局值）
//   - ModuleScope: repo 非空字符串时覆盖
//   - MaxRetryRounds: repo > 0 时覆盖（0 视为未设置，保留全局值）
//   - TestFramework: repo 非空字符串时覆盖
//   - ReviewOnFailure: repo 非 nil 时覆盖（nil 表示未覆盖，保留全局值）
func (c *Config) ResolveTestGenConfig(repoFullName string) TestGenOverride {
	if c == nil {
		return TestGenOverride{}
	}
	merged := c.TestGen
	for _, repo := range c.Repos {
		if repo.Name != repoFullName || repo.TestGen == nil {
			continue
		}
		if repo.TestGen.Enabled != nil {
			merged.Enabled = repo.TestGen.Enabled
		}
		if repo.TestGen.ModuleScope != "" {
			merged.ModuleScope = repo.TestGen.ModuleScope
		}
		if repo.TestGen.MaxRetryRounds > 0 {
			merged.MaxRetryRounds = repo.TestGen.MaxRetryRounds
		}
		if repo.TestGen.TestFramework != "" {
			merged.TestFramework = repo.TestGen.TestFramework
		}
		if repo.TestGen.ReviewOnFailure != nil {
			merged.ReviewOnFailure = repo.TestGen.ReviewOnFailure
		}
		if repo.TestGen.ChangeDriven != nil {
			merged.ChangeDriven = repo.TestGen.ChangeDriven
		}
		break
	}
	return merged
}

// E2EOverride 仓库级 E2E 配置覆盖。
type E2EOverride struct {
	Enabled    *bool  `mapstructure:"enabled"`
	DefaultEnv string `mapstructure:"default_env"`
}

// ResolveE2EConfig 解析指定仓库的最终 E2E 配置覆盖。
func (c *Config) ResolveE2EConfig(repoFullName string) E2EOverride {
	if c == nil {
		return E2EOverride{}
	}
	merged := E2EOverride{
		Enabled:    c.E2E.Enabled,
		DefaultEnv: c.E2E.DefaultEnv,
	}
	for _, repo := range c.Repos {
		if repo.Name != repoFullName || repo.E2E == nil {
			continue
		}
		if repo.E2E.Enabled != nil {
			merged.Enabled = repo.E2E.Enabled
		}
		if repo.E2E.DefaultEnv != "" {
			merged.DefaultEnv = repo.E2E.DefaultEnv
		}
		break
	}
	return merged
}

// ChangeDrivenConfig 变更驱动测试生成配置。
// Enabled 使用指针语义，nil=false（默认关闭），与 TestGenOverride.Enabled 的 nil=true 语义相反。
type ChangeDrivenConfig struct {
	Enabled     *bool    `mapstructure:"enabled"`
	IgnorePaths []string `mapstructure:"ignore_paths"`
}

// IsEnabled 返回变更驱动是否启用。nil 视为 false（默认关闭）。
func (c ChangeDrivenConfig) IsEnabled() bool {
	return c.Enabled != nil && *c.Enabled
}
