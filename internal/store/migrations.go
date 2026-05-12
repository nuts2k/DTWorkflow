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
	{
		Version: 7,
		SQL: `CREATE TABLE IF NOT EXISTS review_results (
			id              TEXT PRIMARY KEY,
			task_id         TEXT REFERENCES tasks(id) ON DELETE SET NULL,
			repo_full_name  TEXT NOT NULL,
			pr_number       INTEGER NOT NULL,
			head_sha        TEXT NOT NULL DEFAULT '',
			verdict         TEXT NOT NULL,
			summary         TEXT NOT NULL DEFAULT '',
			issues_json     TEXT NOT NULL DEFAULT '[]',
			issue_count     INTEGER NOT NULL DEFAULT 0,
			critical_count  INTEGER NOT NULL DEFAULT 0,
			error_count     INTEGER NOT NULL DEFAULT 0,
			warning_count   INTEGER NOT NULL DEFAULT 0,
			info_count      INTEGER NOT NULL DEFAULT 0,
			cost_usd        REAL NOT NULL DEFAULT 0,
			duration_ms     INTEGER NOT NULL DEFAULT 0,
			gitea_review_id INTEGER NOT NULL DEFAULT 0,
			parse_failed    INTEGER NOT NULL DEFAULT 0,
			writeback_error TEXT NOT NULL DEFAULT '',
			created_at      DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
	},
	{
		Version: 8,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_review_results_repo ON review_results(repo_full_name)`,
	},
	{
		Version: 9,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_review_results_verdict ON review_results(verdict)`,
	},
	{
		Version: 10,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_review_results_created ON review_results(created_at)`,
	},
	{
		Version: 11,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_review_results_task_id ON review_results(task_id)`,
	},
	{
		Version: 12,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_review_results_repo_pr ON review_results(repo_full_name, pr_number)`,
	},
	// M2.4: tasks 表新增 pr_number 列
	{
		Version: 13,
		SQL:     `ALTER TABLE tasks ADD COLUMN pr_number INTEGER`,
	},
	// M2.4: 复合索引，支持"查找同一 PR 活跃任务"查询
	{
		Version: 14,
		SQL:     `CREATE INDEX IF NOT EXISTS idx_tasks_repo_pr ON tasks(repo_full_name, pr_number, task_type, status)`,
	},
	// M2.4: 历史数据回填，从 payload JSON 中提取 pr_number
	{
		Version: 15,
		SQL:     `UPDATE tasks SET pr_number = json_extract(payload, '$.pr_number') WHERE task_type = 'review_pr' AND pr_number IS NULL`,
	},
	// M2.7: tasks 表 task_type CHECK 约束添加 gen_daily_report
	{
		Version: 16,
		SQL: `
			CREATE TABLE tasks_new (
				id              TEXT PRIMARY KEY,
				asynq_id        TEXT NOT NULL DEFAULT '',
				task_type       TEXT NOT NULL CHECK(task_type IN ('review_pr', 'fix_issue', 'gen_tests', 'gen_daily_report')),
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
				completed_at    DATETIME,
				pr_number       INTEGER
			);
			INSERT INTO tasks_new SELECT * FROM tasks;
			DROP TABLE tasks;
			ALTER TABLE tasks_new RENAME TO tasks;
			CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
			CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type) WHERE delivery_id != '';
			CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at) WHERE status = 'pending';
			CREATE INDEX IF NOT EXISTS idx_tasks_repo_pr ON tasks(repo_full_name, pr_number, task_type, status);
		`,
	},
	// M3.3: tasks 表新增 triggered_by 列
	{
		Version: 17,
		SQL:     `ALTER TABLE tasks ADD COLUMN triggered_by TEXT NOT NULL DEFAULT 'webhook'`,
	},
	// M3.4: tasks 表 task_type CHECK 约束添加 analyze_issue
	{
		Version: 18,
		SQL: `
			CREATE TABLE tasks_new (
				id              TEXT PRIMARY KEY,
				asynq_id        TEXT NOT NULL DEFAULT '',
				task_type       TEXT NOT NULL CHECK(task_type IN ('review_pr', 'analyze_issue', 'fix_issue', 'gen_tests', 'gen_daily_report')),
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
				completed_at    DATETIME,
				pr_number       INTEGER,
				triggered_by    TEXT NOT NULL DEFAULT 'webhook'
			);
			INSERT INTO tasks_new SELECT * FROM tasks;
			DROP TABLE tasks;
			ALTER TABLE tasks_new RENAME TO tasks;
			CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
			CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type) WHERE delivery_id != '';
			CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at) WHERE status = 'pending';
			CREATE INDEX IF NOT EXISTS idx_tasks_repo_pr ON tasks(repo_full_name, pr_number, task_type, status);
		`,
	},
	// M4.2: 新建 test_gen_results 表持久化 gen_tests 产出；task_id UNIQUE 支撑两阶段 UPSERT 幂等
	//
	// 历史记录（重要）：
	//   v19 首次发布时把 task_id 定义为 NOT NULL + ON DELETE SET NULL，二者冲突
	//   （级联 SET NULL 触发会违反 NOT NULL）。修复方案是新增 v20 重建表而非
	//   修改 v19。本条 SQL 保留原始（错误的）NOT NULL 定义不动——迁移一旦发布
	//   即不可修改，否则不同时点执行过不同版本 v19 的数据库会出现 schema 漂移。
	//   修复路径：部署 v20 重建表（见下一条）。
	{
		Version: 19,
		SQL: `
			CREATE TABLE IF NOT EXISTS test_gen_results (
				id                  TEXT PRIMARY KEY,
				task_id             TEXT NOT NULL UNIQUE REFERENCES tasks(id) ON DELETE SET NULL,
				repo_full_name      TEXT NOT NULL,
				module              TEXT NOT NULL DEFAULT '',
				framework           TEXT NOT NULL,
				base_ref            TEXT NOT NULL,
				branch_name         TEXT NOT NULL DEFAULT '',
				commit_sha          TEXT NOT NULL DEFAULT '',
				pr_number           INTEGER NOT NULL DEFAULT 0,
				pr_url              TEXT NOT NULL DEFAULT '',
				success             INTEGER NOT NULL DEFAULT 0,
				info_sufficient     INTEGER NOT NULL DEFAULT 0,
				verification_passed INTEGER NOT NULL DEFAULT 0,
				failure_category    TEXT NOT NULL DEFAULT 'none',
				failure_reason      TEXT NOT NULL DEFAULT '',
				generated_count     INTEGER NOT NULL DEFAULT 0,
				committed_count     INTEGER NOT NULL DEFAULT 0,
				skipped_count       INTEGER NOT NULL DEFAULT 0,
				test_passed         INTEGER NOT NULL DEFAULT 0,
				test_failed         INTEGER NOT NULL DEFAULT 0,
				test_duration_ms    INTEGER NOT NULL DEFAULT 0,
				review_enqueued     INTEGER NOT NULL DEFAULT 0,
				cost_usd            REAL NOT NULL DEFAULT 0,
				duration_ms         INTEGER NOT NULL DEFAULT 0,
				output_json         TEXT NOT NULL DEFAULT '{}',
				created_at          DATETIME NOT NULL DEFAULT (datetime('now')),
				updated_at          DATETIME NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_repo ON test_gen_results(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_repo_module ON test_gen_results(repo_full_name, module);
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_created ON test_gen_results(created_at);
		`,
	},
	// M4.2 修复：test_gen_results.task_id 原始 v19 NOT NULL + ON DELETE SET NULL 冲突。
	// 本迁移重建表，把 task_id 改为可空；保留既有索引与数据。
	// v19 原始 SQL 保持不变（见上一条注释），确保迁移不可变原则。
	{
		Version: 20,
		SQL: `
			DROP TABLE IF EXISTS test_gen_results_new;
			CREATE TABLE test_gen_results_new (
				id                  TEXT PRIMARY KEY,
				task_id             TEXT UNIQUE REFERENCES tasks(id) ON DELETE SET NULL,
				repo_full_name      TEXT NOT NULL,
				module              TEXT NOT NULL DEFAULT '',
				framework           TEXT NOT NULL,
				base_ref            TEXT NOT NULL,
				branch_name         TEXT NOT NULL DEFAULT '',
				commit_sha          TEXT NOT NULL DEFAULT '',
				pr_number           INTEGER NOT NULL DEFAULT 0,
				pr_url              TEXT NOT NULL DEFAULT '',
				success             INTEGER NOT NULL DEFAULT 0,
				info_sufficient     INTEGER NOT NULL DEFAULT 0,
				verification_passed INTEGER NOT NULL DEFAULT 0,
				failure_category    TEXT NOT NULL DEFAULT 'none',
				failure_reason      TEXT NOT NULL DEFAULT '',
				generated_count     INTEGER NOT NULL DEFAULT 0,
				committed_count     INTEGER NOT NULL DEFAULT 0,
				skipped_count       INTEGER NOT NULL DEFAULT 0,
				test_passed         INTEGER NOT NULL DEFAULT 0,
				test_failed         INTEGER NOT NULL DEFAULT 0,
				test_duration_ms    INTEGER NOT NULL DEFAULT 0,
				review_enqueued     INTEGER NOT NULL DEFAULT 0,
				cost_usd            REAL NOT NULL DEFAULT 0,
				duration_ms         INTEGER NOT NULL DEFAULT 0,
				output_json         TEXT NOT NULL DEFAULT '{}',
				created_at          DATETIME NOT NULL DEFAULT (datetime('now')),
				updated_at          DATETIME NOT NULL DEFAULT (datetime('now'))
			);
			INSERT INTO test_gen_results_new (
				id, task_id, repo_full_name, module, framework, base_ref,
				branch_name, commit_sha, pr_number, pr_url,
				success, info_sufficient, verification_passed,
				failure_category, failure_reason,
				generated_count, committed_count, skipped_count,
				test_passed, test_failed, test_duration_ms,
				review_enqueued, cost_usd, duration_ms, output_json,
				created_at, updated_at
			)
			SELECT
				id, task_id, repo_full_name, module, framework, base_ref,
				branch_name, commit_sha, pr_number, pr_url,
				success, info_sufficient, verification_passed,
				failure_category, failure_reason,
				generated_count, committed_count, skipped_count,
				test_passed, test_failed, test_duration_ms,
				review_enqueued, cost_usd, duration_ms, output_json,
				created_at, updated_at
			FROM test_gen_results;
			DROP TABLE test_gen_results;
			ALTER TABLE test_gen_results_new RENAME TO test_gen_results;
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_repo ON test_gen_results(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_repo_module ON test_gen_results(repo_full_name, module);
			CREATE INDEX IF NOT EXISTS idx_test_gen_results_created ON test_gen_results(created_at);
		`,
	},
	// M5.2: tasks 表 task_type CHECK 约束追加 run_e2e（M5.1 遗漏）。
	{
		Version: 21,
		SQL: `
			CREATE TABLE tasks_new (
				id              TEXT PRIMARY KEY,
				asynq_id        TEXT NOT NULL DEFAULT '',
				task_type       TEXT NOT NULL CHECK(task_type IN ('review_pr', 'analyze_issue', 'fix_issue', 'gen_tests', 'gen_daily_report', 'run_e2e')),
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
				completed_at    DATETIME,
				pr_number       INTEGER,
				triggered_by    TEXT NOT NULL DEFAULT 'webhook'
			);
			INSERT INTO tasks_new SELECT * FROM tasks;
			DROP TABLE tasks;
			ALTER TABLE tasks_new RENAME TO tasks;
			CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
			CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type) WHERE delivery_id != '';
			CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at) WHERE status = 'pending';
			CREATE INDEX IF NOT EXISTS idx_tasks_repo_pr ON tasks(repo_full_name, pr_number, task_type, status);
		`,
	},
	// M5.2: E2E 测试结果聚合表。task_id 可空 + ON DELETE SET NULL 与 test_gen_results 一致。
	// created_issues JSON 字段存 case_path → issue_number 映射，用于幂等 guard。
	{
		Version: 22,
		SQL: `
			CREATE TABLE IF NOT EXISTS e2e_results (
				id              TEXT PRIMARY KEY,
				task_id         TEXT UNIQUE REFERENCES tasks(id) ON DELETE SET NULL,
				repo            TEXT NOT NULL,
				environment     TEXT NOT NULL DEFAULT '',
				module          TEXT NOT NULL DEFAULT '',
				total_cases     INTEGER NOT NULL DEFAULT 0,
				passed_cases    INTEGER NOT NULL DEFAULT 0,
				failed_cases    INTEGER NOT NULL DEFAULT 0,
				error_cases     INTEGER NOT NULL DEFAULT 0,
				skipped_cases   INTEGER NOT NULL DEFAULT 0,
				success         INTEGER NOT NULL DEFAULT 0,
				duration_ms     INTEGER NOT NULL DEFAULT 0,
				created_issues  TEXT NOT NULL DEFAULT '{}',
				created_at      DATETIME NOT NULL DEFAULT (datetime('now')),
				updated_at      DATETIME NOT NULL DEFAULT (datetime('now'))
			);
			CREATE INDEX IF NOT EXISTS idx_e2e_results_task_id ON e2e_results(task_id);
			CREATE INDEX IF NOT EXISTS idx_e2e_results_repo ON e2e_results(repo);
			CREATE INDEX IF NOT EXISTS idx_e2e_results_created_at ON e2e_results(created_at);
		`,
	},
	// M5.4: tasks 表 task_type CHECK 约束追加 triage_e2e。
	{
		Version: 23,
		SQL: `
			CREATE TABLE tasks_new (
				id              TEXT PRIMARY KEY,
				asynq_id        TEXT NOT NULL DEFAULT '',
				task_type       TEXT NOT NULL CHECK(task_type IN ('review_pr', 'analyze_issue', 'fix_issue', 'gen_tests', 'gen_daily_report', 'run_e2e', 'triage_e2e')),
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
				completed_at    DATETIME,
				pr_number       INTEGER,
				triggered_by    TEXT NOT NULL DEFAULT 'webhook'
			);
			INSERT INTO tasks_new SELECT * FROM tasks;
			DROP TABLE tasks;
			ALTER TABLE tasks_new RENAME TO tasks;
			CREATE INDEX IF NOT EXISTS idx_tasks_status ON tasks(status);
			CREATE INDEX IF NOT EXISTS idx_tasks_repo ON tasks(repo_full_name);
			CREATE INDEX IF NOT EXISTS idx_tasks_type_status ON tasks(task_type, status);
			CREATE UNIQUE INDEX IF NOT EXISTS idx_tasks_delivery_dedup ON tasks(delivery_id, task_type) WHERE delivery_id != '';
			CREATE INDEX IF NOT EXISTS idx_tasks_pending_created ON tasks(status, created_at) WHERE status = 'pending';
			CREATE INDEX IF NOT EXISTS idx_tasks_repo_pr ON tasks(repo_full_name, pr_number, task_type, status);
		`,
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交迁移版本 %d 事务失败: %w", m.Version, err)
	}
	return nil
}
