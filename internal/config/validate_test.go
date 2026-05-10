package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func validBaseConfig() *Config {
	return &Config{
		Server:  ServerConfig{Port: 8080},
		Gitea:   GiteaConfig{URL: "http://gitea:3000", Token: "test-token"},
		Claude:  ClaudeConfig{APIKey: "test-api-key"},
		Redis:   RedisConfig{Addr: "localhost:6379"},
		Webhook: WebhookConfig{Secret: "test-secret"},
		Worker:  WorkerConfig{Concurrency: 1, Timeout: 30 * time.Minute},
		Notify: NotifyConfig{
			DefaultChannel: "gitea",
			Channels: map[string]ChannelConfig{
				"gitea": {Enabled: true},
			},
		},
	}
}

func TestValidate_MissingWebhookSecret(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Webhook.Secret = ""

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}

	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("error type = %T, want *ValidationError", err)
	}
	if !strings.Contains(err.Error(), "webhook.secret") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "webhook.secret")
	}
}

func TestValidate_WorkerConcurrencyLessThanOne(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.Concurrency = 0

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "worker.concurrency") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "worker.concurrency")
	}
}

func TestValidate_NotifyDefaultChannelUndefinedOrDisabled(t *testing.T) {
	t.Run("channel not configured", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Notify.Channels = map[string]ChannelConfig{}

		err := Validate(cfg)
		if err == nil {
			t.Fatalf("Validate returned nil error")
		}
		if !strings.Contains(err.Error(), "notify.default_channel") {
			t.Fatalf("error message = %q, want contains %q", err.Error(), "notify.default_channel")
		}
	})

	t.Run("channel disabled", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Notify.Channels["gitea"] = ChannelConfig{Enabled: false}

		err := Validate(cfg)
		if err == nil {
			t.Fatalf("Validate returned nil error")
		}
		if !strings.Contains(err.Error(), "notify.default_channel") {
			t.Fatalf("error message = %q, want contains %q", err.Error(), "notify.default_channel")
		}
	})
}

func TestValidate_NotifyRoutesReferenceUnknownChannel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"slack"}}}

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "notify.routes[0]") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "notify.routes[0]")
	}
}

func TestValidate_RepoNotifyRoutesReferenceUnknownChannel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"slack"}}}},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "repos[0].notify.routes[0]") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "repos[0].notify.routes[0]")
	}
}

func TestValidate_NotifyDefaultChannelOnlySupportsGitea(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["fallback-channel"] = ChannelConfig{Enabled: true}
	cfg.Notify.DefaultChannel = "fallback-channel"

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "notify.default_channel") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "notify.default_channel")
	}
	if !strings.Contains(err.Error(), "仅支持") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "仅支持")
	}
}

func TestValidate_NotifyRoutesOnlySupportGitea(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["repo"] = ChannelConfig{Enabled: true}
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"repo"}}}

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "notify.routes[0]") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "notify.routes[0]")
	}
	if !strings.Contains(err.Error(), "仅支持") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "仅支持")
	}
}

func TestValidate_NilConfig(t *testing.T) {
	err := Validate(nil)
	if err == nil {
		t.Fatal("Validate(nil) 应返回错误")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	err := Validate(validBaseConfig())
	if err != nil {
		t.Fatalf("合法配置应通过校验，但返回: %v", err)
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 100000} {
		cfg := validBaseConfig()
		cfg.Server.Port = port
		err := Validate(cfg)
		if err == nil {
			t.Errorf("port=%d 应返回错误", port)
		}
	}
}

func TestValidate_MissingRedisAddr(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Redis.Addr = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空 redis.addr 应返回错误")
	}
	if !strings.Contains(err.Error(), "redis.addr") {
		t.Errorf("错误应包含 redis.addr，得到: %v", err)
	}
}

func TestValidate_MissingGiteaURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Gitea.URL = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空 gitea.url 应返回错误")
	}
	if !strings.Contains(err.Error(), "gitea.url") {
		t.Errorf("错误应包含 gitea.url，得到: %v", err)
	}
}

func TestValidate_InvalidGiteaURLFormat(t *testing.T) {
	for _, u := range []string{"ftp://gitea:3000", "not-a-url", "gitea:3000"} {
		cfg := validBaseConfig()
		cfg.Gitea.URL = u
		err := Validate(cfg)
		if err == nil {
			t.Errorf("gitea.url=%q 应返回错误", u)
		}
	}
}

func TestValidate_MissingGiteaToken(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Gitea.Token = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空 gitea.token 应返回错误")
	}
	if !strings.Contains(err.Error(), "gitea.token") {
		t.Errorf("错误应包含 gitea.token，得到: %v", err)
	}
}

// TestValidate_SplitGiteaTokens 验证显式拆分 review/fix/gen_tests token 时，仍要求基础 gitea.token 必填；
// 同时各职能 Token helper 应优先返回专属 token。
func TestValidate_SplitGiteaTokens(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Gitea.Token = "tok-base"
	cfg.Gitea.Tokens.Review = "tok-review"
	cfg.Gitea.Tokens.Fix = "tok-fix"
	cfg.Gitea.Tokens.GenTests = "tok-gen-tests"
	if err := Validate(cfg); err != nil {
		t.Fatalf("显式配置 review/fix/gen_tests token 且基础 token 存在时应通过校验，错误: %v", err)
	}
	if got := cfg.Gitea.ReviewToken(); got != "tok-review" {
		t.Errorf("ReviewToken()=%q, 期望 tok-review", got)
	}
	if got := cfg.Gitea.FixToken(); got != "tok-fix" {
		t.Errorf("FixToken()=%q, 期望 tok-fix", got)
	}
	if got := cfg.Gitea.GenTestsToken(); got != "tok-gen-tests" {
		t.Errorf("GenTestsToken()=%q, 期望 tok-gen-tests", got)
	}
}

// TestValidate_PartialSplitTokenFallback 验证仅拆一个 token 时，另一种用途回退到兜底 gitea.token。
func TestValidate_PartialSplitTokenFallback(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Gitea.Token = "tok-fallback"
	cfg.Gitea.Tokens.Review = "tok-review"
	// Tokens.Fix 留空
	if err := Validate(cfg); err != nil {
		t.Fatalf("部分拆分应通过校验，错误: %v", err)
	}
	if got := cfg.Gitea.FixToken(); got != "tok-fallback" {
		t.Errorf("FixToken() 未回退到兜底 token, 得到 %q", got)
	}
	if got := cfg.Gitea.GenTestsToken(); got != "tok-fallback" {
		t.Errorf("GenTestsToken() 未回退到兜底 token, 得到 %q", got)
	}
}

// TestValidate_SplitTokensWithoutBaseToken 验证即使显式配置 review/fix token，基础 gitea.token 仍不可省略。
func TestValidate_SplitTokensWithoutBaseToken(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Gitea.Token = ""
	cfg.Gitea.Tokens.Review = "tok-review"
	cfg.Gitea.Tokens.Fix = "tok-fix"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("缺少基础 gitea.token 时应返回错误")
	}
	if !strings.Contains(err.Error(), "gitea.token") {
		t.Errorf("错误应提示 gitea.token, 得到: %v", err)
	}
}

func TestValidate_MissingClaudeAPIKey(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Claude.APIKey = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空 claude.api_key 应返回错误")
	}
	if !strings.Contains(err.Error(), "claude.api_key") {
		t.Errorf("错误应包含 claude.api_key，得到: %v", err)
	}
}

func TestValidate_WorkerTimeoutZero(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.Timeout = 0
	err := Validate(cfg)
	if err == nil {
		t.Fatal("worker.timeout=0 应返回错误")
	}
	if !strings.Contains(err.Error(), "worker.timeout") {
		t.Errorf("错误应包含 worker.timeout，得到: %v", err)
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Log.Level = "verbose"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("无效 log.level 应返回错误")
	}
	if !strings.Contains(err.Error(), "log.level") {
		t.Errorf("错误应包含 log.level，得到: %v", err)
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Log.Format = "yaml"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("无效 log.format 应返回错误")
	}
	if !strings.Contains(err.Error(), "log.format") {
		t.Errorf("错误应包含 log.format，得到: %v", err)
	}
}

func TestValidate_EmptyNotifyDefaultChannel(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.DefaultChannel = ""
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空 notify.default_channel 应返回错误")
	}
	if !strings.Contains(err.Error(), "notify.default_channel") {
		t.Errorf("错误应包含 notify.default_channel，得到: %v", err)
	}
}

func TestValidate_RepoNameFormat(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{"", true},
		{"noslash", true},
		{"owner/repo", false},
	}
	for _, tc := range tests {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{Name: tc.name}}
		err := Validate(cfg)
		if tc.wantErr && err == nil {
			t.Errorf("repo name=%q 应返回错误", tc.name)
		}
		if !tc.wantErr && err != nil {
			// 该 repo 名合法，但可能因其他原因（如缺少 notify 覆盖的 channel）而失败
			// 这里只验证不会因 name 格式错误
			if strings.Contains(err.Error(), "repos[0].name") {
				t.Errorf("repo name=%q 不应有 name 格式错误: %v", tc.name, err)
			}
		}
	}
}

func TestValidate_RepoNilNotify_Skipped(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Repos = []RepoConfig{{Name: "owner/repo", Notify: nil}}
	err := Validate(cfg)
	if err != nil {
		t.Fatalf("repo 无 notify 覆盖不应报错，但返回: %v", err)
	}
}

func TestValidate_MultipleErrors(t *testing.T) {
	cfg := &Config{} // 几乎所有字段都不合法
	err := Validate(cfg)
	if err == nil {
		t.Fatal("空配置应返回错误")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("错误类型应为 *ValidationError，得到 %T", err)
	}
	// 至少应有 port、redis、gitea url、gitea token、claude、webhook、worker 等多个错误
	if len(ve.Errors) < 5 {
		t.Errorf("期望至少 5 个校验错误，得到 %d: %v", len(ve.Errors), err)
	}
}

func TestValidate_RepoNotifyRoutesOnlySupportGitea(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["repo"] = ChannelConfig{Enabled: true}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"repo"}}}},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatalf("Validate returned nil error")
	}
	if !strings.Contains(err.Error(), "repos[0].notify.routes[0]") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "repos[0].notify.routes[0]")
	}
	if !strings.Contains(err.Error(), "仅支持") {
		t.Fatalf("error message = %q, want contains %q", err.Error(), "仅支持")
	}
}

func TestValidate_ReviewDimensions(t *testing.T) {
	t.Run("有效维度通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Dimensions = []string{"security", "logic", "architecture", "style"}
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效维度应通过校验，但返回: %v", err)
		}
	})

	t.Run("无效维度报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Dimensions = []string{"security", "perf"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("包含无效维度 perf 应返回错误")
		}
		if !strings.Contains(err.Error(), "review.dimensions") {
			t.Errorf("错误应包含 review.dimensions，得到: %v", err)
		}
		if !strings.Contains(err.Error(), "perf") {
			t.Errorf("错误应包含无效维度名 perf，得到: %v", err)
		}
	})

	t.Run("空列表通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Dimensions = []string{}
		if err := Validate(cfg); err != nil {
			t.Fatalf("空维度列表应通过校验，但返回: %v", err)
		}
	})

	t.Run("仓库级无效维度报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "owner/repo",
			Review: &ReviewOverride{Dimensions: []string{"perf"}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级无效维度应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].review.dimensions") {
			t.Errorf("错误应包含 repos[0].review.dimensions，得到: %v", err)
		}
	})
}

func TestValidate_ReviewSeverity(t *testing.T) {
	t.Run("有效值通过 warning", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Severity = "warning"
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效 severity=warning 应通过，但返回: %v", err)
		}
	})

	t.Run("有效值通过 critical", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Severity = "critical"
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效 severity=critical 应通过，但返回: %v", err)
		}
	})

	t.Run("无效值报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Severity = "high"
		err := Validate(cfg)
		if err == nil {
			t.Fatal("无效 severity=high 应返回错误")
		}
		if !strings.Contains(err.Error(), "review.severity") {
			t.Errorf("错误应包含 review.severity，得到: %v", err)
		}
	})

	t.Run("空字符串通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Severity = ""
		if err := Validate(cfg); err != nil {
			t.Fatalf("空 severity 应通过校验，但返回: %v", err)
		}
	})

	t.Run("大小写不敏感", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Severity = "WARNING"
		if err := Validate(cfg); err != nil {
			t.Fatalf("severity=WARNING 大小写不敏感应通过，但返回: %v", err)
		}
	})

	t.Run("仓库级无效值报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "owner/repo",
			Review: &ReviewOverride{Severity: "high"},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级无效 severity 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].review.severity") {
			t.Errorf("错误应包含 repos[0].review.severity，得到: %v", err)
		}
	})
}

func TestValidate_ClaudeEffort(t *testing.T) {
	t.Run("有效值通过并允许大小写混用", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "HIGH"
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效 claude.effort 应通过校验，但返回: %v", err)
		}
	})

	t.Run("无效值报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Claude.Effort = "hgh"
		err := Validate(cfg)
		if err == nil {
			t.Fatal("无效 claude.effort 应返回错误")
		}
		if !strings.Contains(err.Error(), "claude.effort") {
			t.Errorf("错误应包含 claude.effort，得到: %v", err)
		}
	})
}

func TestValidate_ReviewEffort(t *testing.T) {
	t.Run("全局 review.effort 有效值通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Effort = "medium"
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效 review.effort 应通过校验，但返回: %v", err)
		}
	})

	t.Run("全局 review.effort 无效值报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.Effort = "ultra"
		err := Validate(cfg)
		if err == nil {
			t.Fatal("无效 review.effort 应返回错误")
		}
		if !strings.Contains(err.Error(), "review.effort") {
			t.Errorf("错误应包含 review.effort，得到: %v", err)
		}
	})

	t.Run("仓库级 review.effort 无效值报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "owner/repo",
			Review: &ReviewOverride{Effort: "hgh"},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级无效 review.effort 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].review.effort") {
			t.Errorf("错误应包含 repos[0].review.effort，得到: %v", err)
		}
	})
}

func TestValidate_WorkerTimeouts_Negative(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(cfg *Config)
		errKey string
	}{
		{
			name: "负数 review_pr",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.ReviewPR = -1 * time.Minute
			},
			errKey: "worker.timeouts.review_pr",
		},
		{
			name: "负数 fix_issue",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.FixIssue = -1 * time.Minute
			},
			errKey: "worker.timeouts.fix_issue",
		},
		{
			name: "负数 gen_tests",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.GenTests = -1 * time.Minute
			},
			errKey: "worker.timeouts.gen_tests",
		},
		{
			name: "负数 run_e2e",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.RunE2E = -1 * time.Minute
			},
			errKey: "worker.timeouts.run_e2e",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBaseConfig()
			tc.mutate(cfg)
			err := Validate(cfg)
			if err == nil {
				t.Fatalf("负数 %s 应校验失败", tc.errKey)
			}
			if !strings.Contains(err.Error(), tc.errKey) {
				t.Errorf("错误应包含 %s，得到: %v", tc.errKey, err)
			}
		})
	}
}

func TestValidate_WorkerTimeouts_ExceedsMax(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(cfg *Config)
		errKey string
	}{
		{
			name: "review_pr 超过 24h",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.ReviewPR = 25 * time.Hour
			},
			errKey: "worker.timeouts.review_pr",
		},
		{
			name: "fix_issue 超过 24h",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.FixIssue = 48 * time.Hour
			},
			errKey: "worker.timeouts.fix_issue",
		},
		{
			name: "gen_tests 超过 24h",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.GenTests = 999 * time.Hour
			},
			errKey: "worker.timeouts.gen_tests",
		},
		{
			name: "run_e2e 超过 24h",
			mutate: func(cfg *Config) {
				cfg.Worker.Timeouts.RunE2E = 25 * time.Hour
			},
			errKey: "worker.timeouts.run_e2e",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validBaseConfig()
			tc.mutate(cfg)
			err := Validate(cfg)
			if err == nil {
				t.Fatalf("超过 24h 的 %s 应校验失败", tc.errKey)
			}
			if !strings.Contains(err.Error(), tc.errKey) {
				t.Errorf("错误应包含 %s，得到: %v", tc.errKey, err)
			}
			if !strings.Contains(err.Error(), "不能超过") {
				t.Errorf("错误应包含 '不能超过'，得到: %v", err)
			}
		})
	}
}

func TestValidate_WorkerTimeouts_ZeroAllowed(t *testing.T) {
	// 零值表示使用默认值，不应报错
	cfg := validBaseConfig()
	cfg.Worker.Timeouts.ReviewPR = 0
	cfg.Worker.Timeouts.FixIssue = 0
	cfg.Worker.Timeouts.GenTests = 0
	cfg.Worker.Timeouts.RunE2E = 0
	err := Validate(cfg)
	if err != nil {
		t.Errorf("零值 timeouts 应通过校验，但返回: %v", err)
	}
}

func TestValidate_WorkerImageE2E_Invalid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.ImageE2E = "bad image"
	err := Validate(cfg)
	if err == nil {
		t.Fatal("worker.image_e2e 含空格应校验失败")
	}
	if !strings.Contains(err.Error(), "worker.image_e2e") {
		t.Errorf("错误应包含 worker.image_e2e，得到: %v", err)
	}
}

func TestValidate_E2EConfig(t *testing.T) {
	t.Run("enabled true 时 environments 不能为空", func(t *testing.T) {
		cfg := validBaseConfig()
		enabled := true
		cfg.E2E.Enabled = &enabled
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "e2e.environments") {
			t.Fatalf("应校验 e2e.environments 不能为空，实际: %v", err)
		}
	})

	t.Run("default_env 必须存在", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.E2E.DefaultEnv = "staging"
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "e2e.default_env") {
			t.Fatalf("应校验 e2e.default_env，实际: %v", err)
		}
	})

	t.Run("base_url 必须合法", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.E2E.Environments = map[string]E2EEnvironment{
			"staging": {BaseURL: "ftp://example.com"},
		}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "base_url") {
			t.Fatalf("应校验 e2e base_url，实际: %v", err)
		}
	})

	t.Run("db 字段必填", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.E2E.Environments = map[string]E2EEnvironment{
			"staging": {
				BaseURL: "https://staging.example.com",
				DB:      &E2EDBConfig{Host: "db.internal"},
			},
		}
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), "db.user") || !strings.Contains(err.Error(), "db.database") {
			t.Fatalf("应校验 e2e db 必填字段，实际: %v", err)
		}
	})

	t.Run("有效 E2E 配置通过", func(t *testing.T) {
		cfg := validBaseConfig()
		enabled := true
		cfg.E2E.Enabled = &enabled
		cfg.E2E.DefaultEnv = "staging"
		cfg.E2E.Environments = map[string]E2EEnvironment{
			"staging": {
				BaseURL: "https://staging.example.com",
				DB: &E2EDBConfig{
					Host: "db.internal", User: "tester", Database: "app_test",
				},
			},
		}
		if err := Validate(cfg); err != nil {
			t.Fatalf("有效 E2E 配置应通过校验: %v", err)
		}
	})
}

func TestValidate_StreamMonitor_InvalidActivityTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.StreamMonitor.Enabled = true
	cfg.Worker.StreamMonitor.ActivityTimeout = -1 * time.Second
	err := Validate(cfg)
	if err == nil {
		t.Fatal("启用 stream_monitor 时负数 activity_timeout 应校验失败")
	}
	if !strings.Contains(err.Error(), "stream_monitor.activity_timeout") {
		t.Errorf("错误应包含 stream_monitor.activity_timeout，得到: %v", err)
	}
}

func TestValidate_StreamMonitor_ZeroActivityTimeout(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.StreamMonitor.Enabled = true
	cfg.Worker.StreamMonitor.ActivityTimeout = 0
	err := Validate(cfg)
	if err == nil {
		t.Fatal("启用 stream_monitor 时零值 activity_timeout 应校验失败")
	}
	if !strings.Contains(err.Error(), "stream_monitor.activity_timeout") {
		t.Errorf("错误应包含 stream_monitor.activity_timeout，得到: %v", err)
	}
}

func TestValidate_StreamMonitor_DisabledNoValidation(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.StreamMonitor.Enabled = false
	cfg.Worker.StreamMonitor.ActivityTimeout = 0
	err := Validate(cfg)
	if err != nil {
		t.Errorf("stream_monitor 关闭时不应校验 activity_timeout: %v", err)
	}
}

func TestValidate_ReviewIgnorePatterns(t *testing.T) {
	t.Run("合法 pattern 通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.IgnorePatterns = []string{"**/*.md", "docs/**"}
		if err := Validate(cfg); err != nil {
			t.Fatalf("合法 ignore_patterns 应通过校验，但返回: %v", err)
		}
	})

	t.Run("非法 pattern 报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.IgnorePatterns = []string{"[invalid"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("非法 pattern [invalid 应返回错误")
		}
		if !strings.Contains(err.Error(), "review.ignore_patterns[0]") {
			t.Errorf("错误应包含 review.ignore_patterns[0]，得到: %v", err)
		}
	})

	t.Run("空列表通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Review.IgnorePatterns = []string{}
		if err := Validate(cfg); err != nil {
			t.Fatalf("空 ignore_patterns 应通过校验，但返回: %v", err)
		}
	})

	t.Run("仓库级非法 pattern 报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "owner/repo",
			Review: &ReviewOverride{IgnorePatterns: []string{"[invalid"}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级非法 pattern 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].review.ignore_patterns[0]") {
			t.Errorf("错误应包含 repos[0].review.ignore_patterns[0]，得到: %v", err)
		}
	})
}

func TestValidate_FeishuChannelIsValid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{
			"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
		},
	}
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea", "feishu"}}}

	err := Validate(cfg)
	if err != nil {
		t.Fatalf("feishu 应为合法渠道，但校验失败: %v", err)
	}
}

func TestValidate_FeishuDefaultChannelIsValid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{
			"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
		},
	}
	cfg.Notify.DefaultChannel = "feishu"

	err := Validate(cfg)
	if err != nil {
		t.Fatalf("feishu 应可作为 default_channel，但校验失败: %v", err)
	}
}

func TestValidate_FeishuMissingWebhookURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{},
	}
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea", "feishu"}}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("飞书渠道缺少 webhook_url 应校验失败")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Errorf("错误应包含 webhook_url，得到: %v", err)
	}
}

func TestValidate_FeishuChannelAllowsRepoOverrideWithoutGlobalWebhook(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{},
	}
	cfg.Repos = []RepoConfig{{
		Name: "acme/repo",
		Notify: &NotifyOverride{
			Routes: []RouteConfig{{Repo: "acme/repo", Channels: []string{"feishu"}}},
			Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
			},
		},
	}}

	err := Validate(cfg)
	if err != nil {
		t.Fatalf("仅仓库级飞书覆盖使用 feishu 时应允许缺少全局 webhook_url，但返回: %v", err)
	}
}

func TestValidate_RepoFeishuRouteRequiresWebhookSource(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{},
	}
	cfg.Repos = []RepoConfig{{
		Name: "acme/repo",
		Notify: &NotifyOverride{
			Routes: []RouteConfig{{Repo: "acme/repo", Channels: []string{"feishu"}}},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("仓库级 route 使用 feishu 且无全局/仓库 webhook 时应报错")
	}
	if !strings.Contains(err.Error(), "notify.channels.feishu.webhook_url") {
		t.Errorf("错误应包含 notify.channels.feishu.webhook_url，得到: %v", err)
	}
}

func TestValidate_FeishuInvalidWebhookURL(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{
			"webhook_url": "not-a-url",
		},
	}
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"gitea", "feishu"}}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("飞书渠道 webhook_url 格式无效应校验失败")
	}
	if !strings.Contains(err.Error(), "webhook_url") {
		t.Errorf("错误应包含 webhook_url，得到: %v", err)
	}
}

func TestValidate_FeishuDisabled_NoWebhookValidation(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: false,
		Options: map[string]string{},
	}

	err := Validate(cfg)
	if err != nil {
		t.Fatalf("飞书渠道未启用时不应校验 webhook_url，但返回: %v", err)
	}
}

func TestValidate_RepoNotifyRoutesFeishuIsValid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["feishu"] = ChannelConfig{
		Enabled: true,
		Options: map[string]string{
			"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
		},
	}
	cfg.Repos = []RepoConfig{{
		Name:   "acme/repo",
		Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"feishu"}}}},
	}}

	err := Validate(cfg)
	if err != nil {
		t.Fatalf("仓库级 feishu 路由应合法，但校验失败: %v", err)
	}
}

func TestValidate_UnknownChannelStillRejected(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Notify.Channels["slack"] = ChannelConfig{Enabled: true}
	cfg.Notify.Routes = []RouteConfig{{Repo: "*", Channels: []string{"slack"}}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("未知渠道 slack 应校验失败")
	}
	if !strings.Contains(err.Error(), "仅支持") {
		t.Errorf("错误应包含 '仅支持'，得到: %v", err)
	}
}

func TestValidate_DailyReport(t *testing.T) {
	t.Run("disabled skips all checks", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.DailyReport = DailyReportConfig{Enabled: false}
		if err := Validate(cfg); err != nil {
			t.Errorf("disabled daily_report should pass: %v", err)
		}
	})

	t.Run("enabled requires feishu_webhook", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.DailyReport = DailyReportConfig{
			Enabled:  true,
			Cron:     "0 9 * * *",
			Timezone: "Asia/Shanghai",
		}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("expected error for missing feishu_webhook")
		}
		if !strings.Contains(err.Error(), "feishu_webhook") {
			t.Errorf("error should mention feishu_webhook: %v", err)
		}
	})

	t.Run("invalid cron expression", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.DailyReport = DailyReportConfig{
			Enabled:       true,
			Cron:          "not-a-cron",
			Timezone:      "Asia/Shanghai",
			FeishuWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
		}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("expected error for invalid cron")
		}
		if !strings.Contains(err.Error(), "cron") {
			t.Errorf("error should mention cron: %v", err)
		}
	})

	t.Run("invalid timezone", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.DailyReport = DailyReportConfig{
			Enabled:       true,
			Cron:          "0 9 * * *",
			Timezone:      "Invalid/Zone",
			FeishuWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
		}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("expected error for invalid timezone")
		}
		if !strings.Contains(err.Error(), "timezone") {
			t.Errorf("error should mention timezone: %v", err)
		}
	})

	t.Run("valid full config", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.DailyReport = DailyReportConfig{
			Enabled:       true,
			Cron:          "0 9 * * *",
			Timezone:      "Asia/Shanghai",
			FeishuWebhook: "https://open.feishu.cn/open-apis/bot/v2/hook/xxx",
			FeishuSecret:  "sec123",
		}
		if err := Validate(cfg); err != nil {
			t.Errorf("valid config should pass: %v", err)
		}
	})
}

func TestValidate_APITokens(t *testing.T) {
	tests := []struct {
		name    string
		tokens  []TokenConfig
		wantErr bool
		errMsg  string
	}{
		{"空 tokens 列表合法", nil, false, ""},
		{"合法 token", []TokenConfig{{Token: "dtw_a1b2c3d4e5f67890", Identity: "admin"}}, false, ""},
		{"token 缺少 dtw_ 前缀", []TokenConfig{{Token: "a1b2c3d4e5f67890xx", Identity: "admin"}}, true, "dtw_"},
		{"token 太短", []TokenConfig{{Token: "dtw_short", Identity: "admin"}}, true, "16"},
		{"identity 为空", []TokenConfig{{Token: "dtw_a1b2c3d4e5f67890", Identity: ""}}, true, "identity"},
		{"identity 重复", []TokenConfig{
			{Token: "dtw_a1b2c3d4e5f67890", Identity: "admin"},
			{Token: "dtw_x9y8z7w6v5u43210", Identity: "admin"},
		}, true, "重复"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validBaseConfig()
			cfg.API.Tokens = tt.tokens
			err := Validate(cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望错误但未返回")
				}
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Fatalf("错误消息 %q 不包含 %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil && strings.Contains(err.Error(), "api.tokens") {
					t.Fatalf("不期望 API 相关错误: %v", err)
				}
			}
		})
	}
}

func TestValidate_WorkerImageFull(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.ImageFull = ""
	if err := Validate(cfg); err != nil {
		t.Errorf("ImageFull 为空应合法: %v", err)
	}
	cfg.Worker.ImageFull = "dtworkflow-worker-full:latest"
	if err := Validate(cfg); err != nil {
		t.Errorf("ImageFull 合法应通过: %v", err)
	}
	cfg.Worker.ImageFull = "bad image"
	if err := Validate(cfg); err == nil {
		t.Error("ImageFull 含空格应报错")
	}
}

func TestValidate_WorkerTimeoutsAnalyzeIssue(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Worker.Timeouts.AnalyzeIssue = 0
	if err := Validate(cfg); err != nil {
		t.Errorf("AnalyzeIssue 零值应合法: %v", err)
	}
	cfg.Worker.Timeouts.AnalyzeIssue = -1 * time.Minute
	if err := Validate(cfg); err == nil {
		t.Error("AnalyzeIssue 负值应报错")
	}
}

func TestValidate_TestGen_MaxRetryRounds(t *testing.T) {
	t.Run("0 视为默认值通过", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{MaxRetryRounds: 0}
		if err := Validate(cfg); err != nil {
			t.Errorf("max_retry_rounds=0（视为默认值）应通过: %v", err)
		}
	})

	t.Run("1-10 范围内合法", func(t *testing.T) {
		for _, v := range []int{1, 3, 5, 10} {
			cfg := validBaseConfig()
			cfg.TestGen = TestGenOverride{MaxRetryRounds: v}
			if err := Validate(cfg); err != nil {
				t.Errorf("max_retry_rounds=%d 应通过，但返回: %v", v, err)
			}
		}
	})

	t.Run("11 应被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{MaxRetryRounds: 11}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("max_retry_rounds=11 应返回错误")
		}
		if !strings.Contains(err.Error(), "test_gen.max_retry_rounds") {
			t.Errorf("错误应包含 test_gen.max_retry_rounds，得到: %v", err)
		}
	})

	t.Run("负数应被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{MaxRetryRounds: -1}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("max_retry_rounds=-1 应返回错误")
		}
		if !strings.Contains(err.Error(), "test_gen.max_retry_rounds") {
			t.Errorf("错误应包含 test_gen.max_retry_rounds，得到: %v", err)
		}
	})

	t.Run("仓库级 11 应被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{MaxRetryRounds: 11},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级 max_retry_rounds=11 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].test_gen.max_retry_rounds") {
			t.Errorf("错误应包含 repos[0].test_gen.max_retry_rounds，得到: %v", err)
		}
	})
}

func TestValidate_TestGen_TestFramework(t *testing.T) {
	t.Run("空串合法", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{TestFramework: ""}
		if err := Validate(cfg); err != nil {
			t.Errorf("test_framework 空串应合法: %v", err)
		}
	})

	t.Run("junit5 合法", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{TestFramework: "junit5"}
		if err := Validate(cfg); err != nil {
			t.Errorf("test_framework=junit5 应合法: %v", err)
		}
	})

	t.Run("vitest 合法", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{TestFramework: "vitest"}
		if err := Validate(cfg); err != nil {
			t.Errorf("test_framework=vitest 应合法: %v", err)
		}
	})

	t.Run("go 被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{TestFramework: "go"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("test_framework=go 应返回错误")
		}
		if !strings.Contains(err.Error(), "test_gen.test_framework") {
			t.Errorf("错误应包含 test_gen.test_framework，得到: %v", err)
		}
	})

	t.Run("仓库级非法值被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{TestFramework: "rspec"},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级非法 test_framework 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].test_gen.test_framework") {
			t.Errorf("错误应包含 repos[0].test_gen.test_framework，得到: %v", err)
		}
	})
}

func TestValidate_TestGen_ModuleScope(t *testing.T) {
	t.Run("空串合法", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: ""}
		if err := Validate(cfg); err != nil {
			t.Errorf("module_scope 空串应合法: %v", err)
		}
	})

	t.Run("相对路径合法", func(t *testing.T) {
		for _, v := range []string{"backend", "services/api", "src"} {
			cfg := validBaseConfig()
			cfg.TestGen = TestGenOverride{ModuleScope: v}
			if err := Validate(cfg); err != nil {
				t.Errorf("module_scope=%q 应合法: %v", v, err)
			}
		}
	})

	t.Run("以 / 开头被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: "/absolute"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("module_scope 以 / 开头应返回错误")
		}
		if !strings.Contains(err.Error(), "test_gen.module_scope") {
			t.Errorf("错误应包含 test_gen.module_scope，得到: %v", err)
		}
		if !strings.Contains(err.Error(), "/") {
			t.Errorf("错误应说明 / 开头的问题，得到: %v", err)
		}
	})

	t.Run("含 .. 被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: "../escape"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("module_scope 含 .. 应返回错误")
		}
		if !strings.Contains(err.Error(), "test_gen.module_scope") {
			t.Errorf("错误应包含 test_gen.module_scope，得到: %v", err)
		}
		if !strings.Contains(err.Error(), "..") {
			t.Errorf("错误应说明 .. 的问题，得到: %v", err)
		}
	})

	t.Run("中间包含 .. 被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.TestGen = TestGenOverride{ModuleScope: "backend/../escape"}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("module_scope 中间含 .. 应返回错误")
		}
	})

	t.Run("仓库级以 / 开头被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{ModuleScope: "/abs"},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级 module_scope 以 / 开头应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].test_gen.module_scope") {
			t.Errorf("错误应包含 repos[0].test_gen.module_scope，得到: %v", err)
		}
	})

	t.Run("仓库级含 .. 被拒", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:    "acme/repo",
			TestGen: &TestGenOverride{ModuleScope: "../escape"},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仓库级 module_scope 含 .. 应返回错误")
		}
		if !strings.Contains(err.Error(), "repos[0].test_gen.module_scope") {
			t.Errorf("错误应包含 repos[0].test_gen.module_scope，得到: %v", err)
		}
	})
}

func TestValidate_TestGen_RepoNilNotValidated(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Repos = []RepoConfig{{Name: "acme/repo", TestGen: nil}}
	if err := Validate(cfg); err != nil {
		t.Errorf("repo.TestGen 为 nil 时不应触发 test_gen 校验: %v", err)
	}
}

func TestValidate_TestGen_ValidFullOverride(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TestGen = TestGenOverride{
		Enabled:        boolPtr(true),
		ModuleScope:    "backend",
		MaxRetryRounds: 5,
		TestFramework:  "junit5",
	}
	cfg.Repos = []RepoConfig{{
		Name: "acme/repo",
		TestGen: &TestGenOverride{
			Enabled:        boolPtr(false),
			ModuleScope:    "services/api",
			MaxRetryRounds: 8,
			TestFramework:  "vitest",
		},
	}}
	if err := Validate(cfg); err != nil {
		t.Errorf("完整合法 test_gen 配置应通过，但返回: %v", err)
	}
}

func TestValidate_TestGen_RepoMergedChangeDrivenRequiresEnabled(t *testing.T) {
	cfg := validBaseConfig()
	cfg.TestGen = TestGenOverride{Enabled: boolPtr(false)}
	cfg.Repos = []RepoConfig{{
		Name: "acme/repo",
		TestGen: &TestGenOverride{
			ChangeDriven: &ChangeDrivenConfig{Enabled: boolPtr(true)},
		},
	}}

	err := Validate(cfg)
	if err == nil {
		t.Fatal("合并后 change_driven=true 但 test_gen.enabled=false 应产生校验错误")
	}
	if !strings.Contains(err.Error(), "合并后 change_driven.enabled=true") {
		t.Fatalf("error = %v, want merged change_driven conflict", err)
	}
}

func TestValidate_RepoFeishuOverride(t *testing.T) {
	// 辅助函数：构造启用了全局飞书渠道的基础配置
	feishuBaseConfig := func() *Config {
		cfg := validBaseConfig()
		cfg.Notify.Channels["feishu"] = ChannelConfig{
			Enabled: true,
			Options: map[string]string{
				"webhook_url": "https://open.feishu.cn/open-apis/bot/v2/hook/global",
			},
		}
		return cfg
	}

	t.Run("合法仓库级飞书覆盖通过", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				Secret:     "repo-secret",
			}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("合法仓库级飞书覆盖应通过: %v", err)
		}
	})

	t.Run("webhook_url 为空报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: ""}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("空 webhook_url 应报错")
		}
		if !strings.Contains(err.Error(), "webhook_url") {
			t.Errorf("错误应包含 webhook_url: %v", err)
		}
	})

	t.Run("webhook_url 仅空白报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "   "}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("仅空白 webhook_url 应报错")
		}
	})

	t.Run("webhook_url 格式无效报错", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{WebhookURL: "not-a-url"}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("无效 webhook_url 格式应报错")
		}
		if !strings.Contains(err.Error(), "格式无效") {
			t.Errorf("错误应包含 '格式无效': %v", err)
		}
	})

	t.Run("全局飞书渠道未启用时报错", func(t *testing.T) {
		cfg := validBaseConfig() // 全局无飞书渠道
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
			}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("全局飞书未启用时仓库级覆盖应报错")
		}
		if !strings.Contains(err.Error(), "全局飞书渠道未启用") {
			t.Errorf("错误应包含 '全局飞书渠道未启用': %v", err)
		}
	})

	t.Run("全局飞书 disabled 时报错", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Notify.Channels["feishu"] = ChannelConfig{Enabled: false}
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
			}},
		}}
		err := Validate(cfg)
		if err == nil {
			t.Fatal("全局飞书 disabled 时仓库级覆盖应报错")
		}
	})

	t.Run("无 secret 合法（不强制）", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{Feishu: &FeishuOverride{
				WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				Secret:     "",
			}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("无 secret 应合法: %v", err)
		}
	})

	t.Run("仅仓库级覆盖使用 feishu 时允许全局 webhook_url 为空", func(t *testing.T) {
		cfg := validBaseConfig()
		cfg.Notify.Channels["feishu"] = ChannelConfig{
			Enabled: true,
			Options: map[string]string{},
		}
		cfg.Repos = []RepoConfig{{
			Name: "acme/repo",
			Notify: &NotifyOverride{
				Routes: []RouteConfig{{Repo: "acme/repo", Channels: []string{"feishu"}}},
				Feishu: &FeishuOverride{
					WebhookURL: "https://open.feishu.cn/open-apis/bot/v2/hook/repo",
				},
			},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("仅仓库级覆盖使用 feishu 时应允许缺少全局 webhook: %v", err)
		}
	})

	t.Run("Feishu 为 nil 不校验", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{
			Name:   "acme/repo",
			Notify: &NotifyOverride{Routes: []RouteConfig{{Repo: "*", Channels: []string{"gitea"}}}},
		}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("Feishu nil 不应校验: %v", err)
		}
	})

	t.Run("Notify 为 nil 不校验", func(t *testing.T) {
		cfg := feishuBaseConfig()
		cfg.Repos = []RepoConfig{{Name: "acme/repo", Notify: nil}}
		if err := Validate(cfg); err != nil {
			t.Fatalf("Notify nil 不应校验: %v", err)
		}
	})
}
