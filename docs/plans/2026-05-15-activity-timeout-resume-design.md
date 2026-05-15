# 活跃度超时两阶段恢复设计

**日期**: 2026-05-15
**状态**: 设计完成，待实施
**前置讨论**: `docs/discussions/2026-05-13-container-error-recovery.md`

## 问题

Claude CLI 在容器中执行时遇到偶发上游 API 错误会卡住（进程既不输出也不退出），2 分钟无 stdout 输出触发 `streamMonitorLoop` 活跃度超时，容器销毁后全量重试。全量重试需要重新克隆仓库、启动 Claude、从头执行，已完成的分析/编码工作全部丢失。此问题在生产环境高频发生。

核心矛盾：Claude CLI 被一次偶发错误卡住后，没有"踢一脚说继续"的机制，只能整体重来。

## 方案

在容器内原地 resume：活跃度超时时不销毁容器，而是通过 `docker exec` 终止卡住的 CLI 进程并启动新的 `claude --resume` 进程继续工作。

### 两阶段超时状态机

```
正常运行 ──超时──> Phase 1（Soft Timeout）
                    |
                    |-- docker exec: pkill -SIGINT claude
                    |-- 等待进程优雅退出（session 持久化）
                    |-- docker exec: claude --resume <session_id> -p "继续完成任务"
                    |-- 重新监控 resume 进程的 stdout
                    |
                    +──再次超时──> Phase 2（Hard Timeout）
                                   |
                                   +-- cancel(ErrActivityTimeout) -> 现有全量重试路径
```

Phase 1 和 Phase 2 复用同一个 `activity_timeout` 配置值，不引入新配置项。只 resume 一次：如果 resume 后又卡住，说明问题不是偶发的，全量重试是正确做法。

## 详细设计

### 0. Entrypoint 改造（PID 1 问题）

**问题**：当前 `entrypoint.sh` 末尾对非 fix_review 任务使用 `exec "$@"`（第 563 行），使 claude 成为容器 PID 1。SIGINT 杀死 claude 后 PID 1 退出，容器立即进入 exited 状态，后续所有 `docker exec` 调用失败，resume 方案无法执行。

fix_review 不受影响（第 554 行使用 `"$@"` 而非 `exec`），因为它需要在 claude 退出后执行 `push_fix_review_result`。

**修复**：引入哨兵文件协调机制。将 `exec "$@"` 替换为子进程模式，claude 退出后检查哨兵文件决定是否保持容器存活：

```bash
# 替代原来的 exec "$@"（第 563 行）
CMD_STATUS=0
"$@" || CMD_STATUS=$?

# 检查 host 是否请求了 resume（docker exec 在 SIGINT 前创建哨兵文件）
RESUME_MARKER="/tmp/.dtworkflow-resume"
if [ -f "${RESUME_MARKER}" ]; then
    log "Resume 已请求，保持容器运行..."
    RESUME_DONE="/tmp/.dtworkflow-resume-done"
    for i in $(seq 600); do
        [ -f "${RESUME_DONE}" ] && break
        sleep 0.5
    done
fi

exit ${CMD_STATUS}
```

**关键细节**：

- `"$@" || CMD_STATUS=$?`：`||` 防止 `set -e` 在 claude 非零退出时终止 shell
- 哨兵文件 `/tmp/.dtworkflow-resume`：由 host 在 SIGINT 前通过 `docker exec touch` 创建
- 完成哨兵 `/tmp/.dtworkflow-resume-done`：由 host 在 resume 完成后创建，通知 entrypoint 退出
- 最长等待 300 秒（`seq 600 * sleep 0.5`）：兜底超时，防止哨兵文件异常时容器永不退出
- fix_review 路径（第 553-561 行）不受影响，保持现有逻辑

**对正常（非 resume）路径的影响**：无。哨兵文件不存在时直接 `exit ${CMD_STATUS}`，与 `exec "$@"` 的退出行为一致。`WaitContainer` 在 claude 退出后立即返回。

### 1. Session ID 捕获

Claude CLI 在 `stream-json` 模式下输出的事件中包含 `session_id` 字段。新增 `tryExtractSessionID` 函数从流中捕获：

```go
// streamparse.go
func tryExtractSessionID(line string) (string, bool) {
    if !strings.Contains(line, `"session_id"`) {
        return "", false
    }
    var m map[string]any
    if err := json.Unmarshal([]byte(line), &m); err != nil {
        return "", false
    }
    if sid, ok := m["session_id"].(string); ok && sid != "" {
        return sid, true
    }
    return "", false
}
```

在 `monitorStream` 循环中逐行扫描，首次匹配后记录、不再重复解析。

**降级**：超时触发时若未捕获到 session_id（CLI 在初始化阶段就卡住），跳过 resume，直接走 Hard Timeout。此时 CLI 还没开始实质工作，全量重试代价不大。

### 2. DockerClient 接口扩展

新增两个方法：

```go
// docker.go - DockerClient interface

// ExecInContainer 在运行中的容器内执行短命令，阻塞等待完成，返回 stdout+stderr。
// 用于发送信号、等待进程退出等操作。
ExecInContainer(ctx context.Context, containerID string, cmd []string) (string, error)

// ExecStream 在运行中的容器内执行长命令，返回 stdout 的流式 reader。
// 用于启动 resume 进程并持续监控其输出。调用方负责 Close。
ExecStream(ctx context.Context, containerID string, cmd []string) (io.ReadCloser, error)
```

实现基于 Docker SDK 的 `ContainerExecCreate` + `ContainerExecAttach` + `ContainerExecInspect`。

`ExecStream` 返回的 reader 可能带 Docker stdcopy 8 字节 header（取决于 exec 配置是否启用 TTY）。实现中使用非 TTY 模式，调用方需用 `stdcopy.StdCopy` 解复用——与现有 `FollowLogs` 的处理方式一致。

### 3. monitorStream 提取

将当前 `streamMonitorLoop` 中的行扫描+超时检测逻辑提取为独立方法，供 Phase 1 和 Phase 2 复用：

```go
type streamOutcome int
const (
    outcomeStreamEnd       streamOutcome = iota // 流正常结束（进程退出）
    outcomeActivityTimeout                       // 活跃度超时
    outcomeCtxDone                               // 外部 ctx 取消
)

// monitorStream 监控一个 reader 的行输出，返回结束原因和捕获的 session_id。
func (p *Pool) monitorStream(
    ctx context.Context,
    reader io.Reader,
    resultCh chan<- string,
    knownSessionID string,
) (streamOutcome, string)
```

内部逻辑与当前 `streamMonitorLoop` 的 for-select 循环相同：收到新行则重置 timer，提取 result 事件转发到 resultCh，timer 到期返回 `outcomeActivityTimeout`。

### 4. execResume 实现

```go
// execResume 在容器内终止卡住的 claude 进程并启动 resume 会话。
func (p *Pool) execResume(
    ctx context.Context, containerID, sessionID string,
) (io.ReadCloser, error)
```

四步执行序列：

1. **创建哨兵文件**（告知 entrypoint 保持存活）：`ExecInContainer(ctx, id, ["touch", "/tmp/.dtworkflow-resume"])`
2. **SIGINT 终止 claude**：`ExecInContainer(ctx, id, ["pkill", "-SIGINT", "claude"])`
3. **等待进程退出**（最多 15 秒）：`ExecInContainer(ctx, id, ["sh", "-c", "for i in $(seq 30); do pkill -0 claude 2>/dev/null || exit 0; sleep 0.5; done; exit 1"])`
4. **启动 resume 进程**：`ExecStream(ctx, id, ["claude", "--resume", sessionID, "-p", "之前的执行被中断了，请继续完成任务并输出最终结果", "--output-format", "stream-json", "--verbose"])`

步骤 1 必须在步骤 2 之前执行：SIGINT 导致 claude 退出后，entrypoint 检查哨兵文件决定是否保持存活。如果顺序颠倒，entrypoint 可能在哨兵文件创建前就退出，容器进入 exited 状态。

任一步骤失败则返回 error，调用方降级为 Hard Timeout。

### 5. streamMonitorLoop 改造

```go
func (p *Pool) streamMonitorLoop(
    ctx context.Context,
    cancel context.CancelCauseFunc,
    containerID string,
    resultCh chan<- string,
    monitorDone chan<- struct{},
) {
    defer close(resultCh)
    defer close(monitorDone)

    // Phase 1: 监控原始进程
    reader, err := p.docker.FollowLogs(ctx, containerID)
    if err != nil { /* log + return */ }

    outcome, sessionID := p.monitorStream(ctx, reader, resultCh, "")
    reader.Close()

    if outcome != outcomeActivityTimeout {
        return // 流正常结束或外部取消
    }

    // Soft Timeout: 尝试 resume
    if sessionID == "" {
        cancel(ErrActivityTimeout)
        return
    }

    resumeReader, err := p.execResume(ctx, containerID, sessionID)
    if err != nil {
        cancel(ErrActivityTimeout)
        return
    }
    defer resumeReader.Close()

    // Phase 2: 监控 resume 进程
    outcome, _ = p.monitorStream(ctx, resumeReader, resultCh, sessionID)

    // 通知 entrypoint 可以退出
    notifyCtx, notifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer notifyCancel()
    _, _ = p.docker.ExecInContainer(notifyCtx, containerID,
        []string{"touch", "/tmp/.dtworkflow-resume-done"})

    if outcome == outcomeActivityTimeout {
        cancel(ErrActivityTimeout)
    } else {
        // resume 成功（流正常结束），cancel(nil) 解除 WaitContainer 阻塞
        cancel(nil)
    }
}
```

### 6. runContainer 集成调整

**问题**：entrypoint 改造后，resume 期间容器保持存活（entrypoint 在哨兵等待循环中），`WaitContainer` 不会提前返回。当 resume 成功时，`streamMonitorLoop` 调用 `cancel(nil)` 解除 `WaitContainer` 的阻塞。此时 `WaitContainer` 返回 `context.Canceled` 错误，但当前代码只在 `waitErr == nil` 时才读取 stream result，导致 resume 成功的结果被丢弃。

**改动**：

1. **stream result 读取条件放宽**：从 `waitErr == nil` 改为"cause 不是 ErrActivityTimeout"。这确保 resume 成功路径（cause=nil）和正常退出路径都能读取 stream result，只有 Hard Timeout 路径跳过。

```go
// runContainer 中，WaitContainer 之后
exitCode, waitErr = p.docker.WaitContainer(monitorCtx, containerID)

streamResult, hasStreamResult := "", false
causeIsTimeout := errors.Is(context.Cause(monitorCtx), ErrActivityTimeout)
if !causeIsTimeout {
    streamResult, hasStreamResult = waitForStreamResult(resultCh, monitorDone, streamMonitorDrainTimeout)
}
```

2. **stream result 权威覆写**：当从 resultCh 拿到有效 result 时（无论来自 Phase 1 还是 Phase 2），覆写 exitCode=0、waitErr=nil。流式 result 是任务完成的权威信号，优先级高于容器退出码。

```go
if hasStreamResult {
    exitCode = 0
    waitErr = nil
}
```

3. **drain 等待时长不需要调整**：entrypoint 改造解决了原来的竞态问题——容器在 resume 完成前不会退出，`WaitContainer` 不会提前返回。`streamMonitorLoop` 在 resume 完成后才 `cancel(nil)` 解除阻塞，此时 resultCh 中已有结果，2 秒 drain 超时足够。

4. **现有 service 无需改动**：fix.Service、test.Service 等检查 `ExitCode != 0` 的逻辑不受影响，因为 resume 成功时 exitCode 已被覆写为 0，后续正常进入 parseResult 解析 JSON 内容。

## 配置

不新增配置项。resume 功能随 `stream_monitor.enabled: true` 自动生效。

| 参数 | 来源 | 值 |
|------|------|-----|
| Soft/Hard timeout | `stream_monitor.activity_timeout` | 默认 2m |
| 等待进程退出超时 | 内部常量 | 15s |
| entrypoint 兜底等待 | 内部常量 | 300s |
| resume 次数 | 硬编码 | 最多 1 次 |

## 可观测性

结构化日志事件（slog）：

| 事件 | 级别 | 关键字段 |
|------|------|---------|
| 活跃度超时(Phase 1)，尝试 resume | WARN | container_id, session_id |
| 等待 claude 进程退出 | INFO | container_id |
| 启动 resume 进程 | INFO | container_id, session_id |
| resume 成功，获取到流式结果 | INFO | container_id, duration_ms |
| resume 失败，降级 Hard Timeout | ERROR | container_id, error |
| 活跃度超时(Phase 2)，Hard Timeout | WARN | container_id |
| 未捕获 session_id，跳过 resume | WARN | container_id |

## 测试策略

| 层级 | 覆盖内容 | 方式 |
|------|---------|------|
| 单元测试 | `tryExtractSessionID` | 直接函数测试，各种输入格式 |
| 单元测试 | `monitorStream` | mock lineCh，验证三种 outcome + session_id 捕获 |
| 单元测试 | `execResume` | mock DockerClient，验证四步调用序列（哨兵 → SIGINT → 等待 → resume）和错误降级 |
| 集成测试 | 两阶段完整流程 | mock Docker，验证 Phase 1 超时 -> resume -> Phase 2 result -> exitCode=0 |
| 集成测试 | resume 失败降级 | mock ExecStream 返回 error，验证降级为 ErrActivityTimeout |
| 手动验证 | Claude CLI resume 行为 | 在真实容器中 SIGINT + resume，确认 session 保存和恢复 |

手动验证作为实施前的 gate check。

## 改动清单

| 文件 | 改动 |
|------|------|
| `build/docker/entrypoint.sh` | `exec "$@"` 替换为子进程模式 + 哨兵文件等待（PID 1 修复） |
| `internal/worker/docker.go` | DockerClient 接口新增 `ExecInContainer`、`ExecStream`；dockerClient 实现 |
| `internal/worker/streamparse.go` | 新增 `tryExtractSessionID` |
| `internal/worker/pool.go` | 提取 `monitorStream`；改造 `streamMonitorLoop`（两阶段 + resume-done 哨兵 + cancel(nil)）；新增 `execResume`（四步序列）；`runContainer` stream result 读取条件放宽 + exitCode 覆写 |
| `internal/worker/mock_docker_test.go` | mock 新增两个方法 |
| `internal/worker/streamparse_test.go` | `tryExtractSessionID` 单元测试 |
| `internal/worker/pool_test.go` | `monitorStream`、`execResume`、两阶段集成测试 |
| `build/docker/entrypoint_test.sh` | entrypoint 哨兵文件逻辑测试 |

## 不改动的部分

- 配置文件（无新配置项）
- 各 service（review/fix/test/e2e）的 parseResult 逻辑
- 旧路径（`stream_monitor.enabled=false` 时行为不变）
- asynq 重试机制（Hard Timeout 后仍走现有全量重试）

## 风险

| 风险 | 缓解 |
|------|------|
| `--resume` 可靠性未知 | 实施前手动验证（gate check） |
| session_id 事件格式与预期不同 | 手动验证覆盖；`tryExtractSessionID` 解析失败时降级 Hard Timeout |
| exec stdout 需要 stdcopy 解复用 | ExecStream 使用非 TTY 模式，调用方与 FollowLogs 相同方式处理 |
| resume prompt 不当导致 Claude 行为异常 | prompt 简洁明确（"继续完成任务并输出最终结果"），不引入额外指令 |
| PID 1 退出导致容器停止 | entrypoint 改造为子进程模式 + 哨兵文件协调，确保容器在 resume 期间保持存活 |
| entrypoint 改造影响正常路径 | 哨兵文件不存在时行为与 `exec "$@"` 一致；gate check 需同时验证正常退出和 resume 两条路径 |

## 前置验证（Gate Check）清单

实施前必须在真实容器中手动验证以下全部项目，任一项不通过则方案需要调整：

1. **正常退出不受影响**：entrypoint 改造后，无哨兵文件时容器退出行为与 `exec "$@"` 一致（退出码正确、无额外延迟）
2. **容器在 SIGINT 后保持存活**：创建哨兵 → SIGINT claude → `docker exec` 仍可执行
3. **session 持久化**：SIGINT 后 `~/.claude/` 下存在 session 数据文件
4. **session_id 可捕获**：`stream-json` 输出中确认 session_id 字段的事件类型和格式
5. **resume 可恢复上下文**：`claude --resume <session_id> -p "继续"` 能恢复之前的对话上下文并继续工作
6. **resume 输出格式兼容**：resume 进程的 `stream-json` 输出包含 result 事件，格式与首次执行一致
