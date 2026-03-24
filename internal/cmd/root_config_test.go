package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
)

func writeTestConfigFile(t *testing.T, content string) string {
	t.Helper()
	if content == "" {
		content = "webhook:\n  secret: \"test-secret\"\n" +
			"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n"
	}
	p := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}
	return p
}

func resetRootFlagsForTest(t *testing.T) {
	t.Helper()
	// Cobra/pflag 的值会在同一进程内复用，这里显式恢复默认值，避免测试互相影响。
	_ = rootCmd.PersistentFlags().Set("config", "")
	_ = rootCmd.PersistentFlags().Set("json", "false")
	_ = rootCmd.PersistentFlags().Set("verbose", "false")

	// serve 子命令的 flags（本文件用到 port）。
	if serveCmd != nil {
		_ = serveCmd.Flags().Set("port", "8080")
		_ = serveCmd.Flags().Set("host", "0.0.0.0")
	}

	configFile = ""
	jsonOutput = false
	verboseOutput = false
	cfgManager = nil
}

func TestRootPersistentPreRun_LoadConfigAndInitCfgManager(t *testing.T) {
	resetRootFlagsForTest(t)

	cfgPath := writeTestConfigFile(t, "server:\n  port: 9090\n"+
		"webhook:\n  secret: \"test-secret\"\n"+
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n")

	noopCmd := &cobra.Command{
		Use:   "noop",
		Short: "test helper",
		RunE: func(cmd *cobra.Command, args []string) error {
			if cfgManager == nil {
				t.Fatalf("cfgManager 未初始化")
			}
			cfg := cfgManager.Get()
			if cfg == nil {
				t.Fatalf("cfgManager.Get() 返回 nil")
			}
			if cfg.Server.Port != 9090 {
				t.Fatalf("server.port = %d, want %d", cfg.Server.Port, 9090)
			}
			return nil
		},
	}
	rootCmd.AddCommand(noopCmd)
	defer rootCmd.RemoveCommand(noopCmd)

	rootCmd.SetArgs([]string{"--config", cfgPath, "noop"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 noop 命令失败: %v", err)
	}
}

func TestRootPersistentPreRun_ServePortFlagOverridesConfigFile(t *testing.T) {
	resetRootFlagsForTest(t)

	cfgPath := writeTestConfigFile(t, "server:\n  port: 9090\n"+
		"webhook:\n  secret: \"test-secret\"\n"+
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n")

	oldRunE := serveCmd.RunE
	serveCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cfgManager == nil {
			t.Fatalf("cfgManager 未初始化")
		}
		cfg := cfgManager.Get()
		if cfg == nil {
			t.Fatalf("cfgManager.Get() 返回 nil")
		}
		if cfg.Server.Port != 9999 {
			t.Fatalf("server.port = %d, want %d（应被 --port 覆盖）", cfg.Server.Port, 9999)
		}
		return nil
	}
	defer func() { serveCmd.RunE = oldRunE }()

	rootCmd.SetArgs([]string{"--config", cfgPath, "serve", "--port", "9999"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 serve 命令失败: %v", err)
	}
}

func TestRootPersistentPreRun_ServeFlagsOverrideConfigFile(t *testing.T) {
	resetRootFlagsForTest(t)

	// 配置文件给出一组值，命令行显式传入另一组值，预期 cfgManager 中应体现 flag 覆盖。
	cfgPath := writeTestConfigFile(t, "server:\n  host: \"0.0.0.0\"\n  port: 8080\n"+
		"redis:\n  addr: \"file:6379\"\n"+
		"database:\n  path: \"file.db\"\n"+
		"worker:\n  concurrency: 3\n  image: \"file-image:1\"\n"+
		"webhook:\n  secret: \"file-secret\"\n"+
		"claude:\n  api_key: \"file-ck\"\n"+
		"gitea:\n  url: \"https://file.example.com\"\n  token: \"file-gt\"\n"+
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n")

	oldRunE := serveCmd.RunE
	serveCmd.RunE = func(cmd *cobra.Command, args []string) error {
		if cfgManager == nil {
			t.Fatalf("cfgManager 未初始化")
		}
		cfg := cfgManager.Get()
		if cfg == nil {
			t.Fatalf("cfgManager.Get() 返回 nil")
		}

		if cfg.Server.Host != "127.0.0.1" {
			t.Fatalf("server.host = %q, want %q", cfg.Server.Host, "127.0.0.1")
		}
		if cfg.Server.Port != 18080 {
			t.Fatalf("server.port = %d, want %d", cfg.Server.Port, 18080)
		}
		if cfg.Webhook.Secret != "flag-secret" {
			t.Fatalf("webhook.secret = %q, want %q", cfg.Webhook.Secret, "flag-secret")
		}
		if cfg.Redis.Addr != "flag:6379" {
			t.Fatalf("redis.addr = %q, want %q", cfg.Redis.Addr, "flag:6379")
		}
		if cfg.Database.Path != "flag.db" {
			t.Fatalf("database.path = %q, want %q", cfg.Database.Path, "flag.db")
		}
		if cfg.Worker.Concurrency != 9 {
			t.Fatalf("worker.concurrency = %d, want %d", cfg.Worker.Concurrency, 9)
		}
		if cfg.Worker.Image != "flag-image:9" {
			t.Fatalf("worker.image = %q, want %q", cfg.Worker.Image, "flag-image:9")
		}
		if cfg.Claude.APIKey != "flag-ck" {
			t.Fatalf("claude.api_key = %q, want %q", cfg.Claude.APIKey, "flag-ck")
		}
		if cfg.Gitea.URL != "https://flag.example.com" {
			t.Fatalf("gitea.url = %q, want %q", cfg.Gitea.URL, "https://flag.example.com")
		}
		if cfg.Gitea.Token != "flag-gt" {
			t.Fatalf("gitea.token = %q, want %q", cfg.Gitea.Token, "flag-gt")
		}
		return nil
	}
	defer func() { serveCmd.RunE = oldRunE }()

	rootCmd.SetArgs([]string{
		"--config", cfgPath,
		"serve",
		"--host", "127.0.0.1",
		"--port", "18080",
		"--webhook-secret", "flag-secret",
		"--redis-addr", "flag:6379",
		"--db-path", "flag.db",
		"--max-workers", "9",
		"--worker-image", "flag-image:9",
		"--claude-api-key", "flag-ck",
		"--gitea-url", "https://flag.example.com",
		"--gitea-token", "flag-gt",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 serve 命令失败: %v", err)
	}
}

func TestRootPersistentPreRun_ConfigOptional_VersionSkipsConfigLoad(t *testing.T) {
	resetRootFlagsForTest(t)

	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 version 命令失败: %v", err)
	}
	if cfgManager != nil {
		t.Fatalf("version 命令应跳过配置加载，但 cfgManager 不为 nil")
	}
}

func TestRootPersistentPreRun_ConfigOptional_HelpSkipsConfigLoad(t *testing.T) {
	resetRootFlagsForTest(t)

	rootCmd.SetArgs([]string{"help"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 help 命令失败: %v", err)
	}
	if cfgManager != nil {
		t.Fatalf("help 命令应跳过配置加载，但 cfgManager 不为 nil")
	}
}
