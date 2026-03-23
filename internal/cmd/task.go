package cmd

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// taskStore 包级 Store 实例，在 PersistentPreRunE 中初始化
var taskStore store.Store

// task 子命令的 flags
var (
	taskListRepo   string
	taskListStatus string
	taskListLimit  int
	taskDBPath     string
)

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "管理任务",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// 初始化 SQLite Store
		var err error
		taskStore, err = store.NewSQLiteStore(taskDBPath)
		if err != nil {
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		if taskStore != nil {
			return taskStore.Close()
		}
		return nil
	},
}

var taskStatusCmd = &cobra.Command{
	Use:   "status <task-id>",
	Short: "查看任务状态",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id := args[0]

		record, err := taskStore.GetTask(ctx, id)
		if err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("查询任务失败: %w", err)}
		}
		if record == nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("任务 %s 不存在", id)}
		}

		PrintResult(record, func(data any) string {
			r := data.(*model.TaskRecord)
			var sb strings.Builder
			w := tabwriter.NewWriter(&sb, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "ID:\t%s\n", r.ID)
			fmt.Fprintf(w, "类型:\t%s\n", r.TaskType)
			fmt.Fprintf(w, "状态:\t%s\n", r.Status)
			fmt.Fprintf(w, "仓库:\t%s\n", r.RepoFullName)
			fmt.Fprintf(w, "优先级:\t%d\n", r.Priority)
			fmt.Fprintf(w, "重试次数:\t%d / %d\n", r.RetryCount, r.MaxRetry)
			fmt.Fprintf(w, "创建时间:\t%s\n", r.CreatedAt.Local().Format(time.DateTime))
			fmt.Fprintf(w, "更新时间:\t%s\n", r.UpdatedAt.Local().Format(time.DateTime))
			if r.StartedAt != nil {
				fmt.Fprintf(w, "开始时间:\t%s\n", r.StartedAt.Local().Format(time.DateTime))
			}
			if r.CompletedAt != nil {
				fmt.Fprintf(w, "完成时间:\t%s\n", r.CompletedAt.Local().Format(time.DateTime))
			}
			if r.WorkerID != "" {
				fmt.Fprintf(w, "Worker:\t%s\n", r.WorkerID)
			}
			if r.Error != "" {
				fmt.Fprintf(w, "错误:\t%s\n", r.Error)
			}
			w.Flush()
			return sb.String()
		})
		return nil
	},
}

var taskListCmd = &cobra.Command{
	Use:   "list",
	Short: "列出任务",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()

		opts := store.ListOptions{
			RepoFullName: taskListRepo,
			Limit:        taskListLimit,
		}
		if taskListStatus != "" {
			s := model.TaskStatus(taskListStatus)
			if !s.IsValid() {
				return fmt.Errorf("无效的任务状态: %s", taskListStatus)
			}
			opts.Status = s
		}

		records, err := taskStore.ListTasks(ctx, opts)
		if err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("列出任务失败: %w", err)}
		}

		PrintResult(records, func(data any) string {
			list := data.([]*model.TaskRecord)
			if len(list) == 0 {
				return "暂无任务\n"
			}
			var buf bytes.Buffer
			w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\t类型\t状态\t仓库\t创建时间")
			fmt.Fprintln(w, "----\t----\t----\t----\t--------")
			for _, r := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
					r.ID,
					r.TaskType,
					r.Status,
					r.RepoFullName,
					r.CreatedAt.Local().Format(time.DateTime),
				)
			}
			w.Flush()
			return buf.String()
		})
		return nil
	},
}

var taskRetryCmd = &cobra.Command{
	Use:   "retry <task-id>",
	Short: "重试失败的任务",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		id := args[0]

		record, err := taskStore.GetTask(ctx, id)
		if err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("查询任务失败: %w", err)}
		}
		if record == nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("任务 %s 不存在", id)}
		}

		// 只有 failed 或 cancelled 的任务可以重试
		if record.Status != model.TaskStatusFailed && record.Status != model.TaskStatusCancelled {
			return &ExitCodeError{
				Code: 1,
				Err:  fmt.Errorf("任务 %s 状态为 %s，只有 failed 或 cancelled 状态的任务可以重试", id, record.Status),
			}
		}

		// 重置重试计数和状态，同时更新时间戳以便 RecoveryLoop 及时检测
		record.RetryCount = 0
		record.Status = model.TaskStatusPending
		record.Error = ""
		record.UpdatedAt = time.Now()

		if err := taskStore.UpdateTask(ctx, record); err != nil {
			return &ExitCodeError{Code: 1, Err: fmt.Errorf("更新任务失败: %w", err)}
		}

		// 注意：此处仅重置 Store 中的状态为 pending，不直接入队。
		// 任务将由 RecoveryLoop 在下一个扫描周期（默认 60s）内自动重新入队。
		PrintResult(map[string]string{"id": id, "status": string(model.TaskStatusPending)}, func(data any) string {
			return fmt.Sprintf("任务 %s 已重置为 pending 状态，将由 serve 进程的 RecoveryLoop 自动重新入队（约 60 秒内）\n", id)
		})
		return nil
	},
}

func init() {
	taskCmd.PersistentFlags().StringVar(&taskDBPath, "db-path",
		getEnvDefault("DTWORKFLOW_DB_PATH", "data/dtworkflow.db"),
		"SQLite 数据库路径（也可通过 DTWORKFLOW_DB_PATH 环境变量设置）")

	taskListCmd.Flags().StringVar(&taskListRepo, "repo", "", "按仓库过滤")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "按状态过滤")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 20, "限制数量")

	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskRetryCmd)
	rootCmd.AddCommand(taskCmd)
}
