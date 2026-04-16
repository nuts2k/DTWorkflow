package queue

import (
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// TaskTimeoutsConfig 按任务类型的超时配置
// 在 queue 包中单独定义，避免依赖 worker 包导致循环引用
type TaskTimeoutsConfig struct {
	ReviewPR     time.Duration
	FixIssue     time.Duration
	GenTests     time.Duration
	AnalyzeIssue time.Duration // M3.4: 只读分析超时（默认 15m）
}

// TaskTimeout 从配置中获取超时值，零值时回退到硬编码默认值
func TaskTimeout(taskType model.TaskType, cfg TaskTimeoutsConfig) time.Duration {
	var configured time.Duration
	switch taskType {
	case model.TaskTypeReviewPR:
		configured = cfg.ReviewPR
	case model.TaskTypeFixIssue:
		configured = cfg.FixIssue
	case model.TaskTypeGenTests:
		configured = cfg.GenTests
	case model.TaskTypeAnalyzeIssue:
		configured = cfg.AnalyzeIssue
	}
	if configured > 0 {
		return configured
	}
	return defaultTaskTimeout(taskType)
}

// defaultTaskTimeout 硬编码默认超时值（fallback）
func defaultTaskTimeout(taskType model.TaskType) time.Duration {
	switch taskType {
	case model.TaskTypeReviewPR:
		return 10 * time.Minute
	case model.TaskTypeFixIssue:
		return 30 * time.Minute
	case model.TaskTypeGenTests:
		return 20 * time.Minute
	case model.TaskTypeGenDailyReport:
		return 10 * time.Minute
	case model.TaskTypeAnalyzeIssue:
		return 15 * time.Minute
	default:
		return 10 * time.Minute
	}
}

// TODO(M1.8): 从 Viper 配置读取，支持按任务类型区分最大重试次数

// TaskMaxRetry 返回任务最大重试次数
func TaskMaxRetry() int {
	return 3
}

// TaskRetryDelay 返回第 retryCount 次重试的指数退避延迟
// base=30s，factor=2，即 30s, 60s, 120s ...
// retryCount 为负数时按 0 处理，超过 maxRetryForDelay 时钳制以避免浮点溢出
func TaskRetryDelay(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}
	// 钳制上限，防止 math.Pow(2, float64(retryCount)) 在大 retryCount 时浮点溢出
	const maxRetryForDelay = 20
	if retryCount > maxRetryForDelay {
		retryCount = maxRetryForDelay
	}
	base := 30 * time.Second
	delay := base << uint(retryCount)
	return delay
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
