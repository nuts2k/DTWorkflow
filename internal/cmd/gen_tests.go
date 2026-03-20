package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	genTestsRepo   string
	genTestsModule string
	genTestsSync   bool
)

var genTestsCmd = &cobra.Command{
	Use:   "gen-tests",
	Short: "生成测试用例",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if genTestsRepo == "" {
			return fmt.Errorf("必须指定仓库：--repo <仓库名>")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("gen-tests 尚未实现，将在 M4.x 中完成")}
	},
}

func init() {
	genTestsCmd.Flags().StringVar(&genTestsRepo, "repo", "", "仓库名（必填）")
	genTestsCmd.Flags().StringVar(&genTestsModule, "module", "", "指定模块或目录")
	genTestsCmd.Flags().BoolVar(&genTestsSync, "sync", false, "同步执行，直接运行而不提交到任务队列")
	rootCmd.AddCommand(genTestsCmd)
}
