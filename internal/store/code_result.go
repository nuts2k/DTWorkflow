package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// SaveCodeFromDocResult UPSERT code_from_doc 结果。以 task_id 为冲突键。
func (s *SQLiteStore) SaveCodeFromDocResult(ctx context.Context, record *CodeFromDocResultRecord) error {
	if record == nil {
		return ErrNilRecord
	}

	now := time.Now().UTC()
	const maxTextLen = 2048

	implementation := record.Implementation
	if len(implementation) > maxTextLen {
		implementation = implementation[:maxTextLen] + "...(truncated)"
	}
	failureReason := record.FailureReason
	if len(failureReason) > maxTextLen {
		failureReason = failureReason[:maxTextLen] + "...(truncated)"
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO code_from_doc_results (
			task_id, repo, branch, doc_path, success,
			pr_number, pr_url, failure_category, failure_reason,
			files_created, files_modified, test_passed, test_failed,
			implementation, review_enqueued, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET
			repo             = excluded.repo,
			branch           = excluded.branch,
			doc_path         = excluded.doc_path,
			success          = excluded.success,
			pr_number        = excluded.pr_number,
			pr_url           = excluded.pr_url,
			failure_category = excluded.failure_category,
			failure_reason   = excluded.failure_reason,
			files_created    = excluded.files_created,
			files_modified   = excluded.files_modified,
			test_passed      = excluded.test_passed,
			test_failed      = excluded.test_failed,
			implementation   = excluded.implementation,
			updated_at       = excluded.updated_at`,
		record.TaskID,
		record.Repo,
		record.Branch,
		record.DocPath,
		boolToInt(record.Success),
		record.PRNumber,
		record.PRURL,
		record.FailureCategory,
		failureReason,
		record.FilesCreated,
		record.FilesModified,
		record.TestPassed,
		record.TestFailed,
		implementation,
		boolToInt(record.ReviewEnqueued),
		now,
		now,
	)
	if err != nil {
		return fmt.Errorf("SaveCodeFromDocResult: %w", err)
	}
	return nil
}

// GetCodeFromDocResultByTaskID 按 task_id 查询结果，未找到返回 (nil, nil)。
func (s *SQLiteStore) GetCodeFromDocResultByTaskID(ctx context.Context, taskID string) (*CodeFromDocResultRecord, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, task_id, repo, branch, doc_path, success,
		       pr_number, pr_url, failure_category, failure_reason,
		       files_created, files_modified, test_passed, test_failed,
		       implementation, review_enqueued, created_at, updated_at
		FROM code_from_doc_results
		WHERE task_id = ?`, taskID)

	var r CodeFromDocResultRecord
	var successInt, reviewEnqueuedInt int
	var prNumber sql.NullInt64
	var prURL, failureReason, implementation sql.NullString
	var createdAt, updatedAt string

	err := row.Scan(
		&r.ID, &r.TaskID, &r.Repo, &r.Branch, &r.DocPath, &successInt,
		&prNumber, &prURL, &r.FailureCategory, &failureReason,
		&r.FilesCreated, &r.FilesModified, &r.TestPassed, &r.TestFailed,
		&implementation, &reviewEnqueuedInt, &createdAt, &updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GetCodeFromDocResultByTaskID: %w", err)
	}

	r.Success = successInt != 0
	r.ReviewEnqueued = reviewEnqueuedInt != 0
	r.PRNumber = prNumber.Int64
	r.PRURL = prURL.String
	r.FailureReason = failureReason.String
	r.Implementation = implementation.String

	var parseErr error
	r.CreatedAt, parseErr = parseTime(createdAt)
	if parseErr != nil {
		return nil, fmt.Errorf("GetCodeFromDocResultByTaskID 解析 created_at 失败: %w", parseErr)
	}
	r.UpdatedAt, parseErr = parseTime(updatedAt)
	if parseErr != nil {
		return nil, fmt.Errorf("GetCodeFromDocResultByTaskID 解析 updated_at 失败: %w", parseErr)
	}

	return &r, nil
}

// UpdateCodeFromDocReviewEnqueued 阶段 2：只翻转 review_enqueued 标志。
func (s *SQLiteStore) UpdateCodeFromDocReviewEnqueued(ctx context.Context, taskID string) error {
	result, err := s.db.ExecContext(ctx, `
		UPDATE code_from_doc_results
		SET review_enqueued = 1, updated_at = ?
		WHERE task_id = ?`,
		time.Now().UTC(), taskID)
	if err != nil {
		return fmt.Errorf("UpdateCodeFromDocReviewEnqueued: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrCodeFromDocResultNotFound
	}
	return nil
}
