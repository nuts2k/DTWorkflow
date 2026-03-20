package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// task 子命令的 flags
var (
	taskListRepo   string
	taskListStatus string
	taskListLimit  int
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "管理任务",
}

var taskStatusCmd = &cobra.Command{
	Use:   "status <task-id>",
	Short: "查看任务状态",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("task status 尚未实现，将在 M1.5 中完成")}
	},
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出任务",
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("task list 尚未实现，将在 M1.5 中完成")}
	},
}

var taskRetryCmd = &cobra.Command{
	Use:   "retry <task-id>",
	Short: "重试失败的任务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return &ExitCodeError{Code: 1, Err: fmt.Errorf("task retry 尚未实现，将在 M1.5 中完成")}
	},
}

func init() {
	taskListCmd.Flags().StringVar(&taskListRepo, "repo", "", "按仓库过滤")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "按状态过滤")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 20, "限制数量")

	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskRetryCmd)
	rootCmd.AddCommand(taskCmd)
}
