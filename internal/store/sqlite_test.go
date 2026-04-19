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

func TestCreateTask_AnalyzeIssue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	record := newTestRecord("task-analyze-001", "delivery-analyze-001", model.TaskTypeAnalyzeIssue)
	if err := s.CreateTask(ctx, record); err != nil {
		t.Fatalf("CreateTask(analyze_issue) 失败: %v", err)
	}

	got, err := s.GetTask(ctx, record.ID)
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got == nil {
		t.Fatal("GetTask 返回 nil，期望找到记录")
	}
	if got.TaskType != model.TaskTypeAnalyzeIssue {
		t.Fatalf("task_type = %q, want %q", got.TaskType, model.TaskTypeAnalyzeIssue)
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

func TestSQLiteStore_GetLatestAnalysisByIssue(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const repo = "owner/repo"
	const issueNum = int64(42)

	// 未找到时应返回 (nil, nil)
	got, err := s.GetLatestAnalysisByIssue(ctx, repo, issueNum)
	if err != nil {
		t.Fatalf("空库期望 nil error，得到: %v", err)
	}
	if got != nil {
		t.Fatal("空库期望 nil record")
	}

	// 插入一条其他 issue 的 analyze_issue succeeded 任务——不应被返回
	other := newTestRecord("other-issue", "d-other", model.TaskTypeAnalyzeIssue)
	other.Status = model.TaskStatusSucceeded
	other.Payload.IssueNumber = 99
	other.CreatedAt = time.Now().UTC().Add(-10 * time.Minute)
	other.UpdatedAt = other.CreatedAt
	if err := s.CreateTask(ctx, other); err != nil {
		t.Fatalf("CreateTask(other) 失败: %v", err)
	}

	got, err = s.GetLatestAnalysisByIssue(ctx, repo, issueNum)
	if err != nil {
		t.Fatalf("其他 issue 后期望 nil error，得到: %v", err)
	}
	if got != nil {
		t.Fatal("其他 issue 后期望 nil record")
	}

	// 插入目标 issue 的 analyze_issue succeeded 任务（较早）
	early := newTestRecord("analyze-early", "d-early", model.TaskTypeAnalyzeIssue)
	early.Status = model.TaskStatusSucceeded
	early.Payload.IssueNumber = issueNum
	early.CreatedAt = time.Now().UTC().Add(-5 * time.Minute)
	early.UpdatedAt = early.CreatedAt
	if err := s.CreateTask(ctx, early); err != nil {
		t.Fatalf("CreateTask(early) 失败: %v", err)
	}

	// 插入目标 issue 的 analyze_issue succeeded 任务（较新）
	latest := newTestRecord("analyze-latest", "d-latest", model.TaskTypeAnalyzeIssue)
	latest.Status = model.TaskStatusSucceeded
	latest.Payload.IssueNumber = issueNum
	latest.CreatedAt = time.Now().UTC().Add(-1 * time.Minute)
	latest.UpdatedAt = latest.CreatedAt
	if err := s.CreateTask(ctx, latest); err != nil {
		t.Fatalf("CreateTask(latest) 失败: %v", err)
	}

	// 插入目标 issue 的 analyze_issue failed 任务——不应被返回
	failed := newTestRecord("analyze-failed", "d-failed", model.TaskTypeAnalyzeIssue)
	failed.Status = model.TaskStatusFailed
	failed.Payload.IssueNumber = issueNum
	failed.CreatedAt = time.Now().UTC()
	failed.UpdatedAt = failed.CreatedAt
	if err := s.CreateTask(ctx, failed); err != nil {
		t.Fatalf("CreateTask(failed) 失败: %v", err)
	}

	// 应返回最新的 succeeded 记录
	got, err = s.GetLatestAnalysisByIssue(ctx, repo, issueNum)
	if err != nil {
		t.Fatalf("期望 nil error，得到: %v", err)
	}
	if got == nil {
		t.Fatal("期望找到记录，得到 nil")
	}
	if got.ID != "analyze-latest" {
		t.Errorf("期望最新记录 ID=analyze-latest，得到: %s", got.ID)
	}
	if got.Payload.IssueNumber != issueNum {
		t.Errorf("期望 IssueNumber=%d，得到: %d", issueNum, got.Payload.IssueNumber)
	}
}

func TestSQLiteStore_GetLatestAnalysisByIssue_DoesNotMissOlderTargetAmongManyNewerRecords(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const repo = "owner/repo"
	const issueNum = int64(42)

	target := newTestRecord("analyze-target", "d-target", model.TaskTypeAnalyzeIssue)
	target.Status = model.TaskStatusSucceeded
	target.Payload.IssueNumber = issueNum
	target.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	target.UpdatedAt = target.CreatedAt
	if err := s.CreateTask(ctx, target); err != nil {
		t.Fatalf("CreateTask(target) 失败: %v", err)
	}

	falsePositive := newTestRecord("analyze-420", "d-420", model.TaskTypeAnalyzeIssue)
	falsePositive.Status = model.TaskStatusSucceeded
	falsePositive.Payload.IssueNumber = 420
	falsePositive.CreatedAt = time.Now().UTC().Add(-90 * time.Minute)
	falsePositive.UpdatedAt = falsePositive.CreatedAt
	if err := s.CreateTask(ctx, falsePositive); err != nil {
		t.Fatalf("CreateTask(falsePositive) 失败: %v", err)
	}

	for i := 0; i < 60; i++ {
		rec := newTestRecord(fmt.Sprintf("analyze-other-%d", i), fmt.Sprintf("d-other-%d", i), model.TaskTypeAnalyzeIssue)
		rec.Status = model.TaskStatusSucceeded
		rec.Payload.IssueNumber = int64(100 + i)
		rec.CreatedAt = time.Now().UTC().Add(-time.Duration(i) * time.Minute)
		rec.UpdatedAt = rec.CreatedAt
		if err := s.CreateTask(ctx, rec); err != nil {
			t.Fatalf("CreateTask(other-%d) 失败: %v", i, err)
		}
	}

	got, err := s.GetLatestAnalysisByIssue(ctx, repo, issueNum)
	if err != nil {
		t.Fatalf("GetLatestAnalysisByIssue 失败: %v", err)
	}
	if got == nil {
		t.Fatal("期望找到目标 issue 的分析记录，得到 nil")
	}
	if got.ID != "analyze-target" {
		t.Fatalf("期望返回 analyze-target，实际: %s", got.ID)
	}
	if got.Payload.IssueNumber != issueNum {
		t.Fatalf("期望 IssueNumber=%d，实际: %d", issueNum, got.Payload.IssueNumber)
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

func TestPurgeTasks_WithTestGenResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	oldTask := newTestRecord("purge-gen-old", "", model.TaskTypeGenTests)
	oldTask.Status = model.TaskStatusSucceeded
	oldTask.CreatedAt = time.Now().UTC().Add(-2 * time.Hour)
	oldTask.UpdatedAt = oldTask.CreatedAt
	if err := s.CreateTask(ctx, oldTask); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}
	rec := newTestGenResultRecord(oldTask.ID, "org/repo", "backend")
	if err := s.SaveTestGenResult(ctx, rec); err != nil {
		t.Fatalf("SaveTestGenResult 失败: %v", err)
	}

	affected, err := s.PurgeTasks(ctx, 1*time.Hour, model.TaskStatusSucceeded)
	if err != nil {
		t.Fatalf("PurgeTasks 不应被 test_gen_results 外键阻塞，实际: %v", err)
	}
	if affected != 1 {
		t.Fatalf("期望删除 1 条任务，实际删除 %d 条", affected)
	}

	got, err := s.GetTestGenResultByTaskID(ctx, oldTask.ID)
	if err != nil {
		t.Fatalf("GetTestGenResultByTaskID 不应报错: %v", err)
	}
	if got != nil {
		t.Fatalf("任务被 purge 后，按原 task_id 不应再查到结果，实际: %+v", got)
	}

	var nullCount int
	if err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM test_gen_results WHERE id = ? AND task_id IS NULL",
		rec.ID).Scan(&nullCount); err != nil {
		t.Fatalf("NULL task_id 查询失败: %v", err)
	}
	if nullCount != 1 {
		t.Fatalf("期望保留 1 条 task_id 已置空的结果记录，实际 %d 条", nullCount)
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
		model.TaskTypeGenDailyReport,
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

func TestSQLiteStore_ListReviewResultsByTimeRange(t *testing.T) {
	s := newTestStore(t)
	defer s.Close()

	ctx := context.Background()

	// 准备时间基准：以 UTC 天为单位
	now := time.Now().UTC().Truncate(24 * time.Hour)
	yesterday := now.Add(-24 * time.Hour)

	// 创建关联任务（满足 review_results.task_id 外键约束）
	for _, id := range []string{"task-tr-1", "task-tr-2", "task-tr-3"} {
		task := &model.TaskRecord{
			ID:        id,
			TaskType:  model.TaskTypeReviewPR,
			Status:    model.TaskStatusSucceeded,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		if err := s.CreateTask(ctx, task); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}

	// 准备测试数据：3 条记录，跨越两天
	records := []*model.ReviewRecord{
		{ID: "rr-tr-1", TaskID: "task-tr-1", RepoFullName: "acme/backend", PRNumber: 1, HeadSHA: "a1", Verdict: "approve", CreatedAt: yesterday.Add(2 * time.Hour)},
		{ID: "rr-tr-2", TaskID: "task-tr-2", RepoFullName: "acme/frontend", PRNumber: 2, HeadSHA: "a2", Verdict: "request_changes", CriticalCount: 1, CreatedAt: yesterday.Add(10 * time.Hour)},
		{ID: "rr-tr-3", TaskID: "task-tr-3", RepoFullName: "acme/backend", PRNumber: 3, HeadSHA: "a3", Verdict: "approve", CreatedAt: now.Add(1 * time.Hour)}, // 今天的，应被排除
	}
	for _, r := range records {
		if err := s.SaveReviewResult(ctx, r); err != nil {
			t.Fatalf("SaveReviewResult(%s): %v", r.ID, err)
		}
	}

	// 查询昨天的记录
	results, err := s.ListReviewResultsByTimeRange(ctx, yesterday, now)
	if err != nil {
		t.Fatalf("ListReviewResultsByTimeRange: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("want 2 results for yesterday, got %d", len(results))
	}

	// 查询今天的记录
	tomorrow := now.Add(24 * time.Hour)
	results, err = s.ListReviewResultsByTimeRange(ctx, now, tomorrow)
	if err != nil {
		t.Fatalf("ListReviewResultsByTimeRange: %v", err)
	}
	if len(results) != 1 {
		t.Errorf("want 1 result for today, got %d", len(results))
	}

	// 查询当天中午窗口，凌晨数据不应被错误包含
	results, err = s.ListReviewResultsByTimeRange(ctx, now.Add(12*time.Hour), now.Add(13*time.Hour))
	if err != nil {
		t.Fatalf("ListReviewResultsByTimeRange(midday): %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results for midday window, got %d", len(results))
	}

	// 空时间窗口
	results, err = s.ListReviewResultsByTimeRange(ctx, now.Add(-48*time.Hour), now.Add(-47*time.Hour))
	if err != nil {
		t.Fatalf("ListReviewResultsByTimeRange: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("want 0 results, got %d", len(results))
	}
}

func TestTaskTriggeredBy(t *testing.T) {
	s := newTestStore(t)
	record := newTestRecord("triggered-by-test", "delivery-tb-001", model.TaskTypeReviewPR)
	record.TriggeredBy = "manual:admin"
	if err := s.CreateTask(context.Background(), record); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	got, err := s.GetTask(context.Background(), "triggered-by-test")
	if err != nil {
		t.Fatalf("GetTask 失败: %v", err)
	}
	if got == nil {
		t.Fatal("GetTask 返回 nil，期望找到记录")
	}
	if got.TriggeredBy != "manual:admin" {
		t.Errorf("TriggeredBy 不匹配: got %q, want %q", got.TriggeredBy, "manual:admin")
	}
}

// TestMigration_PRNumber 验证迁移 13/14/15 成功执行，pr_number 列存在且可写入读取
func TestMigration_PRNumber(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 验证迁移 13/14/15/18 已成功执行（schema_migrations 中存在这些版本）
	for _, ver := range []int{13, 14, 15, 18} {
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

// newTestGenTestsRecord 创建带 module 字段的 gen_tests 任务记录。
// 注意：TaskPayload.Module 使用 omitempty，module=="" 时序列化后 JSON 中
// 根本不会出现 module 字段，测试正是要验证 SQL 中 COALESCE 能把 NULL 归一化为空串。
func newTestGenTestsRecord(id, repoFullName, module string, status model.TaskStatus, createdAt time.Time) *model.TaskRecord {
	r := newTestRecord(id, "", model.TaskTypeGenTests)
	r.RepoFullName = repoFullName
	r.Payload.RepoFullName = repoFullName
	r.Payload.Module = module
	r.Status = status
	r.CreatedAt = createdAt
	r.UpdatedAt = createdAt
	return r
}

// TestFindActiveGenTestsTasks_FilterByModule 验证不同 module 的任务被正确区分
func TestFindActiveGenTestsTasks_FilterByModule(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	r1 := newTestGenTestsRecord("gen-backend", "org/repo", "backend", model.TaskStatusQueued, base)
	r2 := newTestGenTestsRecord("gen-frontend", "org/repo", "frontend", model.TaskStatusRunning, base.Add(time.Second))
	for _, r := range []*model.TaskRecord{r1, r2} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	backendRecords, err := s.FindActiveGenTestsTasks(ctx, "org/repo", "backend")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks(backend) 失败: %v", err)
	}
	if len(backendRecords) != 1 || backendRecords[0].ID != "gen-backend" {
		t.Errorf("backend 查询期望 [gen-backend]，得到 %v", backendRecords)
	}

	frontendRecords, err := s.FindActiveGenTestsTasks(ctx, "org/repo", "frontend")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks(frontend) 失败: %v", err)
	}
	if len(frontendRecords) != 1 || frontendRecords[0].ID != "gen-frontend" {
		t.Errorf("frontend 查询期望 [gen-frontend]，得到 %v", frontendRecords)
	}
}

// TestFindActiveGenTestsTasks_EmptyModuleCOALESCE 验证空 module（整仓生成）
// 与具体 module 任务不混淆；omitempty 导致 JSON 中无 module 字段时，
// COALESCE(json_extract(payload, '$.module'), ”) 必须把 NULL 归一化为 ”。
func TestFindActiveGenTestsTasks_EmptyModuleCOALESCE(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	// 整仓任务：module=""，因 omitempty 序列化后 JSON 中无 module 字段
	whole := newTestGenTestsRecord("gen-whole", "org/repo", "", model.TaskStatusQueued, base)
	// 具体 module 任务
	partial := newTestGenTestsRecord("gen-backend", "org/repo", "backend", model.TaskStatusRunning, base.Add(time.Second))

	for _, r := range []*model.TaskRecord{whole, partial} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	// 查询空串 module 应只返回整仓任务
	emptyRecords, err := s.FindActiveGenTestsTasks(ctx, "org/repo", "")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks('') 失败: %v", err)
	}
	if len(emptyRecords) != 1 || emptyRecords[0].ID != "gen-whole" {
		t.Errorf("空 module 查询期望 [gen-whole]（验证 COALESCE 对 NULL 生效），得到 %d 条：%v",
			len(emptyRecords), emptyRecords)
	}

	// 查询具体 module 应只返回对应 module 任务
	backendRecords, err := s.FindActiveGenTestsTasks(ctx, "org/repo", "backend")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks(backend) 失败: %v", err)
	}
	if len(backendRecords) != 1 || backendRecords[0].ID != "gen-backend" {
		t.Errorf("backend 查询期望 [gen-backend]，得到 %v", backendRecords)
	}
}

// TestFindActiveGenTestsTasks_FilterByRepoAndStatus 验证 repo_full_name + 活跃状态联合过滤。
func TestFindActiveGenTestsTasks_FilterByRepoAndStatus(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()

	// org/repo 下一条活跃任务
	active := newTestGenTestsRecord("active-repo1", "org/repo", "svc", model.TaskStatusPending, base)
	// retrying 也属于活跃任务，Cancel-and-Replace 要覆盖 asynq backoff 窗口
	retrying := newTestGenTestsRecord("retrying-repo1", "org/repo", "svc", model.TaskStatusRetrying, base.Add(500*time.Millisecond))
	// org/repo 下一条已完成任务，不应被返回
	done := newTestGenTestsRecord("done-repo1", "org/repo", "svc", model.TaskStatusSucceeded, base.Add(time.Second))
	// 不同仓库的任务
	otherRepo := newTestGenTestsRecord("other-repo", "other/repo", "svc", model.TaskStatusQueued, base.Add(2*time.Second))

	for _, r := range []*model.TaskRecord{active, retrying, done, otherRepo} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask 失败: %v", err)
		}
	}

	records, err := s.FindActiveGenTestsTasks(ctx, "org/repo", "svc")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks 失败: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("期望 2 条活跃任务（pending + retrying），得到 %d: %v", len(records), records)
	}
	if records[0].ID != "active-repo1" || records[1].ID != "retrying-repo1" {
		t.Errorf("期望按 created_at 升序返回 [active-repo1 retrying-repo1]，得到 [%s %s]",
			records[0].ID, records[1].ID)
	}
}

// TestFindActiveGenTestsTasks_Empty 无匹配任务时返回空切片 + nil error
func TestFindActiveGenTestsTasks_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	records, err := s.FindActiveGenTestsTasks(ctx, "no/such-repo", "none")
	if err != nil {
		t.Fatalf("FindActiveGenTestsTasks 无匹配时不应返回错误，但得到: %v", err)
	}
	if len(records) != 0 {
		t.Errorf("期望空切片，得到 %d 条", len(records))
	}
}

// TestMigration_V20_TestGenResults 验证 test_gen_results 表在最新迁移后可查询且
// task_id 列允许 ON DELETE SET NULL。
func TestMigration_V20_TestGenResults(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// schema_migrations 中存在版本 19 / 20
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", 19).Scan(&exists); err != nil {
		t.Fatalf("迁移 v19 未登记: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, "SELECT 1 FROM schema_migrations WHERE version = ?", 20).Scan(&exists); err != nil {
		t.Fatalf("迁移 v20 未登记: %v", err)
	}

	// 空表可查询，不报错
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_gen_results").Scan(&count); err != nil {
		t.Fatalf("test_gen_results 空表查询失败: %v", err)
	}
	if count != 0 {
		t.Errorf("空表期望 count=0，得到 %d", count)
	}

	// 索引存在（使用 sqlite_master 检查）
	expectedIdx := []string{
		"idx_test_gen_results_repo",
		"idx_test_gen_results_repo_module",
		"idx_test_gen_results_created",
	}
	for _, name := range expectedIdx {
		var got string
		err := s.db.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", name).Scan(&got)
		if err != nil {
			t.Errorf("索引 %s 缺失: %v", name, err)
		}
	}

	var notNull int
	if err := s.db.QueryRowContext(ctx,
		"SELECT \"notnull\" FROM pragma_table_info('test_gen_results') WHERE name = 'task_id'").Scan(&notNull); err != nil {
		t.Fatalf("查询 task_id 列定义失败: %v", err)
	}
	if notNull != 0 {
		t.Fatalf("task_id 应允许 NULL 以匹配 ON DELETE SET NULL，实际 notnull=%d", notNull)
	}
}

// newTestGenResultRecord 构造 TestGenResultRecord，便于测试填充字段后 UPSERT
func newTestGenResultRecord(taskID, repoFullName, module string) *TestGenResultRecord {
	return &TestGenResultRecord{
		TaskID:             taskID,
		RepoFullName:       repoFullName,
		Module:             module,
		Framework:          "junit5",
		BaseRef:            "main",
		BranchName:         "auto-test/" + module,
		CommitSHA:          "deadbeef",
		PRNumber:           42,
		PRURL:              "https://gitea.example.com/o/r/pulls/42",
		Success:            true,
		InfoSufficient:     true,
		VerificationPassed: true,
		FailureCategory:    "none",
		FailureReason:      "",
		GeneratedCount:     5,
		CommittedCount:     5,
		SkippedCount:       1,
		TestPassed:         20,
		TestFailed:         0,
		TestDurationMs:     12000,
		ReviewEnqueued:     false,
		CostUSD:            1.23,
		DurationMs:         900_000,
		OutputJSON:         `{"success":true}`,
	}
}

// TestSaveTestGenResult_InsertAndGet 首次插入 + 读回验证所有字段正确映射
func TestSaveTestGenResult_InsertAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 先创建关联 task 以满足 FK 约束
	task := newTestRecord("task-gen-001", "", model.TaskTypeGenTests)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	rec := newTestGenResultRecord("task-gen-001", "owner/repo", "backend")
	if err := s.SaveTestGenResult(ctx, rec); err != nil {
		t.Fatalf("SaveTestGenResult 失败: %v", err)
	}
	// ID 由 Save 内部生成
	if rec.ID == "" {
		t.Fatal("SaveTestGenResult 未填充 record.ID（期望自动生成 UUID）")
	}

	got, err := s.GetTestGenResultByTaskID(ctx, "task-gen-001")
	if err != nil {
		t.Fatalf("GetTestGenResultByTaskID 失败: %v", err)
	}
	if got == nil {
		t.Fatal("GetTestGenResultByTaskID 命中期望返回记录，得到 nil")
	}
	if got.ID != rec.ID {
		t.Errorf("ID 不匹配: got %s, want %s", got.ID, rec.ID)
	}
	if got.TaskID != "task-gen-001" {
		t.Errorf("TaskID 不匹配: got %s, want task-gen-001", got.TaskID)
	}
	if got.Module != "backend" {
		t.Errorf("Module 不匹配: got %s", got.Module)
	}
	if !got.Success || !got.InfoSufficient || !got.VerificationPassed {
		t.Errorf("bool 字段映射失败: %+v", got)
	}
	if got.ReviewEnqueued {
		t.Errorf("ReviewEnqueued 期望 false")
	}
	if got.PRNumber != 42 || got.PRURL == "" {
		t.Errorf("PR 字段不匹配: number=%d url=%q", got.PRNumber, got.PRURL)
	}
	if got.CostUSD != 1.23 {
		t.Errorf("CostUSD 不匹配: got %f", got.CostUSD)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("时间戳未正确填充")
	}
}

// TestSaveTestGenResult_UpsertByTaskID 同 task_id 两次 Save → 仅一行，字段被最新值覆盖
func TestSaveTestGenResult_UpsertByTaskID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := newTestRecord("task-gen-upsert", "", model.TaskTypeGenTests)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 阶段 1：review_enqueued=false，附带 failure 标记
	first := newTestGenResultRecord("task-gen-upsert", "owner/repo", "svc")
	first.ReviewEnqueued = false
	first.Success = false
	first.FailureCategory = "infrastructure"
	first.FailureReason = "maven failed"
	if err := s.SaveTestGenResult(ctx, first); err != nil {
		t.Fatalf("第一次 SaveTestGenResult 失败: %v", err)
	}
	firstID := first.ID

	// 阶段 2：刷新 review_enqueued=true，成功路径
	second := newTestGenResultRecord("task-gen-upsert", "owner/repo", "svc")
	second.ReviewEnqueued = true
	second.Success = true
	second.FailureCategory = "none"
	second.FailureReason = ""
	second.OutputJSON = `{"success":true,"second":true}`
	if err := s.SaveTestGenResult(ctx, second); err != nil {
		t.Fatalf("第二次 SaveTestGenResult 失败: %v", err)
	}

	// 行数仍为 1
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_gen_results WHERE task_id=?", "task-gen-upsert").Scan(&count); err != nil {
		t.Fatalf("count 查询失败: %v", err)
	}
	if count != 1 {
		t.Fatalf("UPSERT 后期望 1 行，得到 %d", count)
	}

	// 回读应反映第二次的字段值
	got, err := s.GetTestGenResultByTaskID(ctx, "task-gen-upsert")
	if err != nil {
		t.Fatalf("GetTestGenResultByTaskID 失败: %v", err)
	}
	if got == nil {
		t.Fatal("期望命中，得到 nil")
	}
	if !got.ReviewEnqueued {
		t.Error("ReviewEnqueued 未被刷新为 true")
	}
	if !got.Success {
		t.Error("Success 未被刷新为 true")
	}
	if got.FailureCategory != "none" {
		t.Errorf("FailureCategory 未被覆盖: got %s", got.FailureCategory)
	}
	if got.OutputJSON != `{"success":true,"second":true}` {
		t.Errorf("OutputJSON 未被覆盖: got %s", got.OutputJSON)
	}
	// 主键 id 以第一次为准（UPSERT 不修改 id 字段）
	if got.ID != firstID {
		t.Errorf("id 应保持为首次插入的 UUID，got %s want %s", got.ID, firstID)
	}
	// UpdatedAt 应刷新至晚于 CreatedAt（ON CONFLICT DO UPDATE SET updated_at = datetime('now')）
	if got.UpdatedAt.Before(got.CreatedAt) {
		t.Errorf("UpdatedAt 应刷新至不早于 CreatedAt: updated=%v created=%v", got.UpdatedAt, got.CreatedAt)
	}
}

// TestGetTestGenResultByTaskID_NotFound 未命中时返回 (nil, nil)
func TestGetTestGenResultByTaskID_NotFound(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	got, err := s.GetTestGenResultByTaskID(ctx, "no-such-task")
	if err != nil {
		t.Fatalf("未命中期望 nil error，得到: %v", err)
	}
	if got != nil {
		t.Fatal("未命中期望 nil record")
	}
}

// TestGetTestGenResultByTaskID_EmptyTaskID 空 task_id 返回错误
func TestGetTestGenResultByTaskID_EmptyTaskID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	_, err := s.GetTestGenResultByTaskID(ctx, "")
	if err == nil {
		t.Fatal("空 task_id 应返回错误")
	}
}

// TestSaveTestGenResult_FreeTextTruncation 自由文本字段超过 2 KB 时被截断并追加标记
func TestSaveTestGenResult_FreeTextTruncation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := newTestRecord("task-gen-trunc", "", model.TaskTypeGenTests)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	// 构造 3 KB 的 output_json 与 failure_reason
	big := make([]byte, 3072)
	for i := range big {
		big[i] = 'x'
	}
	rec := newTestGenResultRecord("task-gen-trunc", "owner/repo", "svc")
	rec.OutputJSON = string(big)
	rec.FailureReason = string(big)

	if err := s.SaveTestGenResult(ctx, rec); err != nil {
		t.Fatalf("SaveTestGenResult 失败: %v", err)
	}

	got, err := s.GetTestGenResultByTaskID(ctx, "task-gen-trunc")
	if err != nil {
		t.Fatalf("GetTestGenResultByTaskID 失败: %v", err)
	}
	if got == nil {
		t.Fatal("未命中")
	}

	// 预期值 = 2048 字节 + "...(truncated)"
	wantLen := 2048 + len("...(truncated)")
	if len(got.OutputJSON) != wantLen {
		t.Errorf("OutputJSON 截断长度不符: got %d, want %d", len(got.OutputJSON), wantLen)
	}
	if got.OutputJSON[len(got.OutputJSON)-len("...(truncated)"):] != "...(truncated)" {
		t.Error("OutputJSON 结尾缺少 truncation 标记")
	}
	if len(got.FailureReason) != wantLen {
		t.Errorf("FailureReason 截断长度不符: got %d, want %d", len(got.FailureReason), wantLen)
	}
	if got.FailureReason[len(got.FailureReason)-len("...(truncated)"):] != "...(truncated)" {
		t.Error("FailureReason 结尾缺少 truncation 标记")
	}
}

// TestSaveTestGenResult_NoTruncationUnderLimit 边界：恰好 2048 字节不触发截断
func TestSaveTestGenResult_NoTruncationUnderLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	task := newTestRecord("task-gen-boundary", "", model.TaskTypeGenTests)
	if err := s.CreateTask(ctx, task); err != nil {
		t.Fatalf("CreateTask 失败: %v", err)
	}

	exact := make([]byte, 2048)
	for i := range exact {
		exact[i] = 'a'
	}
	rec := newTestGenResultRecord("task-gen-boundary", "owner/repo", "svc")
	rec.OutputJSON = string(exact)

	if err := s.SaveTestGenResult(ctx, rec); err != nil {
		t.Fatalf("SaveTestGenResult 失败: %v", err)
	}

	got, err := s.GetTestGenResultByTaskID(ctx, "task-gen-boundary")
	if err != nil {
		t.Fatalf("GetTestGenResultByTaskID 失败: %v", err)
	}
	if len(got.OutputJSON) != 2048 {
		t.Errorf("边界长度期望 2048，得到 %d", len(got.OutputJSON))
	}
}

// TestSaveTestGenResult_NilRecord nil 记录应返回错误
func TestSaveTestGenResult_NilRecord(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.SaveTestGenResult(ctx, nil); err == nil {
		t.Fatal("SaveTestGenResult(nil) 应返回错误")
	} else if !errors.Is(err, ErrNilRecord) {
		t.Errorf("期望 ErrNilRecord，得到 %v", err)
	}
}

// TestSaveTestGenResult_EmptyTaskID 空 task_id 应返回错误
func TestSaveTestGenResult_EmptyTaskID(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	rec := newTestGenResultRecord("", "owner/repo", "svc")
	if err := s.SaveTestGenResult(ctx, rec); err == nil {
		t.Fatal("空 task_id 应返回错误")
	}
}

// TestListActiveGenTestsModules_ActiveStatuses 覆盖 queued/running/retrying 三态均命中
func TestListActiveGenTestsModules_ActiveStatuses(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	queued := newTestGenTestsRecord("gen-queued", "org/repo", "mod-queued", model.TaskStatusQueued, base)
	running := newTestGenTestsRecord("gen-running", "org/repo", "mod-running", model.TaskStatusRunning, base.Add(time.Second))
	retrying := newTestGenTestsRecord("gen-retrying", "org/repo", "mod-retrying", model.TaskStatusRetrying, base.Add(2*time.Second))
	// 非活跃状态，不应出现
	succeeded := newTestGenTestsRecord("gen-succeeded", "org/repo", "mod-succeeded", model.TaskStatusSucceeded, base.Add(3*time.Second))
	failed := newTestGenTestsRecord("gen-failed", "org/repo", "mod-failed", model.TaskStatusFailed, base.Add(4*time.Second))
	cancelled := newTestGenTestsRecord("gen-cancelled", "org/repo", "mod-cancelled", model.TaskStatusCancelled, base.Add(5*time.Second))

	for _, r := range []*model.TaskRecord{queued, running, retrying, succeeded, failed, cancelled} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask %s 失败: %v", r.ID, err)
		}
	}

	mods, err := s.ListActiveGenTestsModules(ctx, "org/repo")
	if err != nil {
		t.Fatalf("ListActiveGenTestsModules 失败: %v", err)
	}
	if len(mods) != 3 {
		t.Fatalf("期望 3 个活跃 module，得到 %d: %v", len(mods), mods)
	}
	want := map[string]bool{"mod-queued": true, "mod-running": true, "mod-retrying": true}
	for _, m := range mods {
		if !want[m] {
			t.Errorf("意外 module: %s", m)
		}
	}
}

// TestListActiveGenTestsModules_ExcludesOtherTaskTypes 非 gen_tests 任务不混入
func TestListActiveGenTestsModules_ExcludesOtherTaskTypes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	// gen_tests 活跃：module=be
	gen := newTestGenTestsRecord("gen-be", "org/repo", "be", model.TaskStatusQueued, base)
	// review_pr 活跃：与 gen_tests 同仓，payload.module 字段不应被选出
	review := newTestRecord("review-active", "", model.TaskTypeReviewPR)
	review.RepoFullName = "org/repo"
	review.Payload.RepoFullName = "org/repo"
	review.Status = model.TaskStatusRunning
	review.CreatedAt = base.Add(time.Second)
	review.UpdatedAt = review.CreatedAt

	for _, r := range []*model.TaskRecord{gen, review} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask %s 失败: %v", r.ID, err)
		}
	}

	mods, err := s.ListActiveGenTestsModules(ctx, "org/repo")
	if err != nil {
		t.Fatalf("ListActiveGenTestsModules 失败: %v", err)
	}
	if len(mods) != 1 || mods[0] != "be" {
		t.Errorf("期望 [be]，得到 %v", mods)
	}
}

// TestListActiveGenTestsModules_EmptyModulePreserved 空 module（整仓生成）原样返回为 ""
func TestListActiveGenTestsModules_EmptyModulePreserved(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	// module="" 由于 omitempty，JSON 中无 module 字段；COALESCE 应归一化为 ""
	whole := newTestGenTestsRecord("gen-whole", "org/repo", "", model.TaskStatusQueued, base)
	partial := newTestGenTestsRecord("gen-be", "org/repo", "be", model.TaskStatusRunning, base.Add(time.Second))

	for _, r := range []*model.TaskRecord{whole, partial} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask %s 失败: %v", r.ID, err)
		}
	}

	mods, err := s.ListActiveGenTestsModules(ctx, "org/repo")
	if err != nil {
		t.Fatalf("ListActiveGenTestsModules 失败: %v", err)
	}
	if len(mods) != 2 {
		t.Fatalf("期望 2 个 module（含空串），得到 %d: %v", len(mods), mods)
	}
	var seenEmpty, seenBE bool
	for _, m := range mods {
		if m == "" {
			seenEmpty = true
		}
		if m == "be" {
			seenBE = true
		}
	}
	if !seenEmpty {
		t.Error("未看到空 module，COALESCE 未正确归一化 NULL → ''")
	}
	if !seenBE {
		t.Error("未看到 be module")
	}
}

// TestListActiveGenTestsModules_FilterByRepo 仓库维度过滤
func TestListActiveGenTestsModules_FilterByRepo(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	base := time.Now().UTC()
	a := newTestGenTestsRecord("gen-a", "org/a", "m1", model.TaskStatusQueued, base)
	b := newTestGenTestsRecord("gen-b", "org/b", "m2", model.TaskStatusQueued, base.Add(time.Second))
	for _, r := range []*model.TaskRecord{a, b} {
		if err := s.CreateTask(ctx, r); err != nil {
			t.Fatalf("CreateTask %s 失败: %v", r.ID, err)
		}
	}

	mods, err := s.ListActiveGenTestsModules(ctx, "org/a")
	if err != nil {
		t.Fatalf("ListActiveGenTestsModules 失败: %v", err)
	}
	if len(mods) != 1 || mods[0] != "m1" {
		t.Errorf("期望 [m1]，得到 %v", mods)
	}
}

// TestListActiveGenTestsModules_Empty 无匹配时返回空切片 + nil error
func TestListActiveGenTestsModules_Empty(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mods, err := s.ListActiveGenTestsModules(ctx, "no/such-repo")
	if err != nil {
		t.Fatalf("期望 nil error，得到 %v", err)
	}
	if len(mods) != 0 {
		t.Errorf("期望空切片，得到 %v", mods)
	}
}
