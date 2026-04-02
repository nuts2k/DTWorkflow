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
	err := Validate(cfg)
	if err != nil {
		t.Errorf("零值 timeouts 应通过校验，但返回: %v", err)
	}
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
