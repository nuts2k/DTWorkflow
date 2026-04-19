# 安全说明 & 已知风险

本文件登记 DTWorkflow 容器执行层的已知安全风险、缓解措施，以及运维必须在生产环境执行的加固步骤。每一条风险都附带"影响范围 / 攻击路径 / 临时缓解 / 长期方案"四段。

> 强制要求：**在生产环境部署 DTWorkflow 前必须完成本文件"运维必做事项"章节列出的 Gitea 仓库级加固。** 仅靠容器内的 git hook / prompt 约束不是安全边界。

---

## 运维必做事项

1. **Gitea 仓库级分支保护**：对所有接入 DTWorkflow 的仓库，在 Gitea 管理页面给保护分支（`main` / `master` / `release-*` / `develop` 等）开启 `Push: Restricted`，仅允许白名单用户（不含 DTWorkflow bot 账号）推送。
   - DTWorkflow bot 应仅具有：推 `auto-test/*`、`auto-fix/*` 这两个前缀分支的权限。
   - 有能力的团队可进一步使用 Gitea Webhook 签名校验 + 独立 token per repo。
2. **bot 账号最小权限**：DTWorkflow 使用的 Gitea token 应绑定到专用 bot 账户，账户只加入受支持的仓库，不要使用个人 token 或管理员 token。
3. **容器网络隔离**：Worker 容器应运行在独立 bridge 网络，仅允许出站访问 Gitea 实例、Claude API、Maven Central / npm registry。禁止出站访问其他内网地址。
4. **飞书 Webhook 签名**：飞书 incoming webhook 的 `secret` 必须启用，防止攻击者伪造通知。

---

## 已知风险登记

### S-001: 容器内 credential helper 脚本可读（fix_issue / gen_tests 模式）

- **影响范围**：`fix_issue` / `gen_tests` 任务下容器内运行的 Claude Code CLI。
- **攻击路径**：
  1. Claude 被 prompt injection 后（攻击者控制 Issue 标题 / PR 标题 / 文件内容），获取 bash 执行能力。
  2. `build/docker/entrypoint.sh` 在容器启动时向 `/tmp/.git-credential-helper` 写入脚本，脚本体内含明文 Gitea token（heredoc 展开 `${GITEA_TOKEN}`）。
  3. 虽然 helper 脚本 `chmod 700` 仅 owner 可读，但 Claude 与 helper 都以 `worker` 用户运行（同 UID），Claude 可直接 `cat /tmp/.git-credential-helper` 读到 token。
  4. 得到 token 后 Claude 可使用 Gitea API 做任意 bot 账号权限内的操作（push / 改 issue / 读仓库等）。
- **临时缓解**：
  - 必须执行本文件"运维必做事项"1 —— Gitea 仓库级分支保护是唯一可靠的防越权 push。
  - 容器使用 `ReadonlyRootfs`、`CapDrop ALL`、`no-new-privileges`，限制横向移动。
  - 容器使用率限制（CPU / 内存 / PID 上限），限制攻击者尝试成本。
  - prompt 已明确禁止 Claude 读 `.git/config` / credential helper 脚本，但这是 prompt-level 约束，不是 OS-level 边界。
- **长期方案（M5 规划）**：
  - 改用 Git `GIT_ASKPASS` + FIFO 的一次性 token 注入机制，token 不落盘。
  - 或使用 Docker secrets / tmpfs mount + bind-mount 只读，push 完成后主动擦除。

### S-002: gen_tests 的 `pre-push` hook 可被绕过

- **影响范围**：`gen_tests` 任务下容器内运行的 Claude。
- **攻击路径**：
  1. `entrypoint.sh` 在 `.git/hooks/pre-push` 写入只允许推送 `refs/heads/auto-test/*` 的 shell 检查。
  2. `.git/hooks/` 在容器内默认可写（由 worker 用户拥有），Claude 可以：
     - `git push --no-verify`：绕过 hook 直接推送。
     - `git -c core.hooksPath=/dev/null push ...`：使用临时配置覆盖 hooksPath。
     - 直接删除 / 修改 `.git/hooks/pre-push`。
  3. 得到任意分支推送权限后，Claude 可往 `main` 推恶意 commit，诱发后续 CI 或合并操作。
- **当前缓解**：
  - `entrypoint.sh` 已在 hook 创建后执行 `chmod -R a-w .git/hooks`，Claude 无法再修改 hook 文件；但仍可通过 `--no-verify` / `-c core.hooksPath` 绕过。
  - prompt 明确禁止 Claude 使用 `git push -f / --force / --force-with-lease` 与 rewrite history。
- **长期方案（M5 规划）**：将 `.git/hooks` bind-mount 为只读镜像路径 + 借助 Gitea server-side hook 拒绝非白名单 ref（需要 Gitea 版本支持）。核心保护仍是运维层分支保护（S-001）。

### S-003: Claude 自由文本字段的 prompt-injection 风险（已缓解）

- **影响范围**：飞书卡片、Gitea PR 评论、PR body、结构化日志。
- **攻击路径（历史）**：Claude 输出的 `failure_reason` / `missing_info` / `warnings` / `analysis.priority_notes` / `skipped_targets.reason` 等自由文本字段可被 prompt injection 污染（塞钓鱼链接、控制字符、超长文本）。这些字段原先被直接拼进飞书卡片 + Gitea 评论。
- **当前缓解**（M4.2 已落地）：
  - `internal/test/result.go` 的 `sanitizeTestGenOutput` 统一过滤所有 Claude 自由文本字段：
    - `Warnings`：白名单前缀过滤（只保留 entrypoint 写入的 `AUTO_TEST_BRANCH_*=` / `ENTRYPOINT_BASE_SHA=` KEY=VAL 格式），单条 ≤ 200 字节，总数 ≤ 10 条。
    - 其它自由文本：去控制字符、剥离 `http(s)://` / `ftp://` 链接、按 UTF-8 rune 硬截断。
  - Processor 把 `gen_tests` runErr 写入 `record.Error` 前走 `test.SanitizeErrorMessage` 兜底。
  - Gitea 评论 / 飞书卡片不再二次渲染 URL（即使脱敏漏掉，Markdown 特殊字符转义作为第二层防御）。
- **长期方案**：`warnings` 继续维护 entrypoint 单向白名单；新增自由文本字段必须加入 `sanitizeTestGenOutput` 的覆盖清单。

### S-004: 迁移不可变原则

- **影响范围**：SQLite schema 升级路径。
- **说明**：所有 `internal/store/migrations.go` 中已发布的迁移 SQL 一旦合入 main 就视为**不可变历史记录**。修复某个错误迁移的唯一方式是新增一个更高版本号的迁移来重建/修正表结构。不可以回头修改已有版本的 SQL（会导致不同时点执行过迁移的数据库产生 schema 漂移）。
- **已发生的案例**：v19 首次发布时 `task_id` 误定义为 `NOT NULL + ON DELETE SET NULL`（两者冲突）；修复方案是新增 v20 重建表，保留 v19 SQL 原样不动。

---

## 升级与回滚

- **向前兼容**：本项目承诺迁移版本号单调递增且均为向前兼容 DDL；新版本可以直接在老版本 DB 上启动。
- **回滚**：不提供自动回滚迁移的能力。需要回滚时：
  1. 停止 Worker。
  2. 从备份恢复对应时点的 SQLite 文件。
  3. 启动对应版本的二进制。
- **备份建议**：生产部署应对 `/opt/dtworkflow/dtworkflow.sqlite` 做 `sqlite3 .backup` 热备份（每小时 / 每日按量选择），WAL 模式下此操作不阻塞。

---

## 报告安全问题

请通过私信或邮件联系仓库 owner，不要在公开 Issue / PR 中直接披露漏洞细节。
