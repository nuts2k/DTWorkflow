package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"

	_ "modernc.org/sqlite" // 注册 SQLite 驱动
)

// 编译时确保 SQLiteStore 实现了 Store 接口
var _ Store = (*SQLiteStore)(nil)

// taskColumns 是 tasks 表的 SELECT 列列表，统一在各查询中使用
const taskColumns = `id, asynq_id, task_type, status, priority, payload, repo_full_name, pr_number,
		result, error, retry_count, max_retry, worker_id, delivery_id,
		created_at, updated_at, started_at, completed_at, triggered_by`

// SQLiteStore 基于 SQLite 的任务持久化实现
type SQLiteStore struct {
	db   *sql.DB
	once sync.Once
}

// NewSQLiteStore 创建 SQLiteStore 实例，初始化连接并执行 Schema 迁移
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dbPath == "" {
		return nil, fmt.Errorf("数据库路径不能为空")
	}

	// 非内存数据库需要确保父目录存在
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}

	// 构造 DSN，启用 WAL 模式、busy_timeout 和外键约束
	dsn := dbPath + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	if dbPath == ":memory:" {
		dsn = dbPath
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开 SQLite 数据库失败: %w", err)
	}

	// 内存数据库单独设置 PRAGMA
	// 注意：WAL 模式对内存数据库无实际效果，但设置不会报错，保持与文件模式一致
	if dbPath == ":memory:" {
		pragmas := []string{
			"PRAGMA journal_mode=WAL",
			"PRAGMA busy_timeout=5000",
			"PRAGMA foreign_keys=ON",
		}
		for _, p := range pragmas {
			if _, err := db.Exec(p); err != nil {
				db.Close()
				return nil, fmt.Errorf("设置 PRAGMA 失败 (%s): %w", p, err)
			}
		}
	}

	// 限制最大连接数为 1：
	// 1. 避免 SQLite 写竞争（SQLite 不支持并发写）
	// 2. 对于内存数据库（:memory:），这是必需的：database/sql 的每个新连接会创建独立的内存实例，多连接会导致数据不共享
	// 3. 确保连接级 PRAGMA（如 foreign_keys、journal_mode）在单一连接上正确生效
	db.SetMaxOpenConns(1)

	if err := RunMigrations(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("执行 Schema 迁移失败: %w", err)
	}

	slog.Info("SQLiteStore 初始化成功", "path", dbPath)
	return &SQLiteStore{db: db}, nil
}

// CreateTask 创建任务记录
func (s *SQLiteStore) CreateTask(ctx context.Context, record *model.TaskRecord) error {
	if record == nil {
		return fmt.Errorf("创建任务记录失败: %w", ErrNilRecord)
	}
	if record.ID == "" {
		return fmt.Errorf("创建任务记录失败: %w", ErrInvalidID)
	}
	if !record.TaskType.IsValid() {
		return fmt.Errorf("创建任务记录失败: 无效的任务类型 %q", record.TaskType)
	}
	if !record.Status.IsValid() {
		return fmt.Errorf("创建任务记录失败: 无效的任务状态 %q", record.Status)
	}

	payloadJSON, err := json.Marshal(record.Payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}

	const query = `INSERT INTO tasks (
		id, asynq_id, task_type, status, priority, payload, repo_full_name, pr_number,
		result, error, retry_count, max_retry, worker_id, delivery_id,
		created_at, updated_at, started_at, completed_at, triggered_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err = s.db.ExecContext(ctx, query,
		record.ID,
		record.AsynqID,
		record.TaskType,
		record.Status,
		record.Priority,
		string(payloadJSON),
		record.RepoFullName,
		int64ToNull(record.PRNumber),
		record.Result,
		record.Error,
		record.RetryCount,
		record.MaxRetry,
		record.WorkerID,
		record.DeliveryID,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
		record.UpdatedAt.UTC().Format(time.RFC3339Nano),
		timeToNullString(record.StartedAt),
		timeToNullString(record.CompletedAt),
		record.TriggeredBy,
	)
	if err != nil {
		return fmt.Errorf("插入任务记录失败: %w", err)
	}
	return nil
}

// GetTask 按 ID 获取任务记录，未找到返回 nil, nil
func (s *SQLiteStore) GetTask(ctx context.Context, id string) (*model.TaskRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("查询任务失败: %w", ErrInvalidID)
	}

	query := `SELECT ` + taskColumns + `
	FROM tasks WHERE id = ?`

	row := s.db.QueryRowContext(ctx, query, id)
	record, err := scanTaskRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询任务失败: %w", err)
	}
	return record, nil
}

// UpdateTask 更新任务记录，自动刷新 updated_at
func (s *SQLiteStore) UpdateTask(ctx context.Context, record *model.TaskRecord) error {
	if record == nil {
		return fmt.Errorf("更新任务记录失败: %w", ErrNilRecord)
	}
	if record.ID == "" {
		return fmt.Errorf("更新任务记录失败: %w", ErrInvalidID)
	}
	if !record.TaskType.IsValid() {
		return fmt.Errorf("更新任务记录失败: 无效的任务类型 %q", record.TaskType)
	}
	if !record.Status.IsValid() {
		return fmt.Errorf("更新任务记录失败: 无效的任务状态 %q", record.Status)
	}
	payloadJSON, err := json.Marshal(record.Payload)
	if err != nil {
		return fmt.Errorf("序列化 payload 失败: %w", err)
	}

	// 在写入前刷新 UpdatedAt，确保调用方也能看到最新值
	record.UpdatedAt = time.Now().UTC()

	const query = `UPDATE tasks SET
		asynq_id = ?, task_type = ?, status = ?, priority = ?, payload = ?,
		repo_full_name = ?, pr_number = ?, result = ?, error = ?, retry_count = ?, max_retry = ?,
		worker_id = ?, delivery_id = ?, updated_at = ?,
		started_at = ?, completed_at = ?, triggered_by = ?
	WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query,
		record.AsynqID,
		record.TaskType,
		record.Status,
		record.Priority,
		string(payloadJSON),
		record.RepoFullName,
		int64ToNull(record.PRNumber),
		record.Result,
		record.Error,
		record.RetryCount,
		record.MaxRetry,
		record.WorkerID,
		record.DeliveryID,
		record.UpdatedAt.Format(time.RFC3339Nano),
		timeToNullString(record.StartedAt),
		timeToNullString(record.CompletedAt),
		record.TriggeredBy,
		record.ID,
	)
	if err != nil {
		return fmt.Errorf("更新任务记录失败: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取更新影响行数失败: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("更新任务记录失败: %w", ErrTaskNotFound)
	}
	return nil
}

// defaultMaxLimit 是 ListTasks 无 Limit 时的安全上限，防止无限返回
const defaultMaxLimit = 1000

// ListTasks 按条件列出任务记录
func (s *SQLiteStore) ListTasks(ctx context.Context, opts ListOptions) ([]*model.TaskRecord, error) {
	if opts.Limit < 0 {
		return nil, fmt.Errorf("Limit 不能为负数: %d", opts.Limit)
	}
	if opts.Offset < 0 {
		return nil, fmt.Errorf("Offset 不能为负数: %d", opts.Offset)
	}
	if opts.TaskType != "" && !opts.TaskType.IsValid() {
		return nil, fmt.Errorf("无效的任务类型: %s", opts.TaskType)
	}
	if opts.Status != "" && !opts.Status.IsValid() {
		return nil, fmt.Errorf("无效的任务状态: %s", opts.Status)
	}

	query := `SELECT ` + taskColumns + `
	FROM tasks WHERE 1=1`

	args := []any{}
	if opts.RepoFullName != "" {
		query += " AND repo_full_name = ?"
		args = append(args, opts.RepoFullName)
	}
	if opts.TaskType != "" {
		query += " AND task_type = ?"
		args = append(args, opts.TaskType)
	}
	if opts.Status != "" {
		query += " AND status = ?"
		args = append(args, opts.Status)
	}

	query += " ORDER BY created_at DESC"

	if opts.Limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, opts.Limit, opts.Offset)
	} else {
		// 安全上限，防止无限返回
		query += " LIMIT ? OFFSET ?"
		args = append(args, defaultMaxLimit, opts.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("列表查询任务失败: %w", err)
	}
	defer rows.Close()

	var records []*model.TaskRecord
	for rows.Next() {
		record, err := scanTaskRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描任务记录失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务记录失败: %w", err)
	}
	return records, nil
}

// FindByDeliveryID 按 delivery_id + task_type 查找任务（幂等去重），未找到返回 nil, nil
func (s *SQLiteStore) FindByDeliveryID(ctx context.Context, deliveryID string, taskType model.TaskType) (*model.TaskRecord, error) {
	// 空 deliveryID 不可能匹配有效的幂等记录，直接返回未找到
	if deliveryID == "" {
		return nil, nil
	}

	query := `SELECT ` + taskColumns + `
	FROM tasks WHERE delivery_id = ? AND task_type = ? LIMIT 1`

	row := s.db.QueryRowContext(ctx, query, deliveryID, taskType)
	record, err := scanTaskRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("按 delivery_id 查询任务失败: %w", err)
	}
	return record, nil
}

// ListOrphanTasks 查询 pending 状态且创建时间超过 maxAge 的孤儿任务
func (s *SQLiteStore) ListOrphanTasks(ctx context.Context, maxAge time.Duration) ([]*model.TaskRecord, error) {
	threshold := time.Now().UTC().Add(-maxAge).Format(time.RFC3339Nano)

	query := `SELECT ` + taskColumns + `
	FROM tasks WHERE status = ? AND created_at < ?
	ORDER BY created_at ASC
	LIMIT ?`

	rows, err := s.db.QueryContext(ctx, query, string(model.TaskStatusPending), threshold, defaultMaxLimit)
	if err != nil {
		return nil, fmt.Errorf("查询孤儿任务失败: %w", err)
	}
	defer rows.Close()

	var records []*model.TaskRecord
	for rows.Next() {
		record, err := scanTaskRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描孤儿任务记录失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历任务记录失败: %w", err)
	}
	return records, nil
}

// PurgeTasks 清理指定状态且早于指定时间的历史任务记录，返回清理数量
func (s *SQLiteStore) PurgeTasks(ctx context.Context, olderThan time.Duration, status model.TaskStatus) (int64, error) {
	if olderThan <= 0 {
		return 0, fmt.Errorf("olderThan 必须为正数，当前值: %v", olderThan)
	}
	if !status.IsValid() {
		return 0, fmt.Errorf("无效的任务状态: %s", status)
	}
	cutoff := time.Now().Add(-olderThan)
	result, err := s.db.ExecContext(ctx, "DELETE FROM tasks WHERE status = ? AND created_at < ?", string(status), cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, fmt.Errorf("清理历史任务失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("获取清理影响行数失败: %w", err)
	}
	return affected, nil
}

// SaveReviewResult 持久化评审结果记录到 review_results 表
func (s *SQLiteStore) SaveReviewResult(ctx context.Context, record *model.ReviewRecord) error {
	if record == nil {
		return fmt.Errorf("保存评审结果失败: %w", ErrNilRecord)
	}
	if record.ID == "" {
		return fmt.Errorf("保存评审结果失败: %w", ErrInvalidID)
	}

	// SQLite 无 boolean 类型，使用 int 存储
	parseFailed := 0
	if record.ParseFailed {
		parseFailed = 1
	}

	const query = `INSERT INTO review_results (
		id, task_id, repo_full_name, pr_number, head_sha,
		verdict, summary, issues_json,
		issue_count, critical_count, error_count, warning_count, info_count,
		cost_usd, duration_ms, gitea_review_id,
		parse_failed, writeback_error, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query,
		record.ID,
		record.TaskID,
		record.RepoFullName,
		record.PRNumber,
		record.HeadSHA,
		record.Verdict,
		record.Summary,
		record.IssuesJSON,
		record.IssueCount,
		record.CriticalCount,
		record.ErrorCount,
		record.WarningCount,
		record.InfoCount,
		record.CostUSD,
		record.DurationMs,
		record.GiteaReviewID,
		parseFailed,
		record.WritebackError,
		record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("插入评审结果记录失败: %w", err)
	}
	return nil
}

// reviewResultColumns 是 review_results 表的 SELECT 列列表
const reviewResultColumns = `id, task_id, repo_full_name, pr_number, head_sha,
		verdict, summary, issues_json,
		issue_count, critical_count, error_count, warning_count, info_count,
		cost_usd, duration_ms, gitea_review_id,
		parse_failed, writeback_error, created_at`

// GetReviewResult 按 ID 获取评审结果记录，未找到时返回错误
func (s *SQLiteStore) GetReviewResult(ctx context.Context, id string) (*model.ReviewRecord, error) {
	if id == "" {
		return nil, fmt.Errorf("查询评审结果失败: %w", ErrInvalidID)
	}

	query := `SELECT ` + reviewResultColumns + `
	FROM review_results WHERE id = ?`

	row := s.db.QueryRowContext(ctx, query, id)
	r, err := scanReviewRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("评审结果不存在: %s", id)
	}
	if err != nil {
		return nil, fmt.Errorf("查询评审结果失败: %w", err)
	}
	return r, nil
}

// ListReviewResults 按仓库全名列出评审结果，按创建时间倒序
func (s *SQLiteStore) ListReviewResults(ctx context.Context, repoFullName string, limit, offset int) ([]*model.ReviewRecord, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	query := `SELECT ` + reviewResultColumns + `
	FROM review_results WHERE repo_full_name = ?
	ORDER BY created_at DESC LIMIT ? OFFSET ?`

	rows, err := s.db.QueryContext(ctx, query, repoFullName, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("列表查询评审结果失败: %w", err)
	}
	defer rows.Close()

	var results []*model.ReviewRecord
	for rows.Next() {
		r, err := scanReviewRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描评审结果失败: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历评审结果失败: %w", err)
	}
	return results, nil
}

// ListReviewResultsByTimeRange 按时间范围查询所有仓库的评审结果
func (s *SQLiteStore) ListReviewResultsByTimeRange(ctx context.Context, start, end time.Time) ([]*model.ReviewRecord, error) {
	query := `SELECT ` + reviewResultColumns + `
	FROM review_results
	WHERE julianday(created_at) >= julianday(?) AND julianday(created_at) < julianday(?)
	ORDER BY created_at DESC
	LIMIT 2000`

	rows, err := s.db.QueryContext(ctx, query, start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, fmt.Errorf("按时间范围查询评审结果失败: %w", err)
	}
	defer rows.Close()

	var results []*model.ReviewRecord
	for rows.Next() {
		r, err := scanReviewRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描评审结果失败: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历评审结果失败: %w", err)
	}
	return results, nil
}

// scanReviewRecord 从单行结果扫描 ReviewRecord
func scanReviewRecord(row rowScanner) (*model.ReviewRecord, error) {
	var (
		r              model.ReviewRecord
		taskID         sql.NullString
		issuesJSON     sql.NullString
		writebackError sql.NullString
		parseFailed    int
		createdAt      string
	)

	err := row.Scan(
		&r.ID, &taskID, &r.RepoFullName, &r.PRNumber, &r.HeadSHA,
		&r.Verdict, &r.Summary, &issuesJSON,
		&r.IssueCount, &r.CriticalCount, &r.ErrorCount, &r.WarningCount, &r.InfoCount,
		&r.CostUSD, &r.DurationMs, &r.GiteaReviewID,
		&parseFailed, &writebackError, &createdAt,
	)
	if err != nil {
		return nil, err
	}

	r.ParseFailed = parseFailed != 0
	if taskID.Valid {
		r.TaskID = taskID.String
	}
	if issuesJSON.Valid {
		r.IssuesJSON = issuesJSON.String
	}
	if writebackError.Valid {
		r.WritebackError = writebackError.String
	}

	r.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("解析 created_at 失败: %w", err)
	}
	return &r, nil
}

// Ping 检测数据库连接是否可用，用于健康检查
func (s *SQLiteStore) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}

// Close 关闭数据库连接，幂等（多次调用安全）
func (s *SQLiteStore) Close() error {
	var closeErr error
	s.once.Do(func() {
		closeErr = s.db.Close()
	})
	return closeErr
}

// rowScanner 抽象 *sql.Row 和 *sql.Rows 的共同扫描接口
type rowScanner interface {
	Scan(dest ...any) error
}

// scanTaskRecord 从单行结果扫描 TaskRecord
func scanTaskRecord(row rowScanner) (*model.TaskRecord, error) {
	var (
		r           model.TaskRecord
		payloadJSON string
		createdAt   string
		updatedAt   string
		startedAt   sql.NullString
		completedAt sql.NullString
	)

	var prNumber sql.NullInt64

	err := row.Scan(
		&r.ID, &r.AsynqID, &r.TaskType, &r.Status, &r.Priority,
		&payloadJSON, &r.RepoFullName, &prNumber,
		&r.Result, &r.Error, &r.RetryCount, &r.MaxRetry,
		&r.WorkerID, &r.DeliveryID,
		&createdAt, &updatedAt, &startedAt, &completedAt,
		&r.TriggeredBy,
	)
	if err != nil {
		return nil, err
	}

	if prNumber.Valid {
		r.PRNumber = prNumber.Int64
	}

	if err := json.Unmarshal([]byte(payloadJSON), &r.Payload); err != nil {
		return nil, fmt.Errorf("反序列化 payload 失败: %w", err)
	}

	r.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("解析 created_at 失败: %w", err)
	}
	r.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析 updated_at 失败: %w", err)
	}

	if startedAt.Valid && startedAt.String != "" {
		t, err := parseTime(startedAt.String)
		if err != nil {
			return nil, fmt.Errorf("解析 started_at 失败: %w", err)
		}
		r.StartedAt = &t
	}
	if completedAt.Valid && completedAt.String != "" {
		t, err := parseTime(completedAt.String)
		if err != nil {
			return nil, fmt.Errorf("解析 completed_at 失败: %w", err)
		}
		r.CompletedAt = &t
	}

	return &r, nil
}

// timeToNullString 将 *time.Time 转换为可 NULL 的字符串
func timeToNullString(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

// int64ToNull 将 int64 转换为可 NULL 的值（0 值存储为 NULL）
func int64ToNull(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}

// FindActivePRTasks 查找同一 PR 的活跃评审任务（pending/queued/running）
func (s *SQLiteStore) FindActivePRTasks(ctx context.Context, repoFullName string, prNumber int64, taskType model.TaskType) ([]*model.TaskRecord, error) {
	query := `SELECT ` + taskColumns + `
	FROM tasks
	WHERE repo_full_name = ?
	  AND pr_number = ?
	  AND task_type = ?
	  AND status IN (?, ?, ?)
	ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, repoFullName, prNumber, string(taskType),
		string(model.TaskStatusPending), string(model.TaskStatusQueued), string(model.TaskStatusRunning))
	if err != nil {
		return nil, fmt.Errorf("查找活跃 PR 任务失败: %w", err)
	}
	defer rows.Close()

	var records []*model.TaskRecord
	for rows.Next() {
		record, err := scanTaskRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描活跃 PR 任务失败: %w", err)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活跃 PR 任务失败: %w", err)
	}
	return records, nil
}

// FindActiveIssueTasks 查找同一 Issue 的活跃任务（pending/queued/running）。
func (s *SQLiteStore) FindActiveIssueTasks(ctx context.Context, repoFullName string, issueNumber int64, taskType model.TaskType) ([]*model.TaskRecord, error) {
	query := `SELECT ` + taskColumns + `
	FROM tasks
	WHERE repo_full_name = ?
	  AND task_type = ?
	  AND status IN (?, ?, ?)
	ORDER BY created_at ASC`

	rows, err := s.db.QueryContext(ctx, query, repoFullName, string(taskType),
		string(model.TaskStatusPending), string(model.TaskStatusQueued), string(model.TaskStatusRunning))
	if err != nil {
		return nil, fmt.Errorf("查找活跃 Issue 任务失败: %w", err)
	}
	defer rows.Close()

	var records []*model.TaskRecord
	for rows.Next() {
		record, err := scanTaskRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描活跃 Issue 任务失败: %w", err)
		}
		if record.Payload.IssueNumber == issueNumber {
			records = append(records, record)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历活跃 Issue 任务失败: %w", err)
	}
	return records, nil
}

// GetLatestAnalysisByIssue 返回指定仓库 + Issue 最新一条 analyze_issue succeeded 任务记录。
// 未找到时返回 (nil, nil)。
//
// 使用 json_extract 在 SQL 层精准匹配 payload.issue_number，与 migrations.go 中
// `json_extract(payload, '$.pr_number')` 保持一致：
//   - 不依赖 JSON 字段顺序假设（早期 LIKE pattern 假设 issue_number 后必紧跟 ',' 或 '}'，
//     一旦上游把字段挪位或更换 marshal 库即静默漏匹配）。
//   - 严格类型匹配（json_extract 返回 INTEGER），与参数类型一致。
//   - modernc.org/sqlite 默认启用 json1 扩展，无需额外开关。
func (s *SQLiteStore) GetLatestAnalysisByIssue(ctx context.Context, repoFullName string, issueNumber int64) (*model.TaskRecord, error) {
	query := `SELECT ` + taskColumns + `
	FROM tasks
	WHERE repo_full_name = ?
	  AND task_type = ?
	  AND status = ?
	  AND json_extract(payload, '$.issue_number') = ?
	ORDER BY created_at DESC
	LIMIT 1`

	row := s.db.QueryRowContext(ctx, query, repoFullName,
		string(model.TaskTypeAnalyzeIssue), string(model.TaskStatusSucceeded),
		issueNumber)
	record, err := scanTaskRecord(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询最新分析任务失败: %w", err)
	}
	return record, nil
}

// HasNewerReviewTask 检查是否存在比指定时间更新的同 PR 非终态评审任务
func (s *SQLiteStore) HasNewerReviewTask(ctx context.Context, repoFullName string, prNumber int64, afterCreatedAt time.Time) (bool, error) {
	query := `SELECT EXISTS(
		SELECT 1 FROM tasks
		WHERE repo_full_name = ?
		  AND pr_number = ?
		  AND task_type = ?
		  AND status NOT IN (?, ?)
		  AND created_at > ?
	)`

	var exists bool
	err := s.db.QueryRowContext(ctx, query, repoFullName, prNumber,
		string(model.TaskTypeReviewPR),
		string(model.TaskStatusCancelled), string(model.TaskStatusFailed),
		afterCreatedAt.UTC().Format(time.RFC3339Nano)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("检查更新评审任务失败: %w", err)
	}
	return exists, nil
}

// parseTime 尝试多种格式解析时间字符串，优先使用高精度格式
func parseTime(s string) (time.Time, error) {
	// 含时区信息的格式，使用 time.Parse（自动解析时区）
	tzFormats := []string{
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, f := range tzFormats {
		if t, err := time.Parse(f, s); err == nil {
			return t, nil
		}
	}

	// 对于不含时区信息的格式，显式指定 UTC
	noTZFormats := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, f := range noTZFormats {
		if t, err := time.ParseInLocation(f, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("无法解析时间字符串: %q", s)
}
