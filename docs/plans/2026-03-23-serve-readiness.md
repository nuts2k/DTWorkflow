# serve readiness 语义增强 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 让 `dtworkflow serve` 的 `/readyz` 更诚实地表达实例是否具备执行任务的关键前提，在 Gitea 配置缺失、notifier 未启用或 Worker 镜像缺失时返回 `503` 并给出明确字段，而 `/healthz` 继续只表示进程存活。

**Architecture:** 在 `internal/cmd/serve.go` 中扩展 `ServiceDeps`，保存 readiness 所需的静态事实（如 `GiteaConfigured`、`NotifierEnabled`、`WorkerImagePresent`），并将 `/readyz` 从“只看 Redis/SQLite”升级为“汇总基础依赖与执行前提”的响应。优先抽出小型 helper 计算 readiness 状态，确保逻辑可稳定单测；受本机 Redis/Docker 环境影响的测试继续使用 skip 策略。

**Tech Stack:** Go, Cobra, Gin, asynq, Docker SDK, 现有 `internal/cmd` / `internal/worker` 抽象

---

### Task 1: 为 readiness 逻辑抽出可测试的状态计算 helper

**Files:**
- Modify: `internal/cmd/serve.go`
- Modify: `internal/cmd/serve_test.go`

**Step 1: 写失败测试**

在 `internal/cmd/serve_test.go` 中先新增一个纯逻辑测试，避免一上来依赖 Redis/Docker 环境。例如：

```go
func TestComputeReadyStatus_DegradedWhenGiteaMissing(t *testing.T) {
    payload, httpStatus := computeReadyStatus(readinessSnapshot{
        RedisOK:            true,
        SQLiteOK:           true,
        GiteaConfigured:    false,
        NotifierEnabled:    false,
        WorkerImagePresent: true,
        ActiveWorkers:      0,
    })

    if httpStatus != http.StatusServiceUnavailable { ... }
    if payload["status"] != "degraded" { ... }
    if payload["gitea_configured"] != false { ... }
}
```

再补一个 healthy 场景：

```go
func TestComputeReadyStatus_OkWhenAllCriticalDepsPresent(t *testing.T)
```

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestComputeReadyStatus_' -v`
Expected: FAIL，因为 `computeReadyStatus` 和 `readinessSnapshot` 尚不存在。

**Step 3: 写最小实现**

在 `internal/cmd/serve.go` 中新增：

```go
type readinessSnapshot struct {
    RedisOK            bool
    SQLiteOK           bool
    GiteaConfigured    bool
    NotifierEnabled    bool
    WorkerImagePresent bool
    ActiveWorkers      int
}

func computeReadyStatus(s readinessSnapshot) (map[string]any, int) {
    status := "ok"
    httpStatus := http.StatusOK
    if !s.RedisOK || !s.SQLiteOK || !s.GiteaConfigured || !s.NotifierEnabled || !s.WorkerImagePresent {
        status = "degraded"
        httpStatus = http.StatusServiceUnavailable
    }
    return map[string]any{
        "status":               status,
        "version":              version,
        "redis":                s.RedisOK,
        "sqlite":               s.SQLiteOK,
        "gitea_configured":     s.GiteaConfigured,
        "notifier_enabled":     s.NotifierEnabled,
        "worker_image_present": s.WorkerImagePresent,
        "active_workers":       s.ActiveWorkers,
    }, httpStatus
}
```

仅实现最小规则，不提前抽象更多层级。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestComputeReadyStatus_' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "feat: add readiness status helper"
```

---

### Task 2: 扩展 ServiceDeps，保存 readiness 所需事实

**Files:**
- Modify: `internal/cmd/serve.go`
- Modify: `internal/cmd/serve_test.go`
- Reference: `internal/worker/docker.go:28-47`

**Step 1: 写失败测试**

新增 helper 级测试，验证 `ServiceDeps` 中新增字段的默认行为。例如：

```go
func TestBuildServiceDeps_WithoutGiteaConfig_SetsReadinessFacts(t *testing.T)
```

断言：
- `deps.GiteaConfigured == false`
- `deps.NotifierEnabled == false`

再补：

```go
func TestBuildServiceDeps_WithGiteaConfig_SetsNotifierEnabled(t *testing.T)
```

若 Redis/Docker 不可用，沿用 `skipIfNoRedis`，并在需要时再进一步抽 helper 以降低环境依赖。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestBuildServiceDeps_.*Readiness|TestBuildServiceDeps_WithGiteaConfig_SetsNotifierEnabled' -v`
Expected: FAIL，因为 `ServiceDeps` 还没有这些 readiness 字段。

**Step 3: 写最小实现**

在 `internal/cmd/serve.go` 的 `ServiceDeps` 增加：

```go
GiteaConfigured    bool
NotifierEnabled    bool
WorkerImagePresent bool
```

在 `BuildServiceDeps` 中：

1. 计算 `giteaConfigured := cfg.GiteaURL != "" && cfg.GiteaToken != ""`
2. `notifier != nil` 时设置 `NotifierEnabled = true`
3. 在 Docker client 已创建后，显式调用：

```go
workerImagePresent, err := dockerClient.ImageExists(context.Background(), cfg.WorkerImage)
```

注意这里的策略：
- 查询镜像存在性本身失败 -> 启动失败（因为这是本地关键依赖探测异常）
- 查询成功但镜像不存在 -> 服务可启动，但 `WorkerImagePresent = false`

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestBuildServiceDeps_.*Readiness|TestBuildServiceDeps_WithGiteaConfig_SetsNotifierEnabled' -v`
Expected: PASS 或按 skip 规则稳定跳过依赖环境的测试。

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "feat: expose readiness facts from service deps"
```

---

### Task 3: 将 /readyz 接到新的 readiness 计算逻辑

**Files:**
- Modify: `internal/cmd/serve.go`
- Modify: `internal/cmd/serve_test.go`

**Step 1: 写失败测试**

在 `internal/cmd/serve_test.go` 中新增或改造 `/readyz` 响应测试，至少覆盖以下逻辑：

1. `TestComputeReadyStatus_DegradedWhenNotifierDisabled`
2. `TestComputeReadyStatus_DegradedWhenWorkerImageMissing`
3. `TestComputeReadyStatus_DegradedWhenRedisDown`

若服务级测试依赖环境较强，这一轮优先验证 helper 输出；服务级 `/readyz` 测试只保留最关键一条。

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestComputeReadyStatus_DegradedWhenNotifierDisabled|TestComputeReadyStatus_DegradedWhenWorkerImageMissing|TestServe_Readyz' -v`
Expected: FAIL

**Step 3: 写最小实现**

把 `/readyz` 处理器替换为：

```go
router.GET("/readyz", func(c *gin.Context) {
    ctx := c.Request.Context()
    snapshot := readinessSnapshot{
        RedisOK:            deps.QueueClient.Ping(ctx) == nil,
        SQLiteOK:           deps.Store.Ping(ctx) == nil,
        GiteaConfigured:    deps.GiteaConfigured,
        NotifierEnabled:    deps.NotifierEnabled,
        WorkerImagePresent: deps.WorkerImagePresent,
        ActiveWorkers:      deps.Pool.Stats().Active,
    }
    payload, httpStatus := computeReadyStatus(snapshot)
    c.JSON(httpStatus, payload)
})
```

保留 `/healthz` 逻辑不变。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestComputeReadyStatus_DegradedWhenNotifierDisabled|TestComputeReadyStatus_DegradedWhenWorkerImageMissing|TestServe_Readyz' -v`
Expected: PASS（或按环境依赖稳定 skip 服务级测试）

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "feat: make readiness reflect execution prerequisites"
```

---

### Task 4: 扩展 /readyz 返回体断言，确保字段完整

**Files:**
- Modify: `internal/cmd/serve_test.go`

**Step 1: 写失败测试**

增强现有 `TestServe_Readyz` 或拆分为更细测试，断言返回体包含新增字段：

- `gitea_configured`
- `notifier_enabled`
- `worker_image_present`
- `active_workers`

以及：
- 缺少任一关键项时 `status == degraded`
- 所有关键项满足时 `status == ok`

**Step 2: 运行测试并确认失败**

Run: `go test ./internal/cmd -run 'TestServe_Readyz|TestComputeReadyStatus_' -v`
Expected: FAIL

**Step 3: 写最小实现**

根据测试补齐返回字段，必要时统一使用 `computeReadyStatus(...)` 生成 payload，避免在 handler 里散落多个字段拼接逻辑。

**Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd -run 'TestServe_Readyz|TestComputeReadyStatus_' -v`
Expected: PASS

**Step 5: 提交**

```bash
git add internal/cmd/serve_test.go internal/cmd/serve.go
git commit -m "test: cover readiness response details"
```

---

### Task 5: 做聚焦回归与全量验证

**Files:**
- Modify (if needed): `internal/cmd/serve.go`
- Modify (if needed): `internal/cmd/serve_test.go`

**Step 1: 运行 cmd 聚焦测试**

Run:

```bash
go test ./internal/cmd -v
```

Expected:
- 所有 `internal/cmd` 测试 PASS
- 依赖本机 Redis 的测试在 Redis 不可用时显式 SKIP，而不是 FAIL

**Step 2: 运行关键回归包**

Run:

```bash
go test ./internal/cmd ./internal/queue ./internal/worker ./internal/store -v
```

Expected: PASS / 明确 SKIP

**Step 3: 运行全量测试**

Run:

```bash
go test ./...
```

Expected: 仓库所有 Go 包 PASS

**Step 4: 需求核对**

逐项检查：
- `/healthz` 仍然始终 200
- `/readyz` 不再只依赖 Redis / SQLite
- 缺 Gitea 配置 -> `503`
- 缺 Worker 镜像 -> `503`
- 返回体包含关键字段并能指出缺失项

**Step 5: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go docs/plans/2026-03-23-serve-readiness-design.md docs/plans/2026-03-23-serve-readiness.md
git commit -m "feat: strengthen serve readiness semantics"
```
