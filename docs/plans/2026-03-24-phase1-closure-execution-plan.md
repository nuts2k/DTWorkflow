# Phase 1 收口执行计划（可跑通环境）

> 日期：2026-03-24
> 范围：围绕 `docs/ROADMAP.md` 中 Phase 1 的交付物与验收标准，优先完成一条可重复执行、可观测、可验证的最小运行链路；同时把其中可由 Claude 连续推进的步骤与需要用户配合提供的信息明确拆开。

## 1. 目标

本计划的目标不是继续扩展新业务能力，而是把当前已完成的 M1.0-M1.8 基础设施，收口为一套**真正可跑、可验、可交付**的 Phase 1 环境与清单。

本轮优先达成以下结果：

1. 本地存在一套可运行的 `dtworkflow` 配置与启动方式
2. `Webhook -> Queue -> Worker -> task status/list` 主链路可手工打通
3. `/healthz` 与 `/readyz` 可作为真实验收入口使用
4. 明确哪些步骤可由 Claude 连续执行，哪些步骤必须等待用户提供真实外部信息
5. 为后续收口 `docker-compose.yml`、冷启动基线、并发基线留出清晰顺序

## 2. 当前判断与执行策略

### 2.1 当前判断

基于当前代码与配置约束，Phase 1 第一轮收口**不建议直接以 `docker compose up` 作为唯一启动路径**，而应优先采用：

- **本机运行 `dtworkflow` 主进程**
- **Docker 负责 Redis 与 Worker 容器**

原因：

1. `serve` 已切到统一配置入口，配置校验要求 `notify.default_channel` 对应渠道已启用；仅靠当前 `docker-compose.yml` 中的少量环境变量，不足以稳定满足 M1.8 后的配置要求
2. 当前 `docker-compose.yml` 未挂载 YAML 配置文件，且未显式配置 `notify.channels.gitea.enabled=true`
3. `/readyz` 已检查 `gitea_configured`、`notifier_enabled`、`worker_image_present` 等执行关键前提；第一轮更适合用本机直接观察与定位问题
4. 当前 Worker 仍未实现 clone/worktree 真实仓库执行链路，本轮应先把“最小执行链路”跑通，再决定是否把该项补齐或迁出 Phase 1

### 2.2 本轮执行顺序

推荐按以下顺序推进：

1. 用户提供必要的外部信息
2. Claude 生成本地 `dtworkflow.yaml`
3. Claude 构建 worker image
4. Claude 启动 Redis
5. Claude 本机启动 `dtworkflow serve --config ...`
6. Claude 验证 `/healthz` 与 `/readyz`
7. Claude 构造并发送一条手工 PR Webhook
8. Claude 用 `task list/status` 验证任务链路
9. 如用户提供真实 Gitea 信息与真实 PR/Issue，Claude 再验证通知回写闭环
10. 主链路稳定后，再收口 `docker-compose.yml`

## 3. 用户配合清单（必须先提供）

以下信息中，前 3 项是本轮最小必需；第 4-6 项用于验证真实通知闭环。

### 3.1 最小必需信息

- [ ] `CLAUDE_API_KEY`：真实可用的 Claude API Key
- [ ] `GITEA_URL`：真实 Gitea 实例地址（`http://` 或 `https://`）
- [ ] `GITEA_TOKEN`：真实可用的 Gitea API Token
- [ ] `WEBHOOK_SECRET`：本轮联调使用的固定签名密钥

### 3.2 若要验证“真实通知回写闭环”，还需提供

- [ ] `REPO_OWNER`
- [ ] `REPO_NAME`
- [ ] `REPO_FULL_NAME`（格式 `owner/repo`）
- [ ] `REPO_CLONE_URL`
- [ ] 一个真实存在的 `PR_NUMBER`（优先）或 `ISSUE_NUMBER`
- [ ] 确认该 Token 对目标仓库具备评论权限

### 3.3 用户需要做的唯一决策

- [ ] 决定本轮是否把“通知回写真实闭环”作为 Phase 1 必验项

建议：

- 若本轮目标是先打通执行基座，则可先接受“主链路通过、通知闭环待真实仓库验证”
- 若本轮目标是形成完整对外演示环境，则应把真实通知回写纳入本轮必验项

## 4. Claude 可连续执行的步骤

以下步骤在拿到第 3 节所需信息后，大部分可由 Claude 连续推进。

### Step 1：生成本地配置文件

**Owner：Claude**

从 `configs/dtworkflow.example.yaml` 复制并生成本地 `dtworkflow.yaml`，优先采用 YAML 作为主配置入口。

建议最小配置：

```yaml
server:
  host: "0.0.0.0"
  port: 8080

gitea:
  url: "<GITEA_URL>"
  token: "<GITEA_TOKEN>"
  insecure_skip_verify: false

webhook:
  secret: "<WEBHOOK_SECRET>"

claude:
  api_key: "<CLAUDE_API_KEY>"

redis:
  addr: "localhost:6379"
  password: ""
  db: 0

database:
  path: "./data/dtworkflow.db"

log:
  level: "info"
  format: "text"

worker:
  concurrency: 1
  timeout: "30m"
  image: "dtworkflow-worker:1.0"
  cpu_limit: "2.0"
  memory_limit: "4g"
  network_name: "dtworkflow-net"

notify:
  default_channel: "gitea"
  channels:
    gitea:
      enabled: true
      options: {}
  routes:
    - repo: "*"
      events: ["*"]
      channels: ["gitea"]
```

注意：

1. 第一轮建议 `worker.concurrency=1`，降低并发变量
2. `notify.channels.gitea.enabled` 必须为 `true`
3. `redis.addr` 使用 `localhost:6379`，因为本轮是本机运行 `dtworkflow`，而不是容器内运行主进程

### Step 2：构建 worker image

**Owner：Claude**

执行：

```bash
docker compose --profile build build worker-image
```

验收：

- 本地存在 `dtworkflow-worker:1.0`
- 后续 `/readyz` 返回 `worker_image_present=true`

### Step 3：启动 Redis

**Owner：Claude**

执行：

```bash
docker compose up -d redis
```

验收：

- Redis 容器处于 healthy 状态
- 主进程可连接 `localhost:6379`

### Step 4：构建主程序并准备数据目录

**Owner：Claude**

执行：

```bash
mkdir -p data bin
go build -o ./bin/dtworkflow ./cmd/dtworkflow
```

验收：

- `./bin/dtworkflow` 存在
- `./data/` 已创建

### Step 5：本机启动服务

**Owner：Claude**

执行：

```bash
./bin/dtworkflow serve --config ./dtworkflow.yaml
```

验收：

- 进程成功启动
- 无以下启动级硬失败：
  - `webhook-secret 不能为空`
  - `claude-api-key 不能为空`
  - `gitea-url 与 gitea-token 不能为空`
  - `Redis 连接失败`
  - `镜像 dtworkflow-worker:1.0 不存在`

### Step 6：检查 `/healthz` 与 `/readyz`

**Owner：Claude**

执行：

```bash
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/readyz
```

验收：

- `/healthz` 返回 200
- `/readyz` 返回 200
- `/readyz` 返回体中以下字段均为 `true`：
  - `redis`
  - `sqlite`
  - `gitea_configured`
  - `notifier_enabled`
  - `worker_image_present`

### Step 7：手工发送一条 PR Webhook

**Owner：Claude**

前提：用户已提供真实仓库信息，至少包括：

- `REPO_FULL_NAME`
- `REPO_CLONE_URL`
- `REPO_OWNER`
- `REPO_NAME`
- `PR_NUMBER`

执行策略：

1. Claude 生成最小 PR payload 文件
2. Claude 用 `WEBHOOK_SECRET` 计算 `X-Gitea-Signature`
3. Claude 向 `/webhooks/gitea` 发送请求

最小 payload 结构：

```json
{
  "action": "opened",
  "repository": {
    "full_name": "<REPO_FULL_NAME>",
    "clone_url": "<REPO_CLONE_URL>",
    "default_branch": "main",
    "owner": { "login": "<REPO_OWNER>" },
    "name": "<REPO_NAME>"
  },
  "pull_request": {
    "number": <PR_NUMBER>,
    "title": "Manual Phase 1 acceptance",
    "body": "manual webhook test",
    "html_url": "<REAL_PR_URL_OR_PLACEHOLDER>",
    "head": { "ref": "feature/test", "sha": "headsha" },
    "base": { "ref": "main", "sha": "basesha" }
  },
  "sender": { "login": "tester", "full_name": "Tester" }
}
```

验收：

- Webhook 返回 `200 OK`
- `serve` 日志出现“PR 评审任务已入队”或等价日志

### Step 8：验证任务链路

**Owner：Claude**

执行：

```bash
./bin/dtworkflow --config ./dtworkflow.yaml task list
./bin/dtworkflow --config ./dtworkflow.yaml task status <TASK_ID>
```

验收：

- 存在 `review_pr` 任务记录
- 状态至少发生以下流转之一：
  - `pending -> queued -> running -> succeeded`
  - `pending -> queued -> running -> failed`
- 日志能看到 Worker 容器创建、执行、清理

### Step 9：验证真实通知回写闭环（可选但推荐）

**Owner：Claude，需用户先提供真实仓库/PR/Issue 与有效 Token**

触发条件：

- 用户确认要把通知闭环纳入本轮验收
- 目标 PR 或 Issue 真实存在
- Token 有评论权限

验收：

- 任务完成后，目标 PR/Issue 中出现 Gitea 评论
- 成功时为完成通知，失败时为失败通知

## 5. 需要等待用户配合的暂停点

Claude 可连续推进，但以下节点必须暂停等待用户明确提供信息或做决策：

### Pause A：提供真实外部配置

若用户尚未提供以下内容，Claude 不应伪造：

- `CLAUDE_API_KEY`
- `GITEA_URL`
- `GITEA_TOKEN`
- `WEBHOOK_SECRET`

### Pause B：提供真实目标仓库信息

若用户尚未提供：

- `REPO_OWNER`
- `REPO_NAME`
- `REPO_FULL_NAME`
- `REPO_CLONE_URL`
- `PR_NUMBER` 或 `ISSUE_NUMBER`

则 Claude 只能先把服务起起来并验证 `/healthz`、`/readyz`，不能完成真实链路验收。

### Pause C：是否要求真实通知闭环

这决定本轮“完成定义”：

- 若需要真实通知闭环，则必须使用真实 PR/Issue 和真实评论权限
- 若暂不要求，则 Phase 1 本轮可先以“主链路跑通”为收口标准

## 6. 第二阶段收口项（主链路稳定后再做）

以下内容建议在本计划第 4 节完成后继续推进。

### 6.1 收口 `docker-compose.yml`

**Owner：Claude**

目标：让 compose 真正适配 M1.8 统一配置体系，而不是只传少量 env。

建议改动方向：

1. 挂载 `dtworkflow.yaml`
2. `command` 改为：

```yaml
command: ["serve", "--config", "/app/dtworkflow.yaml"]
```

3. 优先使用统一环境变量名：

```yaml
DTWORKFLOW_DATABASE_PATH=/app/data/dtworkflow.db
```

而不是仅依赖历史兼容变量名 `DTWORKFLOW_DB_PATH`

### 6.2 冷启动基线

**Owner：Claude**

记录至少三项：

1. 服务启动到 `/healthz` 可用耗时
2. 首个 Webhook 到任务进入 `queued/running` 的耗时
3. 首个 Worker 容器执行完成耗时

### 6.3 并发基线

**Owner：Claude**

建议最小测试档位：

- `worker.concurrency=1`
- `worker.concurrency=2`
- `worker.concurrency=4`

记录：

- 是否稳定
- Redis / SQLite / Docker 是否出现异常
- Worker 容器是否积压

### 6.4 clone/worktree 决策收口

**Owner：用户决策，Claude 执行后续实现或文档迁移**

当前事实：

- 路线图仍把“容器内 Git 仓库 clone + worktree 验证”标记为未完成
- 但当前 Worker 实现尚未真正落地 clone/worktree 执行链路

本轮必须形成结论：

- **方案 A：留在 Phase 1 并补实现**
- **方案 B：迁到 Phase 2 前置，不再阻塞当前 Phase 1 收口**

## 7. 本轮完成定义

满足以下条件，可视为本轮 Phase 1 收口第一阶段完成：

1. 用户已提供最小必需配置
2. Claude 生成并校验 `dtworkflow.yaml`
3. Claude 成功构建 `dtworkflow-worker:1.0`
4. Claude 成功启动 Redis 与本机 `dtworkflow serve`
5. `/healthz` 返回 200
6. `/readyz` 返回 200，且关键字段均为 true
7. Claude 成功发送至少一条手工 PR Webhook
8. `task list/status` 能看到任务记录与最终状态

若用户同时要求真实通知闭环，则还需附加：

9. 真实 PR/Issue 上出现评论通知

## 8. 建议的实际推进方式

建议按照以下工作模式执行：

### 模式 A：Claude 连续推进（默认）

适用条件：

- 用户一次性提供第 3 节所需信息
- 允许 Claude 连续执行构建、启动、验证、联调步骤

执行方式：

1. Claude 先生成配置文件
2. Claude 顺序执行构建与启动
3. Claude 在遇到 Pause A/B/C 时再向用户提问
4. 其余步骤连续推进，不在中间频繁停顿

### 模式 B：分段验收

适用条件：

- 用户暂时无法提供真实 Gitea/PR 信息
- 先只做服务启动与 readiness 验证

执行顺序：

1. 起服务
2. 验 `/healthz`、`/readyz`
3. 等用户补齐仓库与 PR 信息后再继续链路验收

## 9. 下一步建议

本计划文档落地后，建议立刻进入以下实际执行序列：

1. 用户提供第 3 节所需最小信息
2. Claude 生成 `dtworkflow.yaml`
3. Claude 构建 worker image 与 Redis 环境
4. Claude 启动本机服务并检查 `/healthz`、`/readyz`
5. Claude 发送手工 PR Webhook 并验证 `task list/status`
6. 视用户决策，继续验证真实通知闭环或转入 compose 收口
