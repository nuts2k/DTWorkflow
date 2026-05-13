package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/model"
)

func (s *SQLiteStore) FindActiveIterationSession(ctx context.Context, repoFullName string, prNumber int64) (*IterationSessionRecord, error) {
	const query = `
		SELECT id, repo_full_name, pr_number, head_branch, status,
		       current_round, max_rounds, total_issues_found, total_issues_fixed,
		       last_error, created_at, updated_at
		FROM iteration_sessions
		WHERE repo_full_name = ? AND pr_number = ?
		  AND status NOT IN ('completed', 'exhausted')
		LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, repoFullName, prNumber)
	return s.scanIterationSession(row)
}

func (s *SQLiteStore) FindOrCreateIterationSession(ctx context.Context, repoFullName string, prNumber int64, headBranch string, maxRounds int) (*IterationSessionRecord, error) {
	existing, err := s.FindActiveIterationSession(ctx, repoFullName, prNumber)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}
	const insert = `
		INSERT INTO iteration_sessions (repo_full_name, pr_number, head_branch, status, max_rounds)
		VALUES (?, ?, ?, 'idle', ?)`
	result, err := s.db.ExecContext(ctx, insert, repoFullName, prNumber, headBranch, maxRounds)
	if err != nil {
		return nil, fmt.Errorf("创建迭代会话: %w", err)
	}
	id, _ := result.LastInsertId()
	return s.getIterationSessionByID(ctx, id)
}

func (s *SQLiteStore) UpdateIterationSession(ctx context.Context, session *IterationSessionRecord) error {
	if session == nil {
		return fmt.Errorf("session 不能为 nil")
	}
	const query = `
		UPDATE iteration_sessions
		SET status = ?, current_round = ?, total_issues_found = ?,
		    total_issues_fixed = ?, last_error = ?, updated_at = datetime('now')
		WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query,
		session.Status, session.CurrentRound, session.TotalIssuesFound,
		session.TotalIssuesFixed, session.LastError, session.ID)
	return err
}

func (s *SQLiteStore) CreateIterationRound(ctx context.Context, round *IterationRoundRecord) error {
	if round == nil {
		return fmt.Errorf("round 不能为 nil")
	}
	const query = `
		INSERT INTO iteration_rounds (session_id, round_number, review_task_id, fix_task_id,
		            issues_found, issues_fixed, fix_report_path, is_recovery)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := s.db.ExecContext(ctx, query,
		round.SessionID, round.RoundNumber, round.ReviewTaskID, round.FixTaskID,
		round.IssuesFound, round.IssuesFixed, round.FixReportPath, boolToInt(round.IsRecovery))
	if err != nil {
		return fmt.Errorf("创建迭代轮次: %w", err)
	}
	id, _ := result.LastInsertId()
	round.ID = id
	return nil
}

func (s *SQLiteStore) UpdateIterationRound(ctx context.Context, round *IterationRoundRecord) error {
	if round == nil {
		return fmt.Errorf("round 不能为 nil")
	}
	const query = `
		UPDATE iteration_rounds
		SET review_task_id = ?, fix_task_id = ?, issues_found = ?,
		    issues_fixed = ?, fix_report_path = ?, completed_at = ?
		WHERE id = ?`
	var completedAt interface{}
	if round.CompletedAt != nil {
		completedAt = round.CompletedAt.UTC().Format(time.DateTime)
	}
	_, err := s.db.ExecContext(ctx, query,
		round.ReviewTaskID, round.FixTaskID, round.IssuesFound,
		round.IssuesFixed, round.FixReportPath, completedAt, round.ID)
	return err
}

func (s *SQLiteStore) GetLatestRound(ctx context.Context, sessionID int64) (*IterationRoundRecord, error) {
	const query = `
		SELECT id, session_id, round_number, review_task_id, fix_task_id,
		       issues_found, issues_fixed, fix_report_path, is_recovery,
		       started_at, completed_at
		FROM iteration_rounds
		WHERE session_id = ?
		ORDER BY round_number DESC
		LIMIT 1`
	row := s.db.QueryRowContext(ctx, query, sessionID)
	return s.scanIterationRound(row)
}

func (s *SQLiteStore) CountNonRecoveryRounds(ctx context.Context, sessionID int64) (int, error) {
	const query = `SELECT COUNT(*) FROM iteration_rounds WHERE session_id = ? AND is_recovery = 0`
	var count int
	err := s.db.QueryRowContext(ctx, query, sessionID).Scan(&count)
	return count, err
}

func (s *SQLiteStore) GetRecentRoundsIssuesFixed(ctx context.Context, sessionID int64, n int) ([]int, error) {
	const query = `
		SELECT issues_fixed FROM iteration_rounds
		WHERE session_id = ? AND is_recovery = 0
		ORDER BY round_number DESC
		LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, sessionID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []int
	for rows.Next() {
		var fixed int
		if err := rows.Scan(&fixed); err != nil {
			return nil, err
		}
		result = append(result, fixed)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) FindActivePRTasksMulti(ctx context.Context, repoFullName string, prNumber int64, taskTypes []model.TaskType) ([]*model.TaskRecord, error) {
	if len(taskTypes) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(taskTypes))
	args := make([]interface{}, 0, len(taskTypes)+2)
	args = append(args, repoFullName, prNumber)
	for i, tt := range taskTypes {
		placeholders[i] = "?"
		args = append(args, string(tt))
	}
	query := fmt.Sprintf(`
		SELECT id, asynq_id, task_type, status, priority, payload,
		       repo_full_name, result, error, retry_count, max_retry,
		       worker_id, delivery_id, created_at, updated_at,
		       started_at, completed_at, pr_number, triggered_by
		FROM tasks
		WHERE repo_full_name = ? AND pr_number = ?
		  AND task_type IN (%s)
		  AND status IN ('pending', 'queued', 'running')
		ORDER BY created_at ASC`,
		strings.Join(placeholders, ","))
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询活跃 PR 多类型任务: %w", err)
	}
	defer rows.Close()
	var tasks []*model.TaskRecord
	for rows.Next() {
		record, scanErr := scanTaskRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		tasks = append(tasks, record)
	}
	return tasks, rows.Err()
}

// --- 内部辅助 ---

func (s *SQLiteStore) getIterationSessionByID(ctx context.Context, id int64) (*IterationSessionRecord, error) {
	const query = `
		SELECT id, repo_full_name, pr_number, head_branch, status,
		       current_round, max_rounds, total_issues_found, total_issues_fixed,
		       last_error, created_at, updated_at
		FROM iteration_sessions WHERE id = ?`
	row := s.db.QueryRowContext(ctx, query, id)
	return s.scanIterationSession(row)
}

func (s *SQLiteStore) scanIterationSession(row *sql.Row) (*IterationSessionRecord, error) {
	var r IterationSessionRecord
	var createdAt, updatedAt string
	err := row.Scan(&r.ID, &r.RepoFullName, &r.PRNumber, &r.HeadBranch, &r.Status,
		&r.CurrentRound, &r.MaxRounds, &r.TotalIssuesFound, &r.TotalIssuesFixed,
		&r.LastError, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.CreatedAt, _ = time.Parse(time.DateTime, createdAt)
	r.UpdatedAt, _ = time.Parse(time.DateTime, updatedAt)
	return &r, nil
}

func (s *SQLiteStore) scanIterationRound(row *sql.Row) (*IterationRoundRecord, error) {
	var r IterationRoundRecord
	var isRecovery int
	var startedAt string
	var completedAt sql.NullString
	err := row.Scan(&r.ID, &r.SessionID, &r.RoundNumber, &r.ReviewTaskID, &r.FixTaskID,
		&r.IssuesFound, &r.IssuesFixed, &r.FixReportPath, &isRecovery,
		&startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.IsRecovery = isRecovery != 0
	r.StartedAt, _ = time.Parse(time.DateTime, startedAt)
	if completedAt.Valid {
		t, _ := time.Parse(time.DateTime, completedAt.String)
		r.CompletedAt = &t
	}
	return &r, nil
}
