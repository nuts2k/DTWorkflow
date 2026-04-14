# 飞书卡片通知增加通知时间与耗时

## 背景

当前飞书卡片通知不包含时间信息，用户无法从通知中直观判断事件发生时间和任务耗时。本次变更为所有飞书卡片通知增加"通知时间"字段，并为评审成功完成通知增加"耗时"字段。

## 需求

1. **通知时间**：所有飞书卡片通知（开始、成功、失败、重试）均显示通知发送时间
2. **耗时**：仅评审成功（succeeded）的完成通知显示任务耗时
3. **时区**：统一使用 Asia/Shanghai (UTC+8)
4. **时间格式**：`2006-01-02 15:04:05`（绝对时间，精确到秒）
5. **耗时格式**：Go 标准库 `time.Duration.Truncate(time.Second).String()`（如 `32s`、`2m30s`、`1h5m30s`）

## 设计方案

### 方案选择：通过 Metadata 传递

沿用现有 Metadata 模式（与 verdict、issue_summary 等一致），新增两个 MetaKey，由 processor 生产、feishu_card 消费。不改动 Message 结构体，不影响 Gitea 等其他通知渠道。

### 数据层：notifier.go

新增常量：

```go
MetaKeyNotifyTime = "notify_time"  // 通知发送时间
MetaKeyDuration   = "duration"     // 任务耗时（仅 succeeded）
```

### 生产端：processor.go

新增两个不导出的辅助函数：

```go
var shanghaiZone = time.FixedZone("CST", 8*3600)

func formatNotifyTime() string {
    return time.Now().In(shanghaiZone).Format("2006-01-02 15:04:05")
}

func formatDuration(d time.Duration) string {
    return d.Truncate(time.Second).String()
}
```

时区使用 `time.FixedZone` 而非 `time.LoadLocation`，避免依赖宿主机 tzdata（Docker 精简镜像可能不含时区数据库）。

**buildStartMessage**：当前两个 TaskType 分支各自直接 `return`（ReviewPR 使用 `buildPRMetadata()`、FixIssue 手动构建 `map[string]string{}`），switch 之后没有公共代码路径。需要**小幅重构**：将各分支改为赋值给 `var msg notify.Message` 而非直接 return，switch 结束后在公共路径统一注入 `MetaKeyNotifyTime` 再返回：

```go
func (p *Processor) buildStartMessage(payload model.TaskPayload) (notify.Message, bool) {
    // ... 校验 ...
    var msg notify.Message
    switch payload.TaskType {
    case model.TaskTypeReviewPR:
        msg = notify.Message{...} // 保持现有构建逻辑
    case model.TaskTypeFixIssue:
        msg = notify.Message{...}
    default:
        return notify.Message{}, false
    }
    // 公共路径：统一注入通知时间
    msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
    return msg, true
}
```

**buildNotificationMessage**：同样存在上述问题——嵌套 switch（TaskType → Status）共有 6 个直接 return 点，无公共路径。采用相同重构策略：各分支赋值给 `var msg notify.Message`，switch 结束后统一注入时间字段：

```go
// 公共路径：统一注入通知时间和耗时
msg.Metadata[notify.MetaKeyNotifyTime] = formatNotifyTime()
if record.Status == model.TaskStatusSucceeded &&
    record.StartedAt != nil && record.CompletedAt != nil {
    msg.Metadata[notify.MetaKeyDuration] = formatDuration(
        record.CompletedAt.Sub(*record.StartedAt))
}
return msg, true
```

**时序前提**：`sendCompletionNotification` 在 `ProcessTask` 中的调用位置确保 `StartedAt`（状态变为 running 时设置）和 `CompletedAt`（达到 succeeded/failed 最终状态时设置）均已赋值。`handleSkipRetryFailure` 路径同理——调用时 `StartedAt` 已在 running 阶段设置，`CompletedAt` 在该函数内设置。`retrying` 状态不设置 `CompletedAt`（见 `ProcessTask` 中 `if record.Status == TaskStatusSucceeded || record.Status == TaskStatusFailed` 条件），因此 duration 自然被排除。

### 消费端：feishu_card.go

在 `FormatFeishuCard` 的 Markdown 区域中，位于现有结构化字段（重试信息）之后、Body 之前，渲染通知时间和耗时：

```go
if notifyTime := msg.Metadata[MetaKeyNotifyTime]; notifyTime != "" {
    mdParts = append(mdParts, fmt.Sprintf("**通知时间**: %s", notifyTime))
}
if duration := msg.Metadata[MetaKeyDuration]; duration != "" {
    mdParts = append(mdParts, fmt.Sprintf("**耗时**: %s", duration))
}
```

### 卡片渲染效果

**开始通知**：
```
**仓库**: org/repo
**PR**: #42 - 修复登录逻辑
**通知时间**: 2026-04-13 14:30:05
正在评审 PR #42 ...
```

**成功完成通知**：
```
**仓库**: org/repo
**PR**: #42 - 修复登录逻辑
**结论**: APPROVE
**发现问题**: 2 WARNING, 1 INFO
**通知时间**: 2026-04-13 14:31:37
**耗时**: 32s
任务 xxx 执行完成 ...
```

**失败/重试通知**（无耗时）：
```
**仓库**: org/repo
**PR**: #42 - 修复登录逻辑
**重试**: 第 2 次 / 共 3 次
**通知时间**: 2026-04-13 14:32:10
任务执行失败，即将重试 ...
```

## 变更范围

| 文件 | 变更内容 |
|------|----------|
| `internal/notify/notifier.go` | +2 MetaKey 常量 |
| `internal/queue/processor.go` | +2 辅助函数（`formatNotifyTime`、`formatDuration`），重构 `buildStartMessage` 和 `buildNotificationMessage`（各分支改赋值+公共路径注入时间字段） |
| `internal/notify/feishu_card.go` | +6 行渲染逻辑 |
| `internal/notify/feishu_card_test.go` | 更新现有用例 + 新增用例 |
| `internal/queue/processor_test.go` | 新增用例 |

## 测试策略

### feishu_card_test.go

- 更新现有测试：Metadata 中加入 `MetaKeyNotifyTime`，验证渲染输出包含通知时间
- 新增：succeeded 完成通知包含耗时字段
- 新增：failed 完成通知不包含耗时字段
- 新增：开始通知包含通知时间但不包含耗时

### processor_test.go

PR 评审场景：
- `buildStartMessage` 返回的 Metadata 包含 `notify_time`
- `buildNotificationMessage` succeeded 时包含 `notify_time` + `duration`
- `buildNotificationMessage` failed 时包含 `notify_time` 但不含 `duration`

FixIssue 场景：
- `buildStartMessage` 返回 FixIssue 开始通知包含 `notify_time`
- `buildNotificationMessage` 返回 FixIssue 成功通知包含 `notify_time` + `duration`
- `buildNotificationMessage` 返回 FixIssue 失败通知包含 `notify_time` 但不含 `duration`
