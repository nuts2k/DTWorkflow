package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// asynq 任务类型常量
const (
	AsynqTypeReviewPR = "dtworkflow:review_pr"
	AsynqTypeFixIssue = "dtworkflow:fix_issue"
	AsynqTypeGenTests = "dtworkflow:gen_tests"
)

// 队列名称常量
const (
	QueueCritical = "critical"
	QueueDefault  = "default"
	QueueLow      = "low"
)

// Enqueuer 定义任务入队能力的接口，便于测试时注入 mock
type Enqueuer interface {
	Enqueue(ctx context.Context, payload model.TaskPayload, opts EnqueueOptions) (string, error)
}

// TaskCanceller 任务取消能力接口（与 Enqueuer 分离）
type TaskCanceller interface {
	// Delete 从队列中删除 pending/queued 任务。
	// queueName 通过 PriorityToQueue(record.Priority) 推导。
	Delete(ctx context.Context, queueName, taskID string) error
	// CancelProcessing 向正在执行的任务 handler 发送取消信号
	CancelProcessing(ctx context.Context, taskID string) error
}

// Client 封装 asynq.Client，提供任务入队和取消能力
type Client struct {
	inner      *asynq.Client
	inspector  *asynq.Inspector
	pingClient redis.UniversalClient // 缓存的 Redis 客户端，用于 Ping 健康检查
	timeouts   TaskTimeoutsConfig    // 超时配置
}

// 编译时检查 *Client 实现 Enqueuer 和 TaskCanceller 接口
var _ Enqueuer = (*Client)(nil)
var _ TaskCanceller = (*Client)(nil)

// EnqueueOptions 入队选项
type EnqueueOptions struct {
	Priority model.TaskPriority
	// TaskID 用于 asynq 层面幂等（可选）
	TaskID string
}

// NewClient 创建并返回一个新的 Client
// redisOpt 接受 asynq.RedisConnOpt 接口类型（如 asynq.RedisClientOpt、asynq.RedisClusterClientOpt 等）
// 注意：创建时不验证 Redis 连通性，调用方可通过 Ping() 方法按需检查。
func NewClient(redisOpt asynq.RedisConnOpt) (*Client, error) {
	inner := asynq.NewClient(redisOpt)
	inspector := asynq.NewInspector(redisOpt)
	rawClient := redisOpt.MakeRedisClient()
	pingClient, ok := rawClient.(redis.UniversalClient)
	if !ok {
		_ = inner.Close()
		if closer, cok := rawClient.(io.Closer); cok {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("NewClient: 不支持的 Redis 客户端类型")
	}
	return &Client{inner: inner, inspector: inspector, pingClient: pingClient}, nil
}

// SetTimeouts 设置任务超时配置。未调用时使用默认值（零值 config）。
// 注意：必须在调用 Enqueue 之前完成设置（即初始化阶段），不支持并发调用。
func (c *Client) SetTimeouts(cfg TaskTimeoutsConfig) {
	c.timeouts = cfg
}

// Enqueue 将任务 payload 序列化后入队，返回 asynq 任务 ID
func (c *Client) Enqueue(ctx context.Context, payload model.TaskPayload, opts EnqueueOptions) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化 TaskPayload 失败: %w", err)
	}

	taskType := taskTypeToAsynq(payload.TaskType)
	task := asynq.NewTask(taskType, data)

	asynqOpts := buildAsynqOptions(payload.TaskType, c.timeouts, opts)
	info, err := c.inner.EnqueueContext(ctx, task, asynqOpts...)
	if err != nil {
		return "", fmt.Errorf("入队失败: %w", err)
	}
	return info.ID, nil
}

// Delete 从队列中删除 pending/queued 任务
func (c *Client) Delete(_ context.Context, queueName, taskID string) error {
	if err := c.inspector.DeleteTask(queueName, taskID); err != nil {
		return fmt.Errorf("从 asynq 删除任务失败: %w", err)
	}
	return nil
}

// CancelProcessing 向正在执行的任务 handler 发送取消信号
func (c *Client) CancelProcessing(_ context.Context, taskID string) error {
	if err := c.inspector.CancelProcessing(taskID); err != nil {
		return fmt.Errorf("取消 asynq 任务处理失败: %w", err)
	}
	return nil
}

// Ping 检测 Redis 连接是否可用，复用缓存的 pingClient 而非每次创建新连接
func (c *Client) Ping(ctx context.Context) error {
	if err := c.pingClient.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis Ping 失败: %w", err)
	}
	return nil
}

// Close 关闭底层 asynq 客户端、Inspector 和缓存的 Redis 连接
func (c *Client) Close() error {
	var pingErr error
	if c.pingClient != nil {
		pingErr = c.pingClient.Close()
	}
	var innerErr error
	if c.inner != nil {
		innerErr = c.inner.Close()
	}
	var inspErr error
	if c.inspector != nil {
		inspErr = c.inspector.Close()
	}
	return errors.Join(pingErr, innerErr, inspErr)
}

// taskTypeToAsynq 将 model.TaskType 转换为 asynq 任务类型字符串
func taskTypeToAsynq(t model.TaskType) string {
	switch t {
	case model.TaskTypeReviewPR:
		return AsynqTypeReviewPR
	case model.TaskTypeFixIssue:
		return AsynqTypeFixIssue
	case model.TaskTypeGenTests:
		return AsynqTypeGenTests
	default:
		slog.Warn("未知的任务类型，将原样使用", slog.String("task_type", string(t)))
		return string(t)
	}
}

// buildAsynqOptions 根据 EnqueueOptions 构建 asynq 入队选项
func buildAsynqOptions(taskType model.TaskType, timeouts TaskTimeoutsConfig, opts EnqueueOptions) []asynq.Option {
	queue := PriorityToQueue(opts.Priority)
	asynqOpts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(TaskMaxRetry()),
		asynq.Timeout(TaskTimeout(taskType, timeouts)),
	}
	if opts.TaskID != "" {
		asynqOpts = append(asynqOpts, asynq.TaskID(opts.TaskID))
	}
	return asynqOpts
}
