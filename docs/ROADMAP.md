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
  - [x] 飞书交互卡片格式化（`feishu_card.go`：蓝色-进行中 / 绿色-成功 / 橙色-需修改 / 红色-失败）
  - [x] 消息内容：评审结论 + 关键 issue 统计摘要 + PR 跳转链接按钮
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
- [ ] 远程测试服务器端到端测试（真实 Gitea PR 流程 → 飞书开始/完成双通知验证）

#### M2.7 每日评审统计报告
> 说明：每日定时通过飞书通知发送前一天的 PR 评审统计摘要。初期聚焦 PR 评审维度，架构预留扩展到 Issue 修复、测试生成等统计维度。
- [ ] 定时调度机制（asynq periodic task，cron 表达式可配置，默认每天 09:00）
- [ ] 前日评审数据聚合（基于 `review_results` + `tasks` 表按时间窗口查���）
  - [ ] 基础统计：评审总数、成功次数、失败次数、需修改（Request Changes）次数
  - [ ] 严重程度分布：CRITICAL / ERROR / WARNING / INFO 各计数
  - [ ] 关键 PR 高亮：评审次数最多的 PR（重新评审触发最多）、issue 数量最多的 PR
  - [ ] 仓库维度分组（多仓库场景下按仓库拆分统计）
- [ ] 飞书统计摘要卡片格式化（复用 M2.6 卡片能力，新设计统计专用布局）
  - [ ] 卡片内容：日期 + 统计表格 + 关键 PR 链接 + 趋势指示（较前日变化）
- [ ] 配置段 `daily_report`
  - [ ] `enabled`：启用开关
  - [ ] `cron`：cron 表达式（默认 `0 9 * * *`）
  - [ ] `timezone`：时区（默认 `Asia/Shanghai`）
  - [ ] `feishu_webhook_url` / `feishu_secret`：独立 Webhook 配置（与实时通知可发不同群）
- [ ] 可扩展统计收集器架构：接口化设计，未来可插拔新增 fix_issue / gen_tests 等维度
- [ ] 空数据处理：前日无评审任务时发送简短"无评审活动"通知或跳过（可配置）
- [ ] 示例配置 `dtworkflow.example.yaml` 补充 `daily_report` 配置示例

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
- [ ] 每日统计报告定时触发 → 飞书收到统计摘要卡片（含评审总数、成功/失败计数、关键 PR 链接）
- [ ] 无评审活动日正确处理（跳过或发送简短通知）

---

## Phase 3：Issue 自动分析与修复

**目标**：分两轮迭代交付——第一轮实现 Issue 自动分析与根因定位（只读），第二轮实现自动修复与 PR 创建（写操作）

**依赖**：Phase 1 完成（Phase 2 非必须依赖，可并行开发）

### 关键设计决策

> 以下决策在 2026-04-02 brainstorming 中确认，指导后续设计与实施。

1. **两轮迭代策略**：第一轮（M3.1-M3.3）只做分析与定位，容器只读模式（安全模型同 Phase 2）；第二轮（M3.4-M3.5）才引入写操作（代码修改、push、PR 创建）。第一轮独立交付价值：根因分析报告本身即可帮助开发者快速定位和修复问题。
2. **PR 创建策略（混合模式）**：容器内完成代码修改 + commit + `git push`（Claude Code 核心能力），容器外由 `fix.Service` 通过 Gitea API 创建 PR（结构化操作更可控：描述模板、Issue 关联、标签管理）。仅需保留 Git push 凭证，不需要给容器 Gitea API Token。
3. **Git 凭证管理（credential helper 缓存）**：entrypoint.sh 在 clone 前配置 `git config --global credential.helper store`，clone 过程自动写入 `.git-credentials` 文件；clone 完成后照常清除 `GITEA_URL` / `REPO_CLONE_URL` 环境变量。凭证仅对 git 命令可用，不像环境变量对所有进程可见，攻击面更小。（仅第二轮 M3.4 需要，第一轮容器无需 push 权限。）
4. **信息充分性判断（MVP 简化）**：不实现自动监听 Issue 新评论恢复分析的状态机。Claude 直接在分析 prompt 中判断信息是否充分，不充分时在 Issue 评论中报告"信息不足"并列出需补充内容；用户补充后移除再添加 `auto-fix` 标签即可重新触发。后续可平滑升级为自动监听恢复，无架构返工。
5. **修复 PR 自动评审（天然复用）**：`fix.Service` 通过 Gitea API 创建 PR 后，Gitea 发出 PR created Webhook，Phase 2 评审流程自然触发。auto-fix PR 与人工 PR 完全一视同仁，不做特殊处理。不存在无限循环风险（评审只产生 comment，不会再触发 fix）。无需额外代码。

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
- [x] 单元测试（7 个用例）覆盖校验、状态检查、API 错误、正常路径、评论截断
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
- [x] 安全防护：Markdown 转义（含反引号和 # 号）+ 动态 fence 代码块包裹 + 长度截断 + 内部错误不泄露
- [x] 单元测试覆盖（formatter_test.go 12 个 + service_test.go 回写相关 5 个 + processor_test.go adaptFixResult 5 个）

---

#### M3.3.1 远程瘦客户端与 REST API 层

> 独立二进制 `dtw`，类似 GitHub CLI（`gh`），可部署在任意客户端或服务器上，通过 HTTP API 远程操作 DTWorkflow 服务端。
> 需同步扩展服务端 REST API 以支撑瘦客户端的全部功能。当前服务端仅有健康检查和 Webhook 端点，无业务 REST API。

**服务端 REST API 扩展**：
- [ ] API 认证中间件（Bearer Token）
- [ ] 任务管理 API：查询、列表、重试
- [ ] PR 评审触发 API：手动触发指定仓库 PR 的评审
- [ ] Issue 修复触发 API：手动触发指定仓库 Issue 的分析/修复
- [ ] 服务状态与版本查询 API
- [ ] API 版本化（`/api/v1/` 前缀）

**瘦客户端（`dtw` 二进制）**：
- [ ] 独立二进制，同仓库 `cmd/dtw` 入口
- [ ] 多服务器认证管理（`dtw auth login/logout/status/switch`），支持同时认证多个 DTWorkflow 实例并切换
- [ ] 认证信息持久化（`~/.config/dtw/hosts.yml`）
- [ ] 覆盖服务端 CLI 命令：`review-pr` / `fix-issue` / `task status/list/retry`
- [ ] `--json` 结构化输出 + 人类可读默认格式（与 `dtworkflow` CLI 输出规范一致）
- [ ] 服务器状态检查（`dtw status`）
- [ ] 命令体系设计与 `dtworkflow` 保持一致，未来新增命令同步扩展

---

#### 第二轮：自动修复与 PR 创建（后续迭代）

> 引入容器写权限，需修改 entrypoint.sh 和容器安全配置。依赖第一轮分析能力验证后启动。

##### M3.4 容器写权限适配与修复执行
- [ ] entrypoint.sh 适配 fix_issue 任务类型：
  - checkout 默认分支（非 PR 分支）
  - 配置 `git config --global credential.helper store`（clone 时自动缓存凭证）
  - clone 完成后照常清除环境变量（凭证已在 `.git-credentials` 文件中）
- [ ] 修复 prompt 设计：基于第一轮分析结果，指导 Claude Code 执行修复
  - 从默认分支创建修复分支（命名规范：`auto-fix/issue-{id}`）
  - 修复代码 + 运行现有测试（如有）
  - git commit（规范化 commit message）+ git push
- [ ] 容器安全配置调整：仓库目录可写（tmpfs 已支持），评估是否需要放宽 `--disallowedTools` 限制
- [ ] 修复输出 JSON schema 定义（分支名、commit SHA、修改文件列表、测试结果等）

##### M3.5 PR 创建与 Issue 关联
- [ ] fix.Service 在容器执行成功后，通过 Gitea API 创建 PR：
  - PR 标题：`fix: #{issue_id} {issue_title}`
  - PR 描述模板：根因分析 + 修复方案 + 影响范围 + `fixes #{issue_id}`（自动关联）
  - 必要时打标签（如 `auto-fix`）
- [ ] Issue 评论通知：修复 PR 已创建，附 PR 链接
- [ ] 修复失败处理：在 Issue 评论中报告失败原因和已尝试的修复方向
- [ ] 修复 PR 自动评审：无需额外代码，Gitea PR created Webhook 自然触发 Phase 2 评审流程
- [ ] 单元测试覆盖

### 交付物

**第一轮**：
- Issue 自动分析与根因定位能力（`internal/fix` 包）
- 分析 prompt 模板
- Issue 评论格式化报告（根因分析 / 信息不足追问 / 失败报告）

**M3.3.1**：
- 服务端 REST API 层（`/api/v1/`，含认证、任务管理、评审/修复触发）
- `dtw` 瘦客户端二进制（多服务器认证、远程命令执行）

**第二轮**：
- 完整的 Issue → 分析 → 修复 → PR 自动化流程
- entrypoint.sh fix_issue 适配（credential helper + 默认分支 checkout）
- 修复 PR 描述模板

### 验证标准

**第一轮**：
- 创建一个描述完整的 bug Issue → 添加 `auto-fix` 标签 → 自动分析 → Issue 收到根因分析评论（定位到文件/方法/行号 + 原因分析 + 修复建议）
- 创建一个描述不足的 Issue → 添加标签 → Issue 收到追问评论（列出需补充的信息）
- 同一 Issue 重复添加标签不重复触发（幂等性，已有基础设施保障）

**M3.3.1**：
- `dtw auth login` 认证到 DTWorkflow 服务端 → `dtw status` 查看连接状态
- `dtw task list` 远程查询任务列表 → 结果与 `dtworkflow task list` 一致
- `dtw review-pr --repo myrepo --pr 42` 远程触发评审 → 服务端创建评审任务
- 认证多个服务器 → `dtw auth switch` 切换 → 命令作用于切换后的服务器

**第二轮**：
- 创建一个描述完整的 bug Issue → 添加 `auto-fix` 标签 → 自动分析根因 → 自动创建修复 PR → PR 关联 Issue → Phase 2 自动评审修复 PR
- 修复失败时 Issue 收到失败报告评论
- Java 和 Vue 仓库各验证一轮

---

## Phase 4：集中化测试（测试补全）

**目标**：补全现有测试缺口，建立变更驱动的测试生成机制

**依赖**：Phase 1 完成

### 里程碑

#### M4.1 测试覆盖率分析
- [ ] Java 项目：集成 JaCoCo 覆盖率报告解析
- [ ] Vue 项目：集成 Istanbul/c8 覆盖率报告解析
- [ ] 识别未覆盖的关键模块/方法（按业务重要性排序）

#### M4.2 测试用例生成
- [ ] Java 单元测试生成（JUnit 5）
  - [ ] Service 层测试（Mock 依赖）
  - [ ] Controller 层测试（MockMvc）
  - [ ] 工具类测试
- [ ] Vue 单元测试生成（Vitest / Jest）
  - [ ] 组件测试
  - [ ] Composable / Store 测试
  - [ ] 工具函数测试
- [ ] 集成测试生成
- [ ] 生成的测试必须能通过运行（编译正确、断言合理）

#### M4.3 测试补全任务
- [ ] 定时任务：周期性扫描覆盖率 → 生成缺失测试 → 创建 PR
- [ ] 手动触发：通过 CLI / API 指定仓库/模块触发测试生成
- [ ] PR 描述包含：新增覆盖的模块、测试策略说明

#### M4.4 变更驱动测试
- [ ] 监听 PR 合并事件
- [ ] 分析合并的代码变更
- [ ] 判断是否有对应测试覆盖
- [ ] 缺失时自动生成测试并创建 PR

### 交付物
- 测试覆盖率分析工具
- Java / Vue 测试生成能力
- 定时补全 + 变更驱动的测试生成流程

### 验证标准
- 指定一个测试覆盖率低的模块 → 自动生成测试 → 测试可运行通过 → 覆盖率提升
- 合并一个无测试覆盖的 PR → 自动检测 → 生成对应测试 PR

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
  ├── Phase 3 (Issue 修复) ──┐
  ├── Phase 4 (测试补全)     │
  └──────────────────────────┴── Phase 5 (E2E 测试)
```

- Phase 2、3、4 在 Phase 1 完成后可并行启动（但建议按顺序，逐步交付价值）
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
| Phase 3 第二轮 | Issue 自动修复与 PR 创建——引入写操作，需第一轮验证分析质量后启动 | M3.3.1 之后 |
| Phase 4 | 测试补全——补足质量基础，为 Phase 5 做准备 | Phase 3 之后 |
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
