package model

import "testing"

func TestTaskType_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		tt    TaskType
		valid bool
	}{
		{"review_pr 有效", TaskTypeReviewPR, true},
		{"fix_issue 有效", TaskTypeFixIssue, true},
		{"gen_tests 有效", TaskTypeGenTests, true},
		{"空字符串无效", TaskType(""), false},
		{"未知类型无效", TaskType("unknown_type"), false},
		{"大小写敏感", TaskType("Review_PR"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.tt.IsValid()
			if got != tc.valid {
				t.Errorf("TaskType(%q).IsValid() = %v, 期望 %v", tc.tt, got, tc.valid)
			}
		})
	}
}

func TestTaskStatus_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		st    TaskStatus
		valid bool
	}{
		{"pending 有效", TaskStatusPending, true},
		{"queued 有效", TaskStatusQueued, true},
		{"running 有效", TaskStatusRunning, true},
		{"succeeded 有效", TaskStatusSucceeded, true},
		{"failed 有效", TaskStatusFailed, true},
		{"retrying 有效", TaskStatusRetrying, true},
		{"cancelled 有效", TaskStatusCancelled, true},
		{"空字符串无效", TaskStatus(""), false},
		{"未知状态无效", TaskStatus("unknown_status"), false},
		{"大小写敏感", TaskStatus("Pending"), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.st.IsValid()
			if got != tc.valid {
				t.Errorf("TaskStatus(%q).IsValid() = %v, 期望 %v", tc.st, got, tc.valid)
			}
		})
	}
}

func TestTaskPriority_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		p     TaskPriority
		valid bool
	}{
		{"PriorityCritical 有效", PriorityCritical, true},
		{"PriorityHigh 有效", PriorityHigh, true},
		{"PriorityNormal 有效", PriorityNormal, true},
		{"PriorityLow 有效", PriorityLow, true},
		{"零值无效", TaskPriority(0), false},
		{"负数无效", TaskPriority(-1), false},
		{"不在枚举中的正数无效", TaskPriority(2), false},
		{"超大值无效", TaskPriority(100), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.p.IsValid()
			if got != tc.valid {
				t.Errorf("TaskPriority(%d).IsValid() = %v, 期望 %v", tc.p, got, tc.valid)
			}
		})
	}
}
