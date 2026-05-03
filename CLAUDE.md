# CLAUDE.md

## 项目概述

DTWorkflow —— 基于 Claude Code 的 Gitea 自动化工作流平台。功能包括 PR 自动评审、Issue 自动修复、测试补全与 AI E2E 测试。

详细需求见 `docs/PRD.md`，路线图见 `docs/ROADMAP.md`。

## 语言与沟通规范

- 非必要情况下，沟通、代码注释、Git 提交消息、文档均使用**中文**
- 变量名、函数名、类型名等代码标识符使用英文

## 技术栈

- **语言**：Go
- **CLI 框架**：Cobra
- **HTTP 框架**：Gin
- **任务队列**：asynq + Redis
- **数据持久化**：SQLite（modernc.org/sqlite，纯 Go，禁止使用 CGO 依赖）
- **容器管理**：Docker SDK for Go
- **配置管理**：Viper（YAML 格式）
- **Gitea 交互**：Gitea REST API
- **执行引擎**：Claude Code CLI

## 架构约束

- **CLI-first，API 并行**：核心能力必须通过 CLI 命令暴露，HTTP API 作为并行接入层
- **CLI 输出规范**：所有命令支持 `--json` 标志输出结构化 JSON；默认人类可读格式；退出码：0 成功 / 1 失败 / 2 部分成功
- **初期不做 Web 界面**：配置通过 YAML 文件管理，状态查询通过 CLI 或 API
- **Docker 隔离**：每个任务在独立 Docker 容器中运行 Claude Code CLI，容器间互不影响

## 跨平台要求

- 开发环境：macOS
- 生产环境：Debian (Linux amd64)
- **禁止使用 CGO**：所有依赖必须为纯 Go 实现，确保 `GOOS=linux GOARCH=amd64 go build` 可直接交叉编译
- 文件路径使用 `filepath.Join()`，不硬编码分隔符

## 项目结构

```
cmd/
  dtworkflow/   # 服务端 CLI 入口
  dtw/          # 瘦客户端 CLI 入口（远程操作）
internal/       # 内部包（不对外暴露）
  api/          # REST API 层（handlers/middleware/router）
  cmd/          # 服务端 Cobra 命令定义
  gitea/        # Gitea API 客户端
  worker/       # Docker Worker 池管理
  queue/        # 任务队列
  review/       # PR 评审逻辑
  fix/          # Issue 分析与修复逻辑（analyze_issue 只读分析 / fix_issue 写权限修复）
  test/         # 测试生成逻辑（test.Service / TestGenOutput / prompt / errors）
  validation/   # 公共输入校验（gen_tests module / framework，三入口共用）
  notify/       # 通知框架
  config/       # 配置管理
  dtw/          # dtw 客户端核心库（HTTP 客户端/配置/Wait 轮询/输出）
    cmd/        # dtw Cobra 命令定义
  store/        # SQLite 数据持久化
  model/        # 共享数据模型
  webhook/      # Webhook 事件解析
  report/       # 报告生成
pkg/            # 可复用的公共包
docs/           # 项目文档（PRD、ROADMAP 等）
configs/        # 配置文件模板
```

## 代码搜索规则

- **探索性问题必须先语义搜索**：当用户提问包含"…是什么"、"怎么实现的"、"机制"、"流程"、"逻辑"、"策略"等探索性关键词时 → **必须**先用 `mcp__fast-context__fast_context_search`，禁止直接 Grep
- **宽泛语义搜索**（"找所有涉及 Y 的文件"、探索陌生模块、需要超过 2 次 Grep 才能定位的问题）→ 优先用 `mcp__fast-context__fast_context_search`
- **精确定位**（已知文件名、函数名、字符串）→ 用 Grep / Glob
- 并行读取多个文件时无需等待，直接同时发起所有 Read 调用

## Issue 自动修复功能（M3.5）

- **触发方式**：
  - 标签触发：Issue 添加 `auto-fix` 标签 → 只读分析（`analyze_issue`）；添加 `fix-to-pr` 标签 → 修复 + PR 创建（`fix_issue`）
  - CLI 触发：`./bin/dtw fix-issue --owner <owner> --repo <repo> --issue <N> --fix`
- **两级镜像**：`analyze_issue` 用轻量镜像（只读），`fix_issue` 用执行镜像（`worker-full`，含 JDK + Maven）
- **修复流程**：`fix.Service.executeFix` 12 步 — 前置校验 → 信息不足检查 → 采集上下文 → 构造 prompt → 容器执行 → 解析 FixOutput → 创建 PR → Issue 评论
- **PR 创建**：容器内 Claude push 分支 `auto-fix/issue-{id}`，容器外 `fix.Service` 通过 Gitea API 创建 PR（`fixes #{id}` 关联 Issue）；幂等保护——重试时先查询同 head 分支的 open PR，存在则复用
- **Tag-as-Ref**：Issue 关联 tag 时，PR Base 改用仓库默认分支，PR 描述中注明
- **凭证安全**：entrypoint.sh 使用脱敏 origin URL + 自定义 credential helper 脚本（按需注入 token），不持久化凭证到 `.git/config`；prompt 追加"禁止读凭证文件"约束
- **错误脱敏**：`executeFix` 返回的 error 不携带 ParseError 详情（可能含 Claude 原始输出），详情仅写结构化日志，防止 prompt-injection 内容泄露到飞书通知或 Issue 评论
- **失败处理**：信息不足 / Claude 返回失败 → SkipRetry + Issue 评论；Push 成功但 PR 创建失败 → 允许重试

## 测试生成功能（M4.1 + M4.2 + M4.2.1）

- **触发方式**（M4.1 + M4.2 仅手动；M4.3 扩展 Webhook PR merged 触发）：
  - CLI 触发：`./bin/dtw gen-tests --repo <owner/repo> [--module <path>] [--ref <branch>] [--framework junit5|vitest]`
  - API 触发：`POST /api/v1/repos/{owner}/{repo}/gen-tests`
- **镜像**：`gen_tests` 使用执行镜像（`worker-full`，含 JDK + Maven）
- **执行流程（M4.2 §4.2 八步）**：`test.Service.Execute` —
  1. 前置校验（`Enabled` / `validateModule` / `resolveBaseRef` / `validateModuleExists`）
  2. `resolveFramework` / `buildPrompt` / 容器执行
  3. `parseResult`（CLI 信封 → TestGenOutput，Success=true 走 `validateSuccessfulTestGenOutput`；Success=false 走 `validateFailureTestGenOutput` 弱校验）
  4. `len(CommittedFiles)>0` 即使 Success=false 也 `createTestPR`（D5 失败保留）
  5. 阶段 1 `SaveTestGenResult`（`review_enqueued=0`）
  6. review 入队决策 + 成功时阶段 2 `UpdateTestGenResultReviewEnqueued` partial UPDATE（`review_enqueued=1`）
  7. 业务失败判定（`!InfoSufficient` / `!Success` → sentinel error）
  8. return result
- **稳定分支 + 断点续传**：分支名 `auto-test/{moduleKey}`（`test.BuildAutoTestBranchName`，空 module 回落 `all`）；用户重触发即 Cancel-and-Replace（删分支删 PR + kill 容器 + `EnqueueHandler.WithBranchCleaner` 在 `EnqueueManualGenTests` 中清理）；`entrypoint.sh` 先 `fetch + checkout BASE_REF`，再 fetch / checkout 稳定分支，落后 base 或非 bot author 自动重置并 `git push --force-with-lease` 对齐远程（失败写 `/tmp/.gen_tests_warnings` → `TestGenOutput.Warnings` 追加到飞书 Warning）
- **完成后自动 enqueue review（D6）**：`Success=true && PRNumber>0` 时 `test.Service` 主动调 `EnqueueManualReview`（`triggered_by="gen_tests:<taskID>"`）；`test_gen.review_on_failure=true` 时失败 + PRNumber>0 也入队；`lookupPreviousEnqueueState` 按 taskID+PRNumber 幂等 guard，重试不重复入队 review；入队失败仅 warn 不阻断主流程
- **review 拦截（D2）**：`enqueue.HandlePullRequest` 检测到 `auto-test/{moduleKey}` 分支时，用 `store.ListActiveGenTestsModules(repo)` 比对 moduleKey（状态集含 pending）；命中活跃任务则跳过 review 入队；查询失败 fail-open
- **通知三件套**：3 个 `EventType`（`EventGenTestsStarted` / `EventGenTestsDone` / `EventGenTestsFailed`）+ 飞书 card + Gitea PR 评论（锚点 `<!-- dtworkflow:gen_tests:done -->` 不带 task_id，跨任务覆盖语义）；PR 评论通过 `Processor.syncGenTestsPRComment`（走 `GenTestsPRCommenter` 接口）在 gen_tests 最终态写回，与路由配置解耦；gen_tests 三事件属 repo 级通知，`repoScopedGiteaNotifier` 包装防误投 Gitea 评论渠道（PR number 为空时静默跳过）
- **failure_category 四枚举**：`infrastructure` / `test_quality` / `info_insufficient` / `none`；Success=true 必须 `none`，Success=false 必须非 `none`；飞书 severity 映射 infrastructure=Warning / test_quality=Info / info_insufficient=Info
- **持久化**：迁移 v19 `test_gen_results` 表（`task_id NOT NULL UNIQUE` + 3 索引 + `review_enqueued` + `updated_at`）；迁移 v20 重建表，`task_id` 改为可空（使 `ON DELETE SET NULL` 在历史任务 purge 时正常生效，不触发 NOT NULL 约束冲突）；`store.SaveTestGenResult` UPSERT + UUID 生成 + 自由文本 2KB 截断；Execute 两阶段写入：阶段 1 UPSERT 主结果（`review_enqueued=0`），阶段 2 `UpdateTestGenResultReviewEnqueued` partial UPDATE（`review_enqueued=1`，不覆盖其它字段）；审计通过 `test_gen_results.task_id → tasks.id → tasks.triggered_by` 反查（表内不重复存 triggered_by）
- **框架检测**：`resolveFramework` 扫描 `module/pom.xml`（Java）或 `package.json`（Vue）；两者并存返回 `ErrAmbiguousFramework`；可通过 `test_gen.test_framework` 显式配置
- **分支保护**：`entrypoint.sh` gen_tests 分支含 pre-push hook，仅允许推送 `refs/heads/auto-test/*`；hooks 目录 `chmod -R a-w` 加固，防止 Claude 在容器内修改 hook；prompt 禁止 Claude 使用 `git push -f / --force / 重写历史`（`--force-with-lease` 仅由 entrypoint 在分支重置场景主动发起）
- **Cancel-and-Replace**：按 stable branch key（`test.ModuleKey(module)` 派生）聚合，`svc/api` 与 `svc-api` 等落到同一 `auto-test/*` 分支的请求互相替换（不再按原始 module 字符串精确匹配）；空 `module`（整仓生成）通过 SQL `COALESCE` 正确处理；cancel 旧任务后由 `queue.BranchCleaner`（注入 `gitea.ClosePullRequest` + `gitea.DeleteBranch`）清理远程 PR + 分支；CLI 手动触发路径同样装配 BranchCleaner，与 serve/API 语义对齐；cleanup 失败仅 warn
- **错误脱敏**：`ErrTestGenParseFailure` 详情不进返回 error（防止 Claude 原始输出泄露到飞书/PR 评论），详情仅写结构化日志；`sanitizeTestGenOutput` 统一过滤 `Warnings / FailureReason / MissingInfo` 等自由文本字段（防 prompt-injection 内容流入飞书/PR/评论），`Processor.record.Error` 也走 `test.SanitizeErrorMessage` 兜底

## 混合仓库多框架支持（M4.2.1）

- **触发场景**：整仓模式（`--module` 留空、`--framework` 未指定）且 `moduleScanner` 已注入时，`EnqueueManualGenTests` 自动扫描仓库 depth=1 子目录，发现多可测试模块后拆分为独立子任务，各自产出独立分支和 PR
- **ScanRepoModules**（`internal/test/scan.go`）：先检查根目录 `pom.xml` / `package.json`，有则直接 early return（不做 depth=1）；根级均无时调用 `ListDir` 扫描子目录；同目录双框架（`pom.xml` + `package.json` 并存）拆分两条 `DiscoveredModule`；`maxScanDirs=30` 截断；单目录检测失败跳过继续；空结果返回 `ErrNoFrameworkDetected`
- **EnqueueManualGenTests 返回 `[]EnqueuedTask`**：整仓多模块时先 `cleanupAllAutoTestBranches` 全量清理旧资源，再逐模块调用 `enqueueSingleGenTests`；扫描失败 fail-open 回退单任务；API 响应新增 `split/tasks` 结构（`{"split":true,"tasks":[{"task_id":"...","module":"backend","framework":"junit5"},...]}` ）
- **framework 感知分支命名**：`BuildAutoTestBranchName(module, framework...)` variadic 参数，多框架拆分场景追加 `-{framework}` 后缀（`auto-test/all-junit5`、`auto-test/mono-vitest`）；单框架不传 framework 行为不变
- **framework 感知 Cancel-and-Replace**：`listActiveGenTestsTasksByBranchKey` 按 `(ModuleKey(module), framework)` 二元组匹配，同 module 不同 framework 的子任务不互相取消
- **cleanupAllAutoTestBranches**：整仓拆分前全量清理——取消所有活跃 gen_tests 任务 + 批量关闭 `auto-test/*` PR + 删除远程分支；操作前记 dry-run 日志；任一步骤失败仅 warn 不阻断入队
- **shouldSkipAutoTestReview 前缀匹配**：从精确 key 匹配改为 `branchKey == candidateKey || strings.HasPrefix(branchKey, candidateKey+"-")`，正确拦截 `auto-test/all-junit5` 等 framework 后缀分支的 PR 评审
- **worker 一致性**：`moduleKeyForContainerWithFramework` 计算 `MODULE_SANITIZED` 时追加 framework 后缀；`container_test.go` 内置 worker ↔ test 一致性对照测试（`moduleKeyForContainerWithFramework(m,f) == strings.TrimPrefix(BuildAutoTestBranchName(m,f), "auto-test/")`）
- **装配层**：`serve.go` 和服务端 CLI `gen_tests.go` 均通过 `WithModuleScanner(&giteaRepoFileChecker{...})` + `WithPRClient(gc)` 注入；未注入时整仓模式 fail-open 退化为现有单任务逻辑

## 测试服务器

- SSH Host 别名：`companytest`（对应 `~/.ssh/config` 中的 Host 条目）
- 连接命令：`ssh companytest`
- 本机可创建 `deploy/local.env`（已被 `.gitignore` 排除）覆盖默认 Host，参考 `deploy/local.env.example`
- 部署目录：`/opt/dtworkflow`

## Gitea 兼容性约束

- **禁止在 Gitea API 请求体中使用 4 字节 emoji（U+10000 及以上）**：目标 Gitea 实例的 MySQL 使用 `utf8` charset（非 `utf8mb4`），写入 4 字节字符会触发数据库内部错误并返回 HTTP 500（无 message）。可用字符范围：仅限 BMP（U+0000–U+FFFF）内的 emoji，如 `✅`（U+2705）、`⚠️`（U+26A0）、`‼️`（U+203C）。禁用示例：`🤖`（U+1F916）、`🚨`（U+1F6A8）、`🚀`（U+1F680）等。
- 新增涉及 Gitea 写入的代码（PR body、Issue 评论、评审内容）时，必须用 `python3 -c "for ch in s: print(hex(ord(ch)))"` 或等效方式确认无 U+10000 以上字符。
- **Token 拆分（规避自评审限制）**：Gitea 禁止同一账号对自己创建的 PR 发 approve/request-changes 类型的 Review。因此 `gitea.tokens` 按职能拆为 `review`、`fix`、`gen_tests` 三个账号，装配层按 token 复用或构造独立 `*gitea.Client`：
  - review 账号（`ServiceDeps.GiteaClient`）：`review.Service` / `review.Writer`、只读 API handlers、`GiteaNotifier` 通知评论；同时作为 `review_pr` / `analyze_issue` 任务容器内 git 凭证。
  - fix 账号（`ServiceDeps.GiteaFixClient`）：`fix.Service` 的 `PRClient` / `IssueClient` / `RefClient`；同时作为 `fix_issue` 任务容器内 git push 凭证（`buildContainerEnv` → `selectGiteaToken` 按 `TaskType` 注入）。
  - gen_tests 账号（`ServiceDeps.GiteaGenTestsClient`）：`test.Service` 的 `PRClient`，以及 gen_tests Cancel-and-Replace 的 `BranchCleaner`；同时作为 `gen_tests` 任务容器内 git push 凭证。
  - 兜底：`gitea.tokens.{review,fix,gen_tests}` 留空时回退 `gitea.token`，单账号部署保持向后兼容。新增会"创建 PR 的任务类型"需在 `worker.PoolConfig` + `selectGiteaToken` + `buildContainerEnv` + host 侧 Gitea client 装配同步扩展。

## 编码规范

- 遵循 Go 官方代码规范和 Effective Go
- 使用 golangci-lint 进行静态检查
- 错误处理：不吞错误，逐层返回或包装（`fmt.Errorf("xxx: %w", err)`）
- 日志使用结构化日志库（如 slog）
