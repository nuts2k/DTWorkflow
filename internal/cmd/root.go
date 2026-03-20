package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

var (
	jsonOutput    bool
	configFile    string
	verboseOutput bool
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

var rootCmd = &cobra.Command{
	Use:           "dtworkflow",
	Short:         "基于 Claude Code 的 Gitea 自动化工作流平台",
	SilenceUsage:  true,
	SilenceErrors: true,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "以 JSON 格式输出")
	rootCmd.PersistentFlags().StringVar(&configFile, "config", "", "配置文件路径")
	rootCmd.PersistentFlags().BoolVarP(&verboseOutput, "verbose", "v", false, "详细日志输出")
}

func Execute() error {
	return rootCmd.Execute()
}
