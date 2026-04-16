package queue

import (
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// TestConstants 验证 asynq 任务类型常量和队列名称常量定义正确
func TestConstants(t *testing.T) {
	// 任务类型常量
	if AsynqTypeReviewPR != "dtworkflow:review_pr" {
		t.Errorf("AsynqTypeReviewPR = %q, want %q", AsynqTypeReviewPR, "dtworkflow:review_pr")
	}
	if AsynqTypeFixIssue != "dtworkflow:fix_issue" {
		t.Errorf("AsynqTypeFixIssue = %q, want %q", AsynqTypeFixIssue, "dtworkflow:fix_issue")
	}
	if AsynqTypeGenTests != "dtworkflow:gen_tests" {
		t.Errorf("AsynqTypeGenTests = %q, want %q", AsynqTypeGenTests, "dtworkflow:gen_tests")
	}

	// 队列名称常量
	if QueueCritical != "critical" {
		t.Errorf("QueueCritical = %q, want %q", QueueCritical, "critical")
	}
	if QueueDefault != "default" {
		t.Errorf("QueueDefault = %q, want %q", QueueDefault, "default")
	}
	if QueueLow != "low" {
		t.Errorf("QueueLow = %q, want %q", QueueLow, "low")
	}
}

func TestTaskTypeToAsynq_AnalyzeIssue(t *testing.T) {
	got := taskTypeToAsynq(model.TaskTypeAnalyzeIssue)
	if got != AsynqTypeAnalyzeIssue {
		t.Errorf("taskTypeToAsynq(AnalyzeIssue) = %q, 期望 %q", got, AsynqTypeAnalyzeIssue)
	}
}

// TestTaskTypeToAsynq 验证 model.TaskType 到 asynq 任务类型的映射
func TestTaskTypeToAsynq(t *testing.T) {
	tests := []struct {
		input    model.TaskType
		expected string
	}{
		{model.TaskTypeReviewPR, AsynqTypeReviewPR},
		{model.TaskTypeFixIssue, AsynqTypeFixIssue},
		{model.TaskTypeGenTests, AsynqTypeGenTests},
		{model.TaskTypeGenDailyReport, AsynqTypeGenDailyReport},
		{"unknown_type", "unknown_type"}, // 未知类型原样返回
	}
	for _, tt := range tests {
		got := taskTypeToAsynq(tt.input)
		if got != tt.expected {
			t.Errorf("taskTypeToAsynq(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}
