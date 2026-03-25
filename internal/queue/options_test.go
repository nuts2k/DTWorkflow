package queue

import (
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func TestTaskTimeout(t *testing.T) {
	tests := []struct {
		taskType model.TaskType
		expected time.Duration
	}{
		{model.TaskTypeReviewPR, 10 * time.Minute},
		{model.TaskTypeFixIssue, 30 * time.Minute},
		{model.TaskTypeGenTests, 20 * time.Minute},
		{"unknown", 10 * time.Minute}, // 未知类型返回默认值
	}
	for _, tt := range tests {
		got := TaskTimeout(tt.taskType)
		if got != tt.expected {
			t.Errorf("TaskTimeout(%q) = %v, want %v", tt.taskType, got, tt.expected)
		}
	}
}

func TestTaskMaxRetry(t *testing.T) {
	if got := TaskMaxRetry(); got != 3 {
		t.Errorf("TaskMaxRetry() = %d, want 3", got)
	}
}

func TestTaskRetryDelay(t *testing.T) {
	// base=30s, factor=2: 第0次=30s, 第1次=60s, 第2次=120s
	// 负数 retryCount 按 0 处理，返回 base 值 30s
	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{-1, 30 * time.Second},  // 负数防御
		{-10, 30 * time.Second}, // 负数防御
		{0, 30 * time.Second},
		{1, 60 * time.Second},
		{2, 120 * time.Second},
	}
	for _, tt := range tests {
		got := TaskRetryDelay(tt.retryCount)
		if got != tt.expected {
			t.Errorf("TaskRetryDelay(%d) = %v, want %v", tt.retryCount, got, tt.expected)
		}
	}
}

func TestTaskRetryDelay_LargeRetryCount(t *testing.T) {
	// retryCount > 20 应被钳制，不应溢出
	d := TaskRetryDelay(100)
	expected := 30 * time.Second << 20 // 钳制到 20
	if d != expected {
		t.Errorf("TaskRetryDelay(100) = %v, want %v", d, expected)
	}

	// 验证 retryCount=20 的精确值
	d20 := TaskRetryDelay(20)
	if d != d20 {
		t.Errorf("TaskRetryDelay(100) = %v 应等于 TaskRetryDelay(20) = %v", d, d20)
	}
}

func TestBuildAsynqOptions_WithTaskID(t *testing.T) {
	opts := buildAsynqOptions(model.TaskTypeReviewPR, EnqueueOptions{
		Priority: model.PriorityHigh,
		TaskID:   "my-task-id",
	})
	// 基本 3 个选项 + TaskID = 4
	if len(opts) != 4 {
		t.Errorf("buildAsynqOptions 带 TaskID 应返回 4 个选项，得到 %d", len(opts))
	}
}

func TestBuildAsynqOptions_WithoutTaskID(t *testing.T) {
	opts := buildAsynqOptions(model.TaskTypeFixIssue, EnqueueOptions{
		Priority: model.PriorityNormal,
	})
	// 无 TaskID：Queue + MaxRetry + Timeout = 3
	if len(opts) != 3 {
		t.Errorf("buildAsynqOptions 无 TaskID 应返回 3 个选项，得到 %d", len(opts))
	}
}

func TestPriorityToQueue(t *testing.T) {
	tests := []struct {
		priority model.TaskPriority
		expected string
	}{
		{model.PriorityCritical, QueueCritical},
		{model.PriorityHigh, QueueDefault},
		{model.PriorityNormal, QueueDefault},
		{model.PriorityLow, QueueLow},
		{99, QueueDefault}, // 未知优先级返回默认队列
	}
	for _, tt := range tests {
		got := PriorityToQueue(tt.priority)
		if got != tt.expected {
			t.Errorf("PriorityToQueue(%d) = %q, want %q", tt.priority, got, tt.expected)
		}
	}
}
