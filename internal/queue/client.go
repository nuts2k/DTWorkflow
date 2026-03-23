package queue

import (
	"context"
	"encoding/json"
	"fmt"

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

// Client 封装 asynq.Client，提供任务入队能力
type Client struct {
	inner    *asynq.Client
	redisOpt asynq.RedisConnOpt
}

// EnqueueOptions 入队选项
type EnqueueOptions struct {
	Priority model.TaskPriority
	// TaskID 用于 asynq 层面幂等（可选）
	TaskID string
}

// NewClient 创建并返回一个新的 Client
func NewClient(redisOpt asynq.RedisClientOpt) (*Client, error) {
	inner := asynq.NewClient(redisOpt)
	return &Client{inner: inner, redisOpt: redisOpt}, nil
}

// Enqueue 将任务 payload 序列化后入队，返回 asynq 任务 ID
func (c *Client) Enqueue(ctx context.Context, payload model.TaskPayload, opts EnqueueOptions) (string, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化 TaskPayload 失败: %w", err)
	}

	taskType := taskTypeToAsynq(payload.TaskType)
	task := asynq.NewTask(taskType, data)

	asynqOpts := buildAsynqOptions(payload.TaskType, opts)
	info, err := c.inner.EnqueueContext(ctx, task, asynqOpts...)
	if err != nil {
		return "", fmt.Errorf("入队失败: %w", err)
	}
	return info.ID, nil
}

// Ping 检测 Redis 连接是否可用
func (c *Client) Ping(ctx context.Context) error {
	rc, ok := c.redisOpt.MakeRedisClient().(*redis.Client)
	if !ok {
		return fmt.Errorf("Ping: 不支持的 Redis 客户端类型")
	}
	defer rc.Close()
	if err := rc.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("Redis Ping 失败: %w", err)
	}
	return nil
}

// Close 关闭底层 asynq 客户端
func (c *Client) Close() error {
	return c.inner.Close()
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
		return string(t)
	}
}

// buildAsynqOptions 根据 EnqueueOptions 构建 asynq 入队选项
func buildAsynqOptions(taskType model.TaskType, opts EnqueueOptions) []asynq.Option {
	queue := PriorityToQueue(opts.Priority)
	asynqOpts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(TaskMaxRetry()),
		asynq.Timeout(TaskTimeout(taskType)),
	}
	if opts.TaskID != "" {
		asynqOpts = append(asynqOpts, asynq.TaskID(opts.TaskID))
	}
	return asynqOpts
}
