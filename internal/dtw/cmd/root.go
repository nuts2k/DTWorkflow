package cmd

import (
	"context"
	"fmt"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var (
	flagServer  string
	flagJSON    bool
	flagVerbose bool
	configPath  string

	hostsCfg *dtw.HostsConfig
	client   *dtw.Client
	printer  *dtw.Printer
)

// 版本信息，通过 ldflags 注入
var (
	dtwVersion   = "dev"
	dtwCommit    = "unknown"
	dtwBuildTime = ""
)

var rootCmd = &cobra.Command{
	Use:   "dtw",
	Short: "DTWorkflow 远程瘦客户端",
	Long:  "dtw 是 DTWorkflow 的远程命令行客户端，通过 REST API 管理任务、触发评审和修复。",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		printer = dtw.NewPrinter(flagJSON)

		// auth 和 version 命令不需要加载配置
		if cmd.Name() == "login" || cmd.Name() == "logout" || cmd.Name() == "version" || cmd.Name() == "help" {
			return nil
		}
		// auth status/switch 也不需要客户端连接
		if cmd.Parent() != nil && cmd.Parent().Name() == "auth" {
			return nil
		}

		if configPath == "" {
			configPath = dtw.DefaultConfigPath()
		}

		cfg, err := dtw.LoadHostsConfig(configPath)
		if err != nil {
			return fmt.Errorf("加载配置失败: %w\n请先运行 dtw auth login 配置服务器", err)
		}
		hostsCfg = cfg

		srv, err := cfg.ResolveServer(flagServer)
		if err != nil {
			return err
		}

		client = dtw.NewClient(srv.URL, srv.Token)
		return nil
	},
	SilenceUsage: true,
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagServer, "server", "", "指定目标服务器名称（覆盖 active 配置）")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "JSON 格式输出")
	rootCmd.PersistentFlags().BoolVarP(&flagVerbose, "verbose", "v", false, "详细输出")
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "配置文件路径（默认 ~/.config/dtw/hosts.yml）")
}

func Execute() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return rootCmd.ExecuteContext(ctx)
}
