# serve readiness 语义增强设计

> 日期：2026-03-23
> 范围：增强 `dtworkflow serve` 的 `/readyz` 语义与返回体，使其更诚实地表达“实例是否具备执行任务的关键前提”，而不是仅反映 Redis/SQLite 存活状态。

## 1. 背景与目标

当前 `/healthz` 和 `/readyz` 的语义是：

- `/healthz`：进程存活即可返回 200
- `/readyz`：检查 Redis / SQLite，可用时返回 200

问题在于：

- Gitea 配置缺失时，服务只打 warning，但 `/readyz` 仍可能返回 200
- Worker 镜像缺失时，实例实际上无法执行任务，但 readiness 无法表达该问题
- readiness 返回体缺少关键能力维度，导致运维只能看到“degraded / ok”，却不知道缺了什么

本轮目标是增强 readiness，让它更准确地表达“当前实例是否具备执行任务的关键前提”。

## 2. 范围与非目标

### 本轮范围

1. 扩展 `ServiceDeps`，暴露 readiness 需要的静态事实
2. 在 `BuildServiceDeps` 中收集并保存这些事实
3. 增强 `/readyz` 返回体与状态判定逻辑
4. 补充相关测试

### 非目标

1. 不实现 Worker 的 clone/worktree/真实仓库执行链路
2. 不做 Gitea 网络连通性探测
3. 不做 Claude API 外部连通性探测
4. 不改变 `/healthz` 的语义
5. 不引入复杂的多模式 readiness 协议

## 3. 方案选择

### 方案 A：保守增强型 readiness（采用）

- 继续保留 `/readyz`
- 增加 readiness 字段，覆盖关键本地前提
- 关键前提不完整时返回 `503`

关键检查项：

- Redis 可用
- SQLite 可用
- Gitea 配置是否完整
- notifier 是否启用
- Worker 镜像是否存在

**优点**：
- 改动面小
- 风险低
- 运维信息明显更充分
- 不依赖外部网络探测，测试更容易控制

### 方案 B：模式化 readiness

区分 ingest / execute / notify ready，当前不采用。

### 方案 C：强依赖型 readiness 但不暴露明细

状态严格但可观测性差，当前不采用。

## 4. 架构设计

### 4.1 `ServiceDeps` 扩展

在 `internal/cmd/serve.go` 的 `ServiceDeps` 中新增 readiness 相关字段，例如：

- `GiteaConfigured bool`
- `NotifierEnabled bool`
- `WorkerImagePresent bool`

这些字段不是新依赖，而是依赖构建阶段产生的事实快照。

### 4.2 `BuildServiceDeps` 中的事实收集

#### Gitea 配置

当以下两个值同时非空时认为完整：

- `cfg.GiteaURL`
- `cfg.GiteaToken`

得到：

- `GiteaConfigured = true/false`

#### notifier 启用状态

规则：

- 成功构造 `buildNotifier(...)` 且返回非 nil -> `NotifierEnabled = true`
- 否则 -> `false`

#### Worker 镜像存在性

当前 `BuildServiceDeps` 已能初始化 Docker client 和 Worker pool。可在依赖构建阶段调用镜像检查，得到：

- `WorkerImagePresent = true/false`

注意：

- 本轮不把“镜像缺失”直接提升为启动失败
- 而是保留服务启动能力，让 `/readyz` 明确返回 `503`

## 5. `/healthz` 与 `/readyz` 语义

### 5.1 `/healthz`

保持现有语义：

- 进程活着 -> 200
- 不承诺业务执行能力

### 5.2 `/readyz`

增强后的语义：

> 当前实例是否具备执行任务的关键前提

具体判断项：

#### 基础项
- `redis`
- `sqlite`

#### 执行前提项
- `gitea_configured`
- `notifier_enabled`
- `worker_image_present`

总规则：

- 所有关键项都为 true -> `200`, `status=ok`
- 任一关键项为 false -> `503`, `status=degraded`

## 6. 返回体设计

`/readyz` 返回至少包含：

- `status`
- `version`
- `redis`
- `sqlite`
- `gitea_configured`
- `notifier_enabled`
- `worker_image_present`
- `active_workers`

示例：

```json
{
  "status": "degraded",
  "version": "dev",
  "redis": true,
  "sqlite": true,
  "gitea_configured": false,
  "notifier_enabled": false,
  "worker_image_present": true,
  "active_workers": 0
}
```

## 7. 错误处理策略

### 7.1 Gitea 配置缺失

- 服务继续启动
- readiness 返回 `503`
- 响应体明确标记：
  - `gitea_configured=false`
  - `notifier_enabled=false`

### 7.2 Worker 镜像缺失

- 服务继续启动
- readiness 返回 `503`
- 响应体标记：
  - `worker_image_present=false`

### 7.3 Redis / SQLite 不可用

- 保持现有 readiness 失败语义
- 若启动阶段已快速失败，则相关测试优先覆盖 helper 或 readiness 判定函数

### 7.4 外部连通性

本轮不检查：

- Gitea 实例是否可访问
- Claude API 是否可访问

因为那会引入网络探测复杂度，并且不利于稳定测试。

## 8. 测试策略

### 8.1 服务级测试

补充或扩展以下测试：

1. 无 Gitea 配置时 `/readyz` 返回 `503`
2. `gitea_configured=false`
3. `notifier_enabled=false`
4. Worker 镜像缺失时 `/readyz` 返回 `503`
5. `worker_image_present=false`
6. 依赖齐全时 `/readyz` 返回 `200`
7. `/healthz` 保持始终 `200`

### 8.2 helper / readiness 判定测试

如果服务测试受本机 Redis / Docker 环境影响较大，则优先抽 helper，例如：

- `computeReadyStatus(...)`
- `buildReadinessPayload(...)`

并对这些纯函数做稳定单元测试。

## 9. 实施边界与兼容性

- 不改变 CLI 用户接口
- 不改变 `/healthz` 协议
- `/readyz` 的返回体会新增字段，但这是向后兼容增强
- 现有调用 `/readyz` 的监控系统若只关心 HTTP 码，不会被破坏，只会得到更准确的 503

## 10. 验收标准

完成后应满足：

1. `/healthz` 仍然始终返回 200
2. `/readyz` 不再只依赖 Redis / SQLite
3. Gitea 配置缺失时 `/readyz` 返回 503
4. Worker 镜像缺失时 `/readyz` 返回 503
5. `/readyz` 返回体能明确指出缺少的关键项
6. 相关测试通过

## 11. 后续演进

后续可继续增强：

1. 引入更细粒度的 ingest / execute / notify 状态
2. 增加 Gitea 连通性探测
3. 增加 Docker daemon / worker image 深度探测
4. 引入执行模式配置，使 readiness 与模式强绑定
