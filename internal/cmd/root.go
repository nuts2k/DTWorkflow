package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)

var (
	jsonOutput    bool
	configFile    string
	verboseOutput bool

	// cfgManager 为全局共享的配置管理器实例。
	//
	// 约定：由 rootCmd.PersistentPreRunE 统一初始化，命令实现可通过该变量读取配置。
	cfgManager *config.Manager
)

// ExitCodeError 携带退出码的错误类型，用于实现 0/1/2 退出码规范
type ExitCodeError struct {
	Code int
	Err  error
}

func (e *ExitCodeError) Error() string {
	return e.Err.Error()
}

func (e *ExitCodeError) Unwrap() error {
	return e.Err
}

// ExitCode 从错误中提取退出码：0 成功 / 1 失败 / 2 部分成功
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitCodeError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

func isConfigOptionalCommand(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	// 根命令本身不执行具体业务逻辑，通常只会展示 help。
	// 注意：这里不能直接引用 rootCmd，否则会形成初始化循环。
	if cmd.Parent() == nil {
		return true
	}
	switch cmd.Name() {
	case "version", "help":
		return true
	default:
		return false
	}
}

func bindFlagsToViper(m *config.Manager, cmd *cobra.Command) error {
	if m == nil {
		return fmt.Errorf("配置管理器不能为空")
	}
	if cmd == nil {
		return nil
	}
	v := m.Viper()

	// 说明：
	// 1) 仅绑定“当前即将执行的命令”相关的 flags，避免无关命令的 flag 默认值覆盖配置文件。
	// 2) 仅在 flag 被用户显式指定时才绑定，否则 viper 会把 flag 默认值当作更高优先级来源。
	//
	bindIfChanged := func(flagName, key, errLabel string) error {
		f := cmd.Flags().Lookup(flagName)
		if f == nil || !cmd.Flags().Changed(flagName) {
			return nil
		}
		if err := v.BindPFlag(key, f); err != nil {
			return fmt.Errorf("绑定 flag %s 失败: %w", errLabel, err)
		}
		return nil
	}

	// 说明：
	// - 仅绑定“当前即将执行的命令”相关的 flags，避免无关命令的 flag 默认值覆盖配置文件。
	// - 仅当 flag 被用户显式指定时才绑定，保持 "flag > env > file > defaults" 语义。
	//
	// 本任务（Task 6）范围：补齐 serve 命令关键 flags 的绑定，避免 runServe 走 cfgManager 后 flag 失效。
	switch cmd.Name() {
	case "serve":
		if err := bindIfChanged("host", "server.host", "--host"); err != nil {
			return err
		}
		if err := bindIfChanged("port", "server.port", "--port"); err != nil {
			return err
		}

		if err := bindIfChanged("webhook-secret", "webhook.secret", "--webhook-secret"); err != nil {
			return err
		}
		if err := bindIfChanged("redis-addr", "redis.addr", "--redis-addr"); err != nil {
			return err
		}
		if err := bindIfChanged("db-path", "database.path", "--db-path"); err != nil {
			return err
		}

		if err := bindIfChanged("max-workers", "worker.concurrency", "--max-workers"); err != nil {
			return err
		}
		if err := bindIfChanged("worker-image", "worker.image", "--worker-image"); err != nil {
			return err
		}

		if err := bindIfChanged("claude-api-key", "claude.api_key", "--claude-api-key"); err != nil {
			return err
		}
		if err := bindIfChanged("gitea-url", "gitea.url", "--gitea-url"); err != nil {
			return err
		}
		if err := bindIfChanged("gitea-token", "gitea.token", "--gitea-token"); err != nil {
			return err
		}
	}
	return nil
}

func ensureConfigManagerForCommand(cmd *cobra.Command) error {
	if cfgManager != nil {
		return nil
	}
	if isConfigOptionalCommand(cmd) {
		return nil
	}

	m, err := config.NewManager(
		config.WithDefaults(),
		config.WithDefaultSearchPaths(),
		config.WithEnvPrefix("DTWORKFLOW"),
	)
	if err != nil {
		return fmt.Errorf("初始化配置管理器失败: %w", err)
	}

	// 处理 --config
	if configFile != "" {
		m.SetConfigFile(configFile)
	}

	// 绑定 CLI flags 到 viper，支持 flag 覆盖配置文件值。
	if err := bindFlagsToViper(m, cmd); err != nil {
		return err
	}

	if err := m.Load(); err != nil {
		return err
	}

	cfgManager = m
	return nil
}

var rootCmd = &cobra.Command{
	Use:           "dtworkflow",
	Short:         "基于 Claude Code 的 Gitea 自动化工作流平台",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return ensureConfigManagerForCommand(cmd)
	},
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "以 JSON 格式输出")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "配置文件路径")
	rootCmd.PersistentFlags().BoolVarP(&verboseOutput, "verbose", "v", false, "详细日志输出")
}

func Execute() error {
	return rootCmd.Execute()
}
