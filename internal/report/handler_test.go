package report

import (
	"context"
	"fmt"
	"testing"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

// mockTaskStore 模拟 TaskStore 接口
type mockTaskStore struct {
	created []*model.TaskRecord
	updated []*model.TaskRecord
}

func (m *mockTaskStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	m.created = append(m.created, record)
	return nil
}

func (m *mockTaskStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	m.updated = append(m.updated, record)
	return nil
}

// mockGenerator 模拟 Generator 接口
type mockGenerator struct {
	called bool
	err    error
}

func (m *mockGenerator) Generate(_ context.Context) error {
	m.called = true
	return m.err
}

func TestDailyReportHandler_Success(t *testing.T) {
	store := &mockTaskStore{}
	gen := &mockGenerator{}
	handler := NewDailyReportHandler(store, gen)

	task := asynq.NewTask("dtworkflow:gen_daily_report", nil)
	err := handler.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("ProcessTask: %v", err)
	}

	if !gen.called {
		t.Error("generator.Generate was not called")
	}

	// 验证 TaskRecord 创建和更新
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created task, got %d", len(store.created))
	}
	if store.created[0].TaskType != model.TaskTypeGenDailyReport {
		t.Errorf("task type = %v, want %v", store.created[0].TaskType, model.TaskTypeGenDailyReport)
	}

	if len(store.updated) != 1 {
		t.Fatalf("expected 1 updated task, got %d", len(store.updated))
	}
	if store.updated[0].Status != model.TaskStatusSucceeded {
		t.Errorf("task status = %v, want %v", store.updated[0].Status, model.TaskStatusSucceeded)
	}
}

func TestDailyReportHandler_GeneratorError(t *testing.T) {
	store := &mockTaskStore{}
	gen := &mockGenerator{err: fmt.Errorf("webhook timeout")}
	handler := NewDailyReportHandler(store, gen)

	task := asynq.NewTask("dtworkflow:gen_daily_report", nil)
	err := handler.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected error from handler")
	}

	// TaskRecord 应被标记为 failed
	if len(store.updated) != 1 {
		t.Fatalf("expected 1 updated task, got %d", len(store.updated))
	}
	if store.updated[0].Status != model.TaskStatusFailed {
		t.Errorf("task status = %v, want %v", store.updated[0].Status, model.TaskStatusFailed)
	}
	if store.updated[0].Error == "" {
		t.Error("task error should be set")
	}
}
