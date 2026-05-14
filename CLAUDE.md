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

## 跨切面设计约定

以下约定贯穿所有功能模块，新增功能时必须遵守：

- **错误脱敏**：Claude 原始输出（含 FixOutput / TestGenOutput / E2EOutput 自由文本字段）不得出现在返回 error、飞书通知、Issue 评论中；详情仅写结构化日志。统一入口：`SanitizeErrorMessage` / `sanitize*Output`
- **确定性失败 → SkipRetry**：配置缺失、env not found、info insufficient 等不可重试错误必须返回 `asynq.SkipRetry`
- **幂等保护**：所有 webhook 触发的入队操作必须构造复合 DeliveryID（`{webhookDeliveryID}:{taskType}[:{moduleKey}]`）防重复
- **Bot PR 过滤**：`auto-test/`、`auto-fix/` 分支前缀的 PR merged 事件必须跳过，防自触发循环
- **Cancel-and-Replace**：同一（repo, branchKey）的新任务入队前取消旧任务 + 清理远程 PR + 分支；cleanup 失败仅 warn 不阻断
- **两阶段持久化**：主结果 UPSERT（阶段 1）→ 链式状态 partial UPDATE（阶段 2，不覆盖其他字段）
- **新增"创建 PR 的任务类型"**：必须同步扩展 `worker.PoolConfig` + `selectGiteaToken` + `buildContainerEnv` + host 侧 Gitea client 装配

## Issue 自动修复功能（M3.5）

详见 `docs/plans/` 相关设计文档。关键约束：

- `auto-fix` 标签 → `analyze_issue`（只读镜像）；`fix-to-pr` 标签 → `fix_issue`（worker-full）
- PR 幂等：重试时查同 head 分支 open PR，存在则复用；Tag-as-Ref 时 base 改用默认分支
- 凭证安全：entrypoint.sh 脱敏 origin URL + credential helper，不持久化 token 到 `.git/config`

## 测试生成功能（M4.1 + M4.2 + M4.2.1 + M4.3）

详见 `docs/plans/` 相关设计文档。关键约束：

- 分支名 `auto-test/{moduleKey}`，Cancel-and-Replace 按 branch key 聚合（`svc/api` 与 `svc-api` 落同一 key）
- 混合仓库：整仓模式深度=1 扫描，同目录双框架拆分为两条任务，分支名追加 `-{framework}` 后缀
- `shouldSkipAutoTestReview` 用前缀匹配（`branchKey == key || HasPrefix(branchKey, key+"-")`）拦截 framework 后缀分支
- 完成后自动 enqueue review（D6）；`lookupPreviousEnqueueState` 幂等 guard 防重复入队
- `failure_category` 四枚举：`infrastructure` / `test_quality` / `info_insufficient` / `none`；Success=true 必须 `none`
- 变更驱动（M4.3）：`filterSourceFiles` + `matchFilesToModules` 过滤后按模块入队；`test.Service.Execute` 零改动

## E2E 测试功能（M5.1 + M5.2 + M5.3 + M5.4）

详见 `docs/plans/` 相关设计文档。关键约束：

- `case.yaml` 路径：`e2e/{module}/cases/{caseName}/case.yaml`；脚本名禁止绝对路径/路径遍历，扩展名限 `.sql/.js/.ts/.spec.ts`
- 失败分类三枚举：`bug` / `script_outdated`（创建 Issue）/ `environment`（跳过 Issue）；`script_outdated` 挂 `fix-to-pr` 标签触发 M3.5
- 截图 bind mount：容器 `/workspace/artifacts/` ↔ 宿主 `{data_dir}/e2e-artifacts/{task_id}/`；路径遍历防御用 `filepath.Clean` + 前缀校验
- M5.4 两阶段流水线：`triage_e2e`（只读，review token）→ `handleTriageE2EResult` 链式入队 `run_e2e`；modules 为空时通知"无需回归" + succeeded
- 回归配置：`regression.enabled` 默认 false；启用时要求 `e2e.enabled` 非 false + `default_env` 非空
- E2E token：`run_e2e` 复用 gen_tests 账号（只读克隆）；Issue 创建用 fix 账号

## 迭代式评审修复（M6.1）

详见 `docs/plans/2026-05-13-iterative-review-fix-design.md`。关键约束：

- **触发条件**：`auto-iterate` 标签 + `review_pr` verdict=request_changes + 存在达到阈值的问题（默认 ERROR 及以上）三者同时满足才入队 `fix_review`
- **push 来源区分**：`synchronized` 事件到达时，先查 `iterate.bot_login` 是否匹配 push 作者；匹配则为 fix_review 自身 push（仅取消旧 review_pr），否则为用户 push（取消 review_pr + fix_review）；`bot_login` 未配置时降级为纯状态机判断（`fixing` 状态）并 warn
- **会话状态机**：`idle → reviewing → fixing → reviewing → ... → completed / exhausted`；`fixing` 状态是区分 push 来源的核心标志，必须在入队 fix_review 前写入
- **fix_review 基础设施**：使用 fix 账号凭证（`selectGiteaToken`）+ `ImageFull` 镜像；entrypoint.sh 中按 `gen_tests` 模式配置 credential helper + git identity
- **通知抑制**：fix_review 任务的 start/completion 通知统一由 `EnqueueHandler` 的迭代通知方法发出，Processor 通用路径通过 `suppressNotification` 标记跳过，避免双发
- **标签操作**：`iterating` / `iterate-passed` / `iterate-exhausted` 由 `EnqueueHandler`（`IterateLabelManager` 接口）操作，使用 review 账号
- **DeliveryID 格式**：`iterate-{sessionID}:fix_review:{roundNumber}`，保证重试幂等
- **validate 行为**：`iterate.enabled=false` 时跳过 `iterate.bot_login` 必填校验；`max_rounds` 合法范围 [1, 10]；`notification_mode` 枚举 progress/silent；`fix_severity_threshold` 枚举 critical/error/warning
- **AfterIterationApproved**：review_pr verdict=approve 且存在活跃迭代会话时，将会话标记 completed + 标签流转 + 发终态通知（竞态保护：approve 回调在 request_changes 入队之前执行，防止误判）

## 测试服务器

- SSH Host 别名：`companytest`（对应 `~/.ssh/config` 中的 Host 条目）
- 连接命令：`ssh companytest`
- 本机可创建 `deploy/local.env`（已被 `.gitignore` 排除）覆盖默认 Host，参考 `deploy/local.env.example`
- 部署目录：`/opt/dtworkflow`

## Gitea 兼容性约束

- **禁止在 Gitea API 请求体中使用 4 字节 emoji（U+10000 及以上）**：目标 Gitea 实例的 MySQL 使用 `utf8` charset（非 `utf8mb4`），写入 4 字节字符会触发数据库内部错误并返回 HTTP 500（无 message）。可用字符范围：仅限 BMP（U+0000–U+FFFF）内的 emoji，如 `✅`（U+2705）、`⚠️`（U+26A0）、`‼️`（U+203C）。禁用示例：`🤖`（U+1F916）、`🚨`（U+1F6A8）、`🚀`（U+1F680）等。
- 新增涉及 Gitea 写入的代码（PR body、Issue 评论、评审内容）时，必须用 `python3 -c "for ch in s: print(hex(ord(ch)))"` 或等效方式确认无 U+10000 以上字符。
- **Token 拆分（规避自评审限制）**：`gitea.tokens` 按职能拆为 `review`、`fix`、`gen_tests` 三个账号：
  - review 账号：`review.Service`、只读 API、通知评论；`review_pr` / `analyze_issue` / `triage_e2e` 容器凭证
  - fix 账号：`fix.Service` PRClient/IssueClient；`fix_issue` 容器凭证
  - gen_tests 账号：`test.Service` PRClient、BranchCleaner；`gen_tests` / `run_e2e` 容器凭证
  - 兜底：三账号留空时回退 `gitea.token`，单账号部署向后兼容

## 编码规范

- 遵循 Go 官方代码规范和 Effective Go
- 使用 golangci-lint 进行静态检查
- 错误处理：不吞错误，逐层返回或包装（`fmt.Errorf("xxx: %w", err)`）
- 日志使用结构化日志库（如 slog）
