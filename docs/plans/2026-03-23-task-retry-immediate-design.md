# task retry 立即重入队设计

> 日期：2026-03-23
> 范围：将 `dtworkflow task retry <task-id>` 从“重置为 pending 并等待 RecoveryLoop”改为“立即重新入队”，使 CLI 语义与实际行为一致。

## 1. 背景与目标

当前 `task retry` 的实现只会：

1. 读取 SQLite 中的任务记录
2. 将 `failed` / `cancelled` 任务重置为 `pending`
3. 等待 `serve` 进程中的 `RecoveryLoop` 在下一个扫描周期重新入队

这带来的问题是：

- 命令名是 `retry`，但行为更像 `reset-to-pending`
- 如果 `serve` 没有运行，命令会看起来成功，实际上不会立刻重试
- CLI 输出与用户真实预期不一致

本轮目标是让 `task retry` 具备“立即重新入队”的真实语义。

## 2. 范围与非目标

### 本轮范围

1. 为 `task` 命令增加 queue client 依赖
2. 为 `task retry` 增加立即入队能力
3. 保持 `failed` / `cancelled` 任务都支持 retry
4. 更新状态为 `queued`，而不是 `pending`
5. 补充测试覆盖主要分支

### 非目标

1. 不修改 RecoveryLoop 的职责
2. 不调整 Worker 执行链路
3. 不引入新的任务状态枚举
4. 不扩展额外 CLI 子命令或复杂 flag 体系

## 3. 方案选择

### 方案 A：立即重新入队（采用）

- 在 `task` 命令中直接初始化 `queue.Client`
- `task retry` 读取任务记录后直接调用 `queue.Client.Enqueue(...)`
- 成功后将任务状态更新为 `queued`

**优点**：
- 命令语义与行为一致
- 用户体验正确
- 不依赖 `serve` / `RecoveryLoop`
- 改动集中在 CLI 与队列接线

### 方案 B：维持现状，但改名为 reset

只修文案，不修行为，不采用。

### 方案 C：双模式（立即入队 + defer 模式）

当前没有必要，引入额外复杂度，不采用。

## 4. 架构设计

### 4.1 task 命令依赖

当前 `internal/cmd/task.go` 只维护包级 `taskStore`。

本轮新增：

- `taskQueueClient *queue.Client`
- `taskRedisAddr string`

在 `taskCmd.PersistentPreRunE` 中：

1. 初始化 `taskStore`
2. 初始化 `taskQueueClient`

在 `PersistentPostRunE` 中：

1. 关闭 `taskQueueClient`
2. 关闭 `taskStore`

Redis 地址来源与 `serve` 保持一致：

- `--redis-addr`
- 或环境变量 `DTWORKFLOW_REDIS_ADDR`

### 4.2 retry 主流程

`task retry` 的成功路径调整为：

1. 读取任务记录
2. 校验状态为 `failed` 或 `cancelled`
3. 校验 payload 可用于重新入队
4. 调用 `queue.Client.Enqueue(...)`
5. 重置运行期字段
6. 更新 SQLite 状态为 `queued`
7. 输出“已重新入队”结果

## 5. 数据流与状态设计

### 5.1 入队请求来源

使用任务记录中的原始 `record.Payload` 重新入队，不重新拼装 payload。

这样可保证：

- 重试与原任务参数一致
- 不需要在 CLI 层重新理解每种任务类型

### 5.2 状态更新

重试成功后更新为：

- `Status = queued`
- `AsynqID = newAsynqID`
- `RetryCount = 0`
- `Error = ""`
- `StartedAt = nil`
- `CompletedAt = nil`
- `WorkerID = ""`
- `UpdatedAt = now`

### 5.3 关于 `cancelled`

保留支持 `cancelled`：

- 与 `failed` 同样允许重新发起执行
- 用户手动取消后再次尝试是合理需求
- 不需要额外分叉命令语义

### 5.4 关于 TaskID 冲突

重试时继续使用确定性 TaskID 规则，以便避免重复执行。

若遇到 asynq `ErrTaskIDConflict`：

- 视为“任务已在队列中”
- 将 SQLite 状态同步为 `queued`
- 将 `AsynqID` 写为确定性的 TaskID
- 输出中明确告知“任务已存在于队列中，状态已同步”

这比直接报错更符合用户直觉。

## 6. 错误处理策略

### 6.1 任务不存在

返回错误，退出码 1。

### 6.2 状态不允许 retry

仅允许：

- `failed`
- `cancelled`

其他状态直接报错。

### 6.3 Payload 无效

如果 payload 中缺少必要字段，或 `TaskType` 非法，则拒绝重试。

### 6.4 Redis / 入队失败

立即返回错误，不更新 SQLite。

### 6.5 SQLite 更新失败

如果入队成功但更新 SQLite 失败：

- 返回错误
- 错误信息应说明“任务可能已重新入队，但状态同步失败”

这是当前阶段最诚实且可控的处理方式。

## 7. 测试策略

### 7.1 task retry 测试

新增或扩展测试覆盖：

1. `failed` 任务 retry 成功 -> 状态变为 `queued`
2. `cancelled` 任务 retry 成功 -> 状态变为 `queued`
3. 非允许状态 retry -> 返回错误
4. 入队失败 -> 返回错误，状态不变
5. TaskID 冲突 -> 视为已入队，状态更新为 `queued`
6. SQLite 更新失败 -> 返回错误，并说明状态同步失败

### 7.2 依赖初始化测试

补充 `task` 命令层的测试，确保：

1. 能初始化 queue client
2. 资源在结束时正确关闭
3. Redis 不可用时命令失败

## 8. 实施边界与兼容性

- 命令名保持 `task retry` 不变
- 对用户而言，语义从“延迟补偿重试”提升为“立即重试”
- RecoveryLoop 仍保留，用于补偿其他 pending 孤儿任务
- 不改变现有任务记录 schema

## 9. 验收标准

完成后应满足：

1. `dtworkflow task retry <task-id>` 能立即重新入队
2. 成功后状态为 `queued`，而非 `pending`
3. `failed` 与 `cancelled` 都支持 retry
4. 入队失败时命令返回失败，不产生伪成功
5. TaskID 冲突时能正确处理为“已在队列中”
6. 相关测试通过

## 10. 后续演进

后续可继续优化：

1. 将 `task` 命令重构为与 `serve` 一致的依赖注入结构
2. 为 retry 增加结构化结果中的更多字段（如 `asynq_id`）
3. 引入更强的一致性保障（如 outbox 或补偿日志）
