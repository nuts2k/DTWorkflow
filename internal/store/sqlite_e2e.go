package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// SaveE2EResult 以 UPSERT 方式写入 e2e_results。
// record.ID 为空时自动生成 UUID；以 task_id 为冲突键保证同一 task 最多一行。
func (s *SQLiteStore) SaveE2EResult(ctx context.Context, record *E2EResultRecord) error {
	if record == nil {
		return fmt.Errorf("保存 E2E 结果失败: %w", ErrNilRecord)
	}
	if record.TaskID == "" {
		return fmt.Errorf("保存 E2E 结果失败: task_id 不能为空")
	}
	if record.ID == "" {
		record.ID = uuid.NewString()
	}

	issuesJSON, err := json.Marshal(record.CreatedIssues)
	if err != nil {
		issuesJSON = []byte("{}")
	}

	const query = `INSERT INTO e2e_results (
		id, task_id, repo, environment, module,
		total_cases, passed_cases, failed_cases, error_cases, skipped_cases,
		success, duration_ms, created_issues
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(task_id) DO UPDATE SET
		repo            = excluded.repo,
		environment     = excluded.environment,
		module          = excluded.module,
		total_cases     = excluded.total_cases,
		passed_cases    = excluded.passed_cases,
		failed_cases    = excluded.failed_cases,
		error_cases     = excluded.error_cases,
		skipped_cases   = excluded.skipped_cases,
		success         = excluded.success,
		duration_ms     = excluded.duration_ms,
		created_issues  = excluded.created_issues,
		updated_at      = datetime('now')`

	_, err = s.db.ExecContext(ctx, query,
		record.ID,
		record.TaskID,
		record.Repo,
		record.Environment,
		record.Module,
		record.TotalCases,
		record.PassedCases,
		record.FailedCases,
		record.ErrorCases,
		record.SkippedCases,
		boolToInt(record.Success),
		record.DurationMs,
		string(issuesJSON),
	)
	if err != nil {
		return fmt.Errorf("写入 E2E 结果失败: %w", err)
	}
	return nil
}

// GetE2EResultByTaskID 按 task_id 查询 E2E 结果，未找到返回 (nil, nil)。
func (s *SQLiteStore) GetE2EResultByTaskID(ctx context.Context, taskID string) (*E2EResultRecord, error) {
	if taskID == "" {
		return nil, fmt.Errorf("查询 E2E 结果失败: task_id 不能为空")
	}

	const query = `SELECT id, task_id, repo, environment, module,
		total_cases, passed_cases, failed_cases, error_cases, skipped_cases,
		success, duration_ms, created_issues, created_at, updated_at
		FROM e2e_results WHERE task_id = ?`

	var r E2EResultRecord
	var successInt int
	var issuesJSON string
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, query, taskID).Scan(
		&r.ID, &r.TaskID, &r.Repo, &r.Environment, &r.Module,
		&r.TotalCases, &r.PassedCases, &r.FailedCases, &r.ErrorCases, &r.SkippedCases,
		&successInt, &r.DurationMs, &issuesJSON, &createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("查询 E2E 结果失败: %w", err)
	}

	r.Success = successInt != 0
	r.CreatedIssues = make(map[string]int64)
	_ = json.Unmarshal([]byte(issuesJSON), &r.CreatedIssues)

	r.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return nil, fmt.Errorf("解析 created_at 失败: %w", err)
	}
	r.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, fmt.Errorf("解析 updated_at 失败: %w", err)
	}

	return &r, nil
}

// UpdateE2ECreatedIssues 只更新 created_issues JSON + updated_at。
// id 为 e2e_results.id（非 task_id）。记录不存在返回 ErrE2EResultNotFound。
func (s *SQLiteStore) UpdateE2ECreatedIssues(ctx context.Context, id string, issues map[string]int64) error {
	if id == "" {
		return fmt.Errorf("更新 E2E created_issues 失败: id 不能为空")
	}

	issuesJSON, err := json.Marshal(issues)
	if err != nil {
		return fmt.Errorf("序列化 created_issues 失败: %w", err)
	}

	const query = `UPDATE e2e_results
		SET created_issues = ?, updated_at = datetime('now')
		WHERE id = ?`

	result, err := s.db.ExecContext(ctx, query, string(issuesJSON), id)
	if err != nil {
		return fmt.Errorf("更新 E2E created_issues 失败: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("获取更新影响行数失败: %w", err)
	}
	if rows == 0 {
		return ErrE2EResultNotFound
	}
	return nil
}
