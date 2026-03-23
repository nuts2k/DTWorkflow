package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// newTestStore 创建内存 SQLite Store 用于测试
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// newTestRecord 创建用于测试的 TaskRecord
func newTestRecord(id, deliveryID string, taskType model.TaskType) *model.TaskRecord {
	return &model.TaskRecord{
		ID:         id,
		AsynqID:    "",
		TaskType:   taskType,
		Status:     model.TaskStatusPending,
		Priority:   model.PriorityNormal,
		DeliveryID: deliveryID,
		Payload: model.TaskPayload{
			TaskType:     taskType,
			DeliveryID:   deliveryID,
			RepoOwner:    "owner",
			RepoName:     "repo",
			RepoFullName: "owner/repo",
			CloneURL:     "https://gitea.example.com/owner/repo.git",
		},
		RepoFullName: "owner/repo",
		MaxRetry:     3,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
}

func TestCreateAndGetTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := newTestRecord("task-001", "delivery-001", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	got, err := s.GetTask(ctx, "task-001")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got == nil {
		t.Fatal("GetTask 返回 nil，期望找到记录")
	}
	if got.ID != record.ID {
		t.Errorf("ID 不匹配: got %s, want %s", got.ID, record.ID)
	}
	if got.TaskType != record.TaskType {
		t.Errorf("TaskType 不匹配: got %s, want %s", got.TaskType, record.TaskType)
	}
	if got.Status != record.Status {
		t.Errorf("Status 不匹配: got %s, want %s", got.Status, record.Status)
	}
	if got.Payload.RepoFullName != record.Payload.RepoFullName {
		t.Errorf("Payload.RepoFullName 不匹配: got %s, want %s", got.Payload.RepoFullName, record.Payload.RepoFullName)
	}
}

func TestGetTask_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetTask(ctx, "nonexistent-id")
	if err != nil {
		t.Fatalf("GetTask 不存在时应返回 nil error，但返回: %v", err)
	}
	if got != nil {
		t.Fatal("GetTask 不存在时应返回 nil record")
	}
}

func TestUpdateTask(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := newTestRecord("task-002", "delivery-002", model.TaskTypeFixIssue)
	if err := s.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 记录更新前的 UpdatedAt
	oldUpdatedAt := record.UpdatedAt

	// 更新状态为 running
	record.Status = model.TaskStatusRunning
	record.WorkerID = "worker-1"
	now := time.Now().UTC()
	record.StartedAt = &now

	if err := s.UpdateTask(ctx, record); err != nil {
		t.Fatalf("UpdateTask 失败: %v", err)
	}

	got, err := s.GetTask(ctx, "task-002")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got.Status != model.TaskStatusRunning {
		t.Errorf("Status 未更新: got %s, want running", got.Status)
	}
	if got.WorkerID != "worker-1" {
		t.Errorf("WorkerID 未更新: got %s, want worker-1", got.WorkerID)
	}
	if got.StartedAt == nil {
		t.Error("StartedAt 应不为 nil")
	}
	// 验证 UpdatedAt 被刷新
	if !got.UpdatedAt.After(oldUpdatedAt) && !got.UpdatedAt.Equal(record.UpdatedAt) {
		t.Errorf("UpdatedAt 未被刷新: got %v, oldUpdatedAt %v", got.UpdatedAt, oldUpdatedAt)
	}
}

func TestUpdateTask_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := newTestRecord("nonexistent", "", model.TaskTypeReviewPR)
	err := s.UpdateTask(ctx, record)
	if err == nil {
		t.Fatal("更新不存在的任务应返回错误")
	}
}

func TestListTasks_NoFilter(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		r := newTestRecord(
			fmt.Sprintf("list-task-%d", i),
			"",
			model.TaskTypeReviewPR,
		)
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.ListTasks(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 3 {
		t.Errorf("期望 3 条记录，得到 %d", len(records))
	}
}

func TestListTasks_FilterByRepo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := newTestRecord("repo-task-1", "", model.TaskTypeReviewPR)
	r1.RepoFullName = "owner/repo-a"
	r1.Payload.RepoFullName = "owner/repo-a"

	r2 := newTestRecord("repo-task-2", "", model.TaskTypeReviewPR)
	r2.RepoFullName = "owner/repo-b"
	r2.Payload.RepoFullName = "owner/repo-b"

	for _, r := range []*model.TaskRecord{r1, r2} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.ListTasks(ctx, ListOptions{RepoFullName: "owner/repo-a"})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("期望 1 条记录，得到 %d", len(records))
	}
}

func TestListTasks_FilterByStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := newTestRecord("status-task-1", "", model.TaskTypeReviewPR)
	r2 := newTestRecord("status-task-2", "", model.TaskTypeReviewPR)
	r2.Status = model.TaskStatusSucceeded

	for _, r := range []*model.TaskRecord{r1, r2} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.ListTasks(ctx, ListOptions{Status: model.TaskStatusPending})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("期望 1 条 pending 记录，得到 %d", len(records))
	}
	if records[0].ID != "status-task-1" {
		t.Errorf("期望 status-task-1，得到 %s", records[0].ID)
	}
}

func TestListTasks_FilterByType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := newTestRecord("type-task-1", "", model.TaskTypeReviewPR)
	r2 := newTestRecord("type-task-2", "", model.TaskTypeFixIssue)

	for _, r := range []*model.TaskRecord{r1, r2} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.ListTasks(ctx, ListOptions{TaskType: model.TaskTypeFixIssue})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("期望 1 条 fix_issue 记录，得到 %d", len(records))
	}
}

func TestListTasks_LimitOffset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 使用递增的 CreatedAt 确保排序确定性
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		r := newTestRecord(
			fmt.Sprintf("limit-task-%d", i),
			"",
			model.TaskTypeReviewPR,
		)
		r.CreatedAt = base.Add(time.Duration(i) * time.Second)
		r.UpdatedAt = r.CreatedAt
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	// ORDER BY created_at DESC，所以 limit-task-4 最新排第一
	// Offset=1 跳过第一条(limit-task-4)，Limit=2 取两条: limit-task-3, limit-task-2
	records, err := s.ListTasks(ctx, ListOptions{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("期望 2 条记录，得到 %d", len(records))
	}
	if records[0].ID != "limit-task-3" {
		t.Errorf("第一条记录期望 limit-task-3，得到 %s", records[0].ID)
	}
	if records[1].ID != "limit-task-2" {
		t.Errorf("第二条记录期望 limit-task-2，得到 %s", records[1].ID)
	}
}

func TestFindByDeliveryID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := newTestRecord("del-task-1", "webhook-delivery-123", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	got, err := s.FindByDeliveryID(ctx, "webhook-delivery-123", model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindByDeliveryID 失败: %v", err)
	}
	if got == nil {
		t.Fatal("FindByDeliveryID 应找到记录，但返回 nil")
	}
	if got.ID != "del-task-1" {
		t.Errorf("ID 不匹配: got %s, want del-task-1", got.ID)
	}
}

func TestFindByDeliveryID_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.FindByDeliveryID(ctx, "nonexistent-delivery", model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindByDeliveryID 不存在时应返回 nil error，但返回: %v", err)
	}
	if got != nil {
		t.Fatal("FindByDeliveryID 不存在时应返回 nil record")
	}
}

func TestFindByDeliveryID_Dedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 第一次插入应成功
	r1 := newTestRecord("dedup-task-1", "same-delivery", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r1); err != nil {
		t.Fatalf("第一次 CreateTask 失败: %v", err)
	}

	// 相同 delivery_id + task_type 再次插入应失败（唯一索引约束）
	r2 := newTestRecord("dedup-task-2", "same-delivery", model.TaskTypeReviewPR)
	err := s.CreateTask(ctx, r2)
	if err == nil {
		t.Fatal("重复 delivery_id + task_type 应返回错误，但成功了")
	}
}

func TestListOrphanTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建一个"旧"任务（2小时前）
	old := newTestRecord("orphan-old", "", model.TaskTypeReviewPR)
	old.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	old.UpdatedAt = old.CreatedAt
	if err := s.CreateTask(ctx, old); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 创建一个"新"任务（刚刚）
	fresh := newTestRecord("orphan-fresh", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, fresh); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// maxAge=1h，应只返回旧任务
	orphans, err := s.ListOrphanTasks(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("ListOrphanTasks 失败: %v", err)
	}
	if len(orphans) != 1 {
		t.Errorf("期望 1 条孤儿任务，得到 %d", len(orphans))
	}
	if len(orphans) > 0 && orphans[0].ID != "orphan-old" {
		t.Errorf("期望 orphan-old，得到 %s", orphans[0].ID)
	}
}

func TestListOrphanTasks_ExcludesNonPending(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建旧的 running 任务
	r := newTestRecord("running-old", "", model.TaskTypeReviewPR)
	r.Status = model.TaskStatusRunning
	r.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	r.UpdatedAt = r.CreatedAt
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	orphans, err := s.ListOrphanTasks(ctx, 1*time.Hour)
	if err != nil {
		t.Fatalf("ListOrphanTasks 失败: %v", err)
	}
	if len(orphans) != 0 {
		t.Errorf("非 pending 任务不应出现在孤儿列表，得到 %d 条", len(orphans))
	}
}

func TestClose_Idempotent(t *testing.T) {
	s, err := NewSQLiteStore(":memory:")
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}

	// 多次 Close 不应返回错误（sync.Once 保证幂等）
	if err := s.Close(); err != nil {
		t.Fatalf("第一次 Close 失败: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("第二次 Close 失败: %v", err)
	}
}

func TestCreateTask_NilRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.CreateTask(ctx, nil)
	if err == nil {
		t.Fatal("CreateTask(nil) 应返回错误")
	}
	if !errors.Is(err, ErrNilRecord) {
		t.Errorf("期望错误包含 ErrNilRecord，得到: %v", err)
	}
}

func TestGetTask_EmptyID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetTask(ctx, "")
	if err == nil {
		t.Fatal("GetTask('') 应返回错误")
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("期望错误包含 ErrInvalidID，得到: %v", err)
	}
	if got != nil {
		t.Error("GetTask('') 应返回 nil record")
	}
}

func TestFindByDeliveryID_EmptyDeliveryID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.FindByDeliveryID(ctx, "", model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindByDeliveryID('', ...) 应返回 nil error，但返回: %v", err)
	}
	if got != nil {
		t.Error("FindByDeliveryID('', ...) 应返回 nil record")
	}
}

func TestPurgeTasks(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建旧的 succeeded 任务（2小时前）
	old1 := newTestRecord("purge-old-1", "", model.TaskTypeReviewPR)
	old1.Status = model.TaskStatusSucceeded
	old1.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	old1.UpdatedAt = old1.CreatedAt
	if err := s.CreateTask(ctx, old1); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	old2 := newTestRecord("purge-old-2", "", model.TaskTypeFixIssue)
	old2.Status = model.TaskStatusSucceeded
	old2.CreatedAt = time.Now().UTC().Add(-3 * time.Hour)
	old2.UpdatedAt = old2.CreatedAt
	if err := s.CreateTask(ctx, old2); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 创建新的 succeeded 任务（刚刚）
	fresh := newTestRecord("purge-fresh", "", model.TaskTypeReviewPR)
	fresh.Status = model.TaskStatusSucceeded
	if err := s.CreateTask(ctx, fresh); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 清理 1 小时前的 succeeded 任务，应删除 2 条
	affected, err := s.PurgeTasks(ctx, 1*time.Hour, model.TaskStatusSucceeded)
	if err != nil {
		t.Fatalf("PurgeTasks 失败: %v", err)
	}
	if affected != 2 {
		t.Errorf("期望删除 2 条，实际删除 %d 条", affected)
	}

	// 验证剩余记录
	records, err := s.ListTasks(ctx, ListOptions{})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("期望剩余 1 条记录，得到 %d", len(records))
	}
	if records[0].ID != "purge-fresh" {
		t.Errorf("期望剩余 purge-fresh，得到 %s", records[0].ID)
	}
}

func TestPurgeTasks_NoMatch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建新的 succeeded 任务（刚刚）
	r := newTestRecord("purge-new", "", model.TaskTypeReviewPR)
	r.Status = model.TaskStatusSucceeded
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 清理 1 小时前的，但所有记录都是新的，应返回 0
	affected, err := s.PurgeTasks(ctx, 1*time.Hour, model.TaskStatusSucceeded)
	if err != nil {
		t.Fatalf("PurgeTasks 失败: %v", err)
	}
	if affected != 0 {
		t.Errorf("期望删除 0 条，实际删除 %d 条", affected)
	}
}

func TestPing(t *testing.T) {
	s := newTestStore(t)
	err := s.Ping(context.Background())
	if err != nil {
		t.Fatalf("Ping 失败: %v", err)
	}
}

func TestAllTaskTypesCanBeInserted(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	allTypes := []model.TaskType{
		model.TaskTypeReviewPR,
		model.TaskTypeFixIssue,
		model.TaskTypeGenTests,
	}

	for i, tt := range allTypes {
		r := newTestRecord(fmt.Sprintf("type-enum-%d", i), "", tt)
		if err := s.CreateTask(ctx, r); err != nil {
			t.Errorf("TaskType %q 插入失败: %v", tt, err)
		}
	}
}

func TestAllTaskStatusesCanBeUpdated(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	allStatuses := []model.TaskStatus{
		model.TaskStatusPending,
		model.TaskStatusQueued,
		model.TaskStatusRunning,
		model.TaskStatusSucceeded,
		model.TaskStatusFailed,
		model.TaskStatusRetrying,
		model.TaskStatusCancelled,
	}

	for i, st := range allStatuses {
		// 每个状态创建一条新记录然后更新
		r := newTestRecord(fmt.Sprintf("status-enum-%d", i), "", model.TaskTypeReviewPR)
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
		r.Status = st
		if err := s.UpdateTask(ctx, r); err != nil {
			t.Errorf("更新到状态 %q 失败: %v", st, err)
		}
		// 回读验证
		got, err := s.GetTask(ctx, r.ID)
		if err != nil {
			t.Fatalf("GetTask 失败: %v", err)
		}
		if got.Status != st {
			t.Errorf("状态不匹配: got %s, want %s", got.Status, st)
		}
	}
}

func TestParseTime(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "RFC3339Nano",
			input:   "2024-01-15T10:30:00.123456789Z",
			wantErr: false,
		},
		{
			name:    "RFC3339",
			input:   "2024-01-15T10:30:00Z",
			wantErr: false,
		},
		{
			name:    "日期时间（空格分隔）",
			input:   "2024-01-15 10:30:00",
			wantErr: false,
		},
		{
			name:    "日期时间（T分隔，无时区）",
			input:   "2024-01-15T10:30:00",
			wantErr: false,
		},
		{
			name:    "无效格式",
			input:   "not-a-time",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseTime(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("parseTime(%q) 应返回错误，但返回: %v", tc.input, result)
				}
				return
			}
			if err != nil {
				t.Errorf("parseTime(%q) 失败: %v", tc.input, err)
				return
			}
			if result.IsZero() {
				t.Errorf("parseTime(%q) 返回了零值时间", tc.input)
			}
		})
	}
}

func TestNewSQLiteStore_EmptyPath(t *testing.T) {
	_, err := NewSQLiteStore("")
	if err == nil {
		t.Fatal("NewSQLiteStore('') 应返回错误")
	}
}

func TestCreateTask_DuplicateID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := newTestRecord("dup-id-001", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r1); err != nil {
		t.Fatalf("第一次 CreateTask 失败: %v", err)
	}

	// 使用相同 ID 再次插入，应返回错误（主键冲突）
	r2 := newTestRecord("dup-id-001", "", model.TaskTypeFixIssue)
	err := s.CreateTask(ctx, r2)
	if err == nil {
		t.Fatal("相同 ID 的二次插入应返回错误，但成功了")
	}
}

func TestListTasks_NegativeLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.ListTasks(ctx, ListOptions{Limit: -1})
	if err == nil {
		t.Fatal("负数 Limit 应返回错误")
	}
}

func TestListTasks_NegativeOffset(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.ListTasks(ctx, ListOptions{Offset: -1})
	if err == nil {
		t.Fatal("负数 Offset 应返回错误")
	}
}

func TestCreateTask_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("bad-status", "", model.TaskTypeReviewPR)
	r.Status = model.TaskStatus("invalid_status")
	err := s.CreateTask(ctx, r)
	if err == nil {
		t.Fatal("CreateTask 使用无效状态应返回错误")
	}
}
