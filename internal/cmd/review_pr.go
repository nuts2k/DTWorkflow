package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var (
	reviewRepo string
	reviewPR   int
	reviewSync bool
)

var reviewPRCmd = &cobra.Command{
	Use:   "review-pr",
	Short: "自动评审 Pull Request",
	PreRunE: func(cmd *cobra.Command, args []string) error {
		if reviewRepo == "" {
			return fmt.Errorf("必须指定仓库：--repo <仓库名>")
		}
		if reviewPR <= 0 {
			return fmt.Errorf("必须指定 PR 编号：--pr <编号>")
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("review-pr 尚未实现，将在 M2.x 中完成")}
	},
}

func init() {
	reviewPRCmd.Flags().StringVar(&reviewRepo, "repo", "", "仓库名（必填）")
	reviewPRCmd.Flags().IntVar(&reviewPR, "pr", 0, "PR 编号（必填）")
	reviewPRCmd.Flags().BoolVar(&reviewSync, "sync", false, "同步执行，直接运行而不提交到任务队列")
	rootCmd.AddCommand(reviewPRCmd)
}
