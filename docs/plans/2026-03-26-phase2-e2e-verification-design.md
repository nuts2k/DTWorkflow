# Phase 2 端到端验证设计

> 目标：以最小验证方式打通 PR 自动评审全链路，确认 Phase 2 核心功能正常工作。

## 前提条件

- Gitea 已部署可访问，已有测试仓库
- Docker 环境可用
- Webhook 未配置，Redis/Worker 镜像/配置文件均未准备

## 验证方案：逐步手动验证

分层递进，每步有明确检查点，失败时易于定位问题。

---

## Step 1：创建配置文件

基于 `configs/dtworkflow.example.yaml` 生成 `dtworkflow.yaml`，执行时向用户询问以下字段值：

| 字段 | 说明 |
|------|------|
| `gitea.url` | Gitea 实例地址 |
| `gitea.token` | Gitea API Token（需 repo 读写权限） |
| `webhook.secret` | 自定义密钥（Gitea Webhook 配置时需一致） |
| `claude.api_key` | Claude API Key |
| `claude.base_url` | Claude API 代理/自定义端点地址 |

其余配置使用默认值，指向 docker-compose 内部网络。

## Step 2：构建 Docker 镜像

```bash
docker compose build dtworkflow                      # 主服务镜像
docker compose --profile build build worker-image    # Worker 镜像
```

## Step 3：启动基础服务

```bash
docker compose up -d redis        # 先启 Redis
docker compose up -d dtworkflow   # 再启主服务
```

## Step 4：健康检查

- `curl http://localhost:8080/healthz` 确认服务存活
- `docker compose logs dtworkflow` 检查无报错
- Redis 连通确认

## Step 5：Gitea Webhook 配置（用户手动操作）

在 Gitea 测试仓库 Settings > Webhooks > Add Webhook：

| 配置项 | 值 |
|--------|-----|
| Target URL | `http://<dtworkflow服务器IP>:8080/api/v1/webhooks/gitea` |
| HTTP Method | POST |
| Content Type | application/json |
| Secret | 与 `webhook.secret` 一致 |
| Trigger On | Pull Request Events |
| Active | 勾选 |

注意：Gitea 必须能访问到 dtworkflow 的 8080 端口。

## Step 6：验证 Webhook 连通

检查 `docker compose logs dtworkflow` 确认收到 Gitea 的 ping 事件。

## Step 7：创建测试 PR

在 Gitea 测试仓库创建包含已知问题的 PR（SQL 注入、空指针、命名不规范等），用于触发不同严重程度的评审结果。

## Step 8：观察全链路执行

逐步检查链路节点：

1. Webhook 接收：日志确认收到 PR 事件
2. 任务入队：`dtworkflow task list` 确认任务已创建
3. Worker 容器启动：`docker ps` 确认 worker 容器运行中
4. 任务执行完成：`dtworkflow task status <task-id>` 确认状态
5. Gitea 回写：PR 页面确认逐行 comment + summary + Request Changes

## Step 9：验证核心验收点

| 验收项 | 检查方式 |
|--------|---------|
| 逐行 comment 存在 | Gitea PR 页面 |
| Summary 评论存在 | Gitea PR 页面 |
| 问题正确分级 | Summary 统计表格 |
| 严重问题自动打回 | PR 状态为 Changes Requested |

## Step 10（可选）：验证重新评审

向同一 PR 推送新 commit，观察：
- 旧任务被取消（Cancel-and-Replace）
- 新评审结果回写
- Summary 标注替代信息

## 职责分工

| 步骤 | 执行者 |
|------|--------|
| Step 1-4 | Claude 自动执行（询问配置值后） |
| Step 5-6 | 用户手动配置 Gitea Webhook |
| Step 7 | 用户创建 PR（Claude 可提供测试代码） |
| Step 8-10 | Claude 协助检查各节点状态 |
