package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

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
