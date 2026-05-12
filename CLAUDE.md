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
  e2e/          # E2E 测试执行逻辑（e2e.Service / E2EOutput / prompt / errors）
  validation/   # 公共输入校验（gen_tests / e2e module / framework，多入口共用）
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

## 测试生成功能（M4.1 + M4.2 + M4.2.1 + M4.3）

- **触发方式**：
  - CLI 触发：`./bin/dtw gen-tests --repo <owner/repo> [--module <path>] [--ref <branch>] [--framework junit5|vitest]`
  - API 触发：`POST /api/v1/repos/{owner}/{repo}/gen-tests`
  - 变更驱动（M4.3）：PR merged webhook → 自动分析变更文件 → 过滤源码 → 匹配模块 → 逐模块入队 gen_tests
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

## 变更驱动测试生成（M4.3）

- **触发流程**：PR merged webhook → `parsePullRequest` 将 `action="closed" + merged=true` 映射为 `action="merged"` → `HandlePullRequest` 路由到 `handleMergedPullRequest`（现有 review 路由逻辑提取为 `handleReviewPullRequest`，零行为变更）
- **7 步数据流**：① Bot PR 过滤（`auto-test/`、`auto-fix/` 分支前缀 → 跳过，防自触发循环）→ ② 配置检查（`ChangeDrivenConfigProvider` 窄接口，`change_driven.enabled` 默认关闭）→ ③ `listAllPullRequestFiles` 分页获取变更文件（`maxPages=20` 安全上限）→ ④ `filterSourceFiles` 预过滤（内置忽略扩展名/文件名/路径前缀/测试文件模式 + `ignore_paths` doublestar glob）→ ⑤ `ScanRepoModules` 发现模块结构 → ⑥ `matchFilesToModules` 按路径前缀归组 → ⑦ 逐模块 `enqueueChangeDrivenGenTests`
- **新增过滤层**（`internal/queue/change_filter.go`）：`extractFilenames` 提取文件名并排除 `status=deleted`；`filterSourceFiles` 链式过滤（文件名精确 → 扩展名 → 路径前缀 → 测试文件模式 → 用户 ignore_paths）；`matchFilesToModules` 按 `DiscoveredModule.Path` 前缀分配文件到模块
- **幂等保护**：`buildChangeDrivenDeliveryID` 构建 `{webhookDeliveryID}:gen_tests:{moduleKey}` 复合 ID，同一 webhook + 同一模块只入队一次；复用 Cancel-and-Replace 语义处理 webhook 重发
- **Prompt 增强**（`internal/test/prompt.go`）：`PromptContext.ChangedFiles` 非空时 `writeChangeDrivenSection` 注入变更聚焦段，引导 Claude 优先为变更文件生成测试；`maxChangedFilesInPrompt=50` 截断 + `sanitize` 防 prompt 注入；手动触发时该字段为空，现有行为不变
- **配置**（`config.ChangeDrivenConfig`）：`Enabled *bool`（nil=false 默认关闭，与 `TestGenOverride.Enabled` nil=true 语义相反）+ `IgnorePaths []string`；挂载在 `TestGenOverride.ChangeDriven`；`ResolveTestGenConfig` 仓库级整体替换合并；校验：`ignore_paths` 语法 + `change_driven.enabled=true` 要求 `test_gen.enabled` 不为 `false`
- **依赖注入**：`WithPRFilesLister(giteaClient)` 注入 review token client（只读查询 PR 文件列表）；`WithConfigProvider(cfgAdapter)` 注入配置读取；均 nil 时 fail-open 跳过变更驱动
- **通知复用**：变更驱动任务与手动触发在 Processor 层完全一致，复用现有三事件；`triggered_by="webhook:pr_merged:<pr_number>"` 区分来源
- **`test.Service.Execute` 零改动**：所有变更驱动逻辑均在入队层（`internal/queue/enqueue.go`）和 prompt 层完成
- **设计文档**：`docs/plans/2026-05-03-m4.3-change-driven-test-gen-design.md`

## E2E 测试执行功能（M5.1）

- **触发方式**：
  - CLI 触发：`./bin/dtw e2e run --repo <owner/repo> --env <环境名> [--suite <suite>] [--module <module>]`
  - API 触发：`POST /api/v1/repos/{owner}/{repo}/e2e`
- **镜像**：`run_e2e` 使用 E2E 专用镜像（`worker-e2e`，含 Playwright + Chromium）
- **执行流程**：`e2e.Service.Execute` — 前置校验（Enabled / resolveEnvironment）→ 构造 prompt → 容器执行 → 解析 E2EOutput → 返回 E2EResult
- **环境解析优先级**：payload.Environment > override.DefaultEnv > 报错 `ErrEnvironmentNotFound`
- **配置**：`e2e` 块含 `enabled`（*bool，nil=允许）、`image`（E2E 镜像名）、`environments`（命名环境列表，各含 base_url / db_* / extra_env）、`default_env`
- **输出解析**：双层解析（CLI 信封 → E2EOutput JSON），`sanitizeE2EOutput` 截断自由文本字段防 prompt injection
- **确定性失败**：`isDeterministicE2EFailure` 检测配置错误（disabled / env not found / no cases），返回 SkipRetry 不重试
- **错误脱敏**：processor 对 `TaskTypeRunE2E` 错误消息调用 `SanitizeErrorMessage`，防止 Claude 原始输出泄露到通知/存储
- **Token 复用**：E2E 当前复用 `gen_tests` 账号的 token 进行容器内 git 操作（只读克隆，不创建 PR）
- **case.yaml schema**：用户在仓库 `e2e/{module}/cases/{caseName}/case.yaml` 中定义用例。Go 类型 `e2e.CaseSpec`（`internal/e2e/case.go`），YAML 字段：`name`（必填）、`description`、`timeout`（[30s, 30m] 范围，默认 5m）、`tags`、`expectations`（业务意图，auto-fix 修复基准）、`setup`（数据准备脚本）、`test`（必填，`.spec.ts` Playwright 脚本）、`teardown`（清理脚本）。脚本名禁止绝对路径/路径遍历/子目录，扩展名限 `.sql` / `.js` / `.ts` / `.spec.ts`。`suite.yaml` 无明确用户场景驱动，暂不实现（YAGNI）
- **设计文档**：`docs/plans/2026-05-09-m5.1-e2e-infrastructure-design.md`、`docs/plans/2026-05-10-m5.1-e2e-infrastructure-impl-plan.md`

## E2E 失败分析与报告（M5.2）

- **失败分类**：Claude 在 `E2EOutput.Cases[].FailureCategory` 中输出三枚举——`bug`（应用行为不符合 expectations）/ `script_outdated`（页面元素/选择器变更）/ `environment`（服务不可用/网络/数据库超时）
- **Issue 自动创建**：`e2e.Service.processFailures` 遍历失败用例，`bug` 和 `script_outdated` 创建 Gitea Issue（`environment` 跳过）；每 Issue 附标题、分类、错误详情、操作路径、case.yaml expectations；`script_outdated` 额外挂 `fix-to-pr` 标签触发 Phase 3 自动修复
- **截图上传**：容器内截图写入 `/workspace/artifacts/`，宿主机映射 `{data_dir}/e2e-artifacts/{task_id}/`；`processFailures` 对每张截图调 `CreateIssueAttachment` 上传到对应 Issue；路径遍历防御通过 `filepath.Clean` + 前缀校验
- **幂等保护**：`processFailures` 执行前读取已有 `e2e_results.created_issues`，已创建过 Issue 的 case 跳过；同一 task 重试不重复创建 Issue
- **两阶段持久化**：阶段 1 `SaveE2EResult`（UPSERT 主结果，`created_issues='{}'`）；阶段 2 `UpdateE2ECreatedIssues`（partial UPDATE，不覆盖其它字段）
- **数据库迁移**：v21 tasks 表追加 `run_e2e` 类型支持；v22 新增 `e2e_results` 表（`task_id UNIQUE` + `environment` / `created_issues` / `updated_at`）
- **Gitea API 扩展**：`CreateIssue`（标题/正文/标签）、`ListRepoLabels`（查找 `fix-to-pr` 标签 ID）、`CreateIssueAttachment`（multipart 上传截图）
- **Issue body 格式化**（`internal/e2e/formatter.go`）：`bug` 模板（错误详情 + 操作路径 + expectations）/ `script_outdated` 模板（额外 auto-fix 提示）；HTML 锚点 `<!-- dtworkflow:e2e:{taskID}:{casePath} -->` 用于幂等检测
- **飞书通知**：三事件 `EventE2EStarted` / `EventE2EDone` / `EventE2EFailed` 专项卡片；失败卡片含失败列表 + 已创建 Issue 号；全通过 → 绿色，部分失败 → 橙色，全失败 → 红色
- **Processor 集成**：`buildNotificationMessage` 为 E2E 任务扩展 metadata（`e2e_failed_list` JSON + `e2e_created_issues` CSV）
- **Worker Artifact Volume**：`pool.go` 为 `run_e2e` 任务创建 `{DataDir}/e2e-artifacts/{taskID}/` 目录（chmod 0777）并 bind mount 到容器 `/workspace/artifacts`
- **启动清理**：`serve.go` 启动时 `cleanupE2EArtifacts` 删除超过 retention 天数的 artifact 目录（默认 7 天，可配 1-90 天）
- **装配层**：`serve.go` 通过 `WithIssueClient(giteaFixClient)` + `WithStore(store)` + `WithArtifactDir(dataDir)` 注入 M5.2 依赖；任一为 nil 时优雅降级保持 M5.1 行为
- **Token 使用**：Issue 创建使用 `fix` 账号 token（`GiteaFixClient`），避免 review 账号自评审冲突
- **设计文档**：`docs/plans/2026-05-11-m5.2-failure-analysis-report-design.md`、`docs/plans/2026-05-11-m5.2-failure-analysis-report-impl-plan.md`

## E2E 模块拆分与并行执行（M5.3）

- **触发条件**：全量模式（`--module` 留空 + `e2eModuleScanner` 已注入 + `baseRef` 非空）时自动扫描拆分；指定 `--module` 时仍走单任务，与 M5.1 行为一致
- **模块发现**：`e2e.ScanE2EModules`（`internal/e2e/scan.go`）调用 `E2EModuleScanner.ListDir("e2e")` 获取子目录，逐子目录校验 `cases/` 是否存在；`maxE2EScanDirs=30` 截断；单子目录扫描失败跳过继续；Gitea 404 由 adapter 层转译为 `ErrDirNotFound`，scan 层映射为 `ErrNoE2EModulesFound`
- **入队层改造**：`EnqueueManualE2E` 返回 `([]EnqueuedTask, error)`；全量拆分前无条件 `cleanupAllActiveE2ETasks`（取消同仓库所有活跃 `run_e2e` 任务，含 `module=""` 的旧全量任务）；逐模块 `enqueueSingleE2E`（Cancel-and-Replace 按 `(repo, module)` 粒度）
- **fail-open 退化**：scanner 未注入 → 单任务；扫描失败 → warn + 回退单任务；`ErrNoE2EModulesFound` → API 422 / CLI 退出码 1
- **API 响应**：单任务保持原格式向后兼容；多任务返回 `{"split": true, "tasks": [{"task_id": "...", "module": "..."}, ...]}`
- **dtw 瘦客户端**：`--no-wait` 输出汇总表格；等待模式逐任务轮询；`--json` 直接输出 API JSON
- **Store 泛化**：`FindActiveGenTestsTasks` → `FindActiveTasksByModule(ctx, repo, module, taskType)`；`ListActiveGenTestsModules` → `ListActiveModules(ctx, repo, taskType)`；旧方法保留为委托，零回归风险
- **Processor 零改动**：各子任务独立路由到 `e2e.Service.Execute`，独立通知和报告，不做聚合（与 gen_tests M4.2.1 一致）
- **不做什么**：聚合通知/汇总报告、`suite.yaml`、显式多模块参数、Webhook 触发（M5.4）
- **装配层**：`serve.go` + 服务端 CLI 通过 `WithE2EModuleScanner(&giteaRepoFileChecker{...})` 注入；`giteaRepoFileChecker` 天然满足 `E2EModuleScanner` 接口
- **设计文档**：`docs/plans/2026-05-11-m5.3-e2e-module-split-parallel-design.md`、`docs/plans/2026-05-11-m5.3-e2e-module-split-parallel-impl-plan.md`

## E2E 回归自动化（M5.4）

- **两阶段流水线**：PR 合并 → 阶段 1（`triage_e2e`）轻量容器内 Claude 分析 git diff + `e2e/**/case.yaml` 关联性，输出应执行的模块列表 → 阶段 2 按模块拆分入队 `run_e2e`，复用 M5.1/M5.2/M5.3 全部能力并行执行
- **TaskType**：新增 `TaskTypeTriageE2E = "triage_e2e"`；轻量镜像（与 `review_pr` / `analyze_issue` 一致）；只读模式（ReadonlyRootfs + `--disallowedTools` + prompt READ-ONLY 约束）；review 账号 token；默认超时 10 分钟
- **触发流程**：Webhook PR merged → `handleMergedE2ERegression`（与 M4.3 gen_tests 变更驱动并列，互不干扰）→ Bot PR 过滤 → 配置检查（`regression.enabled`）→ 宿主侧初筛（复用 `filterSourceFiles`）→ 入队 `triage_e2e`
- **分析 Prompt**：四段式（上下文 + 分析指令 + 约束 + 输出格式）；变更文件列表最多 50 个截断；Claude 容器内自读 `e2e/` 目录和 case.yaml；禁止修改任何文件
- **TriageE2EOutput**：`modules []TriageModule`（必填，可空数组）+ `skipped_modules []TriageModule` + `analysis string`；`TriageModule` 含 `name` + `reason`；`sanitizeTriageOutput` 脱敏自由文本
- **Processor 链式入队**：分析完成后 `handleTriageE2EResult` 逐模块调 `EnqueueManualE2E`；`triggered_by = "triage_e2e:{taskID}"`；modules 为空时通知"无需回归" + succeeded；单模块入队失败仅 warn 不阻断其他模块；与 gen_tests 完成后入队 review（D6）模式一致
- **失败策略**：解析失败允许 asynq 重试；重试耗尽后跳过 + 通知（fail-closed，不做全量降级）
- **配置**：`e2e.regression.enabled *bool`（nil=false 默认关闭）+ `ignore_paths []string`（doublestar glob）；仓库级整体替换覆盖；校验：`regression.enabled=true` 要求 `e2e.enabled` 不为 `false` + `default_env` 非空
- **环境**：回归触发使用 `e2e.default_env`（自动触发无人指定环境）
- **通知**：三事件 `EventE2ETriageStarted` / `EventE2ETriageDone` / `EventE2ETriageFailed` 飞书卡片；链式入队的 `run_e2e` 复用现有 M5.1 三事件
- **幂等保护**：`buildE2ERegressionDeliveryID` 构建 `{webhookDeliveryID}:triage_e2e` 复合 ID；Cancel-and-Replace 按 `(repo, TaskTypeTriageE2E)` 粒度
- **持久化**：不新增结果表；`TaskRecord.Result` 保存原始 JSON 输出，`triggered_by` 可反查链式入队的 `run_e2e` 任务
- **手动触发**：不新增 triage 手动入口，用户直接用 `dtw e2e run`
- **装配层**：`serve_deps.go` 注入 `WithE2ERegressionConfigProvider(cfgAdapter)` + `WithEnqueueHandler(enqueueHandler)`；`selectGiteaToken` 新增 `triage_e2e` → review token
- **设计文档**：`docs/plans/2026-05-12-m5.4-e2e-regression-automation-design.md`

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
