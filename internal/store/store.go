package store

import (
	"context"
	"errors"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

var (
	ErrTaskNotFound          = errors.New("任务不存在")
	ErrInvalidID             = errors.New("任务 ID 不能为空")
	ErrNilRecord             = errors.New("record 不能为 nil")
	ErrTestGenResultNotFound = errors.New("测试生成结果不存在")
	ErrE2EResultNotFound     = errors.New("E2E 结果不存在")
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

	// FindActivePRTasks 查找同一 PR 的活跃评审任务（pending/queued/running）
	// 返回按 created_at 升序排列的任务列表（最旧的在前）
	FindActivePRTasks(ctx context.Context, repoFullName string, prNumber int64, taskType model.TaskType) ([]*model.TaskRecord, error)

	// FindActiveIssueTasks 查找同一 Issue 的活跃任务（pending/queued/running）
	// 返回按 created_at 升序排列的任务列表（最旧的在前）
	FindActiveIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) ([]*model.TaskRecord, error)

	// FindActiveGenTestsTasks 查找同一仓库 + module 粒度的活跃 gen_tests 任务
	// （pending/queued/running/retrying）。
	// module 为空时匹配"整仓生成"的任务（payload.module 字段缺失的记录）。
	// 返回按 created_at 升序排列的任务列表（最旧的在前）。
	FindActiveGenTestsTasks(ctx context.Context, repoFullName, module string) ([]*model.TaskRecord, error)

	// GetLatestAnalysisByIssue 返回指定仓库 + Issue 最新一条 analyze_issue succeeded 任务记录。
	// 未找到时返回 (nil, nil)。
	GetLatestAnalysisByIssue(ctx context.Context, repoFullName string, issueNumber int64) (*model.TaskRecord, error)

	// HasNewerReviewTask 检查是否存在比指定时间更新的同 PR 评审任务
	// 用于回写前的 staleness 检查
	HasNewerReviewTask(ctx context.Context, repoFullName string, prNumber int64, afterCreatedAt time.Time) (bool, error)

	// SaveReviewResult 持久化评审结果记录
	SaveReviewResult(ctx context.Context, record *model.ReviewRecord) error

	// GetReviewResult 按 ID 获取评审结果记录，未找到时返回错误
	GetReviewResult(ctx context.Context, id string) (*model.ReviewRecord, error)

	// ListReviewResults 按仓库全名列出评审结果，按创建时间倒序
	ListReviewResults(ctx context.Context, repoFullName string, limit, offset int) ([]*model.ReviewRecord, error)

	// ListReviewResultsByTimeRange 按时间范围查询所有仓库的评审结果。
	// start inclusive, end exclusive。按 created_at DESC 排序。硬上限 2000 条。
	ListReviewResultsByTimeRange(ctx context.Context, start, end time.Time) ([]*model.ReviewRecord, error)

	// SaveTestGenResult 以 UPSERT 方式持久化 gen_tests 任务产出记录。
	// 当 record.ID 为空时内部生成 UUID；以 task_id 为冲突键保证同一 task 最多一行。
	// 自由文本字段（failure_reason / output_json）超过 2 KB 将被截断并追加 "...(truncated)" 标记，
	// 避免 SQLite 单行过大。调用方无需预先判断记录是否存在。
	SaveTestGenResult(ctx context.Context, record *TestGenResultRecord) error

	// UpdateTestGenResultReviewEnqueued 把 test_gen_results.review_enqueued 单字段刷成 true
	// 并更新 updated_at。若对应 task_id 记录不存在返回 ErrTestGenResultNotFound。
	//
	// 设计动机：两阶段 UPSERT 中，阶段 2（review 入队成功后）只需要翻转 review_enqueued
	// 标志。用 SaveTestGenResult 走全字段 UPSERT 会把其它字段（例如 PR 编号、成功标志）
	// 全部复写为阶段 1 的值，如果期间有其它异步组件（例如后续 M4.3 的 webhook 反查）
	// 更新过同一行，这些变更会被阶段 2 静默覆盖。partial UPDATE 语义更安全。
	UpdateTestGenResultReviewEnqueued(ctx context.Context, taskID string) error

	// GetTestGenResultByTaskID 按 task_id 查询测试生成结果记录。
	// 未找到时返回 (nil, nil)，与 GetTask 等既有查询接口保持一致。
	GetTestGenResultByTaskID(ctx context.Context, taskID string) (*TestGenResultRecord, error)

	// ListActiveGenTestsModules 返回指定仓库下所有活跃 gen_tests 任务（status IN queued/running/retrying）
	// 的 module 列表（包含空字符串，表示整仓粒度），供 webhook 拦截层与 test.ModuleKey 比对使用。
	// 原样返回不去重，由调用侧负责规范化。
	ListActiveGenTestsModules(ctx context.Context, repoFullName string) ([]string, error)

	// FindActiveTasksByModule 查找同一仓库 + module 粒度的活跃任务
	// （pending/queued/running/retrying）。泛化自 FindActiveGenTestsTasks。
	// module 为空时匹配 payload.module 字段缺失的记录。
	FindActiveTasksByModule(ctx context.Context, repoFullName, module string, taskType model.TaskType) ([]*model.TaskRecord, error)

	// ListActiveModules 返回指定仓库下活跃任务的 module 列表。泛化自 ListActiveGenTestsModules。
	ListActiveModules(ctx context.Context, repoFullName string, taskType model.TaskType) ([]string, error)

	// SaveE2EResult 以 UPSERT 方式写入 e2e_results（阶段 1：聚合统计，created_issues={}）。
	// record.ID 为空时内部生成 UUID。以 task_id 为冲突键。
	SaveE2EResult(ctx context.Context, record *E2EResultRecord) error

	// GetE2EResultByTaskID 按 task_id 查询 E2E 结果。未找到返回 (nil, nil)。
	GetE2EResultByTaskID(ctx context.Context, taskID string) (*E2EResultRecord, error)

	// UpdateE2ECreatedIssues 阶段 2：只更新 created_issues JSON + updated_at。
	// id 为 e2e_results.id（非 task_id）。记录不存在返回 ErrE2EResultNotFound。
	UpdateE2ECreatedIssues(ctx context.Context, id string, issues map[string]int64) error

	// Ping 检测数据库连接是否可用，用于健康检查
	Ping(ctx context.Context) error

	// --- M6.1 迭代式评审修复 ---

	// FindActiveIterationSession 查找指定 PR 的活跃迭代会话（status 非 completed/exhausted）。
	// 未找到返回 (nil, nil)。
	FindActiveIterationSession(ctx context.Context, repoFullName string, prNumber int64) (*IterationSessionRecord, error)

	// FindOrCreateIterationSession 查找活跃会话，不存在则创建（status=idle）。
	FindOrCreateIterationSession(ctx context.Context, repoFullName string, prNumber int64, headBranch string, maxRounds int) (*IterationSessionRecord, error)

	// UpdateIterationSession 更新会话记录。
	UpdateIterationSession(ctx context.Context, session *IterationSessionRecord) error

	// CreateIterationRound 创建新轮次记录，自动设置 started_at。
	CreateIterationRound(ctx context.Context, round *IterationRoundRecord) error

	// UpdateIterationRound 更新轮次记录。
	UpdateIterationRound(ctx context.Context, round *IterationRoundRecord) error

	// GetLatestRound 获取会话的最新轮次。未找到返回 (nil, nil)。
	GetLatestRound(ctx context.Context, sessionID int64) (*IterationRoundRecord, error)

	// CountNonRecoveryRounds 统计会话的非恢复轮次数。
	CountNonRecoveryRounds(ctx context.Context, sessionID int64) (int, error)

	// GetRecentRoundsIssuesFixed 获取最近 N 个非恢复轮次的 issues_fixed 列表（从新到旧）。
	GetRecentRoundsIssuesFixed(ctx context.Context, sessionID int64, n int) ([]int, error)

	// GetCompletedRoundsForSession 获取会话的所有已完成轮次（按 round_number 升序）。
	GetCompletedRoundsForSession(ctx context.Context, sessionID int64) ([]*IterationRoundRecord, error)

	// FindActivePRTasksMulti 查找同一 PR 的多种类型活跃任务（pending/queued/running）。
	// 返回按 created_at 升序排列的任务列表。
	FindActivePRTasksMulti(ctx context.Context, repoFullName string, prNumber int64, taskTypes []model.TaskType) ([]*model.TaskRecord, error)

	// Close 关闭底层连接
	Close() error
}

// TestGenResultRecord 对应 test_gen_results 表的单行，用于 M4.2 gen_tests 任务产出持久化。
// 字段与 SQL 列一一对齐；SQLite 无 boolean 类型，bool 字段在存储层映射为 0/1。
type TestGenResultRecord struct {
	ID                 string
	TaskID             string // 任务 purge 后可能因 ON DELETE SET NULL 变为空
	RepoFullName       string
	Module             string
	Framework          string
	BaseRef            string
	BranchName         string
	CommitSHA          string
	PRNumber           int64
	PRURL              string
	Success            bool
	InfoSufficient     bool
	VerificationPassed bool
	FailureCategory    string
	FailureReason      string
	GeneratedCount     int
	CommittedCount     int
	SkippedCount       int
	TestPassed         int
	TestFailed         int
	TestDurationMs     int64
	ReviewEnqueued     bool
	CostUSD            float64
	DurationMs         int64
	OutputJSON         string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// E2EResultRecord 对应 e2e_results 表的单行。
type E2EResultRecord struct {
	ID            string
	TaskID        string
	Repo          string
	Environment   string
	Module        string
	TotalCases    int
	PassedCases   int
	FailedCases   int
	ErrorCases    int
	SkippedCases  int
	Success       bool
	DurationMs    int64
	CreatedIssues map[string]int64 // case_path → issue_number（JSON 序列化存储）
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// IterationSessionRecord 对应 iteration_sessions 表。
type IterationSessionRecord struct {
	ID               int64
	RepoFullName     string
	PRNumber         int64
	HeadBranch       string
	Status           string // idle/reviewing/fixing/completed/exhausted
	CurrentRound     int
	MaxRounds        int
	TotalIssuesFound int
	TotalIssuesFixed int
	LastError        string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IterationRoundRecord 对应 iteration_rounds 表。
type IterationRoundRecord struct {
	ID            int64
	SessionID     int64
	RoundNumber   int
	ReviewTaskID  string
	FixTaskID     string
	IssuesFound   int
	IssuesFixed   int
	FixReportPath string
	FixSummary    string
	IsRecovery    bool
	StartedAt     time.Time
	CompletedAt   *time.Time
}

// ListOptions 列表查询选项
type ListOptions struct {
	RepoFullName string           // 使用冗余列查询
	TaskType     model.TaskType   // 按任务类型过滤
	Status       model.TaskStatus // 按状态过滤
	Limit        int              // 限制返回数量
	Offset       int              // 偏移量
}
