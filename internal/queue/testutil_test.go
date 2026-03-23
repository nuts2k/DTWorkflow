package queue

import (
	"context"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
)

// sharedMockStore 实现 store.Store 接口的通用内存 mock，
// 供 enqueue_test.go、processor_test.go、recovery_test.go 等共用。
// 通过函数字段支持各测试注入特殊行为。
type sharedMockStore struct {
	tasks        map[string]*model.TaskRecord
	byDeliveryID map[string]*model.TaskRecord
	orphans      []*model.TaskRecord
	updated      []*model.TaskRecord

	// 可注入的错误，用于测试异常路径
	createErr  error
	updateErr  error
	findErr    error
	listErr    error
	orphanErr  error
}

func newSharedMockStore() *sharedMockStore {
	return &sharedMockStore{
		tasks:        make(map[string]*model.TaskRecord),
		byDeliveryID: make(map[string]*model.TaskRecord),
	}
}

func (m *sharedMockStore) CreateTask(_ context.Context, record *model.TaskRecord) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.tasks[record.ID] = record
	if record.DeliveryID != "" {
		key := record.DeliveryID + ":" + string(record.TaskType)
		m.byDeliveryID[key] = record
	}
	return nil
}

func (m *sharedMockStore) GetTask(_ context.Context, id string) (*model.TaskRecord, error) {
	r, ok := m.tasks[id]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *sharedMockStore) UpdateTask(_ context.Context, record *model.TaskRecord) error {
	if m.updateErr != nil {
		return m.updateErr
	}
	m.tasks[record.ID] = record
	m.updated = append(m.updated, record)
	return nil
}

func (m *sharedMockStore) ListTasks(_ context.Context, _ store.ListOptions) ([]*model.TaskRecord, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return nil, nil
}

func (m *sharedMockStore) FindByDeliveryID(_ context.Context, deliveryID string, taskType model.TaskType) (*model.TaskRecord, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	key := deliveryID + ":" + string(taskType)
	r, ok := m.byDeliveryID[key]
	if !ok {
		return nil, nil
	}
	return r, nil
}

func (m *sharedMockStore) ListOrphanTasks(_ context.Context, _ time.Duration) ([]*model.TaskRecord, error) {
	if m.orphanErr != nil {
		return nil, m.orphanErr
	}
	return m.orphans, nil
}

func (m *sharedMockStore) PurgeTasks(_ context.Context, _ time.Duration, _ model.TaskStatus) (int64, error) {
	return 0, nil
}

func (m *sharedMockStore) Ping(_ context.Context) error { return nil }

func (m *sharedMockStore) Close() error { return nil }
