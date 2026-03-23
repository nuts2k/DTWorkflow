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
