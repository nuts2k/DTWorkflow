# serve.go 拆分实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 将 `internal/cmd/serve.go`（837 行）按职责拆分为 4 个源文件 + 对应测试文件，不改变任何运行时行为。

**Architecture:** 同包（`package cmd`）多文件拆分。按"通知构造"、"依赖装配"、"配置适配"三个职责轴提取代码，原 `serve.go` 保留 Cobra 命令定义与服务生命周期。

**Tech Stack:** Go, Cobra, Gin, asynq

---

## 前置说明

- **这是纯机械移动**，不修改任何函数签名、逻辑或变量名
- 每个 Task 完成后必须验证 `go build ./...` 和 `go vet ./...`
- 测试辅助函数（`newTestConfig`、`getFreePort`、`skipIfNoRedis`、`skipIfNoDocker`、`buildTestConfigManager`）定义在 `serve_test.go` 和 `root_config_test.go` 中，**同包可见，不需要移动**
- `writeTestConfigFile` 定义在 `root_config_test.go:11`，同包可见
- `resetRootFlagsForTest` 定义在 `root_config_test.go:26`，同包可见

---

### Task 1: 创建 serve_notify.go + serve_notify_test.go

**Files:**
- Create: `internal/cmd/serve_notify.go`
- Create: `internal/cmd/serve_notify_test.go`
- Modify: `internal/cmd/serve.go` — 删除已移出的代码
- Modify: `internal/cmd/serve_test.go` — 删除已移出的测试

**Step 1: 创建 `serve_notify.go`**

从 `serve.go` 移出以下内容（按当前行号）：

```
行 162-192:  buildNotifyRules 函数（含注释）
行 194-286:  configDrivenNotifier 类型 + Send/getRouter/hasRepoNotifyOverride/newRouter 方法
行 332-381:  buildNotifier 函数
```

文件结构：

```go
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
)

// buildNotifyRules ... (行 162-192 原样移入)
// configDrivenNotifier ... (行 194-286 原样移入)
// buildNotifier ... (行 332-381 原样移入)
```

**Step 2: 从 `serve.go` 删除已移出的代码**

删除 `serve.go` 行 162-286 和行 332-381。同时从 import 中移除不再需要的包：`"sync"`。

**Step 3: 创建 `serve_notify_test.go`**

从 `serve_test.go` 移出以下内容：

```
行 103-113:  noopNotifier 类型
行 115-144:  TestBuildNotifyRules_MapsGlobalRoutes
行 146-171:  TestBuildNotifyRules_EventsStarMapsToNotifyEventTypeStar
行 173-204:  TestBuildNotifyRules_RepoOverridePreferred
行 206-232:  TestBuildNotifier_NilConfigOrNilClient
行 234-256:  TestBuildNotifier_WithClientAndConfig
行 258-285:  TestBuildNotifier_FeishuOnlyConfig
行 287-321:  TestConfigDrivenNotifier_ReusesGlobalRouterWithoutRepoOverride
行 323-353:  TestConfigDrivenNotifier_FeishuOnlyRouter
行 355-393:  TestConfigDrivenNotifier_CachesOnlyRepoOverrides
```

文件结构：

```go
package cmd

import (
	"context"
	"log/slog"
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/notify"
)

// noopNotifier ... (行 103-113 原样移入)
// TestBuildNotifyRules_* ... (行 115-204 原样移入)
// TestBuildNotifier_* ... (行 206-285 原样移入)
// TestConfigDrivenNotifier_* ... (行 287-393 原样移入)
```

**Step 4: 从 `serve_test.go` 删除已移出的测试**

删除 `serve_test.go` 行 103-393。同时从 import 中移除不再需要的包（如果有的话）。注意：`"log/slog"` 可能还被其他测试用到，需检查。

**Step 5: 验证**

```bash
go build ./...
go vet ./...
go test ./internal/cmd/... -count=1 -v 2>&1 | tail -30
```

Expected: 全部 PASS，无编译错误。

**Step 6: 提交**

```bash
git add internal/cmd/serve_notify.go internal/cmd/serve_notify_test.go internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "refactor: 拆出 serve_notify.go — 通知构造逻辑

将 buildNotifyRules、configDrivenNotifier、buildNotifier 从 serve.go 移至
serve_notify.go，对应测试移至 serve_notify_test.go。

TD-001 拆分 1/3。"
```

---

### Task 2: 创建 serve_deps.go + serve_deps_test.go

**Files:**
- Create: `internal/cmd/serve_deps.go`
- Create: `internal/cmd/serve_deps_test.go`
- Modify: `internal/cmd/serve.go` — 删除已移出的代码
- Modify: `internal/cmd/serve_test.go` — 删除已移出的测试

**Step 1: 创建 `serve_deps.go`**

从 `serve.go` 移出以下内容（注意：行号已因 Task 1 删除而前移，以**原始行号**标注）：

```
行 99-122:   ServiceDeps 结构体 + readinessSnapshot 结构体
行 124-141:  buildWorkerPoolConfigFromServeConfig 函数
行 143-160:  computeReadyStatus 函数
行 383-512:  BuildServiceDeps 函数
行 514-541:  buildServeConfigFromManager 函数
```

文件结构：

```go
package cmd

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/hibiken/asynq"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/store"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)

// ServiceDeps ... (行 99-122 原样移入)
// buildWorkerPoolConfigFromServeConfig ... (行 124-141 原样移入)
// computeReadyStatus ... (行 143-160 原样移入)
// BuildServiceDeps ... (行 383-512 原样移入)
// buildServeConfigFromManager ... (行 514-541 原样移入)
```

**Step 2: 从 `serve.go` 删除已移出的代码**

删除对应行。从 import 中移除不再需要的包：`"crypto/tls"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/store"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/worker"`。

注意保留 `"otws19.zicp.vip/kelin/dtworkflow/internal/notify"` — 已在 Task 1 中移除，确认不重复。

**Step 3: 创建 `serve_deps_test.go`**

从 `serve_test.go` 移出以下内容：

```
行 395-410:  TestComputeReadyStatus_DegradedWhenWorkerImageMissing
行 412-436:  TestBuildServiceDeps_WithoutGiteaConfig_ReturnsError
行 438-467:  TestBuildServiceDeps_WithGiteaConfig_BuildsNotifier
行 469-543:  TestBuildServeConfigFromManager_ReadsAllRequiredFields
行 545-581:  TestRunServe_UsesConfigManagerSnapshot
行 583-647:  TestBuildServiceDeps_UsesServeConfigResourceLimitsAndNetwork
行 649-670:  TestComputeReadyStatus_DegradedWhenGiteaMissing
行 672-693:  TestComputeReadyStatus_OkWhenAllCriticalDepsPresent
```

文件结构：

```go
package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/worker"
)
```

注意：这些测试使用了 `newTestConfig`（定义在 `serve_test.go`）、`skipIfNoRedis` / `skipIfNoDocker`（定义在 `serve_test.go`）、`buildWorkerPoolConfigFromServeConfig`（定义在 serve_deps.go）。同包可见，无需额外处理。

验证 import：`worker` 仅被 `TestBuildServiceDeps_UsesServeConfigResourceLimitsAndNetwork` 中的 `worker.PoolConfig` 间接引用（通过 `buildWorkerPoolConfigFromServeConfig` 返回值字段），实际检查 `poolCfg.Timeouts.ReviewPR` 等，类型为 `time.Duration`。**如果没有直接引用 `worker` 包的符号，则不需要 import `worker`**。执行时需确认。

**Step 4: 从 `serve_test.go` 删除已移出的测试**

删除对应行。清理不再需要的 import。

**Step 5: 验证**

```bash
go build ./...
go vet ./...
go test ./internal/cmd/... -count=1 -v 2>&1 | tail -30
```

Expected: 全部 PASS。

**Step 6: 提交**

```bash
git add internal/cmd/serve_deps.go internal/cmd/serve_deps_test.go internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "refactor: 拆出 serve_deps.go — 依赖装配逻辑

将 ServiceDeps、BuildServiceDeps、buildServeConfigFromManager、
computeReadyStatus、buildWorkerPoolConfigFromServeConfig 从 serve.go
移至 serve_deps.go，对应测试移至 serve_deps_test.go。

TD-001 拆分 2/3。"
```

---

### Task 3: 将 configAdapter 追加到 adapter.go + 创建 adapter_test.go

**Files:**
- Modify: `internal/cmd/adapter.go` — 追加 configAdapter
- Create: `internal/cmd/adapter_test.go`
- Modify: `internal/cmd/serve.go` — 删除已移出的代码
- Modify: `internal/cmd/serve_test.go` — 删除已移出的测试

**Step 1: 追加 configAdapter 到 `adapter.go`**

从 `serve.go` 移出以下内容（原始行号）：

```
行 288-289:  编译时断言 var _ fix.FixConfigProvider = (*configAdapter)(nil)
行 291-330:  configAdapter 类型 + ResolveReviewConfig/IsReviewEnabled/GetClaudeModel/GetClaudeEffort 方法
```

追加到 `adapter.go` 末尾。更新 import，新增：
- `"otws19.zicp.vip/kelin/dtworkflow/internal/config"`
- `"otws19.zicp.vip/kelin/dtworkflow/internal/fix"`

**Step 2: 从 `serve.go` 删除已移出的代码**

删除对应行。从 import 中移除 `"otws19.zicp.vip/kelin/dtworkflow/internal/fix"`（如果 `serve.go` 中 `runServeWithConfig` 仍直接引用 `fix.NewService`，则保留）。

注意：`runServeWithConfig` 行 657 使用 `fix.NewService`，所以 serve.go **仍需保留** `fix` 包 import。

**Step 3: 创建 `adapter_test.go`**

从 `serve_test.go` 移出以下内容：

```
行 932-943:  TestConfigAdapter_ResolveReviewConfig_ReturnsOverride
行 945-955:  TestConfigAdapter_IsReviewEnabled_DefaultsToTrue
行 957-983:  TestConfigAdapter_IsReviewEnabled_FalseWhenDisabled
```

文件结构：

```go
package cmd

import (
	"testing"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
)
```

注意：这些测试使用 `buildTestConfigManager`（`serve_test.go`）和 `writeTestConfigFile`（`root_config_test.go`），同包可见。

**Step 4: 从 `serve_test.go` 删除已移出的测试**

删除对应行。

**Step 5: 验证**

```bash
go build ./...
go vet ./...
go test ./internal/cmd/... -count=1 -v 2>&1 | tail -30
```

Expected: 全部 PASS。

**Step 6: 提交**

```bash
git add internal/cmd/adapter.go internal/cmd/adapter_test.go internal/cmd/serve.go internal/cmd/serve_test.go
git commit -m "refactor: 将 configAdapter 移入 adapter.go

将 configAdapter 及其 4 个方法从 serve.go 移至 adapter.go（与
giteaCommentAdapter 同文件），对应测试移至 adapter_test.go。

TD-001 拆分 3/3。"
```

---

### Task 4: 最终清理与验证

**Files:**
- Modify: `internal/cmd/serve.go` — 清理 import
- Modify: `internal/cmd/serve_test.go` — 清理 import + 移除未使用的辅助函数
- Modify: `docs/TECH_DEBT.md` — 标记 TD-001 已完成

**Step 1: 清理 serve.go import**

确认 `serve.go` 剩余的 import 块仅包含实际使用的包。预期最终 import：

```go
import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/spf13/cobra"

	"otws19.zicp.vip/kelin/dtworkflow/internal/config"
	"otws19.zicp.vip/kelin/dtworkflow/internal/fix"
	"otws19.zicp.vip/kelin/dtworkflow/internal/queue"
	"otws19.zicp.vip/kelin/dtworkflow/internal/report"
	"otws19.zicp.vip/kelin/dtworkflow/internal/review"
	"otws19.zicp.vip/kelin/dtworkflow/internal/webhook"
)
```

不再需要：`"crypto/tls"`、`"sync"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/gitea"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/notify"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/store"`、`"otws19.zicp.vip/kelin/dtworkflow/internal/worker"`。

**Step 2: 清理 serve_test.go import**

确认测试 import 仅包含实际使用的包。移除不再需要的 import（如 `"log/slog"` 等）。

**Step 3: 运行完整验证**

```bash
go build ./...
go vet ./...
go test ./internal/cmd/... -count=1 -v
```

**Step 4: 验证行数**

```bash
wc -l internal/cmd/serve.go internal/cmd/serve_notify.go internal/cmd/serve_deps.go internal/cmd/adapter.go
```

预期：`serve.go` ~360 行，`serve_notify.go` ~220 行，`serve_deps.go` ~190 行，`adapter.go` ~85 行。

**Step 5: 更新 TECH_DEBT.md**

在 TD-001 条目末尾追加：

```markdown
- **状态**: 已完成（2026-04-08）
```

**Step 6: 提交**

```bash
git add internal/cmd/serve.go internal/cmd/serve_test.go docs/TECH_DEBT.md
git commit -m "refactor: 完成 serve.go 拆分，清理 import，关闭 TD-001

serve.go 从 837 行降至 ~360 行。拆分为：
- serve_notify.go（通知构造，~220 行）
- serve_deps.go（依赖装配，~190 行）
- adapter.go（配置适配器追加，~85 行）

docs/TECH_DEBT.md TD-001 标记为已完成。"
```
