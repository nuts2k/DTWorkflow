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
