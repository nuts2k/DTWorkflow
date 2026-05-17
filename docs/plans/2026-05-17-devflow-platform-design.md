# DevFlow 平台架构设计

> AI 驱动的轻量研发流程管理平台，编排 DTWorkflow Agent 服务

## 1. 背景与动机

### 1.1 现状

DTWorkflow 已实现六大自动化能力：PR 评审（M2）、Issue 分析与修复（M3）、测试生成（M4）、E2E 测试（M5）、迭代式评审修复闭环（M6.1）、文档驱动编码（M6.2）。这些能力通过 CLI + REST API + Webhook 接入，配置通过 YAML 文件管理。

当前架构的局限：
- **单实例部署**：SQLite + 本地 Docker Worker，无法水平扩展
- **无可视化界面**：团队成员通过 CLI / 飞书通知了解任务状态，缺少统一看板
- **配置管理原始**：YAML 文件编辑 + SSH 登录服务器修改，协作困难
- **缺少团队视角**：无法直观看到团队整体研发效能指标

### 1.2 目标

建设 DevFlow 平台，定位为"AI 驱动的轻量研发流程管理平台"：
- 为全团队提供日常使用的 Web 界面
- 统一编排和可视化 DTWorkflow 的自动化能力
- 支持 DTWorkflow 多实例水平扩展（算力扩容）
- 通过 AI Chat 提供更自然的交互方式
- 数据报表替代飞书每日报告，提供更丰富的洞察

### 1.3 两个项目的分工

| 项目 | 定位 | 职责 |
|---|---|---|
| **DTWorkflow** | AI 代码自动化 Agent 服务 | 具体执行：评审、修复、测试、编码。通过 Docker 容器运行 Claude Code CLI |
| **DevFlow** | 研发流程管理平台 | 编排与管理：接收 Webhook、业务决策、任务分发、状态可视化、配置管理、数据分析 |

类比：DTWorkflow 是"手和脑"（干活的），DevFlow 是"指挥部"（编排、展示、管理）。

---

## 2. 系统架构

### 2.1 整体拓扑

```
用户浏览器 (Vue 3 SPA)
    │
    ▼
DevFlow API Server (Go, 1-2 实例)
    │  职责：UI API / Webhook 接收 / WebSocket / 业务决策 / 任务入队
    │
    ├──── PostgreSQL ◄──── 所有服务共享（DevFlow 表 + DTWorkflow 表）
    │         ▲
    ├──── Redis ◄───────── asynq 队列 + 事件 pub/sub + 心跳注册
    │         ▲
    ├──→ DTWorkflow 实例 A (机器 1)
    │     ├── Go 进程（asynq worker + REST API + 心跳）
    │     └── Docker Worker 池（Claude Code CLI 容器）
    │
    ├──→ DTWorkflow 实例 B (机器 2)
    │     ├── Go 进程（asynq worker + REST API + 心跳）
    │     └── Docker Worker 池
    │
    └──→ DTWorkflow 实例 N ...
```

所有服务部署在同一内网。PostgreSQL 和 Redis 各一个实例，所有服务共享连接。

### 2.2 架构原则

1. **Webhook 统一接入**：所有 Gitea Webhook 发送到 DevFlow（不再直接发到 DTWorkflow）。DevFlow 做业务决策后入队
2. **DTWorkflow 变为纯 Worker**：DTWorkflow 实例只从 asynq 队列消费任务 + 执行 + 回写结果。REST API 保留用于 dtw 瘦客户端直连和 DevFlow 代理调用
3. **共享数据层**：DevFlow 直接读 DTWorkflow 的任务/结果表（只读），写操作通过 DTWorkflow API 或 asynq 入队
4. **事件驱动同步**：DTWorkflow 任务状态变更通过 Redis pub/sub 广播，DevFlow 订阅后推送到前端 WebSocket
5. **边界清晰**：DevFlow 只写 DevFlow 表，DTWorkflow 只写 DTWorkflow 表。跨项目只有"读 + 事件"两种交互

### 2.3 数据层设计

#### PostgreSQL 表集划分

| 表集 | 所有者 | 读取者 | 说明 |
|---|---|---|---|
| `tasks`, `review_results`, `test_gen_results`, `e2e_results`, `code_from_doc_results`, `iteration_sessions`, `iteration_rounds` | DTWorkflow | DevFlow (只读) | DTWorkflow 现有表，从 SQLite 迁移 |
| `workspaces`, `members`, `repositories`, `repo_configs` | DevFlow | DevFlow | 团队、仓库、配置管理 |
| `chat_sessions`, `chat_messages` | DevFlow | DevFlow | AI Chat 历史 |
| `audit_log` | DevFlow | DevFlow | 操作审计 |

#### Redis 用途

| Key 模式 | 用途 | 生产者 | 消费者 |
|---|---|---|---|
| `asynq:*` | 任务队列 | DevFlow (入队) | DTWorkflow (消费) |
| `dtw:instance:{id}` | 实例注册（TTL 30s） | DTWorkflow | DevFlow |
| `dtw:events` channel | 任务状态事件 | DTWorkflow | DevFlow |
| `devflow:ws:*` | WebSocket 会话状态 | DevFlow | DevFlow |

### 2.4 认证体系

- **用户认证**：JWT + HttpOnly Cookie（内部系统）。初期用户名密码，后续可接 LDAP/SSO
- **DTWorkflow API 认证**：复用现有 Bearer Token 机制（`api.tokens` 配置）
- **Gitea 交互认证**：复用 DTWorkflow 现有三账号 Token 体系（review / fix / gen_tests）

---

## 3. DevFlow 核心模块

### 3.1 Agent Router（DTWorkflow 实例管理）

**职责**：发现、监控、路由 DTWorkflow 实例。

**服务发现**：
- DTWorkflow 实例启动时写 Redis key `dtw:instance:{id}`，值为 JSON 元数据（地址、端口、版本、支持的任务类型、当前活跃任务数）
- 每 10 秒续约（EXPIRE 30s）
- DevFlow 定期（5s）扫描 `dtw:instance:*` keys 更新实例列表
- 实例挂掉 → key 过期 → 自动下线

**健康检查**：
- 主检查：Redis key 存活
- 辅助检查：定期 HTTP GET DTWorkflow `/healthz`（可选，fail-open）

**路由策略**：
- 默认：Round Robin
- 可选：最少活跃任务（读 asynq 队列深度按 worker 统计）
- 可选：仓库亲和（同一仓库的任务优先路由到同一实例，利用 Docker 镜像缓存和 Git clone 缓存）

**注意**：asynq 本身已经支持多 worker 并发消费同一队列。Agent Router 的"路由"不是直接转发请求，而是在需要直接调用 DTWorkflow REST API 时（如查询日志、触发手动操作）选择目标实例。大部分任务分发由 asynq 自动完成。

### 3.2 Task Orchestrator（任务编排）

**职责**：接管 DTWorkflow 现有的 Webhook 处理和业务决策逻辑。

**从 DTWorkflow 迁移的逻辑**：
- `HandlePullRequest`：PR opened/synchronized 事件处理
- `HandleIssueLabel`：Issue 标签事件处理
- `handleMergedPullRequest`：PR merged 后变更驱动测试 + E2E 回归
- 幂等检查（DeliveryID）
- Cancel-and-Replace 机制
- Bot PR 过滤
- 配置检查（仓库启用、严重度阈值等）

**新增能力**：
- 来自 UI 的手动触发（按钮点击 → API → 入队）
- 来自 Chat 的意图转化（Chat 指令 → 解析 → 入队）
- 批量操作（一键触发多仓库评审等）

**入队方式**：直接调用 asynq client 入队，DTWorkflow 实例自动消费。

### 3.3 实时通信层

**后端**：
```
DTWorkflow 实例 ──(Redis pub/sub)──→ DevFlow Realtime Hub ──(WebSocket)──→ Vue SPA
```

- DTWorkflow 任务 started/completed/failed 时发布 Redis 事件
- DevFlow Hub 维护 WebSocket 连接池，按用户分发事件
- 支持 workspace 级广播（同一团队的所有成员收到更新）

**前端**：
- Vue composable `useWebSocket()` 管理连接生命周期
- 收到事件后更新 Pinia store 或触发 API 刷新（类似 Multica 的 `useRealtimeSync`）
- 断线重连 + 重连后全量刷新

### 3.4 仓库 + 团队管理

**数据模型**：
```
Workspace (团队)
  ├── Members (成员，关联 Gitea 账号)
  └── Repositories (仓库)
       └── RepoConfig (配置)
            ├── 评审配置 (维度、严重度、忽略模式)
            ├── 测试配置 (模块范围、框架)
            ├── 修复配置 (标签映射)
            ├── E2E 配置 (环境、回归策略)
            ├── 迭代配置 (轮次、阈值)
            └── 通知配置 (飞书 Webhook、路由规则)
```

**配置迁移策略**：
- 初期：DevFlow 从 DTWorkflow 的 YAML 配置文件读取并展示，用户在 UI 上修改后写回 YAML（过渡方案）
- 目标：配置完全持久化到 PostgreSQL，DTWorkflow 通过 API 或共享 DB 读取

### 3.5 AI Chat

**分层实现**：

| 层 | 职责 |
|---|---|
| 前端 Chat UI | 消息渲染（Markdown）、输入框、历史记录、打字机效果 |
| 意图识别 | LLM 解析用户输入，提取 repo/PR/issue/操作类型 |
| 操作映射 | 将意图映射到 DevFlow API 调用（入队任务 / 查询状态 / 修改配置） |
| 结果回显 | 任务完成后将结果格式化为 Chat 消息推送回来 |

**初期 MVP**：
- 支持的意图：触发评审、触发修复、触发测试、查询任务状态、查询仓库统计
- 意图识别：结构化 prompt + function calling（JSON mode），不需要复杂 Agent 框架
- 后续扩展：多轮对话、上下文记忆、自定义工作流

### 3.6 数据分析 + 报表

**数据源**：直接 SQL 查询 PostgreSQL（DevFlow 表 + DTWorkflow 表 JOIN）。

**报表维度**：

| 维度 | 指标 | 数据源 |
|---|---|---|
| 评审 | 通过率、Request Changes 率、高频问题类型、平均耗时 | `review_results` + `tasks` |
| 修复 | 成功率、PR 合并率、平均修复时间 | `tasks` (fix_issue) + Gitea API |
| 测试 | 生成量、通过率、覆盖模块数、失败分类分布 | `test_gen_results` |
| E2E | 用例通过率、高频失败用例、失败分类、趋势 | `e2e_results` |
| 团队 | PR 周转时间、Issue 关闭速度、AI 介入率 | Gitea API + DTWorkflow 表 |

**替代飞书每日报告**：DevFlow 提供更丰富的可视化，飞书报告可降级为摘要链接。

---

## 4. DTWorkflow 改造方案

### 4.1 SQLite → PostgreSQL 迁移

**范围**：`internal/store/` 包，约 20+ 个 SQL 查询函数。

**迁移策略**：
1. 新建 `internal/store/pg/` 子包，实现 PostgreSQL 版本的 Store 接口
2. 将现有 SQLite 迁移文件（V1-V26）翻译为 PostgreSQL DDL
3. 主要语法差异：`INTEGER PRIMARY KEY AUTOINCREMENT` → `SERIAL`、`json_extract` → `jsonb` 操作符、`datetime('now')` → `NOW()`、`IFNULL` → `COALESCE`
4. 配置中新增 `database.driver` 切换（`sqlite` / `postgres`），保留 SQLite 作为开发/测试后备
5. 数据迁移脚本：从 SQLite dump → PostgreSQL 导入（一次性）

**风险控制**：Store 接口不变，上层代码零改动。通过接口切换驱动，可随时回退。

### 4.2 实例心跳注册

```go
// 启动时注册
redis.Set("dtw:instance:{id}", instanceMeta, 30*time.Second)

// 每 10s 续约
ticker := time.NewTicker(10 * time.Second)
for range ticker.C {
    redis.Expire("dtw:instance:{id}", 30*time.Second)
}

// instanceMeta JSON
{
    "id": "dtw-01",
    "address": "10.0.1.5:8080",
    "version": "1.2.0",
    "started_at": "2026-05-17T10:00:00Z",
    "capabilities": ["review_pr", "fix_issue", "gen_tests", "run_e2e", "code_from_doc"],
    "active_tasks": 3,
    "max_workers": 5
}
```

### 4.3 事件广播

任务状态变更时通过 Redis pub/sub 发布事件：

```go
// Processor 中任务完成/失败时
event := TaskEvent{
    Type:      "task:completed",
    TaskID:    taskID,
    TaskType:  "review_pr",
    Repo:      "myrepo",
    Status:    "succeeded",
    Result:    summaryJSON,
    Timestamp: time.Now(),
}
redis.Publish("dtw:events", json.Marshal(event))
```

DevFlow 订阅 `dtw:events` channel，转发到 WebSocket Hub。

### 4.4 Worker 模式启动

```bash
# 现有模式（保持向后兼容）：同时启动 Webhook + API + Worker
dtworkflow serve

# 新模式：只启动 Worker（asynq consumer + REST API，不启动 Webhook handler）
dtworkflow serve --mode worker

# DevFlow 模式下推荐 --mode worker，Webhook 由 DevFlow 接收
```

---

## 5. DevFlow 技术栈

| 层 | 技术 | 说明 |
|---|---|---|
| 前端框架 | Vue 3 + Vite | 团队已有 Vue 经验，纯 SPA（内部工具不需要 SSR） |
| 状态管理 | Pinia | Vue 3 官方推荐，比 Vuex 更轻量 |
| 路由 | Vue Router | 标准选择 |
| UI 组件库 | 待定（Naive UI / Element Plus / Ant Design Vue） | 需要评估 |
| API 通信 | Axios / ofetch + Vue Query (TanStack Query Vue) | 服务端状态管理，类似 Multica 的 React Query 模式 |
| 图表 | ECharts / Chart.js | 报表可视化 |
| 后端框架 | Chi (推荐) 或 Gin | Chi 更轻量、符合 Go 标准库风格；DTWorkflow 用 Gin，统一也可以 |
| DB 访问 | sqlc + pgx | Multica 验证过的模式，类型安全 |
| 实时 | gorilla/websocket + Redis pub/sub | WebSocket Hub 模式 |
| 认证 | JWT + HttpOnly Cookie | 内部系统，初期简单认证 |

---

## 6. DevFlow 项目结构

```
devflow/
├── apps/
│   └── web/                          # Vue 3 SPA
│       ├── public/
│       ├── src/
│       │   ├── api/                  # API client + 类型定义
│       │   ├── composables/          # 组合式函数
│       │   │   ├── useWebSocket.ts   # WebSocket 连接管理
│       │   │   ├── useAuth.ts        # 认证状态
│       │   │   └── useTask.ts        # 任务查询 hooks
│       │   ├── stores/               # Pinia stores
│       │   │   ├── auth.ts           # 认证状态
│       │   │   ├── workspace.ts      # 当前 workspace
│       │   │   └── ui.ts             # UI 状态（侧栏、主题）
│       │   ├── views/                # 页面
│       │   │   ├── dashboard/        # 首页仪表盘
│       │   │   ├── tasks/            # 任务看板
│       │   │   ├── repos/            # 仓库管理
│       │   │   ├── chat/             # AI Chat
│       │   │   ├── reports/          # 数据报表
│       │   │   └── settings/         # 设置（成员、通知等）
│       │   ├── components/           # 共享组件
│       │   ├── router/               # 路由定义
│       │   ├── utils/                # 工具函数
│       │   ├── App.vue
│       │   └── main.ts
│       ├── index.html
│       ├── vite.config.ts
│       ├── tsconfig.json
│       └── package.json
├── server/                           # Go 后端
│   ├── cmd/server/
│   │   ├── main.go                   # 入口
│   │   └── router.go                 # 路由组装
│   ├── internal/
│   │   ├── handler/                  # HTTP handlers
│   │   │   ├── auth.go               # 登录/登出/用户信息
│   │   │   ├── workspace.go          # workspace CRUD
│   │   │   ├── repository.go         # 仓库管理
│   │   │   ├── task.go               # 任务查询/触发/取消
│   │   │   ├── webhook.go            # Gitea Webhook 接收
│   │   │   ├── chat.go               # Chat API
│   │   │   ├── report.go             # 报表数据 API
│   │   │   └── agent.go              # DTWorkflow 实例状态
│   │   ├── service/
│   │   │   ├── orchestrator.go       # 任务编排（业务决策 + 入队）
│   │   │   ├── chat.go               # Chat 意图识别 + 操作映射
│   │   │   └── report.go             # 报表数据聚合
│   │   ├── middleware/
│   │   │   ├── auth.go               # JWT 认证
│   │   │   ├── workspace.go          # workspace 上下文注入
│   │   │   └── cors.go               # CORS
│   │   ├── realtime/
│   │   │   ├── hub.go                # WebSocket Hub
│   │   │   └── redis_subscriber.go   # Redis 事件订阅
│   │   ├── agent/
│   │   │   ├── registry.go           # 实例注册表（Redis 扫描）
│   │   │   ├── router.go             # 负载均衡路由
│   │   │   └── health.go             # 健康检查
│   │   └── model/                    # 数据模型
│   ├── pkg/db/
│   │   ├── migrations/               # PostgreSQL 迁移
│   │   └── queries/                  # sqlc SQL 文件
│   ├── sqlc.yaml
│   └── go.mod
├── deploy/
│   ├── docker-compose.yml            # 一键启动（PG + Redis + DevFlow + DTWorkflow）
│   └── nginx.conf                    # 可选反向代理
├── docs/
├── Makefile
└── README.md
```

---

## 7. 实施路线图

### Phase 0：DTWorkflow 基础改造（前置条件，~2-3 周）

在 DTWorkflow 现有仓库中完成。

| 步骤 | 内容 | 工作量 |
|---|---|---|
| P0.1 | SQLite → PostgreSQL 迁移（store 层接口不变，SQL 适配 + pgx 连接池） | 中 |
| P0.2 | 实例心跳注册（Redis key + TTL + 实例元数据） | 小 |
| P0.3 | 任务事件广播（Redis pub/sub，任务状态变更时发布） | 小 |
| P0.4 | Worker 模式启动（`--mode worker`，不启动 Webhook handler） | 小 |

**验证标准**：
- 两个 DTWorkflow 实例连同一个 PG + Redis，通过 asynq 分配任务，互不冲突
- DevFlow（或测试脚本）能通过 Redis 发现两个实例并读取元数据
- 任务完成事件通过 Redis pub/sub 可被外部订阅

### Phase 1：DevFlow 骨架 + 任务看板（~4-6 周）

新建 DevFlow 仓库，首个可用交付。

| 步骤 | 内容 |
|---|---|
| P1.1 | Go 后端骨架（项目结构、路由、sqlc、pgx、认证中间件、配置管理） |
| P1.2 | Vue 3 前端骨架（Vite、Vue Router、Pinia、API client、登录页、基础布局） |
| P1.3 | Agent Router 基础版（Redis 心跳发现、Round Robin、实例状态 API） |
| P1.4 | 任务看板 v1（直接读 DTWorkflow 任务表，列表/筛选/详情/日志） |
| P1.5 | WebSocket 实时推送（DTWorkflow 事件 → Redis → Hub → 前端，任务状态自动刷新） |
| P1.6 | 手动触发操作（前端按钮 → DevFlow API → asynq 入队 或 DTWorkflow API 代理调用） |

**验证标准**：
- 团队成员登录 DevFlow Web 界面，看到所有进行中/已完成的任务列表
- 任务状态实时更新（DTWorkflow 完成任务后 Dashboard 秒级刷新）
- 通过界面手动触发 PR 评审/Issue 修复/测试生成

### Phase 2：配置管理 + Webhook 接管（~3-4 周）

| 步骤 | 内容 |
|---|---|
| P2.1 | Workspace + Member 模型（PostgreSQL 表、CRUD API、前端管理页） |
| P2.2 | Repository 配置管理（仓库注册、评审/测试/修复配置 UI） |
| P2.3 | Webhook 接管（Gitea Webhook 发到 DevFlow → 业务决策 → asynq 入队） |
| P2.4 | 通知配置 UI（飞书 Webhook 配置 + 通知路由规则） |

**验证标准**：
- 通过 UI 注册仓库、配置评审规则，替代 YAML 编辑
- Gitea Webhook 指向 DevFlow 后，现有自动化流程正常运行
- 新成员通过 UI 加入团队，无需 SSH 登录服务器

### Phase 3：数据报表（~2-3 周）

| 步骤 | 内容 |
|---|---|
| P3.1 | 评审统计仪表盘（通过率、高频问题、趋势图表） |
| P3.2 | 修复 + 测试统计（成功率、耗时分布） |
| P3.3 | 团队效能指标（PR 周转时间、Issue 关闭速度、AI 介入率） |

### Phase 4：AI Chat（~3-4 周）

| 步骤 | 内容 |
|---|---|
| P4.1 | Chat 界面（消息列表 + 输入框 + 历史记录持久化） |
| P4.2 | 意图识别（LLM 解析用户输入 → 映射到操作） |
| P4.3 | 结果回显（任务完成后推送到 Chat + 交互式追问） |

---

## 8. 关键设计决策总结

| # | 决策 | 结论 | 理由 |
|---|---|---|---|
| D1 | 产品定位 | DTWorkflow = Agent 服务，DevFlow = 管理平台 | 职责分离，各自可独立演进 |
| D2 | 仓库策略 | 两个独立仓库，通过 API + 共享 DB 集成 | 独立版本管理和部署节奏 |
| D3 | 集成方式 | 共享 PostgreSQL（DevFlow 只读 DTWorkflow 表） | 报表 + 看板直接 SQL，无需数据同步 |
| D4 | 数据库 | PostgreSQL + Redis（DTWorkflow 从 SQLite 迁移） | 多实例要求共享状态 |
| D5 | 前端 | Vue 3 + Vite 纯 SPA | 团队已有 Vue 经验，内部工具不需要 SSR |
| D6 | 后端 | Go | 与 DTWorkflow 统一技术栈 |
| D7 | 网关 | DevFlow 内置 Agent Router | 不引入额外组件，Redis 心跳发现 |
| D8 | 部署拓扑 | 同一内网，共享 PG + Redis | 算力水平扩展，非地理分布 |
| D9 | 多实例策略 | asynq 天然多 worker + Agent Router 辅助路由 | 任务分发由队列完成，Router 用于直接 API 调用 |
| D10 | 配置迁移 | 初期 YAML 导入 UI → 目标 PostgreSQL 持久化 | 渐进式迁移，不一步到位 |

---

## 9. 风险与缓解

| 风险 | 影响 | 缓解措施 |
|---|---|---|
| SQLite→PG 迁移引入 bug | DTWorkflow 现有功能受影响 | Store 接口不变，充分测试，保留 SQLite 驱动可回退 |
| 共享 DB 的 schema 耦合 | DTWorkflow 表结构变更需协调 | DevFlow 只读 DTWorkflow 表，不做写操作；表结构变更由 DTWorkflow 迁移文件管理 |
| 前端工程化投入大 | Phase 1 交付时间延长 | 选用成熟组件库减少自定义开发；初期 UI 可以粗糙 |
| Webhook 接管切换风险 | 切换期间可能丢事件 | Phase 2 之前 DTWorkflow 保持现有 Webhook 能力；灰度切换，先双写再单写 |
| asynq 多实例任务分配不均 | 某些实例空闲而其他过载 | asynq 内置权重调度；Agent Router 可调整队列优先级 |

---

## 10. 与 DTWorkflow 现有路线图的关系

DTWorkflow 现有路线图（Phase 1-6）继续执行，不受 DevFlow 影响：

- **M5.4（E2E 回归自动化）**、**M6.2（文档驱动编码）** 等未完成里程碑照常推进
- **Phase 0 改造**是新增的前置工作，不影响现有功能
- DevFlow Phase 2 "Webhook 接管"完成前，DTWorkflow 保持现有 Webhook 接收能力
- 长期目标：DTWorkflow 的 Webhook 处理、配置管理、通知路由等"编排逻辑"逐步迁移到 DevFlow，DTWorkflow 收缩为纯执行引擎

---

## 11. 参考

- Multica 架构报告（`../multica/docs/architecture-report.html`）：monorepo 分层、实时同步、daemon 模式、状态管理不变量
- DTWorkflow PRD（`docs/PRD.md`）：现有需求和系统架构
- DTWorkflow ROADMAP（`docs/ROADMAP.md`）：现有里程碑和实施计划
