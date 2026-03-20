package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	fixRepo  string
	fixIssue int
	fixSync  bool
)

var fixIssueCmd = &cobra.Command{
	Use:   "fix-issue",
	Short: "自动分析与修复 Issue",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if fixRepo == "" {
			return fmt.Errorf("必须指定仓库：--repo <仓库名>")
		}
		if fixIssue <= 0 {
			return fmt.Errorf("必须指定 Issue 编号：--issue <编号>")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("fix-issue 尚未实现，将在 M3.x 中完成")}
	},
}

func init() {
	fixIssueCmd.Flags().StringVar(&fixRepo, "repo", "", "仓库名（必填）")
	fixIssueCmd.Flags().IntVar(&fixIssue, "issue", 0, "Issue 编号（必填）")
	fixIssueCmd.Flags().BoolVar(&fixSync, "sync", false, "同步执行，直接运行而不提交到任务队列")
	rootCmd.AddCommand(fixIssueCmd)
}
