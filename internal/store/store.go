package store

import (
	"context"
	"errors"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

var (
	ErrTaskNotFound = errors.New("任务不存在")
	ErrInvalidID    = errors.New("任务 ID 不能为空")
	ErrNilRecord    = errors.New("record 不能为 nil")
)

// Store 任务持久化接口
type Store interface {
	// CreateTask 创建任务记录
	CreateTask(ctx context.Context, record *model.TaskRecord) error

	// GetTask 按 ID 获取任务记录，未找到时返回 (nil, nil)
	GetTask(ctx context.Context, id string) (*model.TaskRecord, error)

	// UpdateTask 更新任务记录。
	// 所有 Store 实现必须设置 record.UpdatedAt 为当前 UTC 时间。调用方应注意此副作用。
	// 当目标任务不存在时返回 ErrTaskNotFound。
	UpdateTask(ctx context.Context, record *model.TaskRecord) error

	// ListTasks 列表查询任务。
	// Limit 为 0 时默认返回最多 1000 条记录。
	ListTasks(ctx context.Context, opts ListOptions) ([]*model.TaskRecord, error)

	// FindByDeliveryID 按 delivery_id + task_type 查找任务（幂等去重），未找到时返回 (nil, nil)
	FindByDeliveryID(ctx context.Context, deliveryID string, taskType model.TaskType) (*model.TaskRecord, error)

	// ListOrphanTasks 查询 pending 状态且创建时间超过 maxAge 的孤儿任务
	ListOrphanTasks(ctx context.Context, maxAge time.Duration) ([]*model.TaskRecord, error)

	// PurgeTasks 清理指定状态且早于指定时间的历史任务记录，返回清理数量
	PurgeTasks(ctx context.Context, olderThan time.Duration, status model.TaskStatus) (int64, error)

	// Ping 检测数据库连接是否可用，用于健康检查
	Ping(ctx context.Context) error

	// Close 关闭底层连接
	Close() error
}

// ListOptions 列表查询选项
type ListOptions struct {
	RepoFullName string           // 使用冗余列查询
	TaskType     model.TaskType   // 按任务类型过滤
	Status       model.TaskStatus // 按状态过滤
	Limit        int              // 限制返回数量
	Offset       int              // 偏移量
}
