package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "管理任务",
}

// --- task list ---

var (
	taskListStatus string
	taskListLimit  int
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出任务",
	RunE: func(cmd *cobra.Command, args []string) error {
		path := "/api/v1/tasks"
		sep := "?"
		if taskListStatus != "" {
			path += sep + "status=" + taskListStatus
			sep = "&"
		}
		if taskListLimit > 0 {
			path += fmt.Sprintf("%slimit=%d", sep, taskListLimit)
		}

		var tasks []dtw.TaskStatus
		if err := client.Do(context.Background(), "GET", path, nil, &tasks); err != nil {
			return fmt.Errorf("查询任务列表失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(tasks)
		}

		if len(tasks) == 0 {
			printer.PrintHuman("暂无任务")
			return nil
		}

		for _, t := range tasks {
			printer.PrintHuman("%-36s  %-12s  %s", t.ID, t.Status, t.Result)
		}
		return nil
	},
}

// --- task status ---

var taskStatusCmd = &cobra.Command{
	Use:   "status [task-id]",
	Short: "查看任务详情",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var task dtw.TaskStatus
		if err := client.Do(context.Background(), "GET", "/api/v1/tasks/"+args[0], nil, &task); err != nil {
			return fmt.Errorf("查询任务失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(task)
		}

		printer.PrintHuman("ID:     %s", task.ID)
		printer.PrintHuman("状态:   %s", task.Status)
		if task.Result != "" {
			printer.PrintHuman("结果:   %s", task.Result)
		}
		if task.Error != "" {
			printer.PrintHuman("错误:   %s", task.Error)
		}
		return nil
	},
}

// --- task retry ---

var taskRetryCmd = &cobra.Command{
	Use:   "retry [task-id]",
	Short: "重试失败的任务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var result map[string]any
		if err := client.Do(context.Background(), "POST", "/api/v1/tasks/"+args[0]+"/retry", nil, &result); err != nil {
			return fmt.Errorf("重试任务失败: %w", err)
		}

		return printer.Print(fmt.Sprintf("任务 %s 已提交重试", args[0]), result)
	},
}

func init() {
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "按状态过滤（pending/running/succeeded/failed）")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 0, "限制返回数量")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskRetryCmd)
	rootCmd.AddCommand(taskCmd)
}
