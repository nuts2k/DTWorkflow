package store

import (
	"context"
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

	for i := 0; i < 5; i++ {
		r := newTestRecord(
			fmt.Sprintf("limit-task-%d", i),
			"",
			model.TaskTypeReviewPR,
		)
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.ListTasks(ctx, ListOptions{Limit: 2, Offset: 1})
	if err != nil {
		t.Fatalf("ListTasks 失败: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("期望 2 条记录，得到 %d", len(records))
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
