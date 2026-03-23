package store

import "database/sql"

// RunMigrations 执行 Schema 迁移，幂等（所有语句使用 IF NOT EXISTS）
func RunMigrations(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS tasks (
			id              TEXT PRIMARY KEY,
			asynq_id        TEXT NOT NULL DEFAULT '',
			task_type       TEXT NOT NULL CHECK(task_type IN ('review_pr', 'fix_issue', 'gen_tests')),
			status          TEXT NOT NULL DEFAULT 'pending'
			                CHECK(status IN ('pending','queued','running','succeeded','failed','retrying','cancelled')),
			priority        INTEGER NOT NULL DEFAULT 5,
			payload         TEXT NOT NULL,
			repo_full_name  TEXT NOT NULL DEFAULT '',
			result          TEXT NOT NULL DEFAULT '',
			error           TEXT NOT NULL DEFAULT '',
			retry_count     INTEGER NOT NULL DEFAULT 0,
			max_retry       INTEGER NOT NULL DEFAULT 3,
			worker_id       TEXT NOT NULL DEFAULT '',
			delivery_id     TEXT NOT NULL DEFAULT '',
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			started_at      DATETIME,
			completed_at    DATETIME
		)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name)`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type)
			WHERE delivery_id != ''`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at)
			WHERE status = 'pending'`,
	}

	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
