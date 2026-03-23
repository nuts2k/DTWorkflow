package store

import (
	"database/sql"
	"fmt"
)

// migration 表示一个版本化的数据库迁移
type migration struct {
	Version int
	SQL     string
}

// migrations 按版本号顺序排列的迁移列表
var migrations = []migration{
	{
		Version: 1,
		SQL: `CREATE TABLE IF NOT EXISTS tasks (
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
	},
	{
		Version: 2,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status)`,
	},
	{
		Version: 3,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name)`,
	},
	{
		Version: 4,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status)`,
	},
	{
		Version: 5,
		SQL: `CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type)
			WHERE delivery_id != ''`,
	},
	{
		Version: 6,
		SQL: `CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at)
			WHERE status = 'pending'`,
	},
}

// RunMigrations 执行版本化 Schema 迁移，跳过已执行的版本
func RunMigrations(db *sql.DB) error {
	// 创建迁移版本记录表
	const createMigrationTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`
	if _, err := db.Exec(createMigrationTable); err != nil {
		return fmt.Errorf("创建 schema_migrations 表失败: %w", err)
	}

	for _, m := range migrations {
		if err := executeMigration(db, m); err != nil {
			return err
		}
	}

	return nil
}

// executeMigration 在事务中执行单个迁移，使用 defer 确保事务回滚安全
func executeMigration(db *sql.DB, m migration) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("开启迁移事务失败 (版本 %d): %w", m.Version, err)
	}
	defer tx.Rollback() // Commit 后 Rollback 是安全的无操作

	// 在事务内检查版本是否已执行（消除 TOCTOU 窗口）
	var exists int
	err = tx.QueryRow("SELECT 1 FROM schema_migrations WHERE version = ?", m.Version).Scan(&exists)
	if err == nil {
		return nil // 已执行，跳过
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("查询迁移版本 %d 失败: %w", m.Version, err)
	}

	if _, err := tx.Exec(m.SQL); err != nil {
		return fmt.Errorf("执行迁移版本 %d 失败: %w", m.Version, err)
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", m.Version); err != nil {
		return fmt.Errorf("记录迁移版本 %d 失败: %w", m.Version, err)
	}

	return tx.Commit()
}
