# Phase 2 端到端验证报告

> 执行日期：2026-03-27
> 验证方案：`docs/plans/2026-03-26-phase2-e2e-verification-design.md`

## 验证结论：全部通过

Phase 2 PR 自动评审全链路（Webhook → 任务队列 → Worker 容器 → Claude Code 评审 → Gitea 回写）端到端验证通过。

## 逐步验证结果

| 步骤 | 内容 | 状态 | 说明 |
|------|------|------|------|
| Step 1 | 配置文件创建 | 通过 | 复用 Phase 1 已有的 `dtworkflow.yaml` |
| Step 2 | Docker 镜像构建 | 通过 | 修复 GOPROXY 网络问题后构建成功 |
| Step 3 | 服务启动 | 通过 | 修复 SQLite 目录权限和 Docker socket 权限后启动正常 |
| Step 4 | 健康检查 | 通过 | healthz 200, readyz 全部 true |
| Step 5 | Webhook 配置 | 通过 | Gitea Webhook 配置为 PR 事件触发 |
| Step 6 | Webhook 连通 | 通过 | Gitea POST 请求成功到达 dtworkflow (HTTP 200) |
| Step 7 | 创建测试 PR | 通过 | PR #9 (changyu/ai_workflow_test) |
| Step 8 | 全链路执行 | 通过 | 任务入队 → 容器执行(27.8s) → 容器清理 → 通知回写 |
| Step 9 | 核心验收点 | 通过 | 见下表 |

### Step 9 核心验收点

| 验收项 | 结果 |
|--------|------|
| 逐行 comment 存在 | 通过 - PR 页面可见行级评审评论 |
| Summary 评论存在 | 通过 - 包含问题统计表格 |
| 问题正确分级 | 通过 - Summary 中体现 severity 分级 |
| 严重问题自动打回 | 通过 - PR 标记为 "请求变更"（红色 Request Changes） |

### Step 10（可选：重新评审）

未执行。Cancel-and-Replace 逻辑已有单元测试覆盖，端到端验证在后续迭代中补充。

## 验证过程中发现并修复的问题

### Bug 1：Docker 构建 go mod 下载失败

- **现象**：`go mod download` 报 `unexpected EOF`
- **根因**：Docker 构建容器内无法稳定访问 `proxy.golang.org`
- **修复**：`build/Dockerfile` 新增 `ARG GOPROXY` 支持构建时指定 Go 代理

### Bug 2：SQLite 数据库目录不存在

- **现象**：`unable to open database file: out of memory (14)` (SQLITE_CANTOPEN)
- **根因**：Dockerfile 未创建 `/app/data` 目录，Docker volume 挂载后目录属 root，UID 1001 无法写入
- **修复**：Dockerfile 添加 `mkdir -p /app/data` + `chown -R dtworkflow:dtworkflow /app`

### Bug 3：Docker socket 权限拒绝

- **现象**：`permission denied while trying to connect to the Docker daemon socket`
- **根因**：macOS Docker Desktop 下容器内 `/var/run/docker.sock` 属 root:root(GID 0)，而 Dockerfile 创建的 docker 组 GID=999 不匹配
- **修复**：`docker-compose.yml` 添加 `group_add: ["0"]`

### Bug 4：entrypoint 日志污染 Claude CLI JSON 输出

- **现象**：`CLI JSON 解析失败: invalid character 'e' looking for beginning of value`
- **根因**：`entrypoint.sh` 的 `log()` 函数和 git 命令输出都写到 stdout，与 Claude CLI JSON 输出混合；`GetContainerLogs` 返回 stdout+stderr 拼接字符串。`[entrypoint]` 开头的 `[` 被 JSON 解析器视为数组开始，随后的 `e` 触发解析错误
- **修复**：
  1. `entrypoint.sh`：`log()` 重定向到 stderr，所有 git 命令输出重定向到 stderr
  2. `docker.go`：`GetContainerLogs` 返回结构体 `ContainerLogs{Stdout, Stderr}`，调用方按需取用

### Bug 5：评审结果持久化 FK 约束失败

- **现象**：`插入评审结果记录失败: constraint failed: FOREIGN KEY constraint failed`
- **根因**：`service.go` 将 `payload.DeliveryID`（Webhook delivery ID）赋给 `WritebackInput.TaskID`，而 `review_results.task_id` 外键引用的是 `tasks(id)`（task UUID），两者不是同一个值
- **修复**：
  1. `model/task.go`：`TaskPayload` 新增 `TaskID string` 字段（`json:"-"`，不序列化）
  2. `processor.go`：从 SQLite record 注入 `payload.TaskID = record.ID`
  3. `service.go`：`WritebackInput.TaskID` 改用 `payload.TaskID`

### Bug 6：Webhook 路径文档错误

- **现象**：设计文档中写的 Target URL 为 `/api/v1/webhooks/gitea`，实际路由是 `/webhooks/gitea`
- **修复**：告知用户正确路径（文档已在此报告中更正）

### 附加改进：Gin HTTP 请求日志

- `serve.go` 添加 `gin.Logger()` 中间件，方便排查 Webhook 投递问题

## 性能基线

| 指标 | 值 |
|------|------|
| 容器执行耗时（PR #9） | 27.8s |
| 任务入队延迟 | ~50ms |
| 端到端延迟（Webhook → Gitea 回写） | ~30s |
| Claude API 费用（单次评审） | ~$0.09 |

## 环境信息

- 平台：macOS (Docker Desktop)
- dtworkflow 版本：dev (commit d4d391e)
- Claude Code CLI：v2.1.76
- Worker 镜像：dtworkflow-worker:1.0
- Gitea：https://otws19.zicp.vip:3000
- 测试仓库：changyu/ai_workflow_test
- 验证 PR：#9

## 涉及的代码提交

- `5b52c88` fix: Phase 2 E2E 验证中发现的多项问题修复
- `d4d391e` Fix worker failure logs and Docker GOPROXY default
