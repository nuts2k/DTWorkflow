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
