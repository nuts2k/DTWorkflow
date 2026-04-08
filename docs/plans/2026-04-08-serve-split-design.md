# serve.go 文件拆分设计

## 背景

`internal/cmd/serve.go` 当前 837 行，职责过多（Cobra 命令、通知构造、配置适配、依赖装配、服务生命周期），
参见 `docs/TECH_DEBT.md` TD-001。本次拆分目标：**按职责拆为多文件，降低认知负荷，不改变任何运行时行为**。

## 现状分析

### serve.go 职责区域

| 区域 | 行号 | 行数 |
|------|------|------|
| CLI flags + `serveConfig` + Cobra 命令 | 32-97 | ~65 |
| `ServiceDeps` / `readinessSnapshot` / 辅助函数 | 99-160 | ~60 |
| 通知构造（`configDrivenNotifier` + `buildNotifyRules` + `buildNotifier`） | 162-381 | ~220 |
| 配置适配器（`configAdapter`） | 288-330 | ~45 |
| 依赖装配（`BuildServiceDeps` + `buildServeConfigFromManager`） | 383-541 | ~160 |
| 服务生命周期（`runServe` + `runServeWithConfig` + 优雅关闭） | 543-837 | ~295 |

### 已有拆分

- `adapter.go` — `giteaCommentAdapter`
- `worker_queue_config.go` — 超时/流监控配置构建函数
- `daily_report_task.go` — 每日报告 asynq 任务定义

## 拆分方案

### 目标文件映射

| 目标文件 | 内容 | 预估行数 |
|----------|------|----------|
| `serve.go` | Cobra 命令定义、`serveConfig`、包级变量、`init()`、`runServe`、`runServeWithConfig`、`gracefulShutdown`、`doGracefulShutdown`、`getEnvDefault` | ~360 |
| `serve_notify.go`（新建） | `buildNotifyRules`、`configDrivenNotifier`（类型 + 5 方法）、`buildNotifier` | ~220 |
| `serve_deps.go`（新建） | `ServiceDeps`、`readinessSnapshot`、`BuildServiceDeps`、`buildServeConfigFromManager`、`computeReadyStatus`、`buildWorkerPoolConfigFromServeConfig` | ~190 |
| `adapter.go`（追加） | 现有 `giteaCommentAdapter` + 移入 `configAdapter`（类型 + 4 方法 + 编译时断言） | ~85 |

### 测试文件映射

| 测试文件 | 内容 |
|----------|------|
| `serve_test.go` | 集成测试（`TestServe_Healthz`、`TestServe_Readyz`、`TestServe_PortConflict`、`TestServe_RedisUnavailable`、`TestServe_RequiresWebhookSecret`、`TestServe_WebhookRouteReturnsUnauthorizedWithoutSignature`）+ 辅助函数（`newTestConfig`、`getFreePort`、`skipIfNoRedis`、`skipIfNoDocker`、`buildTestConfigManager`、`writeTestConfigFile`）+ `TestGetEnvDefault_*`、`TestRunServe_*` |
| `serve_notify_test.go`（新建） | `TestBuildNotifyRules_*`、`TestBuildNotifier_*`、`TestConfigDrivenNotifier_*` + `noopNotifier` 辅助类型 |
| `serve_deps_test.go`（新建） | `TestBuildServiceDeps_*`、`TestComputeReadyStatus_*`、`TestBuildServeConfigFromManager_*` |
| `adapter_test.go`（新建） | `TestConfigAdapter_*` |

### 不变的文件

- `worker_queue_config.go` / `worker_queue_config_test.go`
- `daily_report_task.go` / `daily_report_task_test.go`
- `serve_env_only_test.go`

## 影响评估

| 维度 | 影响 |
|------|------|
| 编译 | 零 — 所有文件仍在 `package cmd` |
| 测试 | 零 — 同包内函数可见性不变 |
| 外部引用 | 零 — 导出符号 `BuildServiceDeps` / `ServiceDeps` 不变 |
| 运行时行为 | 零 — 纯文件重组织，无逻辑修改 |
| Git 合并风险 | 低 — 当前 main 无并行修改 serve.go 的分支 |

## 约束

1. **纯移动，不改逻辑** — 不修改任何函数签名、变量名、import 路径
2. **同包同 package** — 所有新文件 `package cmd`
3. **测试辅助函数不重复** — 共享的 helper 留在 `serve_test.go`，其他测试文件按需引用
4. **拆分后跑 `go build ./...` 和 `go test ./internal/cmd/...` 全绿**
