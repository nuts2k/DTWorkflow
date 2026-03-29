# 容器超时与可观测性设计

> 日期：2026-03-29
> 状态：已批准
> 关联：Phase 2 PR 自动评审
> 前置讨论：`docs/discussions/2026-03-27-container-timeout-observability.md`

## 1. 背景与目标

PR 评审在 Docker 容器内运行 `claude -p`，当前是完全黑盒等待：容器启动后到执行结束之间没有任何中间可观测信号。超时值硬编码在代码中，无法按项目特征调整。

**目标**：
1. 超时值可配置化，支持按任务类型区分
2. 利用 `stream-json` 输出格式的流式事件实现活跃度检测
3. 活跃度检测作为可选特性，通过配置开关控制

## 2. 设计决策记录

| # | 决策 | 选择 | 理由 |
|---|------|------|------|
| 1 | 实施范围 | 三步全做（方案 D + E 基础 + E 完整） | 方案 D 单独做价值有限，E 基础和完整耦合度高 |
| 2 | 方案 E 开关 | 单一全局开关，关闭时退回 json + 静态超时 | 流式监控是基础设施层能力，不需要仓库/任务类型粒度 |
| 3 | 超时配置粒度 | 硬超时按任务类型区分，活跃度阈值全局 | 不同任务执行特征差异大，活跃度检测标准通用 |
| 4 | 默认值 | review 15min / fix 45min / gen 30min / 活跃度 2min | 活跃度 2min 基于验证数据（最大静默 9s）留 13 倍余量 |
| 5 | 卡住后动作 | Kill 容器，交给 asynq 重试 | 复用现有重试机制（3 次 / 指数退避） |
| 6 | 流读取方式 | ContainerLogs + Follow | Docker 官方推荐，自带 stdcopy 解复用 |
| 7 | 监控架构 | 并行 goroutine + shared context cancel | 关注点分离：WaitContainer 判完成，监控判卡住 |
| 8 | 结果提取 | 监控层提取 result 事件，上层不感知 | stream-json 是实现细节，不应泄露到业务层 |
| 9 | 接口变更 | DockerClient 新增 FollowLogs 方法 | 保持接口一致性，方便 mock 测试 |
| 10 | 命令注入 | Pool 层透明注入 stream-json 标志 | 集中管控，上层调用方零改动 |

## 3. 配置结构

### 3.1 YAML 配置

```yaml
worker:
  image: "dtworkflow-worker:1.0"
  cpu_limit: "2.0"
  memory_limit: "4g"

  # 方案 D：按任务类型的硬超时（替代硬编码）
  timeouts:
    review_pr: 15m
    fix_issue: 45m
    gen_tests: 30m

  # 方案 E：流式心跳监控（默认关闭）
  stream_monitor:
    enabled: false
    activity_timeout: 2m
```

### 3.2 Go 结构体

```go
// config 包
type WorkerConfig struct {
    Concurrency   int               `mapstructure:"concurrency"`
    Timeout       time.Duration     `mapstructure:"timeout"`       // 保留，向后兼容
    Image         string            `mapstructure:"image"`
    CPULimit      string            `mapstructure:"cpu_limit"`
    MemoryLimit   string            `mapstructure:"memory_limit"`
    NetworkName   string            `mapstructure:"network_name"`
    Timeouts      TaskTimeouts      `mapstructure:"timeouts"`      // 新增
    StreamMonitor StreamMonitorConf `mapstructure:"stream_monitor"` // 新增
}

type TaskTimeouts struct {
    ReviewPR time.Duration `mapstructure:"review_pr"`
    FixIssue time.Duration `mapstructure:"fix_issue"`
    GenTests time.Duration `mapstructure:"gen_tests"`
}

type StreamMonitorConf struct {
    Enabled         bool          `mapstructure:"enabled"`
    ActivityTimeout time.Duration `mapstructure:"activity_timeout"`
}
```

### 3.3 优先级链

`worker.timeouts.<type>` > `worker.timeout` > 硬编码默认值

零配置时行为与当前完全一致。

## 4. DockerClient 接口变更

### 4.1 新增方法

```go
type DockerClient interface {
    // ... 现有方法保持不变 ...

    // FollowLogs 以流式方式读取容器 stdout 日志。
    // 返回的 reader 持续输出数据直到容器退出或 ctx 取消。
    // 调用方负责 Close。
    FollowLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
}
```

### 4.2 实现

```go
func (d *dockerClient) FollowLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
    reader, err := d.cli.ContainerLogs(ctx, containerID, container.LogsOptions{
        ShowStdout: true,
        ShowStderr: false,
        Follow:     true,
        Timestamps: false,
    })
    if err != nil {
        return nil, fmt.Errorf("follow 容器 %s 日志失败: %w", containerID, err)
    }
    return reader, nil
}
```

### 4.3 设计要点

- 只读 stdout — stream-json 事件输出在 stdout
- 返回原始 `io.ReadCloser`，不做 stdcopy 解复用（由监控层处理）
- ctx 控制生命周期 — 容器退出或 ctx cancel 时 reader 自动关闭
- Docker 日志流带 8 字节 header（stdcopy 格式），监控层内部用 `stdcopy.StdCopy` 解复用后再按行扫描

## 5. Pool 层核心改造

### 5.1 runContainer 改造后流程

```
runContainer(ctx, payload, cmd, opts) {
    // 现有：校验、创建容器（不变）

    // 新增：根据配置注入 stream-json 标志
    if streamMonitorEnabled {
        cmd = injectStreamJsonFlags(cmd)
    }

    CreateContainer(ctx, containerCfg)
    StartContainer()

    // 分支：两种执行模式
    if streamMonitorEnabled {
        monitorCtx, monitorCancel := context.WithCancel(ctx)

        resultCh := make(chan string, 1)
        stuckCh  := make(chan struct{}, 1)

        go streamMonitorLoop(monitorCtx, containerID, resultCh, stuckCh)

        exitCode, waitErr = WaitContainer(monitorCtx, containerID)
        monitorCancel()

        select {
        case result := <-resultCh:
            output = result
        default:
            // fallback：流中没拿到 result，事后从日志获取
            logs = GetContainerLogs(containerID)
            output = extractResultFromStreamLogs(logs.Stdout)
        }
    } else {
        // 现有逻辑，完全不变
        exitCode, waitErr = WaitContainer(waitCtx, containerID)
        logs = GetContainerLogs(containerID)
        output = logs.Stdout
    }

    // 现有：构造 ExecutionResult、错误处理（不变）
}
```

### 5.2 streamMonitorLoop

```
streamMonitorLoop(ctx, containerID, resultCh, stuckCh) {
    reader := docker.FollowLogs(ctx, containerID)
    defer reader.Close()

    scanner := bufio.NewScanner(stdcopyReader(reader))
    timer := time.NewTimer(activityTimeout)

    for scanner.Scan() {
        timer.Reset(activityTimeout)     // 每收到一行就重置计时器
        line := scanner.Text()

        if isResultEvent(line) {
            resultCh <- line
        }
    }

    // scanner 结束：容器退出或读取出错
    select {
    case <-timer.C:
        stuckCh <- struct{}{}            // 通知主流程：卡住
    default:
    }
}
```

### 5.3 卡住检测触发链路

```
timer 到期（2 分钟无新事件）
  -> monitorLoop 检测到超时
  -> 关闭 monitorCtx（cancel）
  -> WaitContainer 收到 ctx cancel，返回错误
  -> runContainer 判断为超时，kill 容器
  -> 返回错误，asynq 接管重试
```

### 5.4 injectStreamJsonFlags

```go
// injectStreamJsonFlags 将命令中的 --output-format 替换为 stream-json 模式。
// 无 --output-format 参数时直接追加。
func injectStreamJsonFlags(cmd []string) []string {
    // 1. 移除已有的 --output-format 及其值
    // 2. 追加: --output-format stream-json --verbose --include-partial-messages
}
```

### 5.5 两条路径隔离

开关关闭时（`stream_monitor.enabled: false`），`runContainer` 走 else 分支，代码路径与当前完全一致，零改动零风险。

## 6. stream-json 事件解析

### 6.1 新文件：`internal/worker/streamparse.go`

```go
type streamEvent struct {
    Type string `json:"type"`
}

type resultEvent struct {
    Type       string  `json:"type"`
    Subtype    string  `json:"subtype"`
    CostUSD    float64 `json:"cost_usd"`
    DurationMs int64   `json:"duration_ms"`
    IsError    bool    `json:"is_error"`
    NumTurns   int     `json:"num_turns"`
    Result     string  `json:"result"`
    SessionID  string  `json:"session_id"`
}

// isResultEvent 快速筛选（只解析 type 字段）
func isResultEvent(line string) bool

// parseResultEvent 完整解析 result 事件
func parseResultEvent(line string) (*resultEvent, error)

// resultEventToCLIJSON 将 result 事件转换为与 --output-format json 兼容的 JSON
func resultEventToCLIJSON(event *resultEvent) (string, error)
```

### 6.2 上层透明性

```
stream-json result 事件
  -> parseResultEvent()
  -> resultEventToCLIJSON()       // 转换为 CLI JSON 信封格式
  -> 填入 ExecutionResult.Output
  -> review.Service.parseResult() // 正常解析，与 json 模式一致
```

上层代码（review / fix / gentest）零改动。

## 7. 配置传递

### 7.1 PoolConfig 扩展

```go
// worker 包
type PoolConfig struct {
    Image       string
    CPULimit    string
    MemoryLimit string
    NetworkName string
    WorkDir     string
    Timeouts      TaskTimeoutsConfig    // 新增
    StreamMonitor StreamMonitorConfig   // 新增
}

type TaskTimeoutsConfig struct {
    ReviewPR time.Duration
    FixIssue time.Duration
    GenTests time.Duration
}

type StreamMonitorConfig struct {
    Enabled         bool
    ActivityTimeout time.Duration
}
```

### 7.2 options.go 改造

```go
// TaskTimeout 从配置中获取超时值，带 fallback 链
func TaskTimeout(taskType model.TaskType, cfg TaskTimeoutsConfig) time.Duration {
    var configured time.Duration
    switch taskType {
    case model.TaskTypeReviewPR:
        configured = cfg.ReviewPR
    case model.TaskTypeFixIssue:
        configured = cfg.FixIssue
    case model.TaskTypeGenTests:
        configured = cfg.GenTests
    }
    if configured > 0 {
        return configured
    }
    return defaultTaskTimeout(taskType)
}
```

### 7.3 依赖方向

```
cmd 层（组装）
  -> config.WorkerConfig    （YAML 结构）
  -> worker.PoolConfig      （运行时配置）
  -> worker.TaskTimeoutsConfig / StreamMonitorConfig
```

worker 包不依赖 config 包，转换在 cmd 层完成。

### 7.4 asynq 超时同步

`queue.buildAsynqOptions` 也使用 `TaskTimeout()`，签名同步变更，确保 asynq 超时与容器超时一致。

## 8. 文件变更清单

| 文件 | 变更类型 | 说明 |
|------|----------|------|
| `internal/config/config.go` | 修改 | WorkerConfig 新增 Timeouts、StreamMonitor 字段 |
| `internal/config/validate.go` | 修改 | 新增超时值和活跃度阈值的校验规则 |
| `internal/config/config_test.go` | 修改 | 新增配置解析和校验测试 |
| `internal/worker/docker.go` | 修改 | DockerClient 接口新增 FollowLogs；dockerClient 实现 |
| `internal/worker/pool.go` | 修改 | runContainer 分支逻辑、injectStreamJsonFlags、监控 goroutine |
| `internal/worker/streamparse.go` | **新建** | stream-json 事件解析、result 提取、信封转换 |
| `internal/worker/streamparse_test.go` | **新建** | 事件解析单测 |
| `internal/worker/pool_test.go` | 修改 | 新增流式监控模式的测试用例 |
| `internal/worker/docker_mock_test.go` | 修改 | mock 新增 FollowLogs 方法 |
| `internal/queue/options.go` | 修改 | TaskTimeout 签名变更，接收配置参数 |
| `internal/queue/options_test.go` | 修改 | 适配新签名 |
| `internal/queue/client.go` | 修改 | buildAsynqOptions 传入超时配置 |
| `configs/dtworkflow.yaml` | 修改 | 新增 timeouts 和 stream_monitor 配置示例 |

## 9. 测试策略

### 单元测试

1. **streamparse_test.go** — result 事件识别、解析、信封转换、畸形 JSON 容错
2. **injectStreamJsonFlags** — 已有 `--output-format` 时替换、无该参数时追加、空命令边界
3. **TaskTimeout fallback 链** — 配置值优先 > 默认值回退
4. **配置校验** — 负数超时、零值活跃度阈值、非法配置组合

### 集成测试（mock Docker）

5. **流式监控正常完成** — mock FollowLogs 返回模拟事件流（含 result），验证 Output 正确提取
6. **活跃度超时触发** — mock FollowLogs 写入几行后停止，验证超时后 ctx 被 cancel
7. **开关关闭时走旧路径** — 验证 StreamMonitor.Enabled=false 时行为与当前一致
8. **result 提取 fallback** — 流中无 result 事件时，验证从 GetContainerLogs 兜底
