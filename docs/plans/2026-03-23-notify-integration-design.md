# M1.7 通知主流程接线设计

> 日期：2026-03-23
> 范围：在不引入 M1.8 配置管理的前提下，将现有通知框架接入 `serve -> queue.Processor -> notify.Router` 主链路，形成最小可用闭环。

## 1. 背景与目标

当前代码库已经具备以下通知基础设施：

- `notify.Router`：负责按规则将消息路由到通知渠道
- `notify.GiteaNotifier`：通过 Gitea Issue/PR 评论发送通知
- `giteaCommentAdapter`：将 `gitea.Client` 适配为 `notify.GiteaCommentCreator`

但这些能力尚未接入任务执行主流程，导致：

- 任务执行成功/失败后不会真正发出通知
- M1.7 仅完成了“框架骨架”，未达到“功能可用”
- 任务可观测性不足，用户无法通过 Gitea 获知任务结果

本轮目标是以最小改动打通通知主流程，显著提升 M1.7 的验收完整性。

## 2. 范围与非目标

### 本轮范围

1. 在 `BuildServiceDeps` 中构造可选通知路由器
2. 将通知依赖注入 `queue.Processor`
3. 在任务最终状态落库后发送 Gitea 评论通知
4. 为成功/失败路径补充单元测试

### 非目标

1. 不引入 M1.8 配置管理与 YAML 路由配置
2. 不实现企业微信/钉钉/飞书等新通知渠道
3. 不扩展 Worker 的 clone/worktree/真实仓库执行链路
4. 不为 `gen_tests` 任务强行指定通知目标
5. 不修改 CLI/API 对外协议

## 3. 方案选择

### 方案 A：最小闭环接线（采用）

- 在 `serve` 构建依赖时，如果 Gitea 配置可用，则构造：
  - `giteaCommentAdapter`
  - `notify.GiteaNotifier`
  - `notify.Router`
- 使用最小默认路由：`* -> gitea`，fallback 也为 `gitea`
- 在 `queue.Processor` 中注入 router，在最终状态时发送通知
- `gen_tests` 暂不发送通知

**优点**：改动最小、复用现有抽象、风险低、收益直接。

### 方案 B：接线 + 配置化路由

会扩大到 M1.8 范围，当前不采用。

### 方案 C：绕过 Router，直接在 Processor 中调用 Gitea API

会破坏现有通知抽象，形成技术债，当前不采用。

## 4. 架构设计

### 4.1 依赖构建层（`internal/cmd/serve.go`）

在 `ServiceDeps` 中新增可选通知依赖，例如：

- `Notifier *notify.Router`

在 `BuildServiceDeps` 中：

1. 若 `GiteaClient` 不可用，则 `Notifier == nil`
2. 若 `GiteaClient` 可用：
   - 使用 `giteaCommentAdapter` 适配 Gitea client
   - 构造 `notify.GiteaNotifier`
   - 构造 `notify.Router`
   - 默认规则：
     - `RepoPattern: "*"`
     - `Channels: []string{"gitea"}`
   - `Fallback: "gitea"`

这样可以在不依赖配置系统的情况下，形成稳定的最小通知闭环。

### 4.2 执行层（`internal/queue/processor.go`）

`Processor` 新增可选通知依赖。推荐做法：

- 在结构体中保存一个 `Notifier` 接口或 `*notify.Router`
- `NewProcessor(...)` 增加相应参数
- `nil` 表示当前运行模式未启用通知

执行顺序：

1. 查找任务记录
2. 更新状态为 `running`
3. 调用 `pool.Run(...)`
4. 根据执行结果设置最终状态
5. 落库最终状态
6. 尝试发送通知
7. 保持原有返回语义

**关键原则**：先写库，后通知。SQLite 记录是事实来源，通知只是副作用。

## 5. 数据流与消息映射

### 5.1 通知发送时机

仅在任务达到最终状态时发送：

- `succeeded`
- `failed`

不在以下状态发送：

- `pending`
- `queued`
- `running`
- `retrying`
- `cancelled`

原因：避免在重试过程中重复刷评论，也避免中间状态噪声。

### 5.2 目标对象映射

从 `record.Payload` 推导通知目标：

#### `review_pr`

- `Owner = payload.RepoOwner`
- `Repo = payload.RepoName`
- `Number = payload.PRNumber`
- `IsPR = true`

#### `fix_issue`

- `Owner = payload.RepoOwner`
- `Repo = payload.RepoName`
- `Number = payload.IssueNumber`
- `IsPR = false`

#### `gen_tests`

本轮不发送通知。

原因：当前模型中没有稳定的 PR/Issue 目标编号，强行发送会引入不准确语义。

### 5.3 事件类型与文案映射

为保持最小改动，采用“已有事件类型 + 明确文案”的策略。

#### `review_pr` 成功

- `EventType = pr.review.done`
- `Severity = info`
- 标题：`PR 自动评审任务完成`

#### `review_pr` 失败

- `EventType = system.error`
- `Severity = warning` 或 `critical`（本轮统一采用 `warning`，除非后续引入更精确分级）
- 标题：`PR 自动评审任务失败`

#### `fix_issue` 成功

当前任务成功不等于“修复 PR 已创建”，因此不能冒用 `fix.pr.created`。

采用：

- `EventType = issue.analysis.done`
- `Severity = info`
- 标题：`Issue 自动修复任务完成`

正文中明确写“任务执行完成”，避免误导为“已创建修复 PR”。

#### `fix_issue` 失败

- `EventType = system.error`
- `Severity = warning`
- 标题：`Issue 自动修复任务失败`

### 5.4 正文内容

正文包含最小必要信息：

- 任务 ID
- 仓库
- 任务类型
- 最终状态
- 失败时：错误摘要
- 成功时：输出摘要（需要做长度截断，避免评论过长）

建议对 `record.Result` / `record.Error` 做统一摘要处理，例如最大 500~1000 rune。

## 6. 错误处理策略

### 6.1 通知系统未启用

- `Notifier == nil` 时直接跳过
- 仅记录 debug/info 日志（如需要）
- 不影响任务执行结果

### 6.2 目标无法构造

例如：

- `review_pr` 但 `PRNumber <= 0`
- `fix_issue` 但 `IssueNumber <= 0`
- `RepoOwner` / `RepoName` 为空

此时：

- 跳过通知
- 记录 warning 日志
- 不影响主流程

### 6.3 路由或发送失败

若 `Router.Send(...)` 返回错误：

- 仅记录错误日志
- 不修改任务状态
- 不改变 `ProcessTask` 返回值

这样可以避免“通知发送失败”污染“任务执行结果”。

## 7. 测试策略

### 7.1 Processor 测试

新增或扩展以下测试：

1. `review_pr` 成功后发送通知
2. `review_pr` 失败后发送通知
3. `fix_issue` 成功后发送通知
4. `fix_issue` 失败后发送通知
5. `retrying` 状态不发送通知
6. 通知发送失败时：
   - 任务最终状态仍正确落库
   - `ProcessTask` 返回语义保持不变
7. `gen_tests` 任务不发送通知

建议为测试引入一个轻量 stub notifier / stub router，记录收到的消息数量与内容。

### 7.2 Serve 依赖构建测试

扩展 `BuildServiceDeps` 相关测试：

1. Gitea 配置存在时，`Notifier` 被正确构造
2. Gitea 配置缺失时，`Notifier == nil`
3. 构造通知器失败时，`BuildServiceDeps` 返回错误并正确 cleanup

## 8. 实施边界与兼容性

- 本轮不会改变现有 webhook、queue、worker 的协议结构
- 不会改变 `notify.Router` 的对外接口
- `Processor` 构造函数签名会变化，需要同步更新调用方和测试
- `ServiceDeps` 新增字段属于内部实现调整，不影响 CLI 用户

## 9. 验收标准

完成后应满足：

1. 当 Gitea 配置可用时，任务成功/失败后会自动向目标 PR 或 Issue 发送评论
2. 当 Gitea 配置缺失时，服务仍可启动，通知功能静默禁用
3. 通知失败不会导致任务状态错误或执行结果误判
4. `retrying` 状态不会重复发送通知
5. 相关单元测试通过

## 10. 后续演进

本轮完成后，后续可继续演进：

1. 在 M1.8 中将默认硬编码路由替换为配置驱动路由
2. 为 `gen_tests` 建立明确的通知目标模型
3. 增加任务结果摘要格式化器，支持更好的 Markdown 输出
4. 为通知失败增加可观测性指标或死信补偿策略
