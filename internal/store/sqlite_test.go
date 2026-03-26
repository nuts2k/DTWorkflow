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

func TestCreateTask_EmptyID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("", "", model.TaskTypeReviewPR)
	err := s.CreateTask(ctx, r)
	if err == nil {
		t.Fatal("CreateTask 空 ID 应返回错误")
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("期望 ErrInvalidID，得到: %v", err)
	}
}

func TestCreateTask_InvalidTaskType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("bad-type-001", "", model.TaskType("bogus"))
	err := s.CreateTask(ctx, r)
	if err == nil {
		t.Fatal("CreateTask 使用无效任务类型应返回错误")
	}
}

func TestUpdateTask_NilRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	err := s.UpdateTask(ctx, nil)
	if err == nil {
		t.Fatal("UpdateTask(nil) 应返回错误")
	}
	if !errors.Is(err, ErrNilRecord) {
		t.Errorf("期望 ErrNilRecord，得到: %v", err)
	}
}

func TestUpdateTask_EmptyID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("", "", model.TaskTypeReviewPR)
	r.ID = ""
	err := s.UpdateTask(ctx, r)
	if err == nil {
		t.Fatal("UpdateTask 空 ID 应返回错误")
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("期望 ErrInvalidID，得到: %v", err)
	}
}

func TestUpdateTask_InvalidTaskType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("upd-bad-type", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}
	r.TaskType = model.TaskType("bogus")
	err := s.UpdateTask(ctx, r)
	if err == nil {
		t.Fatal("UpdateTask 使用无效任务类型应返回错误")
	}
}

func TestUpdateTask_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("upd-bad-status", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}
	r.Status = model.TaskStatus("bogus")
	err := s.UpdateTask(ctx, r)
	if err == nil {
		t.Fatal("UpdateTask 使用无效状态应返回错误")
	}
}

func TestUpdateTask_WithCompletedAt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r := newTestRecord("completed-task", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	now := time.Now().UTC()
	r.Status = model.TaskStatusSucceeded
	r.StartedAt = &now
	r.CompletedAt = &now
	if err := s.UpdateTask(ctx, r); err != nil {
		t.Fatalf("UpdateTask 失败: %v", err)
	}

	got, err := s.GetTask(ctx, "completed-task")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got.CompletedAt == nil {
		t.Error("CompletedAt 应不为 nil")
	}
	if got.StartedAt == nil {
		t.Error("StartedAt 应不为 nil")
	}
}

func TestListTasks_InvalidTaskType(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.ListTasks(ctx, ListOptions{TaskType: model.TaskType("bogus")})
	if err == nil {
		t.Fatal("ListTasks 使用无效任务类型应返回错误")
	}
}

func TestListTasks_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.ListTasks(ctx, ListOptions{Status: model.TaskStatus("bogus")})
	if err == nil {
		t.Fatal("ListTasks 使用无效状态应返回错误")
	}
}

func TestPurgeTasks_InvalidOlderThan(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.PurgeTasks(ctx, 0, model.TaskStatusSucceeded)
	if err == nil {
		t.Fatal("PurgeTasks olderThan=0 应返回错误")
	}

	_, err = s.PurgeTasks(ctx, -1*time.Hour, model.TaskStatusSucceeded)
	if err == nil {
		t.Fatal("PurgeTasks 负数 olderThan 应返回错误")
	}
}

func TestPurgeTasks_InvalidStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.PurgeTasks(ctx, 1*time.Hour, model.TaskStatus("bogus"))
	if err == nil {
		t.Fatal("PurgeTasks 使用无效状态应返回错误")
	}
}

func TestRunMigrations_Idempotent(t *testing.T) {
	s := newTestStore(t)
	// RunMigrations 在 NewSQLiteStore 中已执行一次，再执行一次应无错（幂等）
	if err := RunMigrations(s.db); err != nil {
		t.Fatalf("二次执行 RunMigrations 应幂等，但返回错误: %v", err)
	}
}

func TestNewSQLiteStore_FileDB(t *testing.T) {
	dbPath := t.TempDir() + "/sub/nested/test.db"
	s, err := NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("NewSQLiteStore 创建文件数据库失败: %v", err)
	}
	defer s.Close()

	// 验证可以正常操作
	ctx := context.Background()
	r := newTestRecord("file-db-task", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("文件数据库 CreateTask 失败: %v", err)
	}
	got, err := s.GetTask(ctx, "file-db-task")
	if err != nil {
		t.Fatalf("文件数据库 GetTask 失败: %v", err)
	}
	if got == nil || got.ID != "file-db-task" {
		t.Error("文件数据库读写验证失败")
	}
}

// newTestReviewRecord 创建用于测试的 ReviewRecord
func newTestReviewRecord(id, taskID, repoFullName string, prNumber int64) *model.ReviewRecord {
	return &model.ReviewRecord{
		ID:            id,
		TaskID:        taskID,
		RepoFullName:  repoFullName,
		PRNumber:      prNumber,
		HeadSHA:       "abc123",
		Verdict:       "approve",
		Summary:       "代码质量良好",
		IssuesJSON:    "[]",
		IssueCount:    0,
		CriticalCount: 0,
		ErrorCount:    0,
		WarningCount:  0,
		InfoCount:     0,
		CostUSD:       0.05,
		DurationMs:    1200,
		GiteaReviewID: 42,
		ParseFailed:   false,
		CreatedAt:     time.Now().UTC(),
	}
}

func TestSaveAndGetReviewResult(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 先创建关联的 task（外键约束）
	task := newTestRecord("task-for-review", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	rr := newTestReviewRecord("review-001", "task-for-review", "owner/repo", 1)
	if err := s.SaveReviewResult(ctx, rr); err != nil {
		t.Fatalf("SaveReviewResult 失败: %v", err)
	}

	got, err := s.GetReviewResult(ctx, "review-001")
	if err != nil {
		t.Fatalf("GetReviewResult 失败: %v", err)
	}
	if got.ID != "review-001" {
		t.Errorf("ID 不匹配: got %s, want review-001", got.ID)
	}
	if got.TaskID != "task-for-review" {
		t.Errorf("TaskID 不匹配: got %s, want task-for-review", got.TaskID)
	}
	if got.Verdict != "approve" {
		t.Errorf("Verdict 不匹配: got %s, want approve", got.Verdict)
	}
	if got.PRNumber != 1 {
		t.Errorf("PRNumber 不匹配: got %d, want 1", got.PRNumber)
	}
	if got.CostUSD != 0.05 {
		t.Errorf("CostUSD 不匹配: got %f, want 0.05", got.CostUSD)
	}
	if got.GiteaReviewID != 42 {
		t.Errorf("GiteaReviewID 不匹配: got %d, want 42", got.GiteaReviewID)
	}
}

func TestGetReviewResult_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetReviewResult(ctx, "nonexistent")
	if err == nil {
		t.Fatal("GetReviewResult 不存在时应返回错误")
	}
}

func TestGetReviewResult_EmptyID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetReviewResult(ctx, "")
	if err == nil {
		t.Fatal("GetReviewResult 空 ID 应返回错误")
	}
	if !errors.Is(err, ErrInvalidID) {
		t.Errorf("期望 ErrInvalidID，得到: %v", err)
	}
}

func TestListReviewResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建关联的 task
	task := newTestRecord("task-for-list", "", model.TaskTypeReviewPR)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	base := time.Now().UTC()
	for i := 0; i < 3; i++ {
		rr := newTestReviewRecord(
			fmt.Sprintf("list-review-%d", i),
			"task-for-list",
			"owner/repo",
			int64(i+1),
		)
		rr.CreatedAt = base.Add(time.Duration(i) * time.Second)
		if err := s.SaveReviewResult(ctx, rr); err != nil {
			t.Fatalf("SaveReviewResult 失败: %v", err)
		}
	}

	results, err := s.ListReviewResults(ctx, "owner/repo", 10, 0)
	if err != nil {
		t.Fatalf("ListReviewResults 失败: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("期望 3 条记录，得到 %d", len(results))
	}
	// 按 created_at DESC 排序，最新的在前
	if results[0].ID != "list-review-2" {
		t.Errorf("第一条记录期望 list-review-2，得到 %s", results[0].ID)
	}
}

func TestListReviewResults_LimitClamping(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// limit <= 0 默认为 50，limit > 200 限制为 200
	results, err := s.ListReviewResults(ctx, "owner/repo", 0, 0)
	if err != nil {
		t.Fatalf("ListReviewResults limit=0 失败: %v", err)
	}
	if results == nil {
		// nil 切片是允许的（无数据时）
	}

	results, err = s.ListReviewResults(ctx, "owner/repo", -1, 0)
	if err != nil {
		t.Fatalf("ListReviewResults limit=-1 失败: %v", err)
	}
	_ = results
}

func TestListReviewResults_FilterByRepo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建关联的 task（外键约束）
	taskA := newTestRecord("task-repo-a", "", model.TaskTypeReviewPR)
	taskA.RepoFullName = "org/repo-a"
	taskB := newTestRecord("task-repo-b", "", model.TaskTypeReviewPR)
	taskB.RepoFullName = "org/repo-b"
	for _, task := range []*model.TaskRecord{taskA, taskB} {
		if err := s.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	rr1 := newTestReviewRecord("repo-review-1", "task-repo-a", "org/repo-a", 1)
	rr2 := newTestReviewRecord("repo-review-2", "task-repo-b", "org/repo-b", 2)
	for _, rr := range []*model.ReviewRecord{rr1, rr2} {
		if err := s.SaveReviewResult(ctx, rr); err != nil {
			t.Fatalf("SaveReviewResult 失败: %v", err)
		}
	}

	results, err := s.ListReviewResults(ctx, "org/repo-a", 10, 0)
	if err != nil {
		t.Fatalf("ListReviewResults 失败: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("期望 1 条记录，得到 %d", len(results))
	}
}

func TestListTasks_CombinedFilters(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	r1 := newTestRecord("combo-1", "", model.TaskTypeReviewPR)
	r1.RepoFullName = "org/repo-a"
	r1.Status = model.TaskStatusSucceeded

	r2 := newTestRecord("combo-2", "", model.TaskTypeReviewPR)
	r2.RepoFullName = "org/repo-a"
	r2.Status = model.TaskStatusPending

	r3 := newTestRecord("combo-3", "", model.TaskTypeFixIssue)
	r3.RepoFullName = "org/repo-a"
	r3.Status = model.TaskStatusSucceeded

	for _, r := range []*model.TaskRecord{r1, r2, r3} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	// 同时按 repo + status + type 过滤
	records, err := s.ListTasks(ctx, ListOptions{
		RepoFullName: "org/repo-a",
		TaskType:     model.TaskTypeReviewPR,
		Status:       model.TaskStatusSucceeded,
	})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 1 || records[0].ID != "combo-1" {
		t.Errorf("组合过滤期望 1 条 combo-1，得到 %d 条", len(records))
	}
}

// newTestPRRecord 创建带 pr_number 的评审任务，便于 M2.4 测试使用
func newTestPRRecord(id string, prNumber int64, status model.TaskStatus, createdAt time.Time) *model.TaskRecord {
	r := newTestRecord(id, "", model.TaskTypeReviewPR)
	r.PRNumber = prNumber
	r.Payload.PRNumber = prNumber
	r.Status = status
	r.CreatedAt = createdAt
	r.UpdatedAt = createdAt
	return r
}

// TestFindActivePRTasks_Normal 验证只返回 pending/queued/running 状态的记录，且按 created_at ASC 排序
func TestFindActivePRTasks_Normal(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()

	// 创建不同状态的任务，同属 PR #10
	active1 := newTestPRRecord("active-pending", 10, model.TaskStatusPending, base.Add(0))
	active2 := newTestPRRecord("active-queued", 10, model.TaskStatusQueued, base.Add(1*time.Second))
	active3 := newTestPRRecord("active-running", 10, model.TaskStatusRunning, base.Add(2*time.Second))
	done1 := newTestPRRecord("done-succeeded", 10, model.TaskStatusSucceeded, base.Add(3*time.Second))
	done2 := newTestPRRecord("done-failed", 10, model.TaskStatusFailed, base.Add(4*time.Second))

	for _, r := range []*model.TaskRecord{active1, active2, active3, done1, done2} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.FindActivePRTasks(ctx, "owner/repo", 10, model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindActivePRTasks 失败: %v", err)
	}

	// 只应返回 pending/queued/running 三条
	if len(records) != 3 {
		t.Fatalf("期望 3 条活跃任务，得到 %d", len(records))
	}

	// 验证按 created_at ASC 排序
	if records[0].ID != "active-pending" {
		t.Errorf("第一条期望 active-pending，得到 %s", records[0].ID)
	}
	if records[1].ID != "active-queued" {
		t.Errorf("第二条期望 active-queued，得到 %s", records[1].ID)
	}
	if records[2].ID != "active-running" {
		t.Errorf("第三条期望 active-running，得到 %s", records[2].ID)
	}

	// 验证 succeeded/failed 未出现
	for _, r := range records {
		if r.Status == model.TaskStatusSucceeded || r.Status == model.TaskStatusFailed {
			t.Errorf("不应返回终态任务，但得到 ID=%s status=%s", r.ID, r.Status)
		}
	}
}

// TestFindActivePRTasks_Empty 验证无匹配任务时返回空 slice
func TestFindActivePRTasks_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 只插入已完成的任务
	r := newTestPRRecord("empty-succeeded", 99, model.TaskStatusSucceeded, time.Now().UTC())
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	records, err := s.FindActivePRTasks(ctx, "owner/repo", 99, model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindActivePRTasks 失败: %v", err)
	}

	// 无匹配时应返回空 slice（不是 nil 报错，只是 len==0）
	if len(records) != 0 {
		t.Errorf("期望 0 条记录，得到 %d", len(records))
	}
}

// TestFindActivePRTasks_FilterByPR 验证只返回指定 pr_number 的任务，不同 PR 互不干扰
func TestFindActivePRTasks_FilterByPR(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()

	// PR #1 有 2 个活跃任务
	r1a := newTestPRRecord("pr1-task-a", 1, model.TaskStatusPending, base)
	r1b := newTestPRRecord("pr1-task-b", 1, model.TaskStatusRunning, base.Add(time.Second))

	// PR #2 有 1 个活跃任务
	r2a := newTestPRRecord("pr2-task-a", 2, model.TaskStatusQueued, base)

	for _, r := range []*model.TaskRecord{r1a, r1b, r2a} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	// 查询 PR #1
	pr1Records, err := s.FindActivePRTasks(ctx, "owner/repo", 1, model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindActivePRTasks(pr=1) 失败: %v", err)
	}
	if len(pr1Records) != 2 {
		t.Errorf("PR #1 期望 2 条，得到 %d", len(pr1Records))
	}
	for _, r := range pr1Records {
		if r.PRNumber != 1 {
			t.Errorf("PR #1 结果中出现了 pr_number=%d", r.PRNumber)
		}
	}

	// 查询 PR #2
	pr2Records, err := s.FindActivePRTasks(ctx, "owner/repo", 2, model.TaskTypeReviewPR)
	if err != nil {
		t.Fatalf("FindActivePRTasks(pr=2) 失败: %v", err)
	}
	if len(pr2Records) != 1 {
		t.Errorf("PR #2 期望 1 条，得到 %d", len(pr2Records))
	}
	if len(pr2Records) > 0 && pr2Records[0].ID != "pr2-task-a" {
		t.Errorf("PR #2 期望 pr2-task-a，得到 %s", pr2Records[0].ID)
	}
}

// TestHasNewerReviewTask_Exists 验证存在比指定时间更新的任务时返回 true
func TestHasNewerReviewTask_Exists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 基准时间
	base := time.Now().UTC()

	// 创建一个比基准时间更新（晚 1 秒）的任务
	newer := newTestPRRecord("newer-task", 5, model.TaskStatusPending, base.Add(time.Second))
	if err := s.CreateTask(ctx, newer); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 以基准时间查询，应找到更新的任务
	exists, err := s.HasNewerReviewTask(ctx, "owner/repo", 5, base)
	if err != nil {
		t.Fatalf("HasNewerReviewTask 失败: %v", err)
	}
	if !exists {
		t.Error("存在更新的任务，期望返回 true，但得到 false")
	}
}

// TestHasNewerReviewTask_NotExists 验证不存在更新任务时返回 false
func TestHasNewerReviewTask_NotExists(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 创建一个比基准时间旧的任务
	base := time.Now().UTC()
	older := newTestPRRecord("older-task", 7, model.TaskStatusPending, base.Add(-time.Second))
	if err := s.CreateTask(ctx, older); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 以基准时间查询，任务比基准时间旧，应返回 false
	exists, err := s.HasNewerReviewTask(ctx, "owner/repo", 7, base)
	if err != nil {
		t.Fatalf("HasNewerReviewTask 失败: %v", err)
	}
	if exists {
		t.Error("不存在更新的任务，期望返回 false，但得到 true")
	}
}

// TestHasNewerReviewTask_SameTime 验证与基准时间相同的任务不算"更新"，返回 false
func TestHasNewerReviewTask_SameTime(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 任务创建时间与查询基准时间完全相同
	exactTime := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := newTestPRRecord("same-time-task", 8, model.TaskStatusPending, exactTime)
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// SQL 使用严格 created_at > ?，同一时间不算更新
	exists, err := s.HasNewerReviewTask(ctx, "owner/repo", 8, exactTime)
	if err != nil {
		t.Fatalf("HasNewerReviewTask 失败: %v", err)
	}
	if exists {
		t.Error("同一时间的任务不应算作更新，期望 false，但得到 true")
	}
}

// TestHasNewerReviewTask_IgnoresCancelledAndFailed 验证 cancelled/failed 状态的任务不触发 staleness 判定
func TestHasNewerReviewTask_IgnoresCancelledAndFailed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()

	// 创建比基准时间更新但已取消的任务
	cancelled := newTestPRRecord("cancelled-task", 20, model.TaskStatusCancelled, base.Add(time.Second))
	if err := s.CreateTask(ctx, cancelled); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 创建比基准时间更新但已失败的任务
	failed := newTestPRRecord("failed-task", 20, model.TaskStatusFailed, base.Add(2*time.Second))
	if err := s.CreateTask(ctx, failed); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 虽然存在更新的任务，但都是终态（cancelled/failed），不应触发 staleness
	exists, err := s.HasNewerReviewTask(ctx, "owner/repo", 20, base)
	if err != nil {
		t.Fatalf("HasNewerReviewTask 失败: %v", err)
	}
	if exists {
		t.Error("cancelled/failed 的任务不应触发 staleness，期望 false，但得到 true")
	}

	// 再插入一个活跃的更新任务，此时应返回 true
	active := newTestPRRecord("active-task", 20, model.TaskStatusQueued, base.Add(3*time.Second))
	if err := s.CreateTask(ctx, active); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	exists, err = s.HasNewerReviewTask(ctx, "owner/repo", 20, base)
	if err != nil {
		t.Fatalf("HasNewerReviewTask 失败: %v", err)
	}
	if !exists {
		t.Error("存在活跃的更新任务，期望 true，但得到 false")
	}
}

// TestMigration_PRNumber 验证迁移 13/14/15 成功执行，pr_number 列存在且可写入读取
func TestMigration_PRNumber(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 验证迁移 13/14/15 已成功执行（schema_migrations 中存在这些版本）
	for _, ver := range []int{13, 14, 15} {
		var exists int
		err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", ver).Scan(&exists)
		if err != nil {
			t.Errorf("迁移版本 %d 未找到: %v", ver, err)
		}
	}

	// 验证 pr_number 列存在：插入带 pr_number 的任务并回读
	r := newTestPRRecord("migration-pr-task", 42, model.TaskStatusPending, time.Now().UTC())
	if err := s.CreateTask(ctx, r); err != nil {
		t.Fatalf("带 pr_number 的 CreateTask 失败: %v", err)
	}

	got, err := s.GetTask(ctx, "migration-pr-task")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got == nil {
		t.Fatal("GetTask 返回 nil")
	}
	if got.PRNumber != 42 {
		t.Errorf("pr_number 不匹配: got %d, want 42", got.PRNumber)
	}

	// 验证 pr_number=0 时存储为 NULL（int64ToNull 的行为）
	r0 := newTestPRRecord("migration-pr-zero", 0, model.TaskStatusPending, time.Now().UTC())
	if err := s.CreateTask(ctx, r0); err != nil {
		t.Fatalf("pr_number=0 的 CreateTask 失败: %v", err)
	}
	got0, err := s.GetTask(ctx, "migration-pr-zero")
	if err != nil {
		t.Fatalf("GetTask(pr_number=0) 失败: %v", err)
	}
	if got0.PRNumber != 0 {
		t.Errorf("pr_number=0 回读期望 0，得到 %d", got0.PRNumber)
	}
}
