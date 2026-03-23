package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// taskStore 包级 Store 实例，在 PersistentPreRunE 中初始化
// TODO: 当前 task 命令使用包级全局变量管理配置和依赖（taskStore, taskDBPath 等），
// 这在单元测试中需要手动保存/恢复全局状态。后续迭代中应重构为与 serve.go 一致的
// 依赖注入模式（提取 taskConfig 结构体 + 注入 Store 实例），提升可测试性。
var taskStore store.Store
var taskQueueClient *queue.Client

// task 子命令的 flags
var (
	taskListRepo   string
	taskListStatus string
	taskListLimit  int
	taskDBPath     string
	taskRedisAddr  string
)

type taskEnqueuer interface {
	Enqueue(ctx context.Context, payload model.TaskPayload, opts queue.EnqueueOptions) (string, error)
}

func buildRetryTaskID(deliveryID string, taskType model.TaskType) string {
	if deliveryID != "" {
		return fmt.Sprintf("%s:%s", deliveryID, taskType)
	}
	return ""
}

func retryTask(ctx context.Context, s store.Store, q taskEnqueuer, id string) (*model.TaskRecord, string, error) {
	record, err := s.GetTask(ctx, id)
	if err != nil {
		return nil, "", fmt.Errorf("查询任务失败: %w", err)
	}
	if record == nil {
		return nil, "", fmt.Errorf("任务 %s 不存在", id)
	}
	if record.Status != model.TaskStatusFailed && record.Status != model.TaskStatusCancelled {
		return nil, "", fmt.Errorf("任务 %s 状态为 %s，只有 failed 或 cancelled 状态的任务可以重试", id, record.Status)
	}
	if !record.Payload.TaskType.IsValid() {
		return nil, "", fmt.Errorf("任务 %s 的 TaskType 非法: %s", id, record.Payload.TaskType)
	}

	taskID := buildRetryTaskID(record.DeliveryID, record.TaskType)
	asynqID, err := q.Enqueue(ctx, record.Payload, queue.EnqueueOptions{Priority: record.Priority, TaskID: taskID})
	message := "任务已重新入队"
	if errors.Is(err, asynq.ErrTaskIDConflict) {
		asynqID = taskID
		message = "任务已在队列中，状态已同步为 queued"
	} else if err != nil {
		return nil, "", fmt.Errorf("任务重新入队失败: %w", err)
	}

	record.RetryCount = 0
	record.Error = ""
	record.StartedAt = nil
	record.CompletedAt = nil
	record.WorkerID = ""
	record.Status = model.TaskStatusQueued
	record.AsynqID = asynqID
	record.UpdatedAt = time.Now()
	if err := s.UpdateTask(ctx, record); err != nil {
		return nil, "", fmt.Errorf("任务可能已重新入队，但状态同步失败: %w", err)
	}
	return record, message, nil
}

var taskCmd = &cobra.Command{
	Use:   "task",
	Short: "管理任务",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		var err error
		taskStore, err = store.NewSQLiteStore(taskDBPath)
		if err != nil {
			return fmt.Errorf("初始化数据库失败: %w", err)
		}
		taskQueueClient, err = queue.NewClient(asynq.RedisClientOpt{Addr: taskRedisAddr})
		if err != nil {
			_ = taskStore.Close()
			taskStore = nil
			return fmt.Errorf("初始化队列客户端失败: %w", err)
		}
		return nil
	},
	PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
		var errs []error
		if taskQueueClient != nil {
			errs = append(errs, taskQueueClient.Close())
			taskQueueClient = nil
		}
		if taskStore != nil {
			errs = append(errs, taskStore.Close())
			taskStore = nil
		}
		return errors.Join(errs...)
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
		if taskListLimit <= 0 || taskListLimit > 1000 {
			return fmt.Errorf("--limit 必须在 1-1000 范围内，当前值: %d", taskListLimit)
		}

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

		record, message, err := retryTask(ctx, taskStore, taskQueueClient, id)
		if err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}

		PrintResult(map[string]any{
			"id":       id,
			"status":   string(record.Status),
			"asynq_id": record.AsynqID,
			"message":  message,
		}, func(data any) string {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%s\n", message)
			fmt.Fprintf(&sb, "当前状态: %s\n", record.Status)
			return sb.String()
		})
		return nil
	},
}

func init() {
	taskCmd.PersistentFlags().StringVar(&taskDBPath, "db-path",
		getEnvDefault("DTWORKFLOW_DB_PATH", "data/dtworkflow.db"),
		"SQLite 数据库路径（也可通过 DTWORKFLOW_DB_PATH 环境变量设置）")
	taskCmd.PersistentFlags().StringVar(&taskRedisAddr, "redis-addr",
		getEnvDefault("DTWORKFLOW_REDIS_ADDR", "localhost:6379"),
		"Redis 地址（也可通过 DTWORKFLOW_REDIS_ADDR 环境变量设置）")

	taskListCmd.Flags().StringVar(&taskListRepo, "repo", "", "按仓库过滤")
	taskListCmd.Flags().StringVar(&taskListStatus, "status", "", "按状态过滤")
	taskListCmd.Flags().IntVar(&taskListLimit, "limit", 20, "限制数量")

	taskCmd.AddCommand(taskStatusCmd)
	taskCmd.AddCommand(taskListCmd)
	taskCmd.AddCommand(taskRetryCmd)
	rootCmd.AddCommand(taskCmd)
}
