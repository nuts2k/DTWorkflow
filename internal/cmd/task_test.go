package cmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

type mockTaskStore struct {
	tasks     map[string]*model.TaskRecord
	updateErr error
}

func newMockStore() *mockTaskStore {
	return &mockTaskStore{tasks: make(map[string]*model.TaskRecord)}
}

func (m *mockTaskStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	m.tasks[record.ID] = record
	return nil
}

func (m *mockTaskStore) GetTask(_ context.Context, id string) (*model.TaskRecord, error) {
	r, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *mockTaskStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.tasks[record.ID] = record
	return nil
}

func (m *mockTaskStore) ListTasks(_ context.Context, _ store.ListOptions) ([]*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockTaskStore) FindByDeliveryID(_ context.Context, _ string, _ model.TaskType) (*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockTaskStore) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	return nil, nil
}
func (m *mockTaskStore) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}
func (m *mockTaskStore) Ping(_ context.Context) error { return nil }
func (m *mockTaskStore) Close() error                 { return nil }

type stubTaskEnqueuer struct {
	asynqID     string
	enqueueErr  error
	called      int
	lastPayload model.TaskPayload
	lastOpts    queue.EnqueueOptions
}

func (s *stubTaskEnqueuer) Enqueue(_ context.Context, payload model.TaskPayload, opts queue.EnqueueOptions) (string, error) {
	s.called++
	s.lastPayload = payload
	s.lastOpts = opts
	if s.enqueueErr != nil {
		return "", s.enqueueErr
	}
	return s.asynqID, nil
}

func newTaskRecord(status model.TaskStatus) *model.TaskRecord {
	now := time.Now()
	started := now.Add(-2 * time.Minute)
	completed := now.Add(-1 * time.Minute)
	return &model.TaskRecord{
		ID:           "task-1",
		TaskType:     model.TaskTypeReviewPR,
		Status:       status,
		Priority:     model.PriorityHigh,
		RepoFullName: "org/repo",
		Payload: model.TaskPayload{
			TaskType:     model.TaskTypeReviewPR,
			DeliveryID:   "delivery-1",
			RepoOwner:    "org",
			RepoName:     "repo",
			RepoFullName: "org/repo",
			PRNumber:     42,
		},
		RetryCount:  2,
		MaxRetry:    3,
		Error:       "old error",
		WorkerID:    "worker-1",
		DeliveryID:  "delivery-1",
		CreatedAt:   now.Add(-10 * time.Minute),
		UpdatedAt:   now.Add(-5 * time.Minute),
		StartedAt:   &started,
		CompletedAt: &completed,
	}
}

func TestTaskCommand_HasRedisAddrFlag(t *testing.T) {
	flag := taskCmd.PersistentFlags().Lookup("redis-addr")
	if flag == nil {
		t.Fatal("task command should define --redis-addr flag")
	}
}

func resetTaskFlagsForTest(t *testing.T) {
	t.Helper()

	// 注意：pflag 的 Changed 状态会在同一进程内复用，需显式重置。
	if f := taskCmd.PersistentFlags().Lookup("db-path"); f != nil {
		_ = f.Value.Set(getEnvDefault("DTWORKFLOW_DB_PATH", "data/dtworkflow.db"))
		f.Changed = false
	}
	if f := taskCmd.PersistentFlags().Lookup("redis-addr"); f != nil {
		_ = f.Value.Set(getEnvDefault("DTWORKFLOW_REDIS_ADDR", "localhost:6379"))
		f.Changed = false
	}

	taskStore = nil
	taskQueueClient = nil
}

func TestApplyTaskConfigFromManager_AppliesDatabasePathAndRedisAddr(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	// 先写入一份最小可通过 Validate 的配置文件（需要 webhook/notify）。
	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "" +
		"database:\n" +
		"  path: \"/tmp/test.db\"\n" +
		"redis:\n" +
		"  addr: \"redis.test:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(
		config.WithDefaults(),
		config.WithEnvPrefix("DTWORKFLOW"),
		config.WithConfigFile(cfgPath),
	)
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// 预置成其他值，确保确实被覆盖。
	taskDBPath = "data/old.db"
	taskRedisAddr = "old:6379"

	if err := applyTaskConfigFromManager(mgr, true, true); err != nil {
		t.Fatalf("applyTaskConfigFromManager 失败: %v", err)
	}
	if taskDBPath != "/tmp/test.db" {
		t.Fatalf("taskDBPath = %q, want %q", taskDBPath, "/tmp/test.db")
	}
	if taskRedisAddr != "redis.test:6379" {
		t.Fatalf("taskRedisAddr = %q, want %q", taskRedisAddr, "redis.test:6379")
	}
}

func TestTaskCommand_PersistentPreRun_PrefersCfgManager(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "" +
		"database:\n" +
		"  path: \":memory:\"\n" +
		"redis:\n" +
		"  addr: \"127.0.0.1:6380\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(
		config.WithDefaults(),
		config.WithConfigFile(cfgPath),
	)
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// 模拟 root.PersistentPreRunE 已经初始化了 cfgManager。
	cfgManager = mgr

	// 由于本测试不模拟用户显式传 flag，因此需确保对应 flag 的 Changed 为 false。
	// 通过直接写入变量（而非 flags.Set）即可模拟“旧值/其他来源”，验证 cfgManager 可覆盖。
	taskDBPath = "data/should-be-overwritten.db"
	taskRedisAddr = "should-be-overwritten:6379"

	if err := taskCmd.PersistentPreRunE(taskCmd, nil); err != nil {
		t.Fatalf("PersistentPreRunE 失败: %v", err)
	}
	defer func() {
		_ = taskCmd.PersistentPostRunE(taskCmd, nil)
	}()

	if taskDBPath != ":memory:" {
		t.Fatalf("taskDBPath = %q, want %q", taskDBPath, ":memory:")
	}
	if taskRedisAddr != "127.0.0.1:6380" {
		t.Fatalf("taskRedisAddr = %q, want %q", taskRedisAddr, "127.0.0.1:6380")
	}

	if taskStore == nil {
		t.Fatalf("taskStore 不应为 nil")
	}
	if taskQueueClient == nil {
		t.Fatalf("taskQueueClient 不应为 nil")
	}

	// 数据库是 :memory:，可通过 Ping 证明连接成功。
	if err := taskStore.Ping(context.Background()); err != nil {
		t.Fatalf("taskStore.Ping 失败: %v", err)
	}
}

func TestApplyTaskConfigFromManager_ExplicitDBPathNotOverridden(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "" +
		"database:\n" +
		"  path: \"/tmp/from-cfg.db\"\n" +
		"redis:\n" +
		"  addr: \"from-cfg:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(
		config.WithDefaults(),
		config.WithConfigFile(cfgPath),
	)
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// 模拟用户显式传入 --db-path
	_ = taskCmd.PersistentFlags().Set("db-path", ":memory:")

	if err := applyTaskConfigFromManager(mgr, false, true); err != nil {
		t.Fatalf("applyTaskConfigFromManager 失败: %v", err)
	}
	if taskDBPath != ":memory:" {
		t.Fatalf("taskDBPath = %q, want %q", taskDBPath, ":memory:")
	}
	// redis-addr 未显式传入，应从 cfgManager 应用
	if taskRedisAddr != "from-cfg:6379" {
		t.Fatalf("taskRedisAddr = %q, want %q", taskRedisAddr, "from-cfg:6379")
	}
}

func TestApplyTaskConfigFromManager_ExplicitRedisAddrNotOverridden(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "" +
		"database:\n" +
		"  path: \":memory:\"\n" +
		"redis:\n" +
		"  addr: \"from-cfg:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(
		config.WithDefaults(),
		config.WithConfigFile(cfgPath),
	)
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}

	// 模拟用户显式传入 --redis-addr
	_ = taskCmd.PersistentFlags().Set("redis-addr", "explicit:6379")

	if err := applyTaskConfigFromManager(mgr, true, false); err != nil {
		t.Fatalf("applyTaskConfigFromManager 失败: %v", err)
	}
	if taskRedisAddr != "explicit:6379" {
		t.Fatalf("taskRedisAddr = %q, want %q", taskRedisAddr, "explicit:6379")
	}
	// db-path 未显式传入，应从 cfgManager 应用
	if taskDBPath != ":memory:" {
		t.Fatalf("taskDBPath = %q, want %q", taskDBPath, ":memory:")
	}
}

func TestTaskCommand_PersistentPreRun_ExplicitFlagsNotOverridden(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	cfgPath := filepath.Join(t.TempDir(), "dtworkflow.yaml")
	content := "" +
		"database:\n" +
		"  path: \"/tmp/from-cfg.db\"\n" +
		"redis:\n" +
		"  addr: \"from-cfg:6379\"\n" +
		"webhook:\n" +
		"  secret: \"test-secret\"\n" +
		"notify:\n" +
		"  default_channel: \"gitea\"\n" +
		"  channels:\n" +
		"    gitea:\n" +
		"      enabled: true\n"
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("写入临时配置文件失败: %v", err)
	}

	mgr, err := config.NewManager(
		config.WithDefaults(),
		config.WithConfigFile(cfgPath),
	)
	if err != nil {
		t.Fatalf("NewManager 失败: %v", err)
	}
	if err := mgr.Load(); err != nil {
		t.Fatalf("Load 失败: %v", err)
	}
	cfgManager = mgr

	// 模拟真实解析路径：由叶子命令解析并持有 Changed 状态，再触发父命令的 PersistentPreRunE。
	if err := taskStatusCmd.ParseFlags([]string{"--db-path", ":memory:", "--redis-addr", "explicit:6379", "task-1"}); err != nil {
		t.Fatalf("ParseFlags 失败: %v", err)
	}

	if err := taskCmd.PersistentPreRunE(taskStatusCmd, []string{"task-1"}); err != nil {
		t.Fatalf("PersistentPreRunE 失败: %v", err)
	}
	defer func() {
		_ = taskCmd.PersistentPostRunE(taskCmd, nil)
	}()

	if taskDBPath != ":memory:" {
		t.Fatalf("taskDBPath = %q, want %q", taskDBPath, ":memory:")
	}
	if taskRedisAddr != "explicit:6379" {
		t.Fatalf("taskRedisAddr = %q, want %q", taskRedisAddr, "explicit:6379")
	}

	if taskStore == nil {
		t.Fatalf("taskStore 不应为 nil")
	}
	if taskQueueClient == nil {
		t.Fatalf("taskQueueClient 不应为 nil")
	}
}

func TestTaskCommand_Execute_ConfigFileAppliedToTaskWhenFlagsNotSet(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	// 真实命令链路：rootCmd.Execute() + --config + task status
	// 证明点：task 使用配置文件中的 database.path / redis.addr（而不是 task flags 的默认值）。
	cfgPath := writeTestConfigFile(t, "database:\n  path: \":memory:\"\n"+
		"redis:\n  addr: \"from-cfg:6379\"\n"+
		"webhook:\n  secret: \"test-secret\"\n"+
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n")

	// 将 task status 的 RunE 临时替换为“只观察最终配置”，避免依赖真实任务数据。
	oldRunE := taskStatusCmd.RunE
	defer func() { taskStatusCmd.RunE = oldRunE }()

	var gotDBPath, gotRedisAddr string
	taskStatusCmd.RunE = func(cmd *cobra.Command, args []string) error {
		gotDBPath = taskDBPath
		gotRedisAddr = taskRedisAddr
		return nil
	}

	rootCmd.SetArgs([]string{
		"--config", cfgPath,
		"task",
		"status", "task-1",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 task status 失败: %v", err)
	}

	if gotDBPath != ":memory:" {
		t.Fatalf("gotDBPath = %q, want %q（应来自配置文件）", gotDBPath, ":memory:")
	}
	if gotRedisAddr != "from-cfg:6379" {
		t.Fatalf("gotRedisAddr = %q, want %q（应来自配置文件）", gotRedisAddr, "from-cfg:6379")
	}
}

func TestTaskCommand_Execute_ExplicitFlagsNotOverridden(t *testing.T) {
	resetRootFlagsForTest(t)
	resetTaskFlagsForTest(t)
	defer func() {
		resetRootFlagsForTest(t)
		resetTaskFlagsForTest(t)
	}()

	// cfgManager 通过 rootCmd.PersistentPreRunE 初始化，因此这里使用 rootCmd.Execute() 走真实链路。
	cfgPath := writeTestConfigFile(t, "database:\n  path: \"/tmp/from-cfg.db\"\n"+
		"redis:\n  addr: \"from-cfg:6379\"\n"+
		"webhook:\n  secret: \"test-secret\"\n"+
		"notify:\n  default_channel: \"gitea\"\n  channels:\n    gitea:\n      enabled: true\n")

	// 将 task status 的 RunE 临时替换为“只观察最终配置”，避免依赖真实 DB/Redis 或任务数据。
	oldRunE := taskStatusCmd.RunE
	defer func() { taskStatusCmd.RunE = oldRunE }()

	var gotDBPath, gotRedisAddr string
	taskStatusCmd.RunE = func(cmd *cobra.Command, args []string) error {
		gotDBPath = taskDBPath
		gotRedisAddr = taskRedisAddr
		return nil
	}

	// 显式传入 task 的两个 flag，应覆盖 cfgManager 中的 database.path/redis.addr。
	rootCmd.SetArgs([]string{
		"--config", cfgPath,
		"task",
		"--db-path", ":memory:",
		"--redis-addr", "explicit:6379",
		"status", "task-1",
	})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("执行 task status 失败: %v", err)
	}

	if gotDBPath != ":memory:" {
		t.Fatalf("gotDBPath = %q, want %q", gotDBPath, ":memory:")
	}
	if gotRedisAddr != "explicit:6379" {
		t.Fatalf("gotRedisAddr = %q, want %q", gotRedisAddr, "explicit:6379")
	}
}

func TestRetryTask_FailedTask_EnqueuesImmediately(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-123"}

	updated, message, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if message == "" {
		t.Fatal("retryTask should return non-empty message")
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
	if updated.AsynqID != "asynq-123" {
		t.Fatalf("asynq id = %q, want %q", updated.AsynqID, "asynq-123")
	}
	if q.called != 1 {
		t.Fatalf("enqueue called %d times, want 1", q.called)
	}
	if q.lastPayload.DeliveryID != record.Payload.DeliveryID {
		t.Fatalf("payload delivery_id = %q, want %q", q.lastPayload.DeliveryID, record.Payload.DeliveryID)
	}
}

func TestRetryTask_CancelledTask_EnqueuesImmediately(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusCancelled)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-456"}

	updated, _, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
}

func TestRetryTask_InvalidStatus_ReturnsError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusRunning)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return error for invalid status")
	}
}

func TestRetryTask_EnqueueFailure_ReturnsError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{enqueueErr: errors.New("redis down")}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return enqueue error")
	}
}

func TestRetryTask_TaskIDConflict_TreatAsQueued(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{enqueueErr: asynq.ErrTaskIDConflict}

	updated, message, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", updated.Status, model.TaskStatusQueued)
	}
	expectedID := buildRetryTaskID(record.DeliveryID, record.TaskType)
	if updated.AsynqID != expectedID {
		t.Fatalf("asynq id = %q, want %q", updated.AsynqID, expectedID)
	}
	if message == "" {
		t.Fatal("message should not be empty on task id conflict")
	}
}

func TestRetryTask_ResetExecutionFields(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-789"}

	updated, _, err := retryTask(context.Background(), s, q, record.ID)
	if err != nil {
		t.Fatalf("retryTask error: %v", err)
	}
	if updated.RetryCount != 0 {
		t.Fatalf("retry_count = %d, want 0", updated.RetryCount)
	}
	if updated.Error != "" {
		t.Fatalf("error = %q, want empty", updated.Error)
	}
	if updated.StartedAt != nil {
		t.Fatal("started_at should be nil")
	}
	if updated.CompletedAt != nil {
		t.Fatal("completed_at should be nil")
	}
	if updated.WorkerID != "" {
		t.Fatalf("worker_id = %q, want empty", updated.WorkerID)
	}
}

func TestTaskRetryCommand_SuccessPrintsQueued(t *testing.T) {
	oldStore := taskStore
	oldQueueClient := taskQueueClient
	oldJSON := jsonOutput
	defer func() {
		taskStore = oldStore
		taskQueueClient = oldQueueClient
		jsonOutput = oldJSON
	}()

	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	q := &stubTaskEnqueuer{asynqID: "asynq-cmd-1"}
	taskStore = s
	taskQueueClient = &queue.Client{}
	jsonOutput = false

	oldRetry := taskRetryCmd.RunE
	defer func() { taskRetryCmd.RunE = oldRetry }()
	taskRetryCmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx := cmd.Context()
		rec, message, err := retryTask(ctx, taskStore, q, args[0])
		if err != nil {
			return &ExitCodeError{Code: 1, Err: err}
		}
		PrintResult(map[string]any{
			"id":       args[0],
			"status":   string(rec.Status),
			"asynq_id": rec.AsynqID,
			"message":  message,
		}, func(data any) string {
			return "任务已重新入队\n当前状态: queued\n"
		})
		return nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe error: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	taskRetryCmd.SetArgs([]string{record.ID})
	err = taskRetryCmd.RunE(taskRetryCmd, []string{record.ID})
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("ReadAll error: %v", readErr)
	}
	if !strings.Contains(string(out), "queued") {
		t.Fatalf("output = %q, want contains queued", string(out))
	}
	if err != nil {
		t.Fatalf("taskRetryCmd.RunE error: %v", err)
	}
	if s.tasks[record.ID].Status != model.TaskStatusQueued {
		t.Fatalf("status = %q, want %q", s.tasks[record.ID].Status, model.TaskStatusQueued)
	}
	_ = q
}

func TestRetryTask_UpdateStoreFailure_ReturnsSyncError(t *testing.T) {
	s := newMockStore()
	record := newTaskRecord(model.TaskStatusFailed)
	s.tasks[record.ID] = record
	s.updateErr = errors.New("sqlite write failed")
	q := &stubTaskEnqueuer{asynqID: "asynq-999"}

	_, _, err := retryTask(context.Background(), s, q, record.ID)
	if err == nil {
		t.Fatal("retryTask should return sync error")
	}
}
