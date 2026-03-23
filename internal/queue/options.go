package queue

import (
	"math"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// TaskTimeout 返回指定任务类型的执行超时时长
// PR 评审：10分钟 / Issue 修复：30分钟 / 测试生成：20分钟
func TaskTimeout(taskType model.TaskType) time.Duration {
	switch taskType {
	case model.TaskTypeReviewPR:
		return 10 * time.Minute
	case model.TaskTypeFixIssue:
		return 30 * time.Minute
	case model.TaskTypeGenTests:
		return 20 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// TaskMaxRetry 返回任务最大重试次数
func TaskMaxRetry() int {
	return 3
}

// TaskRetryDelay 返回第 retryCount 次重试的指数退避延迟
// base=30s，factor=2，即 30s, 60s, 120s ...
func TaskRetryDelay(retryCount int) time.Duration {
	base := 30 * time.Second
	delay := float64(base) * math.Pow(2, float64(retryCount))
	return time.Duration(delay)
}

// PriorityToQueue 将 model.TaskPriority 映射到 asynq 队列名称
// 注意：High 和 Normal 都映射到 QueueDefault，优先级差异通过 asynq Server 的队列权重配置体现，
// 而非使用不同队列名称。Low 优先级单独使用低优先级队列。
func PriorityToQueue(priority model.TaskPriority) string {
	switch priority {
	case model.PriorityCritical:
		return QueueCritical
	case model.PriorityHigh:
		return QueueDefault
	case model.PriorityNormal:
		return QueueDefault
	case model.PriorityLow:
		return QueueLow
	default:
		return QueueDefault
	}
}
