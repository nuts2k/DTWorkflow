# 容器超时与可观测性实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 实现容器执行的可配置超时和基于 stream-json 的活跃度心跳检测，解决执行期间无可观测性的问题。

**Architecture:** 三层改造：(1) 配置层新增超时和监控配置 (2) worker 层新增流式事件解析和监控 goroutine (3) queue 层适配新的超时函数签名。开关关闭时代码路径与当前完全一致。

**Tech Stack:** Go / Docker SDK / asynq / Viper

**Design doc:** `docs/plans/2026-03-29-container-timeout-observability-design.md`

---

### Task 1: 配置层 — 新增 Timeouts 和 StreamMonitor 结构体

**Files:**
- Modify: `internal/config/config.go:62-70` (WorkerConfig)
- Modify: `internal/config/config.go:194-224` (WithDefaults)
- Test: `internal/config/config_test.go`

**Step 1: 写失败测试 — 验证新配置字段可解析**

在 `internal/config/config_test.go` 中新增测试：

```go
func TestWorkerTimeoutsAndStreamMonitor_Parse(t *testing.T) {
	yaml := `
server: { host: "0.0.0.0", port: 8080 }
gitea: { url: "https://g.example.com", token: "tok" }
claude: { api_key: "key" }
redis: { addr: "localhost:6379" }
webhook: { secret: "sec" }
notify: { default_channel: "gitea", channels: { gitea: { enabled: true } } }
worker:
  concurrency: 3
  timeout: 30m
  image: "dtworkflow-worker:1.0"
  timeouts:
    review_pr: 15m
    fix_issue: 45m
    gen_tests: 30m
  stream_monitor:
    enabled: true
    activity_timeout: 2m
`
	m, err := NewManager()
	if err != nil {
		t.Fatal(err)
	}
	m.v.SetConfigType("yaml")
	if err := m.v.ReadConfig(strings.NewReader(yaml)); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{}
	if err := m.v.Unmarshal(cfg); err != nil {
		t.Fatal(err)
	}

	if cfg.Worker.Timeouts.ReviewPR != 15*time.Minute {
		t.Errorf("ReviewPR = %v, want 15m", cfg.Worker.Timeouts.ReviewPR)
	}
	if cfg.Worker.Timeouts.FixIssue != 45*time.Minute {
		t.Errorf("FixIssue = %v, want 45m", cfg.Worker.Timeouts.FixIssue)
	}
	if cfg.Worker.Timeouts.GenTests != 30*time.Minute {
		t.Errorf("GenTests = %v, want 30m", cfg.Worker.Timeouts.GenTests)
	}
	if !cfg.Worker.StreamMonitor.Enabled {
		t.Error("StreamMonitor.Enabled should be true")
	}
	if cfg.Worker.StreamMonitor.ActivityTimeout != 2*time.Minute {
		t.Errorf("ActivityTimeout = %v, want 2m", cfg.Worker.StreamMonitor.ActivityTimeout)
	}
}
```

**Step 2: 运行测试确认失败**

运行: `go test ./internal/config/ -run TestWorkerTimeoutsAndStreamMonitor_Parse -v`
预期: 编译失败 — `cfg.Worker.Timeouts` 不存在

**Step 3: 实现 — 在 WorkerConfig 中新增字段**

修改 `internal/config/config.go`，在 `WorkerConfig` 结构体末尾新增：

```go
type WorkerConfig struct {
	Concurrency int           `mapstructure:"concurrency"`
	Timeout     time.Duration `mapstructure:"timeout"`
	Image       string        `mapstructure:"image"`
	CPULimit    string        `mapstructure:"cpu_limit"`
	MemoryLimit string        `mapstructure:"memory_limit"`
	NetworkName string        `mapstructure:"network_name"`
	Timeouts      TaskTimeouts      `mapstructure:"timeouts"`       // 按任务类型的硬超时
	StreamMonitor StreamMonitorConf `mapstructure:"stream_monitor"` // 流式心跳监控配置
}

// TaskTimeouts 按任务类型配置硬超时，零值表示使用默认值
type TaskTimeouts struct {
	ReviewPR time.Duration `mapstructure:"review_pr"`
	FixIssue time.Duration `mapstructure:"fix_issue"`
	GenTests time.Duration `mapstructure:"gen_tests"`
}

// StreamMonitorConf 流式心跳监控配置
type StreamMonitorConf struct {
	Enabled         bool          `mapstructure:"enabled"`
	ActivityTimeout time.Duration `mapstructure:"activity_timeout"`
}
```

**Step 4: 运行测试确认通过**

运行: `go test ./internal/config/ -run TestWorkerTimeoutsAndStreamMonitor_Parse -v`
预期: PASS

**Step 5: 确保全量测试通过**

运行: `go test ./internal/config/ -v`
预期: 全部 PASS

**Step 6: 提交**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): 新增 worker.timeouts 和 worker.stream_monitor 配置结构"
```

---

### Task 2: 配置校验 — 超时和活跃度阈值

**Files:**
- Modify: `internal/config/validate.go:53-59`
- Test: `internal/config/config_test.go`

**Step 1: 写失败测试 — 负数超时值应被拒绝**

```go
func TestValidate_WorkerTimeouts_Negative(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Worker.Timeouts.ReviewPR = -1 * time.Minute
	err := Validate(cfg)
	if err == nil {
		t.Error("负数 timeouts.review_pr 应校验失败")
	}
}

func TestValidate_StreamMonitor_InvalidActivityTimeout(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Worker.StreamMonitor.Enabled = true
	cfg.Worker.StreamMonitor.ActivityTimeout = -1 * time.Second
	err := Validate(cfg)
	if err == nil {
		t.Error("启用 stream_monitor 时负数 activity_timeout 应校验失败")
	}
}

func TestValidate_StreamMonitor_DisabledNoValidation(t *testing.T) {
	cfg := validMinimalConfig()
	cfg.Worker.StreamMonitor.Enabled = false
	cfg.Worker.StreamMonitor.ActivityTimeout = 0 // 关闭时不校验
	err := Validate(cfg)
	if err != nil {
		t.Errorf("stream_monitor 关闭时不应校验 activity_timeout: %v", err)
	}
}
```

注意：`validMinimalConfig()` 是测试中已有的辅助函数。如果不存在，需要创建一个返回可通过校验的最小配置。

**Step 2: 运行测试确认失败**

运行: `go test ./internal/config/ -run "TestValidate_WorkerTimeouts|TestValidate_StreamMonitor" -v`
预期: 第一个和第二个 FAIL（没有校验逻辑），第三个可能 PASS

**Step 3: 实现 — 在 Validate() 中新增校验规则**

在 `internal/config/validate.go` 的 `Validate()` 函数中，在 `worker.timeout` 校验之后（约第 59 行后）新增：

```go
	// worker.timeouts 各字段非负校验（零值表示使用默认值，允许）
	if cfg.Worker.Timeouts.ReviewPR < 0 {
		errs = append(errs, fmt.Errorf("worker.timeouts.review_pr 不能为负数，当前值: %s", cfg.Worker.Timeouts.ReviewPR))
	}
	if cfg.Worker.Timeouts.FixIssue < 0 {
		errs = append(errs, fmt.Errorf("worker.timeouts.fix_issue 不能为负数，当前值: %s", cfg.Worker.Timeouts.FixIssue))
	}
	if cfg.Worker.Timeouts.GenTests < 0 {
		errs = append(errs, fmt.Errorf("worker.timeouts.gen_tests 不能为负数，当前值: %s", cfg.Worker.Timeouts.GenTests))
	}

	// worker.stream_monitor 校验（仅在 enabled 时校验 activity_timeout）
	if cfg.Worker.StreamMonitor.Enabled {
		if cfg.Worker.StreamMonitor.ActivityTimeout <= 0 {
			errs = append(errs, fmt.Errorf("worker.stream_monitor.activity_timeout 启用时必须大于 0，当前值: %s", cfg.Worker.StreamMonitor.ActivityTimeout))
		}
	}
```

**Step 4: 运行测试确认通过**

运行: `go test ./internal/config/ -run "TestValidate_WorkerTimeouts|TestValidate_StreamMonitor" -v`
预期: 全部 PASS

**Step 5: 确保全量测试通过**

运行: `go test ./internal/config/ -v`
预期: 全部 PASS

**Step 6: 提交**

```bash
git add internal/config/validate.go internal/config/config_test.go
git commit -m "feat(config): 新增 worker.timeouts 和 stream_monitor 配置校验"
```

---

### Task 3: Worker 类型层 — PoolConfig 新增超时和监控配置

**Files:**
- Modify: `internal/worker/types.go:36-47` (PoolConfig)
- Modify: `internal/worker/pool_test.go:15-25` (defaultPoolConfig)
- Test: `internal/worker/pool_test.go`

**Step 1: 修改 PoolConfig 结构体**

在 `internal/worker/types.go` 的 `PoolConfig` 结构体末尾新增：

```go
type PoolConfig struct {
	Image        string
	CPULimit     string
	MemoryLimit  string
	GiteaURL     string
	GiteaToken   SecretString `json:"-"`
	ClaudeAPIKey  SecretString `json:"-"`
	ClaudeBaseURL string
	WorkDir       string
	NetworkName  string
	GiteaInsecureSkipVerify bool
	Timeouts      TaskTimeoutsConfig  // 按任务类型的硬超时配置
	StreamMonitor StreamMonitorConfig // 流式心跳监控配置
}

// TaskTimeoutsConfig Worker 层的超时配置（从 config.TaskTimeouts 转换而来）
type TaskTimeoutsConfig struct {
	ReviewPR time.Duration
	FixIssue time.Duration
	GenTests time.Duration
}

// StreamMonitorConfig Worker 层的流式监控配置
type StreamMonitorConfig struct {
	Enabled         bool
	ActivityTimeout time.Duration
}
```

需要在 import 中新增 `"time"`。

**Step 2: 运行全量测试确认通过（向后兼容）**

运行: `go test ./internal/worker/ -v`
预期: 全部 PASS（新增字段零值不影响现有逻辑）

**Step 3: 提交**

```bash
git add internal/worker/types.go
git commit -m "feat(worker): PoolConfig 新增 Timeouts 和 StreamMonitor 配置字段"
```

---

### Task 4: Queue 层 — TaskTimeout 签名改造

**Files:**
- Modify: `internal/queue/options.go:9-22` (TaskTimeout)
- Modify: `internal/queue/client.go:156-167` (buildAsynqOptions)
- Test: `internal/queue/options_test.go`

**Step 1: 写失败测试 — TaskTimeout 接收配置参数**

修改 `internal/queue/options_test.go`，替换现有 `TestTaskTimeout`：

```go
func TestTaskTimeout(t *testing.T) {
	// 测试配置值优先
	cfg := TaskTimeoutsConfig{
		ReviewPR: 15 * time.Minute,
		FixIssue: 45 * time.Minute,
		GenTests: 30 * time.Minute,
	}
	tests := []struct {
		taskType model.TaskType
		expected time.Duration
	}{
		{model.TaskTypeReviewPR, 15 * time.Minute},
		{model.TaskTypeFixIssue, 45 * time.Minute},
		{model.TaskTypeGenTests, 30 * time.Minute},
	}
	for _, tt := range tests {
		got := TaskTimeout(tt.taskType, cfg)
		if got != tt.expected {
			t.Errorf("TaskTimeout(%q, cfg) = %v, want %v", tt.taskType, got, tt.expected)
		}
	}
}

func TestTaskTimeout_FallbackToDefault(t *testing.T) {
	// 配置为零值时回退到默认值
	cfg := TaskTimeoutsConfig{} // 全部零值
	tests := []struct {
		taskType model.TaskType
		expected time.Duration
	}{
		{model.TaskTypeReviewPR, 10 * time.Minute},  // 默认
		{model.TaskTypeFixIssue, 30 * time.Minute},  // 默认
		{model.TaskTypeGenTests, 20 * time.Minute},  // 默认
		{"unknown", 10 * time.Minute},                // 未知类型默认
	}
	for _, tt := range tests {
		got := TaskTimeout(tt.taskType, cfg)
		if got != tt.expected {
			t.Errorf("TaskTimeout(%q, zero) = %v, want %v", tt.taskType, got, tt.expected)
		}
	}
}
```

同时修改 `TestBuildAsynqOptions_WithTaskID` 和 `TestBuildAsynqOptions_WithoutTaskID`，给 `buildAsynqOptions` 传入新参数：

```go
func TestBuildAsynqOptions_WithTaskID(t *testing.T) {
	opts := buildAsynqOptions(model.TaskTypeReviewPR, TaskTimeoutsConfig{}, EnqueueOptions{
		Priority: model.PriorityHigh,
		TaskID:   "my-task-id",
	})
	if len(opts) != 4 {
		t.Errorf("buildAsynqOptions 带 TaskID 应返回 4 个选项，得到 %d", len(opts))
	}
}

func TestBuildAsynqOptions_WithoutTaskID(t *testing.T) {
	opts := buildAsynqOptions(model.TaskTypeFixIssue, TaskTimeoutsConfig{}, EnqueueOptions{
		Priority: model.PriorityNormal,
	})
	if len(opts) != 3 {
		t.Errorf("buildAsynqOptions 无 TaskID 应返回 3 个选项，得到 %d", len(opts))
	}
}
```

**Step 2: 运行测试确认失败**

运行: `go test ./internal/queue/ -run "TestTaskTimeout|TestBuildAsynqOptions" -v`
预期: 编译失败 — `TaskTimeout` 签名不匹配

**Step 3: 实现 — 改造 TaskTimeout 和 buildAsynqOptions**

修改 `internal/queue/options.go`：

```go
// TaskTimeoutsConfig 按任务类型的超时配置（从 worker 层镜像，避免循环依赖）
type TaskTimeoutsConfig struct {
	ReviewPR time.Duration
	FixIssue time.Duration
	GenTests time.Duration
}

// TaskTimeout 从配置中获取超时值，零值时回退到硬编码默认值
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

// defaultTaskTimeout 硬编码默认超时值（fallback）
func defaultTaskTimeout(taskType model.TaskType) time.Duration {
	switch taskType {
	case model.TaskTypeReviewPR:
		return 10 * time.Minute
	case model.TaskTypeFixIssue:
		return 30 * time.Minute
	case model.TaskTypeGenTests:
		return 20 * time.Minute
	default:
		return 10 * time.Minute
	}
}
```

修改 `internal/queue/client.go` 的 `buildAsynqOptions`：

```go
func buildAsynqOptions(taskType model.TaskType, timeouts TaskTimeoutsConfig, opts EnqueueOptions) []asynq.Option {
	queue := PriorityToQueue(opts.Priority)
	asynqOpts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(TaskMaxRetry()),
		asynq.Timeout(TaskTimeout(taskType, timeouts)),
	}
	if opts.TaskID != "" {
		asynqOpts = append(asynqOpts, asynq.TaskID(opts.TaskID))
	}
	return asynqOpts
}
```

同时需要修改 `Enqueue` 方法。由于 `Client` 需要持有 `TaskTimeoutsConfig`，在 `Client` 中新增字段：

```go
type Client struct {
	inner      *asynq.Client
	inspector  *asynq.Inspector
	pingClient redis.UniversalClient
	timeouts   TaskTimeoutsConfig // 超时配置
}
```

修改 `NewClient` 签名以接收超时配置（或新增 `WithTimeouts` option）。最简方案是新增公开字段或在 `Enqueue` 中传入。

**设计决策**：为避免修改 `NewClient` 签名（影响面大），在 `Client` 上新增 `SetTimeouts` 方法：

```go
// SetTimeouts 设置任务超时配置。未调用时使用默认值。
func (c *Client) SetTimeouts(cfg TaskTimeoutsConfig) {
	c.timeouts = cfg
}
```

`Enqueue` 中使用 `c.timeouts`：

```go
func (c *Client) Enqueue(ctx context.Context, payload model.TaskPayload, opts EnqueueOptions) (string, error) {
	// ...
	asynqOpts := buildAsynqOptions(payload.TaskType, c.timeouts, opts)
	// ...
}
```

**Step 4: 运行测试确认通过**

运行: `go test ./internal/queue/ -v`
预期: 全部 PASS

**Step 5: 检查其他包对 TaskTimeout / buildAsynqOptions 的调用点是否需要适配**

运行: `grep -rn "TaskTimeout\|buildAsynqOptions" internal/ --include="*.go" | grep -v _test.go`
如有其他调用点，逐一适配。

**Step 6: 提交**

```bash
git add internal/queue/options.go internal/queue/options_test.go internal/queue/client.go
git commit -m "feat(queue): TaskTimeout 改为从配置读取，支持按任务类型自定义超时"
```

---

### Task 5: DockerClient 接口 — 新增 FollowLogs

**Files:**
- Modify: `internal/worker/docker.go:31-53` (DockerClient 接口)
- Modify: `internal/worker/docker_test.go:16-27` (mockDockerClient)
- Test: `internal/worker/docker_test.go`

**Step 1: 在 DockerClient 接口新增 FollowLogs 方法**

在 `internal/worker/docker.go` 的 `DockerClient` 接口中，`Close()` 之前新增：

```go
	// FollowLogs 以流式方式读取容器 stdout 日志。
	// 返回的 reader 持续输出数据直到容器退出或 ctx 取消。
	// 调用方负责 Close。
	FollowLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
```

**Step 2: 实现 dockerClient.FollowLogs**

在 `internal/worker/docker.go` 中 `GetContainerLogs` 方法之后新增：

```go
// FollowLogs 以 Follow 模式读取容器 stdout，用于流式心跳监控
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

**Step 3: 更新 mockDockerClient**

在 `internal/worker/docker_test.go` 的 `mockDockerClient` 中新增：

```go
type mockDockerClient struct {
	// ... 现有字段 ...
	followLogsFunc       func(ctx context.Context, containerID string) (io.ReadCloser, error)
}

func (m *mockDockerClient) FollowLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	if m.followLogsFunc != nil {
		return m.followLogsFunc(ctx, containerID)
	}
	return io.NopCloser(strings.NewReader("")), nil
}
```

需在 docker_test.go 的 import 中确保有 `"strings"`。

**Step 4: 运行测试确认编译通过**

运行: `go test ./internal/worker/ -v`
预期: 全部 PASS（含 `TestMockDockerClientImplementsInterface`）

**Step 5: 提交**

```bash
git add internal/worker/docker.go internal/worker/docker_test.go
git commit -m "feat(worker): DockerClient 接口新增 FollowLogs 方法"
```

---

### Task 6: stream-json 事件解析模块

**Files:**
- Create: `internal/worker/streamparse.go`
- Create: `internal/worker/streamparse_test.go`

**Step 1: 写失败测试**

创建 `internal/worker/streamparse_test.go`：

```go
package worker

import (
	"encoding/json"
	"testing"
)

func TestIsResultEvent(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{"result event", `{"type":"result","subtype":"success"}`, true},
		{"assistant event", `{"type":"assistant","message":{}}`, false},
		{"system event", `{"type":"system","subtype":"init"}`, false},
		{"empty line", "", false},
		{"invalid json", "not json", false},
		{"empty object", "{}", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResultEvent(tt.line); got != tt.want {
				t.Errorf("isResultEvent(%q) = %v, want %v", tt.line, got, tt.want)
			}
		})
	}
}

func TestParseResultEvent(t *testing.T) {
	line := `{"type":"result","subtype":"success","cost_usd":0.05,"duration_ms":12345,"is_error":false,"num_turns":8,"result":"review output","session_id":"sess-123"}`
	e, err := parseResultEvent(line)
	if err != nil {
		t.Fatalf("parseResultEvent 返回错误: %v", err)
	}
	if e.Type != "result" {
		t.Errorf("Type = %q, want result", e.Type)
	}
	if e.Subtype != "success" {
		t.Errorf("Subtype = %q, want success", e.Subtype)
	}
	if e.CostUSD != 0.05 {
		t.Errorf("CostUSD = %f, want 0.05", e.CostUSD)
	}
	if e.NumTurns != 8 {
		t.Errorf("NumTurns = %d, want 8", e.NumTurns)
	}
	if e.Result != "review output" {
		t.Errorf("Result = %q, want 'review output'", e.Result)
	}
}

func TestParseResultEvent_NotResult(t *testing.T) {
	_, err := parseResultEvent(`{"type":"assistant"}`)
	if err == nil {
		t.Error("非 result 事件应返回错误")
	}
}

func TestParseResultEvent_InvalidJSON(t *testing.T) {
	_, err := parseResultEvent("not json")
	if err == nil {
		t.Error("无效 JSON 应返回错误")
	}
}

func TestResultEventToCLIJSON(t *testing.T) {
	event := &resultEvent{
		Type:       "result",
		Subtype:    "success",
		CostUSD:    0.05,
		DurationMs: 12345,
		IsError:    false,
		NumTurns:   8,
		Result:     `{"summary":"good","verdict":"approve","issues":[]}`,
		SessionID:  "sess-123",
	}
	out, err := resultEventToCLIJSON(event)
	if err != nil {
		t.Fatalf("resultEventToCLIJSON 返回错误: %v", err)
	}

	// 验证输出是合法 JSON
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}

	// 验证关键字段
	if parsed["type"] != "success" {
		t.Errorf("type = %v, want success", parsed["type"])
	}
	if parsed["is_error"] != false {
		t.Errorf("is_error = %v, want false", parsed["is_error"])
	}
	if parsed["result"] != event.Result {
		t.Errorf("result 内容不匹配")
	}
}

func TestInjectStreamJsonFlags_NoExisting(t *testing.T) {
	cmd := []string{"claude", "-p", "-", "--disallowedTools", "Edit,Write"}
	got := injectStreamJsonFlags(cmd)

	// 应包含 stream-json 标志
	assertContains(t, got, "--output-format")
	assertContains(t, got, "stream-json")
	assertContains(t, got, "--verbose")
	assertContains(t, got, "--include-partial-messages")
	// 应保留原有参数
	assertContains(t, got, "--disallowedTools")
}

func TestInjectStreamJsonFlags_ReplaceExisting(t *testing.T) {
	cmd := []string{"claude", "-p", "-", "--output-format", "json", "--disallowedTools", "Edit"}
	got := injectStreamJsonFlags(cmd)

	// 不应包含 "json"（已被替换为 "stream-json"）
	for _, arg := range got {
		if arg == "json" {
			t.Error("旧的 --output-format json 应被替换")
		}
	}
	assertContains(t, got, "stream-json")
	assertContains(t, got, "--verbose")
	assertContains(t, got, "--include-partial-messages")
}

func TestInjectStreamJsonFlags_EmptyCmd(t *testing.T) {
	got := injectStreamJsonFlags(nil)
	assertContains(t, got, "stream-json")
}

func assertContains(t *testing.T, slice []string, item string) {
	t.Helper()
	for _, s := range slice {
		if s == item {
			return
		}
	}
	t.Errorf("切片 %v 中未找到 %q", slice, item)
}
```

**Step 2: 运行测试确认失败**

运行: `go test ./internal/worker/ -run "TestIsResultEvent|TestParseResultEvent|TestResultEventToCLIJSON|TestInjectStreamJsonFlags" -v`
预期: 编译失败 — 函数不存在

**Step 3: 实现 streamparse.go**

创建 `internal/worker/streamparse.go`：

```go
package worker

import (
	"encoding/json"
	"fmt"
)

// streamEvent 流式事件的类型标识（仅用于快速筛选）
type streamEvent struct {
	Type string `json:"type"`
}

// resultEvent stream-json 的 result 事件完整结构
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

// isResultEvent 快速判断一行是否为 result 事件（仅解析 type 字段）
func isResultEvent(line string) bool {
	if len(line) == 0 {
		return false
	}
	var e streamEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return false
	}
	return e.Type == "result"
}

// parseResultEvent 完整解析 result 事件
func parseResultEvent(line string) (*resultEvent, error) {
	var e resultEvent
	if err := json.Unmarshal([]byte(line), &e); err != nil {
		return nil, fmt.Errorf("解析 result 事件失败: %w", err)
	}
	if e.Type != "result" {
		return nil, fmt.Errorf("非 result 事件: type=%s", e.Type)
	}
	return &e, nil
}

// resultEventToCLIJSON 将 stream-json result 事件转换为与 --output-format json 兼容的 JSON 字符串。
// 上层 review.Service.parseResult() 期望的是 CLI JSON 信封格式，此函数做格式对齐。
func resultEventToCLIJSON(event *resultEvent) (string, error) {
	envelope := map[string]any{
		"type":        event.Subtype,
		"cost_usd":    event.CostUSD,
		"duration_ms": event.DurationMs,
		"is_error":    event.IsError,
		"num_turns":   event.NumTurns,
		"result":      event.Result,
		"session_id":  event.SessionID,
	}
	data, err := json.Marshal(envelope)
	if err != nil {
		return "", fmt.Errorf("序列化 CLI JSON 信封失败: %w", err)
	}
	return string(data), nil
}

// injectStreamJsonFlags 将命令中的 --output-format 替换为 stream-json 模式。
// 无 --output-format 参数时直接追加。
func injectStreamJsonFlags(cmd []string) []string {
	// 复制原命令，移除已有的 --output-format 及其值
	result := make([]string, 0, len(cmd)+4)
	skip := false
	for _, arg := range cmd {
		if skip {
			skip = false
			continue // 跳过 --output-format 的值
		}
		if arg == "--output-format" {
			skip = true
			continue // 跳过 --output-format 本身
		}
		result = append(result, arg)
	}
	// 追加 stream-json 标志
	result = append(result, "--output-format", "stream-json", "--verbose", "--include-partial-messages")
	return result
}
```

**Step 4: 运行测试确认通过**

运行: `go test ./internal/worker/ -run "TestIsResultEvent|TestParseResultEvent|TestResultEventToCLIJSON|TestInjectStreamJsonFlags" -v`
预期: 全部 PASS

**Step 5: 提交**

```bash
git add internal/worker/streamparse.go internal/worker/streamparse_test.go
git commit -m "feat(worker): 新增 stream-json 事件解析和命令注入模块"
```

---

### Task 7: Pool 层 — streamMonitorLoop 实现

**Files:**
- Modify: `internal/worker/pool.go`
- Test: `internal/worker/pool_test.go`

**Step 1: 写失败测试 — 流式监控正常完成**

在 `internal/worker/pool_test.go` 中新增：

```go
func TestPool_RunWithStreamMonitor_Success(t *testing.T) {
	// 模拟 stream-json 事件流
	streamEvents := strings.Join([]string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"assistant","message":{}}`,
		`{"type":"result","subtype":"success","cost_usd":0.05,"duration_ms":1000,"is_error":false,"num_turns":3,"result":"{\"summary\":\"good\",\"verdict\":\"approve\",\"issues\":[]}","session_id":"sess-1"}`,
	}, "\n") + "\n"

	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(streamEvents)), nil
		},
	}

	cfg := defaultPoolConfig()
	cfg.StreamMonitor = StreamMonitorConfig{
		Enabled:         true,
		ActivityTimeout: 5 * time.Second,
	}

	pool := mustNewPool(t, cfg, mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("ExitCode = %d, want 0", result.ExitCode)
	}
	// Output 应为转换后的 CLI JSON 信封
	if !strings.Contains(result.Output, "approve") {
		t.Errorf("Output 应包含评审结果，实际: %q", result.Output)
	}
}

func TestPool_RunWithStreamMonitor_Disabled(t *testing.T) {
	// 开关关闭时走旧路径
	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "old path output"}, nil
		},
	}

	cfg := defaultPoolConfig()
	cfg.StreamMonitor = StreamMonitorConfig{Enabled: false}

	pool := mustNewPool(t, cfg, mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}
	if result.Output != "old path output" {
		t.Errorf("开关关闭时应走旧路径, Output = %q", result.Output)
	}
}

func TestPool_RunWithStreamMonitor_ActivityTimeout(t *testing.T) {
	// 模拟流式输出停止（只输出一行然后静默）
	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			// 阻塞直到 ctx 被取消（模拟卡住的容器）
			<-ctx.Done()
			return -1, ctx.Err()
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			// 返回一行后就不再输出
			return io.NopCloser(strings.NewReader(`{"type":"system","subtype":"init"}` + "\n")), nil
		},
	}

	cfg := defaultPoolConfig()
	cfg.StreamMonitor = StreamMonitorConfig{
		Enabled:         true,
		ActivityTimeout: 100 * time.Millisecond, // 短阈值加速测试
	}

	pool := mustNewPool(t, cfg, mock)
	_, err := pool.Run(context.Background(), defaultPayload())
	if err == nil {
		t.Fatal("活跃度超时应返回错误")
	}
}
```

**Step 2: 运行测试确认失败**

运行: `go test ./internal/worker/ -run "TestPool_RunWithStreamMonitor" -v`
预期: 编译可能通过但测试 FAIL（pool.go 中还没有监控分支逻辑）

**Step 3: 实现 — 修改 runContainer 和新增 streamMonitorLoop**

修改 `internal/worker/pool.go`，核心改动在 `runContainer` 方法中。

在 `runContainer` 的 `StartContainer` 成功之后、`waitCtx` 创建之前，插入命令注入逻辑。然后重构等待逻辑为两条路径。

关键实现点：

1. 在 `runContainer` 开头，根据 `p.config.StreamMonitor.Enabled` 决定是否注入 stream-json 标志（注意：注入发生在构建 `containerCfg` 之前，需将 `cmd` 变量提前修改）

2. `WaitContainer` 之后的结果获取逻辑分两条路径

3. 新增 `streamMonitorLoop` 方法

具体代码较长，以下是关键骨架（完整代码在实施时编写）：

```go
// runContainer 中的命令注入（在构建 containerCfg 之前）
if p.config.StreamMonitor.Enabled {
    cmd = injectStreamJsonFlags(cmd)
}

// WaitContainer 前后的分支逻辑
if p.config.StreamMonitor.Enabled {
    monitorCtx, monitorCancel := context.WithCancel(ctx)
    defer monitorCancel()

    resultCh := make(chan string, 1)

    go p.streamMonitorLoop(monitorCtx, containerID, resultCh)

    exitCode, waitErr = p.docker.WaitContainer(monitorCtx, containerID)
    monitorCancel()

    // 尝试从流中获取 result
    select {
    case streamResult := <-resultCh:
        output = streamResult
    default:
        // fallback: 从日志获取
        logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
        defer logCancel()
        logs, _ := p.docker.GetContainerLogs(logCtx, containerID)
        output = logs.Stdout
    }
} else {
    // 现有逻辑不变
    waitCtx, waitCancel := context.WithTimeout(ctx, 30*time.Minute)
    defer waitCancel()
    exitCode, waitErr = p.docker.WaitContainer(waitCtx, containerID)
    logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer logCancel()
    logs, _ := p.docker.GetContainerLogs(logCtx, containerID)
    output = logs.Stdout
}
```

```go
// streamMonitorLoop 流式心跳监控，在独立 goroutine 中运行
func (p *Pool) streamMonitorLoop(ctx context.Context, containerID string, resultCh chan<- string) {
    reader, err := p.docker.FollowLogs(ctx, containerID)
    if err != nil {
        p.logger.ErrorContext(ctx, "启动流式监控失败", slog.String("error", err.Error()))
        return
    }
    defer reader.Close()

    // stdcopy 解复用
    pr, pw := io.Pipe()
    go func() {
        defer pw.Close()
        _, _ = stdcopy.StdCopy(pw, io.Discard, reader)
    }()

    scanner := bufio.NewScanner(pr)
    timer := time.NewTimer(p.config.StreamMonitor.ActivityTimeout)
    defer timer.Stop()

    for {
        select {
        case <-timer.C:
            p.logger.WarnContext(ctx, "容器活跃度超时，判定卡住",
                slog.String("container_id", containerID),
                slog.Duration("threshold", p.config.StreamMonitor.ActivityTimeout))
            return // goroutine 退出，reader.Close() 会触发 ctx cancel 的间接效果
            // 注意：需要通过 cancel monitorCtx 来中断 WaitContainer
        case <-ctx.Done():
            return
        default:
            if !scanner.Scan() {
                return // 流结束（容器退出）
            }
            timer.Reset(p.config.StreamMonitor.ActivityTimeout)
            line := scanner.Text()
            if isResultEvent(line) {
                event, err := parseResultEvent(line)
                if err == nil {
                    cliJSON, err := resultEventToCLIJSON(event)
                    if err == nil {
                        select {
                        case resultCh <- cliJSON:
                        default: // 已有 result，丢弃后续
                        }
                    }
                }
            }
        }
    }
}
```

注意：上面 select + scanner.Scan() 的组合有阻塞问题。实际实现时需要用 goroutine 读取 scanner 并通过 channel 传递行数据，或使用其他非阻塞读取模式。在实施时需仔细处理这个并发细节。

**Step 4: 运行测试确认通过**

运行: `go test ./internal/worker/ -run "TestPool_RunWithStreamMonitor" -v -timeout 30s`
预期: 全部 PASS

**Step 5: 运行全量测试确认无回归**

运行: `go test ./internal/worker/ -v -timeout 60s`
预期: 全部 PASS

**Step 6: 提交**

```bash
git add internal/worker/pool.go internal/worker/pool_test.go
git commit -m "feat(worker): 实现 stream-json 流式心跳监控和活跃度检测"
```

---

### Task 8: 配置模板更新

**Files:**
- Modify: `configs/dtworkflow.example.yaml:48-55`

**Step 1: 在 worker 段新增 timeouts 和 stream_monitor 配置示例**

在 `configs/dtworkflow.example.yaml` 的 worker 段（约第 55 行后）新增：

```yaml
worker:
  concurrency: 3
  timeout: "30m"
  image: "dtworkflow-worker:1.0"
  cpu_limit: "2.0"
  memory_limit: "4g"
  network_name: "dtworkflow-net"
  # 按任务类型的硬超时（覆盖 timeout 默认值）
  timeouts:
    review_pr: "15m"
    fix_issue: "45m"
    gen_tests: "30m"
  # 流式心跳监控（stream-json 活跃度检测）
  # 启用后容器内 claude -p 改用 stream-json 输出格式，
  # 通过监控 stdout 事件流检测容器是否卡住。
  stream_monitor:
    enabled: false
    activity_timeout: "2m"
```

**Step 2: 验证配置模板可被正确加载**

运行: `go test ./internal/config/ -run TestExampleConfig -v`（如有此测试）
或手动验证：确保新增字段不破坏现有配置加载。

**Step 3: 提交**

```bash
git add configs/dtworkflow.example.yaml
git commit -m "docs(config): 配置模板新增 worker.timeouts 和 stream_monitor 示例"
```

---

### Task 9: 集成验证 — result 提取 fallback

**Files:**
- Test: `internal/worker/pool_test.go`

**Step 1: 写测试 — 流中无 result 事件时从日志兜底**

```go
func TestPool_RunWithStreamMonitor_FallbackToLogs(t *testing.T) {
	// 流中没有 result 事件（只有 system/assistant）
	streamEvents := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"assistant","message":{}}` + "\n"

	mock := &mockDockerClient{
		waitContainerFunc: func(ctx context.Context, containerID string) (int64, error) {
			return 0, nil
		},
		followLogsFunc: func(ctx context.Context, containerID string) (io.ReadCloser, error) {
			return io.NopCloser(strings.NewReader(streamEvents)), nil
		},
		getContainerLogsFunc: func(ctx context.Context, containerID string) (ContainerLogs, error) {
			return ContainerLogs{Stdout: "fallback log output"}, nil
		},
	}

	cfg := defaultPoolConfig()
	cfg.StreamMonitor = StreamMonitorConfig{
		Enabled:         true,
		ActivityTimeout: 5 * time.Second,
	}

	pool := mustNewPool(t, cfg, mock)
	result, err := pool.Run(context.Background(), defaultPayload())
	if err != nil {
		t.Fatalf("Run 返回错误: %v", err)
	}
	if result.Output != "fallback log output" {
		t.Errorf("无 result 事件时应 fallback 到日志, Output = %q", result.Output)
	}
}
```

**Step 2: 运行测试确认通过**

运行: `go test ./internal/worker/ -run TestPool_RunWithStreamMonitor_FallbackToLogs -v`
预期: PASS（如果 Task 7 实现正确）

**Step 3: 运行全项目测试**

运行: `go test ./... -timeout 120s`
预期: 全部 PASS

**Step 4: 提交**

```bash
git add internal/worker/pool_test.go
git commit -m "test(worker): 新增 stream monitor fallback 场景测试"
```

---

### Task 10: 最终验证与清理

**Step 1: 运行 lint 检查**

运行: `golangci-lint run ./...`
修复所有 lint 错误。

**Step 2: 运行全量测试**

运行: `go test ./... -race -timeout 120s`
预期: 全部 PASS，无竞态条件

**Step 3: 确认编译**

运行: `GOOS=linux GOARCH=amd64 go build ./...`
预期: 交叉编译成功

**Step 4: 提交（如有 lint 修复）**

```bash
git add -A
git commit -m "chore: lint 修复和最终清理"
```
