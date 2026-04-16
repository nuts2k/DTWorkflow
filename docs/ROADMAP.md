# DTWorkflow 项目路线图 (ROADMAP)

> 基于 Claude Code 的 Gitea 自动化工作流平台

## 总览

项目分为 **5 个阶段**，采用增量交付策略。每个阶段都产出可独立运行的功能，后续阶段在前者基础上扩展。

```
Phase 1          Phase 2          Phase 3          Phase 4          Phase 5
基础设施          PR 自动评审       Issue 自动修复    集中化测试        AI E2E 测试
& 核心框架        (核心功能)        (核心功能)       (扩展功能)        (扩展功能)
─────────────── ─────────────── ─────────────── ─────────────── ───────────────
  基座搭建          首个业务价值      第二业务价值      测试体系补全      自动化闭环
```

---

## Phase 1：基础设施与核心框架

**目标**：搭建可运行的骨架系统，验证 Claude Code CLI 在 Docker 中的可行性

### 里程碑

#### M1.0 PoC 验证：Claude Code CLI 在 Docker 中的可行性

在正式开发前，先用最小成本验证核心技术假设。PoC 不追求代码质量和架构，目标是快速确认可行性并记录关键参数。

**验证项**：

1. **容器内安装与运行**
   - [x] 构建 Docker 镜像：基础镜像（Debian/Node.js）+ 安装 Claude Code CLI（`npm install -g @anthropic-ai/claude-code`）
   - [x] 容器内执行 `claude -v` 确认安装成功
   - [x] 容器内执行 `claude -p "你好"` 确认非交互模式可用
   - [x] 验证 `--output-format json` 等输出格式控制参数

2. **API Key 认证**
   - [x] 通过环境变量 `ANTHROPIC_API_KEY` 注入容器，验证 Claude API 调用正常
   - [x] 确认 Key 不会泄露到容器日志或文件系统

3. **代码操作能力**
   - [x] 在容器内准备一个示例代码目录（挂载或容器内创建）
   - [x] 用 `claude -p` 让 Claude Code 读取代码文件并输出分析结果
   - [x] 用 `claude -p` 让 Claude Code 修改代码文件，验证文件写入能力
   - [x] 用 `claude -p` 让 Claude Code 执行 Git 操作（init、创建分支、commit）

4. **资源与性能基线**
   - [x] 记录单次 `claude -p` 调用的内存峰值和耗时
   - [x] 记录容器启动到可执行任务的冷启动耗时（healthz 118ms / 入队 44ms / 完整执行 32s）
   - [ ] 评估单机可并发运行的 Worker 容器数量上限（并发基线待测试）

5. **跨平台验证**
   - [x] macOS（Docker Desktop）环境以上各项通过
   - [x] Debian（Docker Engine）环境以上各项通过

**产出物**：
- PoC 验证用的 Dockerfile
- docker-compose.yml（含环境变量配置示例）
- 验证结果报告（每项通过/失败、关键参数记录、踩坑记录）

**通过标准**：
以上 1-3 全部通过，4-5 记录基线数据。任一项 1-3 失败需评估替代方案后再决定是否继续。

---

#### M1.1 项目初始化
- [x] Go 项目骨架（Go Modules）
- [x] 项目目录结构规划（cmd / internal / pkg）
- [x] golangci-lint 配置
- [x] Docker / Docker Compose 开发环境

#### M1.2 CLI 框架
- [x] Cobra CLI 骨架搭建
- [x] 子命令结构设计（review-pr / fix-issue / gen-tests / task / serve）
- [x] 全局 flags：`--json`（结构化输出）、`--config`（配置文件路径）、`--verbose`（详细日志）
- [x] 退出码规范：0 成功 / 1 失败 / 2 部分成功
- [x] `serve` 子命令：启动 HTTP 服务（API + Webhook 接收器）
- [x] 统一输出层（PrintResult / PrintError）
- [x] 单元测试（23 个测试，覆盖率 86.9%）

#### M1.3 Gitea API 客户端
- [x] 封装 Gitea REST API 客户端（Go），纯标准库实现，零外部依赖
- [x] 实现 20 个核心 API：PR 操作（8 个）、Issue 操作（7 个）、仓库操作（5 个）
- [x] API Token 认证，Functional Options 模式配置
- [x] 错误处理：类型化 ErrorResponse + IsNotFound/IsUnauthorized/IsForbidden/IsConflict 判断
- [x] URL 路径安全转义，响应体大小限制，分页支持
- [x] 单元测试（53 个测试，覆盖率 85.8%）+ 集成测试框架

#### M1.4 Webhook 接收器
- [x] Gin 路由接收 Gitea Webhook
- [x] Webhook 签名验证（安全性）
- [x] 事件解析：PR 事件（opened / synchronized / reopened）、Issue 标签变更事件
- [x] Gitea 侧 Webhook 配置指南

#### M1.5 任务队列
> 说明：本轮 M1.5 与 M1.6 按“任务执行引擎”合并实施，以下条目按实际交付状态同步。
- [x] Redis + asynq 集成
- [x] 任务类型定义（PR 评审 / Issue 修复 / 测试生成）
- [x] 优先级、重试、超时机制
- [x] 任务状态持久化到 SQLite
- [x] CLI 命令：`dtworkflow task status/list/retry`

#### M1.6 Docker Worker 池
- [x] Claude Code CLI Docker 镜像构建（基础镜像 + Claude Code + Git + 常用工具）
- [x] Docker SDK for Go 集成，程序化创建/销毁容器
- [x] Worker 池管理：并发数控制、资源限制（CPU / 内存）
- [x] 容器内 Git 仓库 clone + worktree 实现（entrypoint.sh 实现 clone + PR 分支 checkout，Claude Code CLI 固定为 v2.1.76）
- [x] Claude Code CLI 非交互模式验证（`claude -p` 在容器内运行）
- [x] API Key 安全注入（环境变量 / Docker secrets）
- [x] 安全加固：entrypoint.sh clone 阶段结束后清除 `GITEA_URL` / `REPO_CLONE_URL`，确保 Claude Code 执行阶段不可读取克隆凭证
- [x] Issue 修复 Ref 支持：entrypoint.sh 在 `ISSUE_REF` 环境变量非空时自动 `git fetch origin <ref>` 并 `checkout FETCH_HEAD`，确保容器内代码基线正确

#### M1.7 通知框架
- [x] 通知接口定义（Go interface，策略模式）
- [x] Gitea 评论通知实现（默认通道）
- [x] 主流程最小接线：`serve -> queue.Processor -> notify.Router`
- [x] 按仓库/事件类型的配置化通知路由（M1.8 配置驱动实现）

#### M1.8 配置管理
> 说明：2026-03-24 完成全部实施，含两轮审核修复（共 11 处 Critical/High 问题）。
- [x] Viper 集成，全局配置文件格式定义（YAML）
  - [x] `internal/config` 包：Manager + Config 模型 + Functional Options
  - [x] 默认值（WithDefaults）、环境变量绑定（WithEnvPrefix）、配置文件加载
  - [x] 配置校验（Validate）：server.port 范围、gitea.url 格式、redis.addr/worker.timeout/webhook.secret/claude.api_key 必填、log.level/format 白名单、notify 路由合法性、claude.model/effort 格式校验、review.model/effort 及仓库级 model/effort 格式校验
  - [x] 错误体系：ErrInvalidConfig 哨兵 + ValidationError（支持 errors.Is/As）
  - [x] `root` 命令统一配置入口（PersistentPreRunE）、CLI flag > env > file > default 优先级
  - [x] `serve` 从 cfgManager 读取全部运行参数，包括 Redis password/DB 透传
  - [x] `task` 接入统一配置体系，Viper flag 绑定 + 环境变量命名统一
  - [x] 通知路由配置驱动：全局默认路由 + 仓库级 notify override + configDrivenNotifier 按需构建
  - [x] 示例配置模板 `configs/dtworkflow.example.yaml` 覆盖全部配置段
- [x] 仓库级配置支持
  - [x] `repos` 配置段：per-repo notify 路由覆盖
  - [x] `repos` 配置段：per-repo review 配置预留（Enabled/Severity/IgnorePatterns）
  - [x] `repos` 配置段：per-repo model/effort 覆盖（继承全局 claude.model/effort，仓库级可单独覆盖）
  - [x] ResolveNotifyRoutes / ResolveReviewConfig 合并逻辑
- [x] 配置热加载（Viper WatchConfig）
  - [x] fsnotify 目录级监听 + 200ms debounce 防抖
  - [x] 校验失败不污染当前快照，编辑器 rename 重试
  - [x] OnChange 回调框架（panic recovery + slog 结构化日志）
  - [x] Manager.Stop() 优雅退出，serve 退出路径正确调用
  - [x] Get() 返回深拷贝（Config.Clone），SetConfigFile 并发安全
  - [x] 热加载生效边界：通知路由等轻量配置即时生效；server/redis/database/worker 需重启

### 交付物
- `dtworkflow` 单二进制文件，包含 CLI 命令和 `serve` 模式
- Gitea API 客户端库
- M1.5-6 任务执行引擎（队列 / 持久化 / Worker / task CLI）
- M1.7 通知框架与主流程最小接线
- M1.8 统一配置管理（Viper YAML / 环境变量 / CLI flags / 校验 / 热加载 / 仓库级覆盖）
- Docker Compose 一键启动脚本（Redis + dtworkflow serve + Worker 镜像构建配置）

### 验证标准
- Gitea 发送 Webhook → 服务接收 → 创建任务 → 写入 SQLite / 入队 Redis → 启动 Docker 容器 → 容器内 Claude Code CLI 执行基础 prompt → 最终状态可通过 `task` 命令查询
- 当 Gitea 配置完整且任务目标明确时，`review_pr` / `fix_issue` 任务最终成功或失败可回写 Gitea 评论通知
- 基础设施主链路打通即为通过；完整业务语义在后续 Phase 继续补齐

### 风险项
- **Claude Code CLI Docker 兼容性**：✅ 已验证，v2.1.76 在 ReadonlyRootfs 容器中正常运行（需 entrypoint 迁移 HOME 到可写 tmpfs）
- **网络配置**：✅ 已验证，容器通过 dtworkflow-net bridge 网络同时访问 Gitea（内网）和 Claude API（外网），自签名证书通过 GIT_SSL_NO_VERIFY 处理

---

## Phase 2：PR 自动评审

**目标**：实现完整的 PR 自动评审流程，交付团队首个业务价值

**依赖**：Phase 1 完成

### 里程碑

#### M2.1 PR 仓库环境准备与变更上下文提取
> 说明：Phase 2 采用"完整仓库 + Claude Code CLI"评审模式，而非仅传 diff 文本。
> Worker 容器内拥有完整代码仓库，Claude Code CLI 可自主探索调用链、关联文件和项目结构，
> 评审质量（尤其是安全分析与架构评审）显著优于纯 diff 模式。
> 详细设计见 `docs/plans/2026-03-25-m2.1-pr-review-context-design.md`。
- [x] Worker 容器内 clone 目标仓库并 checkout PR 分支（Phase 1 entrypoint.sh 已完成）
- [x] 通过 Gitea API 获取 PR 元数据（变更文件列表、变更行数、PR 描述）作为评审上下文
- [x] 大型 PR 检测与警告（超 10000 行日志 warn，超阈值在 prompt 中引导聚焦关键变更；分批策略 YAGNI 暂不实现）
- [x] 将 PR 变更上下文与评审指令注入 Claude Code CLI prompt（四段式：上下文→指令→JSON schema→大 PR 警示）
- [x] 新建 `internal/review` 包：评审编排层（Service/Execute/parseResult/resolveConfig）
- [x] 定义评审输出 JSON schema（ReviewOutput/VerdictType/ReviewIssue，M2.3 解析合约）
- [x] 双层 JSON 解析：CLI 信封（CLIResponse）→ 内层评审结果（ReviewOutput），含 code fence 清理
- [x] 评审指令可配置化（全局 YAML + 仓库级追加覆盖，ConfigProvider 接口支持热加载）
- [x] Pool.RunWithCommand 支持自定义容器命令（不破坏现有 PoolRunner 接口）
- [x] Processor 集成：ProcessorOption 模式注入 ReviewExecutor，review_pr 任务分发到 review.Service
- [x] ErrPRNotOpen 确定性失败跳过重试（asynq.SkipRetry）
- [x] 单元测试 19 个（Execute 流程、JSON 解析、prompt 构造、配置解析）

#### M2.2 评审 Prompt 工程
> 说明：M2.1 已实现基础评审 prompt（含四维度指令和严重程度定义），M2.2 聚焦模板精细化和专项 prompt。
> 详细设计见 `docs/plans/2026-03-25-m2.2-prompt-engineering-design.md`。
- [x] 设计评审 prompt 模板，覆盖四个维度：风格/逻辑/安全/架构（M2.1 defaultReviewInstructions 已实现基础版）
- [x] 严重程度分级指令（CRITICAL / ERROR / WARNING / INFO）（M2.1 已实现）
- [x] 输出格式定义（结构化 JSON，便于解析）（M2.1 jsonSchemaInstruction 已实现）
- [x] 引导 Claude Code 主动探索上下文：追溯调用链、检查相关文件、评估回归影响（M2.1 已在评审原则中实现）
- [x] Prompt 改用 stdin 传入（避免 ps aux 暴露完整 prompt，提升安全性；Pool.RunWithCommandAndStdin + Docker Attach stdin）
- [x] 技术栈自动检测（基于 PR 变更文件扩展名，TechStack 位掩码；配置 tech_stack 可覆盖自动检测）
- [x] Java 代码评审专项 prompt（Spring Boot 事务/MyBatis SQL 安全/循环依赖/N+1 查询等）
- [x] Vue 代码评审专项 prompt（响应式/组件设计/XSS/性能/Pinia 等，含 Vue 信号检测避免误触发）
- [x] 项目编码规范文件集成（指导 Claude 在容器内自行读取 CLAUDE.md / .code-standards / CONTRIBUTING.md 等；配置 code_standards_paths 可覆盖默认扫描列表）
- [x] 配置扩展：ReviewOverride 新增 TechStack / CodeStandardsPaths 字段，仓库级覆盖合并
- [x] 示例配置 dtworkflow.example.yaml 补充 tech_stack / code_standards_paths 示例
- [x] Dimensions 配置动态控制 prompt 评审维度（M2.5 实现：拆分 reviewPreamble + dimensionInstructions，按配置动态组装）
- [x] Claude 模型与推理强度可配置：全局 `claude.model` / `claude.effort`，仓库级可单独覆盖；`buildCommand` 透传 `--model` / `--effort` 给 Claude Code CLI
- [x] 安全加固：prompt 最前置 READ-ONLY 约束文本（禁止调用外部 API/网络），与 `--disallowedTools` 形成双层防御（意图层 + 工具层）
- [x] extractJSON 兼容性修复：无 code fence 时定位首个 `{` 与末尾 `}` 提取 JSON，兼容 Claude 在 JSON 前后输出自然语言的场景

#### M2.3 评审结果解析与回写
> 说明：2026-03-26 完成全部实施与审核修复（14 个审核问题已修复）。
> 详细设计见 `docs/plans/2026-03-26-m2.3-review-writeback-design.md`。
- [x] 解析 Claude 输出的结构化评审结果（双层 JSON 解析，M2.1 已实现基础，M2.3 集成到回写链路）
- [x] 逐行 comment：通过 Gitea PR Review API 添加行级评论
  - [x] unified diff 解析器（`diff.go`：按文件/hunk 分割，提取行号范围，10MB 大小上限防 OOM）
  - [x] 行号映射（Semantic A：`new_position` = 新文件绝对行号，范围检查 `[NewStart, NewStart+NewCount)`）
  - [x] issues 映射分流：映射成功→行级评论，映射失败→降级到 summary body
- [x] 整体 summary：生成格式化的总结评论并发布
  - [x] 正常场景：统计表格 + unmapped issues 列表 + 元信息
  - [x] 降级场景：Claude 原始输出包裹在代码块中（Markdown 注入防护）
  - [x] Markdown 转义（`escapeMarkdown`）+ UTF-8 安全截断（`truncateString`）
- [x] 自动 Request Changes：当存在 CRITICAL 或 ERROR 时触发
  - [x] Verdict 安全网：即使 Claude 返回 approve，issues 含 CRITICAL/ERROR 也强制 REQUEST_CHANGES
  - [x] 降级场景固定为 COMMENT（不对解析失败的结果做 approve/reject 判断）
- [x] 评审结果持久化到数据库（`review_results` 表，含 severity 分计数、费用、耗时等）
  - [x] Store 接口：SaveReviewResult / GetReviewResult / ListReviewResults
  - [x] 外键约束（task_id REFERENCES tasks ON DELETE SET NULL）+ task_id 索引 + (repo, pr) 复合索引
- [x] 错误处理与降级链
  - [x] 三级降级：正常→diff 失败→parse 失败，每级都向用户交付有价值的信息
  - [x] 解析失败显式重试：`ErrParseFailure` 哨兵错误，`Execute` 在 parseResult 后新增步骤 7.5——解析失败时直接返回该错误触发 asynq 重试，重试耗尽后由 Processor 调用 `WriteDegraded` 发送降级评论（原始输出包裹代码块）
  - [x] `parseLocation` 支持多位置字符串（分号分隔），取第一个有效 file:line；`description`/`title` 字段兜底填充 `message`，空 file/line 在 formatter 中优雅处理
  - [x] 回写失败不导致任务失败（WritebackError 独立记录）
  - [x] Store 持久化失败仅日志告警，不影响回写
- [x] 安全加固（审核修复）
  - [x] Markdown 注入防护：escapeMarkdown 转义外部内容，降级 rawOutput 包裹代码块
  - [x] 内部错误详情不泄露到 Gitea 评论（parseErr 仅写服务端日志）
  - [x] diff 解析 10MB 大小上限防 OOM
- [x] 单元测试覆盖率：review 92.7% / queue 85.2% / store 84.1%

#### M2.4 重新评审机制（Cancel-and-Replace）
> 说明：2026-03-26 完成全部实施与两轮审核修复（4 个 CRITICAL + 5 个 MEDIUM 问题已修复）。
> 详细设计见 `docs/plans/2026-03-26-m2.4-re-review-design.md`。
- [x] PR 更新（新 push / synchronized 事件）时自动触发重新评审
- [x] Cancel-and-Replace 策略：新任务入队成功后取消同一 PR 的旧评审任务
  - [x] Schema 迁移 V13/V14/V15：pr_number 列 + 复合索引 + 历史数据回填
  - [x] Store 新增 FindActivePRTasks / HasNewerReviewTask 查询
  - [x] TaskCanceller 接口（ISP）：Delete（pending/queued）+ CancelProcessing（running）
  - [x] 原子性保证：先创建新任务再取消旧任务，避免"两空"状态
  - [x] 取消失败 best-effort：asynq 操作失败时保留旧任务原始状态，不更新 SQLite
- [x] Processor 层 context.Canceled 响应：使用 context.Background() 确保状态更新
  - [x] Pre-cancelled 检查：record.Status == cancelled 时跳过执行
  - [x] ErrStaleReview 处理：过时评审标记为 cancelled 而非 succeeded
- [x] 回写 Staleness Check：回写前查询是否存在更新的非终态评审任务（fail-open）
- [x] Summary 替代标注：新评审标注"替代了之前基于 `xxx` 的评审"
- [x] FormatOptions 重构：formatReviewBody 参数结构体化
- [x] SQL 参数化：FindActivePRTasks / HasNewerReviewTask 引用 model 常量，消除硬编码
- [x] cancelTasks 可观测性：统计并记录取消失败数量
- [ ] 增量评审策略：仅评审新变更的文件（延后至后续迭代，当前仍为全量评审）

#### M2.5 评审配置
> 说明：2026-03-26 完成全部实施与代码审核修复（1 个 Important 问题已修复）。
> 详细设计见 `docs/plans/2026-03-26-m2.5-review-config-design.md`。
- [x] 仓库级评审开关：Processor 层 ReviewEnabledChecker 接口，Enabled=false 时跳过评审任务（标记 succeeded）
- [x] 按维度裁剪评审范围：拆分 defaultReviewInstructions 为 reviewPreamble + 4 个维度常量，buildDynamicInstructions 按 cfg.Dimensions 动态组装
- [x] 按严重程度过滤噪音：新建 filter.go（SeverityLevel 有序类型、FilterIssues 双重过滤），回写层 recalcVerdict 基于 visible issues 重算 verdict
- [x] 按文件模式忽略评审：doublestar glob 匹配（MatchesIgnorePattern），Prompt 层 + 回写层双重过滤
- [x] 配置校验增强：维度白名单、severity 白名单、ignore_patterns 语法校验（全局 + 仓库级）
- [x] 数据流完整性：ReviewConfig 新增 Severity/IgnorePatterns 字段，resolveConfig 传递到 WritebackInput，formatter 展示过滤提示文案
- [x] 新增 doublestar/v4 依赖（纯 Go，零传递依赖）
- [x] 评审者身份配置：Bot 账户配置以文档指南形式提供（示例配置注释说明，非代码实现）
- [x] 示例配置 dtworkflow.example.yaml 补充 M2.5 配置示例与注释

#### M2.6 飞书通知渠道接入
> 说明：2026-04-02 完成全部实施（常量化重构 + 代码审核 4 个 Major 问题修复 + 测试覆盖补充）。
> 详细设计见 `docs/plans/2026-04-01-m2.6-feishu-notification-design.md`。
- [x] 飞书自定义机器人（Webhook）通知渠道实现（`FeishuNotifier`）
  - [x] Webhook POST + 可选 HMAC-SHA256 签名校验
  - [x] 飞书交互卡片格式化（`feishu_card.go`：蓝色-进行中 / 绿色-成功 / 橙色-需修改或重试中 / 红色-失败）
  - [x] 消息内容：评审结论 + 关键 issue 统计摘要 + 重试次数（重试场景）+ 通知时间 + 耗时（仅成功）+ PR 跳转链接按钮
  - [x] 通知时间与耗时字段（`MetaKeyNotifyTime` / `MetaKeyDuration`）：所有通知注入 notify_time，成功通知额外注入当次尝试耗时；时区统一 Asia/Shanghai
  - [x] 重试信息字段（`MetaKeyRetryCount` / `MetaKeyMaxRetry` / `MetaKeyTaskStatus`）：重试场景卡片显示"第 N 次 / 共 M 次"，主题色强制橙色
- [x] 双通知时机
  - [x] 任务开始时发送"评审开始"通知（新增 `EventPRReviewStarted`）
  - [x] 任务完成时发送状态同步通知（复用现有 `sendCompletionNotification`，补充 Metadata）
- [x] 配置与装配
  - [x] `notify.channels.feishu` 配置段（webhook_url / secret）
  - [x] 配置校验：飞书渠道启用时 webhook_url 必填且格式合法
  - [x] `serve.go` 装配层按配置动态注册 FeishuNotifier
  - [x] 示例配置 `dtworkflow.example.yaml` 补充飞书配置示例
- [x] 错误隔离：飞书通知失败不影响 Gitea 回写和任务执行
- [x] 预留应用机器人（App Bot）扩展空间（个人消息 / @用户 / 卡片回调）
- [x] 本地开发环境对接飞书 Webhook 联调测试（2026-04-02 验证通过：蓝色"开始"卡片 + 绿色"完成"卡片均正常推送，HMAC-SHA256 签名校验通过）
- [x] 远程测试服务器端到端测试（真实 Gitea PR 流程 → 飞书开始/完成双通知验证通过）
- [x] 仓库级飞书 Webhook 覆盖（2026-04-15 实施）
  - [x] `FeishuOverride` 结构体与 `NotifyOverride.Feishu` 字段
  - [x] `ResolveFeishuOverride` 仓库级飞书覆盖解析（nil 时回退全局）
  - [x] `Config.Clone()` 深拷贝补充 `FeishuOverride`
  - [x] 配置校验：webhook_url 必填/格式合法/全局飞书已启用前提
  - [x] `hasRepoNotifyOverride` 支持飞书覆盖检测
  - [x] `newRouter` 注入仓库级 FeishuNotifier（无覆盖时使用全局）
  - [x] 示例配置补充仓库级飞书覆盖注释
  - [x] 详细设计见 `docs/superpowers/specs/2026-04-15-repo-feishu-override-design.md`

#### M2.7 每日评审统计报告
> 说明：每日定时通过飞书通知发送前一天的 PR 评审统计摘要。初期聚焦 PR 评审维度，架构预留扩展到 Issue 修复、测试生成等统计维度。
- [x] 定时调度机制（asynq periodic task，cron 表达式可配置，默认每天 09:00）
- [x] 前日评审数据聚合（基于 `review_results` + `tasks` 表按时间窗口查���）
  - [x] 基础统计：评审总数、成功次数、失败次数、需修改（Request Changes）次数
  - [x] 严重程度分布：CRITICAL / ERROR / WARNING / INFO 各计数
  - [x] 关键 PR 高亮：评审次数最多的 PR（重新评审触发最多）、issue 数量最多的 PR
  - [x] 仓库维度分组（多仓库场景下按仓库拆分统计）
- [x] 飞书统计摘要卡片格式化（复用 M2.6 卡片能力，新设计统计专用布局）
  - [x] 卡片内容：日期 + 统计表格 + 关键 PR 链接 + 趋势指示（较前日变化）
- [x] 配置段 `daily_report`
  - [x] `enabled`：启用开关
  - [x] `cron`：cron 表达式（默认 `0 9 * * *`）
  - [x] `timezone`：时区（默认 `Asia/Shanghai`）
  - [x] `feishu_webhook` / `feishu_secret`：独立 Webhook 配置（与实时通知可发不同群）
- [x] 可扩展统计收集器架构：接口化设计，未来可插拔新增 fix_issue / gen_tests 等维度
- [x] 空数据处理：前日无评审任务时发送简短"无评审活动"通知或跳过（可配置）
- [x] 示例配置 `dtworkflow.example.yaml` 补充 `daily_report` 配置示例

### 交付物
- 完整可用的 PR 自动评审功能
- 评审 prompt 模板库（Java / Vue）
- 评审配置文档（含 Claude 模型/推理强度配置说明）
- 容器执行超时可配置化（`worker.timeouts` 按任务类型）+ stream-json 活跃度检测（`worker.stream_monitor`，默认关闭）
- 飞书通知渠道（Webhook 模式，交互卡片格式，双通知时机）
- 每日评审统计报告（定时飞书推送，前日 PR 评审摘要）

#### 容器执行超时与可观测性
> 讨论记录：`docs/discussions/2026-03-27-container-timeout-observability.md`
> 设计文档：`docs/plans/2026-03-29-container-timeout-observability-design.md`
> 说明：2026-03-29 完成全部实施（方案 D 可配置超时 + 方案 E stream-json 活跃度检测）。

- [x] `claude -p` 非交互模式下 stderr 是否有稳定的进度输出（决定能否用作心跳信号）——已验证不可行（stderr 全程静默），改用 `--output-format stream-json --verbose --include-partial-messages` 的 stdout 事件流
- [x] 基于验证结果选定方案：选定方案 E（stream-json stdout 心跳）；2 分钟活跃度阈值，最大静默间隔实测 9 秒，留 13 倍余量；关闭时自动降级为原有 JSON 路径，零改动
- [x] 将 `TaskTimeout` 改为 YAML 可配置（`worker.timeouts.review_pr / fix_issue / gen_tests`），零值时回退硬编码默认值；`stream_monitor.enabled / activity_timeout` 控制活跃度检测开关

### 验证标准
- [x] 创建一个包含已知问题的 PR → 自动评审 → 正确识别问题并分级 → 逐行 comment + summary → 严重问题自动打回（2026-03-27 E2E 验证通过，PR #9）
- [ ] 更新 PR 后自动重新评审（单元测试覆盖，E2E 待补充）
- [ ] Java 和 Vue 仓库各验证一轮
- [x] 每日统计报告定时触发 → 飞书收到统计摘要卡片（含评审总数、成功/失败计数、关键 PR 链接）
- [x] 无评审活动日正确处理（跳过或发送简短通知）

---

## Phase 3：Issue 自动分析与修复

**目标**：分两轮迭代交付——第一轮实现 Issue 自动分析与根因定位（只读），第二轮实现自动修复与 PR 创建（写操作）

**依赖**：Phase 1 完成（Phase 2 非必须依赖，可并行开发）

### 关键设计决策

> 决策 1-6 在 2026-04-02 brainstorming 中确认；决策 7-10 在 2026-04-16 brainstorming 中确认（M3.4/M3.5 设计评审）。

1. **两轮迭代策略**：第一轮（M3.1-M3.3）只做分析与定位，容器只读模式（安全模型同 Phase 2）；第二轮（M3.4-M3.5）才引入写操作（代码修改、push、PR 创建）。第一轮独立交付价值：根因分析报告本身即可帮助开发者快速定位和修复问题。
2. **PR 创建策略（混合模式）**：容器内完成代码修改 + commit + `git push`（Claude Code 核心能力），容器外由 `fix.Service` 通过 Gitea API 创建 PR（结构化操作更可控：描述模板、Issue 关联、标签管理）。仅需保留 Git push 凭证，不需要给容器 Gitea API Token。
3. **Git 凭证管理（credential helper cache）**：entrypoint.sh 修复模式使用 `credential.helper cache --timeout=3600`（内存模式，非文件持久化），clone 过程自动缓存凭证到 cache daemon；clone 完成后照常清除环境变量。已接受风险：Claude 拥有 Bash 执行能力，理论上可通过 `git credential fill` 从 cache daemon 提取凭证；缓解措施：建议为修复任务配置专用受限 Gitea Token（仅目标仓库 push 权限）。（仅第二轮 M3.4 需要，第一轮容器无需 push 权限。）
4. **信息充分性判断（MVP 简化）**：不实现自动监听 Issue 新评论恢复分析的状态机。Claude 直接在分析 prompt 中判断信息是否充分，不充分时在 Issue 评论中报告"信息不足"并列出需补充内容；用户补充后移除再添加 `auto-fix` 标签即可重新触发。后续可平滑升级为自动监听恢复，无架构返工。
5. **修复 PR 自动评审（天然复用）**：`fix.Service` 通过 Gitea API 创建 PR 后，Gitea 发出 PR created Webhook，Phase 2 评审流程自然触发。auto-fix PR 与人工 PR 完全一视同仁，不做特殊处理。不存在无限循环风险（评审只产生 comment，不会再触发 fix）。无需额外代码。
6. **Ref 关联机制（分支/tag 基线选择）**：Issue 的 Ref 字段（Gitea 右侧边栏）用于指定代码基线。Ref 为空时回复提醒用户设置；Ref 无效（分支和 tag 均不存在）时回复错误提醒；两者均为确定性失败，跳过重试。Ref 有效时，容器 `ISSUE_REF` 环境变量注入 + entrypoint checkout，prompt 注入 ref 上下文信息。优先使用 Gitea API 返回的最新 `Issue.Ref`，fallback 到入队时 payload 快照值。
7. **标签触发机制（两标签分离）**：`auto-fix` 标签触发分析（`TaskTypeAnalyzeIssue`），`fix-to-pr` 标签触发修复（`TaskTypeFixIssue`）。两标签并存时 `fix-to-pr` 优先。无前序分析时 Claude 在修复容器中隐式分析；前序分析"信息不足"时系统查 DB 前置检查并提醒用户。
8. **TaskType 拆分**：将现有 `TaskTypeFixIssue`（实际做分析）拆分为 `TaskTypeAnalyzeIssue`（分析）+ `TaskTypeFixIssue`（修复），修正语义。镜像选择、超时配置、通知路由均以 TaskType 为路由依据。历史数据库记录不回填。
9. **单容器全托管执行模型**：修复流程在一次容器调用中完成全部操作（分析→修复→测试→commit→push），不拆分为多容器步骤。Claude Code CLI 核心优势是自主探索和迭代，保持上下文连续性效率最高。
10. **里程碑按层切分**：M3.4 = 纯基础设施（镜像、Pool、TaskType、entrypoint、Webhook），M3.5 = 纯业务逻辑（Prompt、容器执行、PR 创建、通知）。

### 已有基础设施（Phase 1/2 已搭建）

以下能力在 Phase 1/2 中已实现，Phase 3 直接复用：

- **Webhook 接收**：`IssueLabelEvent` 解析 + `auto-fix` 标签检测（`internal/webhook/parser.go`、`event.go`）
- **任务入队**：`HandleIssueLabel` 幂等入队（`internal/queue/enqueue.go`），含 DeliveryID 幂等检查
- **任务模型**：`TaskTypeFixIssue` 已定义，`TaskPayload` 含 `IssueNumber` / `IssueTitle`（`internal/model/task.go`）
- **容器执行**：`buildContainerCmd` 有基础 fix_issue prompt（`internal/worker/container.go`），第一轮需替换为分析 prompt
- **通知**：`EventFixIssueDone` 已定义（`internal/notify/notifier.go`），Processor 已有 fix_issue 通知构建逻辑
- **Gitea API**：`GetIssue` / `ListIssueComments` / `CreateIssueComment` / Label 操作（`internal/gitea/issues.go`）
- **Processor**：fix_issue 当前走默认 `pool.Run()` 路径，需改为分发到 `fix.Service`（同 review 的 `ReviewExecutor` 模式）

### 里程碑

---

#### 第一轮：Issue 分析与定位（只读）

> 容器安全模型与 Phase 2 一致（ReadonlyRootfs、无 push 权限）。交付物为 Issue 评论中的根因分析报告。
>
> **完成说明**：2026-04-04 完成全部实施（M3.1-M3.3）。交付物：Issue 自动分析与根因定位能力，用户可收到结构化分析报告（正常/信息不足/降级三场景）。

##### M3.1 fix.Service 骨架与 Issue 上下文采集
- [x] 新建 `internal/fix` 包，架构仿照 `internal/review`（Service/Execute 模式）
- [x] 定义 `FixExecutor` 窄接口（M3.2 激活路由时使用，M3.1 仅声明接口）
- [x] 通过 Gitea API 采集 Issue 富上下文：Issue 详情（标题、描述、状态）、评论（单页最多 50 条）、标签列表
- [x] Issue 上下文结构体定义（`IssueContext`），包含纯原始数据（Issue 详情、评论、标签），智能提取由 M3.2 Claude 分析完成
- [x] Issue 状态检查：Issue 已关闭时返回 `ErrIssueNotOpen`（类似 review 的 `ErrPRNotOpen`）
- [x] Ref 有效性校验：`RefClient` 窄接口（`GetBranch`/`GetTag`），`validateRef` 先调用 `stripRefPrefix` 剥离 Gitea 返回的 `refs/heads/` / `refs/tags/` 前缀后再查询 API（先查分支再查 tag），均不存在返回 `ErrInvalidIssueRef`；为空返回 `ErrMissingIssueRef`。两种错误均在 Issue 评论中给出友好提醒，Processor 层标记为 SkipRetry
- [x] 单元测试覆盖校验、状态检查、API 错误、正常路径、评论截断、ref 校验（空/无效/分支有效/tag 有效/评论回写失败可重试）
- 注：M3.1 的 fix_issue 任务仍走 `pool.Run()` 默认路径，`fix.Service` 尚未接管 Processor 路由（避免上下文采集阶段任务空跑成功）。M3.2 已激活路由，fix_issue 任务现在走 `fixService.Execute` 路径。

##### M3.2 分析 Prompt 工程与容器执行
- [x] 设计分析 prompt，包含以下层次：
  - 信息充分性判断指令（判断 Issue 是否包含足够信息进行分析）
  - 根因分析指令（代码库搜索策略、调用链追踪、关联文件检查）
  - 输出格式定义（结构化 JSON schema）
- [x] 定义分析输出 JSON schema（`AnalysisOutput`），字段包括：
  - `info_sufficient`（bool）：信息是否充分
  - `missing_info`（[]string）：缺失的信息项（信息不足时填写）
  - `root_cause`：根因定位（文件路径、方法名、行号范围、原因描述）
  - `analysis`：详细分析说明
  - `fix_suggestion`：修复建议
  - `confidence`（high/medium/low）：分析置信度
  - `related_files`（[]string）：相关文件列表
- [x] Prompt 通过 stdin 传入容器（同 Phase 2 安全实践，避免 ps aux 暴露）
- [x] 容器只读模式执行，安全约束同 Phase 2（ReadonlyRootfs + `--disallowedTools` + READ-ONLY 约束文本）
- [x] 双层 JSON 解析（同 Phase 2）：CLI 信封（`CLIResponse`）→ 内层分析结果（`AnalysisOutput`）
- [ ] 技术栈检测复用（可选）：根据仓库文件结构检测技术栈，为分析 prompt 提供上下文
- [x] Ref 信息注入：prompt 中追加"当前代码基于 ref: xxx"上下文，容器启动信息包含 ref checkout 状态
- [x] Claude 模型与推理强度可配置（复用 `claude.model` / `claude.effort` 配置体系）

##### M3.3 分析结果解析与 Issue 评论回写
> 说明：2026-04-04 完成全部实施（含三场景格式化、WritebackError 处理、adaptFixResult 重构、代码审核修复）。
> 详细设计见 `docs/plans/2026-04-04-m3.3-analysis-writeback-design.md`。
- [x] 解析 Claude 输出的结构化分析结果
- [x] **正常场景（信息充分）**：格式化根因分析报告并发布为 Issue 评论
  - 报告内容：置信度 + 根因定位（文件/方法/行号）+ 原因分析 + 修复建议 + 相关文件
  - Markdown 格式化 + UTF-8 安全截断（60000 字节上限）
- [x] **信息不足场景**：发布追问评论，列出需要补充的具体信息项
  - 评论提示用户补充信息后重新添加 `auto-fix` 标签触发
- [x] **降级场景**：Claude 输出解析失败时，将原始输出包裹在动态 fence 代码块中发布（同 Phase 2 降级策略）
- [x] 分析结果持久化到数据库（暂不做；TaskRecord.result 已保存原始输出，Issue 评论本身即持久化）
- [x] 错误处理与降级链：用户至少能看到一条评论；回写失败触发任务重试（与 review 有意不对称）
- [x] Ref 校验评论回写：ref 为空时发送"请设置 Ref"提醒；ref 无效时发送"该 ref 对应的分支和 tag 均不存在"提醒；提醒评论回写失败时返回可重试错误（非 sentinel），asynq 自动重试
- [x] 安全防护：Markdown 转义（含反引号和 # 号）+ 动态 fence 代码块包裹 + 长度截断 + 内部错误不泄露
- [x] 单元测试覆盖（formatter_test.go 12 个 + service_test.go 回写相关 5 个 + processor_test.go adaptFixResult 5 个）

---

#### M3.3.1 远程瘦客户端与 REST API 层

> 独立二进制 `dtw`，类似 GitHub CLI（`gh`），可部署在任意客户端或服务器上，通过 HTTP API 远程操作 DTWorkflow 服务端。
> 需同步扩展服务端 REST API 以支撑瘦客户端的全部功能。当前服务端仅有健康检查和 Webhook 端点，无业务 REST API。

**说明**：2026-04-14 完成全部实施（含代码审核修复：HTTP 客户端超时、signal context 传播、`--token-stdin` 安全读取、注释修正）。

**服务端 REST API 扩展**：
- [x] API 认证中间件（Bearer Token，constant-time compare 防时序攻击）
- [x] 任务管理 API：查询、列表、重试（`RetryTask` 提取为 CLI/API 共用）
- [x] PR 评审触发 API：手动触发指定仓库 PR 的评审（`EnqueueManualReview`）
- [x] Issue 修复触发 API：手动触发指定仓库 Issue 的分析/修复（`EnqueueManualFix`）
- [x] 服务状态与版本查询 API
- [x] API 版本化（`/api/v1/` 前缀）
- [x] `api.tokens` 配置结构与校验（前缀格式、最小长度、identity 唯一性）
- [x] `triggered_by` 列（V17 迁移），区分 webhook / manual 触发来源
- [x] 未配置 tokens 时 API 路由不注册，不影响 Webhook 功能

**瘦客户端（`dtw` 二进制）**：
- [x] 独立二进制，同仓库 `cmd/dtw` 入口 + Makefile 构建目标
- [x] 多服务器认证管理（`dtw auth login/logout/status/switch`），支持同时认证多个 DTWorkflow 实例并切换
- [x] 认证信息持久化（`~/.config/dtw/hosts.yml`，权限 0600）
- [x] `--token-stdin` 安全读取 Token（推荐用于脚本/CI），`--token` 使用时输出安全警告
- [x] 覆盖服务端 CLI 命令：`review-pr` / `fix-issue` / `task status/list/retry`
- [x] `--json` 结构化输出 + 人类可读默认格式（与 `dtworkflow` CLI 输出规范一致）
- [x] 服务器状态检查（`dtw status`）
- [x] `--no-wait` 异步提交 + `WaitForTask` 轮询等待（可配超时）
- [x] HTTP 客户端 30s 超时 + signal context 支持 Ctrl+C 取消
- [x] 命令体系设计与 `dtworkflow` 保持一致，未来新增命令同步扩展

---

#### 第二轮：自动修复与 PR 创建（后续迭代）

> 引入容器写权限，按层切分为 M3.4（基础设施）和 M3.5（业务逻辑）。依赖第一轮分析能力验证后启动。
> 详细设计见 `docs/superpowers/specs/2026-04-16-m3.4-m3.5-fix-execution-design.md`。

##### M3.4 修复基础设施层（已完成 2026-04-16）
- [x] **两级镜像策略**（Phase 4 复用的基础设施）：
  - 新建 `build/Dockerfile.worker-full`（执行镜像）：在现有 worker 镜像基础上叠加 JDK 17 + Maven
  - 两级镜像共享 Node.js + Claude CLI 基础层，Docker layer cache 友好
  - Maven/Gradle 缓存目录重定向到 `/workspace`（2GB tmpfs），避免 `/tmp`（256MB）溢出
  - Makefile 新增 `build-worker-full` 构建目标（依赖 `build-worker`）
- [x] **Pool 多镜像支持**：
  - `PoolConfig` 新增 `ImageFull` 字段，`runContainer` 中 `resolveImage(taskType)` 按任务类型选择镜像
  - 镜像映射：`review_pr` / `analyze_issue` → 轻量镜像；`fix_issue` / `gen_tests` → 执行镜像
  - `ImageFull` 为空时向后兼容，所有任务用 `Image`
  - 配置段 `worker.image_full`，`WorkerConfig` 结构体新增字段，`buildWorkerPoolConfigFromServeConfig` 传递
- [x] **TaskType 拆分**：
  - 新增 `TaskTypeAnalyzeIssue = "analyze_issue"`，现有分析流程从 `fix_issue` 迁移
  - `fix.Service.Execute` 入口按 `payload.TaskType` 路由：`analyze_issue` → 现有分析流程（`executeAnalysis`），`fix_issue` → 新修复流程（`executeFix`）
  - `TaskTimeoutsConfig` 新增 `AnalyzeIssue` 字段（默认 15 分钟），`fix_issue` 调整为 30 分钟（含分析 + 修复 + 测试）
  - `queue/client.go` 新增 `AsynqTypeAnalyzeIssue` 常量 + 路由注册
  - `serve.go` asynq `ServeMux` 注册 `AsynqTypeAnalyzeIssue` handler
  - `queue/processor.go` switch 新增 `TaskTypeAnalyzeIssue` → `fixService`（分析模式）
- [x] **entrypoint.sh 修复模式**：
  - `fix_issue)` case：`credential.helper cache --timeout=3600`（内存模式）+ `git config user.name/email`（Bot 身份）+ Maven/Gradle 缓存重定向
  - `analyze_issue)` case：搬自原 `fix_issue` 只读行为（Ref checkout，无凭证、无写权限）
  - 环境变量照常清除（`GITEA_TOKEN` / `AUTH_URL` 等），不设 pre-commit hook / 不锁 push URL
- [x] **Webhook 标签路由扩展**：
  - 新增 `isFixToPRLabel` 识别 `fix-to-pr` 标签（大小写不敏感）
  - `IssueLabelEvent` 新增 `FixToPRAdded` 字段
  - `HandleIssueLabel` 按标签分流入队：`auto-fix` → `analyze_issue`，`fix-to-pr` → `fix_issue`，后者优先
  - 幂等检查按各自 TaskType 独立查询
  - 并发修复同一 Issue：参照 M2.4 Cancel-and-Replace 机制
- [x] **通知事件拆分**：
  - 新增 `EventIssueAnalyzeStarted` / `EventIssueAnalyzeDone`
  - 现有 `EventIssueFixStarted` / `EventFixIssueDone` 语义修正为修复（非分析）
  - `buildStartMessage` / `buildNotificationMessage` 新增 `TaskTypeAnalyzeIssue` case
- [x] `worker/container.go` `buildContainerEnv` / `buildContainerCmd` 新增 `analyze_issue` case
- [x] `dtw fix-issue` 命令新增 `--fix` flag 区分触发分析或修复
- [x] 示例配置 `dtworkflow.example.yaml` 补充 `worker.image_full` 和 `worker.timeouts.analyze_issue`
- [x] 现有 M3.1-M3.3 分析流程回归验证

##### M3.5 修复业务逻辑层（已完成 2026-04-16）
- [x] **修复 Prompt 设计**（stdin 传入，四段式）：
  - 上下文段：仓库/Issue/Ref 信息 + 指示 Claude 阅读 Issue 评论参考前序分析
  - 修复指令段：分析→修复→补充测试→运行测试（`mvn test` / `npm test`）→失败重试（最多 3 轮）→创建分支 `auto-fix/issue-{id}` + commit + push
  - 约束段：不修改无关文件、不删除现有测试、commit message 格式 `fix: #{issue_id} {简述}`
  - 输出格式段：FixOutput JSON schema
  - 安全：不加 `--disallowedTools` Bash 限制（需运行测试），保留 prompt 层网络访问禁令，包含 `--output-format json`
- [x] **修复输出 JSON schema**（`FixOutput`）：
  - `success` / `info_sufficient` / `missing_info` / `branch_name` / `commit_sha` / `modified_files` / `test_results` / `analysis` / `fix_approach`
  - `TestResults`：`passed` / `failed` / `skipped` / `all_passed`
  - 校验不变量：`success=true` 时 `branch_name` 和 `commit_sha` 必须非空
- [x] **fix.Service `executeFix` 流程**：
  - 前置校验：Issue open + Ref 有效（区分分支 vs tag）
  - "信息不足"前置检查：查 DB 最新 `analyze_issue` 结果，`info_sufficient=false` → Issue 评论提醒 + SkipRetry，不启动容器
  - 采集 Issue 上下文（复用 `collectContext`）
  - 构造修复 prompt + 容器执行（full image）
  - 双层 JSON 解析：CLI 信封 → FixOutput
  - 按结果分流处理
  - 新增窄接口：`PRClient`（`CreatePullRequest`）、`FixStaleChecker`（`GetLatestAnalysisByIssue`），ServiceOption 注入
  - `store/` 新增 `GetLatestAnalysisByIssue` 查询方法
- [x] **PR 创建**（Gitea API）：
  - 触发条件：`success=true` + `branch_name` 非空
  - PR 标题：`fix: #{issue_id} {issue_title}`
  - PR 描述模板：关联 Issue（`fixes #{issue_id}`）+ 根因分析 + 修复方案 + 修改文件 + 测试结果
  - Tag 作为 Ref 边界处理：Gitea API `Base` 不支持 tag，改用仓库默认分支，PR 描述中注明
  - Issue 评论通知：修复 PR 已创建 + PR 链接 + 修改文件数
- [x] **失败处理**：
  - Issue 已关闭 / Ref 无效 / Ref 缺失：同分析模式，Issue 评论提醒 + SkipRetry
  - DB 前置检查"信息不足"：Issue 评论提醒补充信息 + SkipRetry
  - 容器执行失败：Issue 评论报告错误 + 允许 asynq 重试
  - Claude 返回 `info_sufficient=false` / `success=false`：Issue 评论 + SkipRetry
  - Push 成功但 PR 创建失败：Issue 评论报告 + 允许重试
  - 分支已存在（重试场景）：prompt 指示 `git push --force-with-lease`
- [x] **FixResult 扩展**：新增 `Fix *FixOutput` / `PRNumber` / `PRURL` 字段
- [x] 修复 PR 自动评审：无需额外代码，Gitea PR created Webhook 自然触发 Phase 2 评审流程
- [x] 通知适配：Processor 修复模式下飞书+Gitea 通知（开始/成功含 PR 链接/失败三场景）
- [x] 单元测试覆盖

> **完成说明**（2026-04-16）：14 个 commit 实现了完整的 Issue 自动修复到 PR 创建闭环。
> 核心交付：`executeFix` 12 步主流程（前置检查 → 容器执行 → 解析 → PR 创建 → 评论回写）、
> `buildFixPrompt` 四段式修复 prompt、`parseFixResult` 双层 JSON 解析（含成功不变量校验）、
> `checkPreviousAnalysis` fail-open 前置检查、5 种 Issue 评论 formatter、
> Processor 路由 `fix_issue` → `fixService` + `ErrInfoInsufficient`/`ErrFixFailed` SkipRetry、
> 飞书卡片绿色修复成功 + PR 按钮、`serve.go` 装配 PRClient + FixStaleChecker。
> 全量 16 包测试通过，`make build` + Linux amd64 交叉编译成功。

### 交付物

**第一轮**：
- Issue 自动分析与根因定位能力（`internal/fix` 包）
- 分析 prompt 模板
- Issue 评论格式化报告（根因分析 / 信息不足追问 / 失败报告）

**M3.3.1**：
- 服务端 REST API 层（`/api/v1/`，含认证、任务管理、评审/修复触发）
- `dtw` 瘦客户端二进制（多服务器认证、远程命令执行）

**第二轮 M3.4**：
- 两级镜像体系（`Dockerfile.worker-full`：JDK + Maven + Node.js + Claude CLI）
- Pool 多镜像选择（`PoolConfig.ImageFull` + `resolveImage`）
- `TaskTypeAnalyzeIssue` 拆分 + 全链路迁移（webhook/queue/processor/notify/entrypoint/config/dtw）
- entrypoint.sh 修复模式（credential cache + git identity + Maven 缓存重定向）
- Webhook `fix-to-pr` 标签路由 + Cancel-and-Replace

**第二轮 M3.5**：
- 完整的 Issue → 修复 → 测试 → PR 自动化流程（`fix.Service.executeFix`）
- 修复 Prompt 模板（四段式，含测试验证和重试指令）
- PR 创建（Gitea API，含 Tag-as-Ref 处理）+ Issue 评论通知
- 修复输出 JSON schema（`FixOutput` / `TestResults`）
- "信息不足"前置检查 + 完整失败处理链

### 验证标准

**第一轮**：
- 创建一个描述完整的 bug Issue → 添加 `auto-fix` 标签 → 自动分析 → Issue 收到根因分析评论（定位到文件/方法/行号 + 原因分析 + 修复建议）
- 创建一个描述不足的 Issue → 添加标签 → Issue 收到追问评论（列出需补充的信息）
- 创建 Issue 但未设置 Ref → 添加 `auto-fix` 标签 → Issue 收到友好提醒评论（告知用户如何设置 Ref）
- 创建 Issue 并设置 Ref 为不存在的分支/tag → 添加标签 → Issue 收到错误提醒（该 Ref 不存在）
- 创建 Issue 并设置 Ref 为有效分支或 tag → 添加标签 → 自动分析基于该 Ref 的代码执行
- 同一 Issue 重复添加标签不重复触发（幂等性，已有基础设施保障）

**M3.3.1**：
- `dtw auth login` 认证到 DTWorkflow 服务端 → `dtw status` 查看连接状态
- `dtw task list` 远程查询任务列表 → 结果与 `dtworkflow task list` 一致
- `dtw review-pr --repo myrepo --pr 42` 远程触发评审 → 服务端创建评审任务
- 认证多个服务器 → `dtw auth switch` 切换 → 命令作用于切换后的服务器

**第二轮 M3.4**：
- `make build-worker-full` 成功构建执行镜像（含 JDK + Maven + Node.js + Claude CLI）
- `PoolConfig.ImageFull` 配置后，`fix_issue` 任务使用执行镜像、`review_pr` / `analyze_issue` 使用轻量镜像
- `auto-fix` 标签 → 入队 `analyze_issue` 任务；`fix-to-pr` 标签 → 入队 `fix_issue` 任务
- `entrypoint.sh` fix_issue 模式：credential cache 已配置、git identity 已设置、可 commit + push
- `entrypoint.sh` analyze_issue 模式：行为与原 fix_issue 一致（只读）
- 现有 M3.1-M3.3 分析流程不受影响（回归验证）

**第二轮 M3.5**：
- `fix-to-pr` 标签触发 → Claude 在容器内分析 + 修复 + 测试 + push → `fix.Service` 创建 PR → Issue 收到 PR 链接评论
- PR 描述包含根因分析、修复方案、修改文件列表、测试结果；PR 关联 Issue（`fixes #N`）
- 修复 PR 自动进入 Phase 2 评审流程（Gitea Webhook 自然触发）
- 无前序分析 → Claude 隐式分析后修复（不阻拦）
- 前序分析"信息不足" → `fix-to-pr` 触发时 Issue 评论提醒 + 不启动容器
- 修复失败 → Issue 评论报告失败原因
- 分支已存在（重试场景）→ force-with-lease push → PR 正常创建
- 飞书通知：修复开始/成功（含 PR 链接）/失败三场景
- `dtw fix-issue --fix` 远程触发修复 → 服务端创建 fix_issue 任务
- Java 和 Vue 仓库各验证一轮

---

## Phase 4：集中化测试（测试补全）

**目标**：建立 AI 驱动的测试生成机制，补全现有测试缺口，确保生成的测试通过运行验证后再提交

**依赖**：M3.5 完成（容器写权限 + PR 创建能力 + 执行镜像）

### 关键设计决策

> 以下决策在 2026-04-15 brainstorming 中确认，指导后续设计与实施。

1. **AI 原生测试缺口分析**：不依赖传统覆盖率工具（JaCoCo / Istanbul），由 Claude Code CLI 直接分析代码结构、现有测试文件和 Git 变更历史来识别测试缺口。优势：语义级分析（不只看行覆盖，而是评估边界条件和逻辑路径）、零基础设施依赖、跨语言统一。
2. **测试必须验证通过**：生成的测试代码在容器内编译并运行，全部通过后才创建 PR。失败时 Claude 根据错误信息自动修正并重试。
3. **两级镜像策略**（M3.4 引入，Phase 4 复用）：分析镜像（现有 `Dockerfile.worker`，Node.js + Claude Code CLI + Git）服务于 review_pr 和 fix_issue 分析；执行镜像（`Dockerfile.worker-full`，分析镜像 + JDK + Maven/Gradle）服务于 fix_issue 修复和 gen_tests。Worker 池根据任务类型选择镜像。
4. **全栈项目支持**：Java 后端 + Vue 前端一体化仓库，执行镜像同时包含 JDK 和 Node.js，Claude 根据目标模块自动选择测试框架（JUnit 5 / Vitest）。
5. **两轮迭代 + 调度外置**：第一轮（M4.1-M4.2）手动触发完整管线，验证核心能力；第二轮（M4.3）变更驱动，PR 合并后自动补测试。定时补全不纳入 Phase 4 范围，由外部调度器通过 REST API（`POST /api/v1/gen-tests`）触发，DTWorkflow 专注执行。

### 已有基础设施

以下能力已实现或将在前序 milestone 中实现，Phase 4 直接复用：

**已实现**：
- **任务类型**：`TaskTypeGenTests` 已定义，asynq 路由已注册（`internal/model/task.go`、`internal/queue/client.go`）
- **容器命令**：`buildContainerCmd` 有基础 gen_tests prompt（`internal/worker/container.go`），M4.2 Processor 接管后该分支成为死代码（同 review/fix 的 Service 模式）
- **超时配置**：`worker.timeouts.gen_tests` 已有配置结构（默认 20 分钟，含容器内重试时间）
- **技术栈检测**：Phase 2 的 `TechStack` 位掩码，可复用于选择测试框架
- **通知**：飞书 + Gitea 通知框架（Processor 需新增 gen_tests 通知 case，见 M4.2）

**待实现（前序 milestone 交付）**：
- **执行镜像**：M3.4 引入的 `Dockerfile.worker-full`（分析镜像 + JDK + Maven/Gradle）
- **PR 创建**：M3.5 提供容器内 push + Gitea API 创建 PR 能力

### 里程碑

---

#### 第一轮：手动触发测试生成（完整管线）

> 通过 CLI / API 手动触发指定仓库/模块的测试生成。容器使用执行镜像（读写权限），Claude 分析代码缺口 → 生成测试 → 运行验证 → commit push，DTWorkflow 创建 PR。

##### M4.1 test.Service 骨架 + AI 原生测试缺口分析 + Prompt 工程
- [ ] 新建 `internal/test` 包，架构仿照 `internal/review` 和 `internal/fix`（Service/Execute 模式）
- [ ] 定义 `TestExecutor` 窄接口（M4.2 激活路由时使用）
- [ ] AI 原生测试缺口分析设计：
  - Claude 分析源代码目录结构与现有测试文件的映射关系
  - 识别无测试覆盖的关键模块/类/方法
  - 评估优先级：公共 API > 复杂业务逻辑 > 工具类
  - 分析现有测试风格和模式（命名规范、Mock 框架、断言库），确保生成的测试风格一致
- [ ] 测试生成 Prompt 工程：
  - Java 单元测试生成指令（JUnit 5 + Mockito，覆盖 Service / Controller / 工具类）
  - Vue 单元测试生成指令（Vitest，覆盖组件 / Composable / Store / 工具函数）
  - 输出格式定义（结构化 JSON schema）：生成的文件列表、测试策略说明、每个测试的目标描述
  - 验证指令：生成后运行 `mvn test` / `npm test`，失败时根据错误信息自动修正
- [ ] 分析+生成输出 JSON schema（`TestGenOutput`），字段包括：
  - `analysis`：测试缺口分析（未覆盖模块列表、优先级排序）
  - `generated_files`（[]GeneratedFile）：生成的测试文件路径和描述
  - `test_results`：测试运行结果（通过数 / 失败数 / 跳过数）
  - `verification_passed`（bool）：所有测试是否通过
  - `branch_name`：创建的分支名
  - `commit_sha`：提交的 SHA
- [ ] 手动触发入口（参照 M3.3.1 的 `review-pr` / `fix-issue` 模式）：
  - `EnqueueManualGenTests` 函数（`internal/queue/enqueue.go`）
  - REST API handler + 路由注册（`POST /api/v1/gen-tests`，`internal/api/`）
  - 服务端 CLI 命令 `dtworkflow gen-tests --repo --module`（`internal/cmd/`）
  - 瘦客户端命令 `dtw gen-tests --repo --module`（`internal/dtw/cmd/`），支持 `--no-wait` 异步提交
- [ ] 配置扩展：`test_gen` 配置段，仓库级覆盖
  - `enabled`：启用开关
  - `module_scope`：模块范围限定
  - `max_retry_rounds`：容器内自动修正最大轮次（默认 3）
  - `test_framework`：测试框架覆盖（默认自动检测）

##### M4.2 容器执行 + 测试验证 + PR 创建 + 通知
- [ ] Processor 集成：ProcessorOption 注入 TestExecutor，gen_tests 任务分发到 test.Service
- [ ] 容器执行流程（执行镜像，读写权限）：
  - Clone 仓库 + checkout 目标分支
  - Claude 分析测试缺口
  - 生成测试代码文件
  - 运行测试套件验证
  - 容器内 prompt 级自动修正：测试失败时 Claude 在同一容器会话中根据错误信息修改测试代码并重新运行（最多 N 轮，`test_gen.max_retry_rounds` 可配置，默认 3）。注意：此为单次容器调用内的 prompt 重试，与 asynq 的跨容器重试是独立机制；需确保总耗时在 `worker.timeouts.gen_tests` 范围内
  - 全部通过后创建分支（命名规范：`auto-test/{module}-{timestamp}`）+ commit + push
- [ ] 双层 JSON 解析（同 Phase 2/3）：CLI 信封 → TestGenOutput
- [ ] PR 创建（复用 M3.5 能力）：
  - PR 标题：`test: 补充 {module} 测试用例`
  - PR 描述模板：测试缺口分析摘要 + 生成的测试文件列表 + 测试运行结果 + 覆盖的模块/方法
- [ ] 通知适配：Processor 的 `buildStartMessage` / `buildNotificationMessage` 新增 `TaskTypeGenTests` case 分支（当前仅 review_pr / fix_issue，gen_tests 落入 default 不发通知）
- [ ] 通知内容（Gitea + 飞书）：测试生成开始/完成/失败通知
- [ ] 验证失败处理：所有容器内重试耗尽后，在通知中报告失败原因（编译错误、断言失败等），区分基础设施故障（依赖下载失败、构建工具异常）与测试质量问题
- [ ] 结果持久化到数据库（`test_gen_results` 表，字段设计在 M4.2 实施阶段详细定义，参考 `review_results` 表结构）
- [ ] 单元测试覆盖

---

#### 第二轮：变更驱动测试

> PR 合并后自动分析变更代码是否有对应测试覆盖，缺失时自动触发测试生成。

##### M4.3 变更驱动测试生成
- [ ] Webhook 扩展：新增 PR merged 事件处理（当前仅处理 opened / synchronized / reopened）
- [ ] 变更范围分析：解析合并的 diff，识别变更的模块/类/方法
- [ ] 测试覆盖判断：Claude 分析变更代码是否有对应测试覆盖（复用 M4.1 分析能力，范围缩小到变更文件）
- [ ] 自动触发：覆盖不足时自动入队 gen_tests 任务（范围限定为变更相关模块）
- [ ] 去重机制：检查是否已有针对同一模块的 pending gen_tests 任务
- [ ] 可配置开关：仓库级 `test_gen.change_driven.enabled`
- [ ] 单元测试覆盖

### 交付物

**第一轮**：
- 手动触发的测试生成能力（`internal/test` 包）
- Java / Vue 测试生成 Prompt 模板
- 测试验证 + 自动修正机制
- 测试 PR 自动创建

**第二轮**：
- PR 合并后变更驱动的测试自动生成
- Webhook PR merged 事件处理

### 验证标准

**第一轮**：
- 手动触发 `dtw gen-tests --repo myrepo --module service` → 分析测试缺口 → 生成 JUnit 测试 → 容器内 `mvn test` 通过 → 自动创建 PR（含测试文件和运行结果）
- Vue 模块同理：生成 Vitest 测试 → `npm test` 通过 → 创建 PR
- 生成失败时收到通知，包含失败原因

**第二轮**：
- 合并一个无测试覆盖的 PR → 自动检测 → 自动生成对应测试 → 创建 PR
- 已有充分测试覆盖的 PR 合并后不触发生成

---

## Phase 5：AI 驱动 E2E 模拟用户测试

**目标**：实现 AI 规划和执行的端到端测试，形成完整的自动化质量闭环

**依赖**：Phase 1 + Phase 3（Issue 自动创建能力）

### 里程碑

#### M5.1 测试环境对接
- [ ] 对接团队现有的测试环境部署流程
- [ ] 部署状态检测：确认测试环境可用后再执行测试
- [ ] 环境配置管理：测试 URL、账号等

#### M5.2 E2E 测试框架
- [ ] Playwright 集成
- [ ] Docker 镜像扩展：加入浏览器环境
- [ ] 截图 / 录屏能力

#### M5.3 AI 测试规划
- [ ] 基于需求文档/页面结构生成测试场景
- [ ] 测试路径规划：核心业务流程优先
- [ ] 测试数据准备策略

#### M5.4 测试执行与报告
- [ ] AI 驱动 Playwright 执行测试
- [ ] 异常检测：页面错误、接口报错、UI 异常
- [ ] 测试报告生成：成功/失败用例、截图、操作路径
- [ ] 失败时自动创建 Gitea Issue（附完整复现信息）
- [ ] 通知相关人员

#### M5.5 回归测试
- [ ] 记录测试用例为可重复执行的脚本
- [ ] PR 合并到主分支后自动触发回归测试
- [ ] 测试结果趋势分析

### 交付物
- AI 驱动的 E2E 测试能力
- 自动化测试 → Issue 创建的闭环
- 回归测试套件

### 验证标准
- 针对一个 Web 应用 → AI 自动规划测试路径 → 执行 E2E 测试 → 发现已知 bug → 自动创建 Issue（含截图和操作步骤）

---

## 阶段依赖关系

```
Phase 1 (基础设施)
  ├── Phase 2 (PR 评审)
  ├── Phase 3 (Issue 修复) ──┬── Phase 4 (测试补全)
  └──────────────────────────┴── Phase 5 (E2E 测试)
```

- Phase 2、3 在 Phase 1 完成后可并行启动（但建议按顺序，逐步交付价值）
- Phase 4 依赖 Phase 3 M3.5（容器写权限 + PR 创建能力 + 执行镜像）
- Phase 5 依赖 Phase 1 的基础设施和 Phase 3 的 Issue 自动创建能力

---

## 建议实施顺序与节奏

| 阶段 | 说明 | 建议排期 |
|------|------|---------|
| Phase 1 | 基础设施——所有后续功能的基座 | 第一优先 |
| Phase 2 | PR 评审——最快产出业务价值，团队感知最明显 | 紧接 Phase 1 |
| Phase 3 第一轮 | Issue 分析与定位——只读模式，独立交付根因分析价值 | Phase 2 之后 |
| Phase 3 M3.3.1 | 远程瘦客户端与 REST API——服务端 API 层 + `dtw` 瘦客户端 | 第一轮之后 |
| Phase 2 M2.7 | 每日评审统计报告——定时飞书推送，运营可观测性 | Phase 3 第一轮之后或并行 |
| Phase 3 第二轮 | Issue 自动修复与 PR 创建——引入写操作 + 执行镜像，需第一轮验证分析质量后启动 | M3.3.1 之后 |
| Phase 4 第一轮 | 手动触发测试生成——复用执行镜像 + PR 创建能力 | Phase 3 第二轮之后 |
| Phase 4 第二轮 | 变更驱动测试——PR 合并后自动补测试 | Phase 4 第一轮之后 |
| Phase 5 | E2E 测试——最复杂，依赖前面所有基础 | 最后实施 |

---

## 长期演进方向（不在当前范围内）

以下为未来可能的扩展方向，仅做记录，不纳入当前计划：

- **跨仓库关联分析**：Issue 涉及多个仓库时的联动分析与修复
- **知识积累**：评审意见和修复方案的知识库沉淀，提升后续分析效率
- **指标看板**：评审通过率、修复成功率、测试覆盖率趋势等可视化
- **Gitea Actions 集成**：将部分流程迁移到 Gitea Actions 中运行
- **更多通知渠道**：企业微信 / 钉钉集成；飞书应用机器人升级（个人消息 / @用户 / 卡片回调，Webhook 模式已在 M2.6 实现）
- **自适应评审**：根据历史评审数据自动调整评审严格度
- **定时测试补全外置调度**：通过 DTWorkflow REST API（`/api/v1/gen-tests`）由外部调度器（如上层应用或 cron）触发周期性测试补全扫描，DTWorkflow 专注执行而非调度
