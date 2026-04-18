package api

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

func init() {
	gin.SetMode(gin.TestMode)
}

const testToken = "test-secret-token"
const testIdentity = "ci-bot"

func testTokens() []config.TokenConfig {
	return []config.TokenConfig{
		{Token: testToken, Identity: testIdentity},
	}
}

func authedRequest(method, path string, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	return req
}

func setupTestRouter(t *testing.T, s store.Store) (*gin.Engine, *httptest.ResponseRecorder) {
	t.Helper()
	r := gin.New()
	deps := Dependencies{
		Store:     s,
		Enqueuer:  &mockEnqueuer{},
		Tokens:    testTokens(),
		Version:   "test-v1",
		StartTime: time.Now(),
		Logger:    slog.Default(),
	}
	RegisterRoutes(r, deps)
	w := httptest.NewRecorder()
	return r, w
}

// mockStore 实现 store.Store 接口的简单内存 mock
type mockStore struct {
	mu    sync.RWMutex
	tasks map[string]*model.TaskRecord
}

func newMockStore() *mockStore {
	return &mockStore{tasks: make(map[string]*model.TaskRecord)}
}

func (m *mockStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tasks[record.ID] = record
	return nil
}

func (m *mockStore) GetTask(_ context.Context, id string) (*model.TaskRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (m *mockStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[record.ID]; !ok {
		return store.ErrTaskNotFound
	}
	record.UpdatedAt = time.Now().UTC()
	cp := *record
	m.tasks[record.ID] = &cp
	return nil
}

func (m *mockStore) ListTasks(_ context.Context, opts store.ListOptions) ([]*model.TaskRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*model.TaskRecord
	for _, r := range m.tasks {
		if opts.RepoFullName != "" && r.RepoFullName != opts.RepoFullName {
			continue
		}
		if opts.Status != "" && r.Status != opts.Status {
			continue
		}
		if opts.TaskType != "" && r.TaskType != opts.TaskType {
			continue
		}
		cp := *r
		result = append(result, &cp)
	}
	if opts.Offset > 0 && opts.Offset < len(result) {
		result = result[opts.Offset:]
	} else if opts.Offset >= len(result) {
		result = nil
	}
	if opts.Limit > 0 && opts.Limit < len(result) {
		result = result[:opts.Limit]
	}
	return result, nil
}

func (m *mockStore) FindByDeliveryID(_ context.Context, _ string, _ model.TaskType) (*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}

func (m *mockStore) FindActivePRTasks(_ context.Context, _ string, _ int64, _ model.TaskType) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) FindActiveIssueTasks(_ context.Context, _ string, _ int64, _ model.TaskType) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) FindActiveGenTestsTasks(_ context.Context, _ string, _ string) ([]*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) HasNewerReviewTask(_ context.Context, _ string, _ int64, _ time.Time) (bool, error) {
	return false, nil
}

func (m *mockStore) SaveReviewResult(_ context.Context, _ *model.ReviewRecord) error {
	return nil
}

func (m *mockStore) GetReviewResult(_ context.Context, _ string) (*model.ReviewRecord, error) {
	return nil, nil
}

func (m *mockStore) ListReviewResults(_ context.Context, _ string, _, _ int) ([]*model.ReviewRecord, error) {
	return nil, nil
}

func (m *mockStore) ListReviewResultsByTimeRange(_ context.Context, _, _ time.Time) ([]*model.ReviewRecord, error) {
	return nil, nil
}

func (m *mockStore) GetLatestAnalysisByIssue(_ context.Context, _ string, _ int64) (*model.TaskRecord, error) {
	return nil, nil
}

func (m *mockStore) Ping(_ context.Context) error {
	return nil
}

func (m *mockStore) Close() error {
	return nil
}

// mockEnqueuer 实现 queue.Enqueuer 接口
type mockEnqueuer struct{}

func (m *mockEnqueuer) Enqueue(_ context.Context, _ model.TaskPayload, _ queue.EnqueueOptions) (string, error) {
	return "mock-asynq-id", nil
}

// newMockEnqueueHandler 创建用于测试的真实 EnqueueHandler（使用 mock 依赖）
func newMockEnqueueHandler(s store.Store) *queue.EnqueueHandler {
	return queue.NewEnqueueHandler(&mockEnqueuer{}, nil, s, slog.Default())
}
