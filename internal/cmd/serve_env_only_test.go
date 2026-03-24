package cmd

import (
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestRunServe_EnvOnly_NoConfigFile_DoesNotFailOnConfigLoadStage(t *testing.T) {
	resetRootFlagsForTest(t)
	defer resetRootFlagsForTest(t)

	// 不提供 --config，也不在默认搜索路径放置配置文件。
	// 通过 env-only 补齐 Validate/serve 必需字段，证明 root 的统一配置入口不会因“找不到配置文件”而直接失败。
	// 这里不试图真正启动服务；只验证能进入 serveCmd.RunE。

	t.Setenv("DTWORKFLOW_WEBHOOK_SECRET", "env-secret")
	t.Setenv("DTWORKFLOW_NOTIFY_CHANNELS_GITEA_ENABLED", "true")
	t.Setenv("DTWORKFLOW_CLAUDE_API_KEY", "env-ck")
	t.Setenv("DTWORKFLOW_GITEA_URL", "https://gitea.example.com")
	t.Setenv("DTWORKFLOW_GITEA_TOKEN", "gt")
	_ = os.Unsetenv("DTWORKFLOW_CONFIG")

	oldRunE := serveCmd.RunE
	serveCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return nil
	}
	defer func() { serveCmd.RunE = oldRunE }()

	rootCmd.SetArgs([]string{"serve"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("预期 env-only serve 可通过统一配置入口并进入 RunE，但失败: %v", err)
	}
}
