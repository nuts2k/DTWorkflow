package cmd

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/dtw"
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "管理任务",
}

// --- task list ---

var (
	taskListRepo   string
	taskListStatus string
	taskListLimit  int
)

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出任务",
	RunE: func(cmd *cobra.Command, args []string) error {
		query := url.Values{}
		if taskListRepo != "" {
			query.Set("repo", taskListRepo)
		}
		if taskListStatus != "" {
			query.Set("status", taskListStatus)
		}
		if taskListLimit > 0 {
			query.Set("limit", strconv.Itoa(taskListLimit))
		}
		path := "/api/v1/tasks"
		if encoded := query.Encode(); encoded != "" {
			path += "?" + encoded
		}

		var result dtw.TaskListResponse
		if err := client.Do(context.Background(), "GET", path, nil, &result); err != nil {
			return fmt.Errorf("查询任务列表失败: %w", err)
		}

		if flagJSON {
			return printer.PrintJSON(result)
		}

		if len(result.Tasks) == 0 {
			printer.PrintHuman("暂无任务")
			return nil
		}

		for _, task := range result.Tasks {
			target := ""
			if task.PRNumber > 0 {
				target = fmt.Sprintf("PR #%d", task.PRNumber)
			} else if task.IssueNumber > 0 {
				target = fmt.Sprintf("Issue #%d", task.IssueNumber)
			}
			printer.PrintHuman("%-36s  %-10s  %-12s  %-24s  %s",
				task.ID, task.Type, task.Status, task.Repo, target)
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
		if task.Type != "" {
			printer.PrintHuman("类型:   %s", task.Type)
		}
		printer.PrintHuman("状态:   %s", task.Status)
		if task.Repo != "" {
			printer.PrintHuman("仓库:   %s", task.Repo)
		}
		if task.PRNumber > 0 {
			printer.PrintHuman("PR:     #%d", task.PRNumber)
		}
		if task.IssueNumber > 0 {
			printer.PrintHuman("Issue:  #%d", task.IssueNumber)
		}
		if task.TriggeredBy != "" {
			printer.PrintHuman("触发:   %s", task.TriggeredBy)
		}
		if task.Result != "" {
			printer.PrintHuman("结果:   %s", task.Result)
		}
		if task.Error != "" {
			printer.PrintHuman("错误:   %s", task.Error)
		}
		if !task.CreatedAt.IsZero() {
			printer.PrintHuman("创建:   %s", task.CreatedAt.Local().Format(time.DateTime))
		}
		if !task.UpdatedAt.IsZero() {
			printer.PrintHuman("更新:   %s", task.UpdatedAt.Local().Format(time.DateTime))
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
	taskListCmd.Flags().StringVar(&taskListRepo, "repo", "", "按仓库过滤（owner/repo）")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "按状态过滤（pending/running/succeeded/failed）")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 0, "限制返回数量")

	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskRetryCmd)
	rootCmd.AddCommand(taskCmd)
}
