# 容器执行超时与可观测性讨论

> 日期：2026-03-27
> 状态：技术验证完成，方案 E（stream-json）可行
> 关联：Phase 2 PR 自动评审

## 背景

PR 评审在 Docker 容器内运行 Claude Code CLI（`claude -p`），当前是完全黑盒等待：容器启动后到执行结束之间没有任何中间可观测信号。如果 Claude Code 因远程 API 不可达等原因卡住，系统只能等到超时才能发现。

## 当前超时机制（三层保护）

| 层级 | 超时值 | 机制 | 触发后行为 |
|------|--------|------|-----------|
| asynq 任务超时 | 10 分钟（review_pr） | 取消 handler 的 context | Processor 收到 context.DeadlineExceeded |
| 容器等待超时 | 30 分钟（受 asynq 约束实际 10 分钟） | context.WithTimeout(ctx, 30m) | WaitContainer 返回错误 |
| 容器清理保障 | 30 秒 | context.Background() + defer | 强制 RemoveContainer |

超时后触发重试：最大 3 次，指数退避（30s, 60s, 120s），最坏情况 ~30+ 分钟后标记 failed。

## 问题

### 1. 超时值难以确定

10 分钟是个静态值，但实际评审耗时取决于多个不可预测因素：

- **仓库大小**：clone 时间 + Claude 探索上下文的范围
- **代码复杂度**：改了一行核心逻辑，Claude 可能追溯整个调用链，读几十个文件
- **网络状况**：API 延迟波动不可控
- **Claude 的探索深度**：不可预测

关键认知：**Claude Code 拥有整个仓库**，很小的改动也可能触发大量上下文探索。评审耗时与 diff 大小**不成正比**，与项目大小和复杂度关系更大。因此基于 diff 大小的动态超时方案（如按变更行数计算）并不可靠。

### 2. 执行期间无可观测性

`WaitContainer` 阻塞等待，期间唯一的外部手段是手动 `docker stats` 查看 CPU/内存。没有日志流、没有心跳、没有进度信号。

## 讨论过的方案

### 方案 A：基于 diff 大小的动态超时（已否决）

根据 PR 变更行数动态计算超时值。

**否决原因**：评审耗时与 diff 大小不成正比。小改动在大项目里可能花很长时间探索上下文，本质上是在猜。

### 方案 B：活跃度检测（Docker Stats CPU 监控）

定期 poll Docker Stats API，检测容器 CPU 使用率。连续 N 分钟 CPU 为 0 则判定卡住。

**待验证问题**：
- Claude Code 在等待 API 响应时处于 IO-wait 状态，CPU 接近 0，但实际是正常工作
- 难以区分"正常等 API 响应"（秒级）和"API 不可达导致 hang"（分钟级）
- CPU 监控的可靠性需要实际测试确认

### 方案 C：stderr 输出流作为心跳信号（已验证，不可行）

核心思路：不预测"该给多长时间"，而是检测"它还在干活吗"。

设一个宽松的硬超时（如 30 分钟）作为绝对上限，同时通过 stderr 输出流检测活跃度。如果长时间（如 5 分钟）没有新输出，判定卡住并提前终止。

**验证结果（2026-03-28）**：

在 `--output-format json` 模式下，`claude -p` 的 stderr 行为如下：

| 测试场景 | 耗时 | API 轮次 | stderr 中间输出 |
|----------|------|----------|----------------|
| 简单 prompt（写冒泡排序） | ~10s | 1 轮 | 无 |
| 代码评审（pool.go，触发多次文件读取） | ~175s | 8 轮 | 无 |

- stderr 在整个执行期间**完全静默**，只在最后一次性输出完整 JSON 结果
- JSON 结果同时输出到 stdout 和 stderr
- 执行期间没有任何进度信号、工具调用日志、或心跳输出

**结论：`--output-format json` 模式下 stderr 不可用作心跳信号。**

**替代方向：`--output-format stream-json --verbose`**（已验证，见方案 E）。

### 方案 E：stream-json stdout 流式事件作为心跳（已验证，可行）

> 2026-03-28 验证

使用 `--output-format stream-json --verbose --include-partial-messages` 替代 `--output-format json`，通过 stdout 上的流式 JSON 事件检测活跃度。

**验证结果**：

| 模式 | 总事件数 | 最大静默间隔 | 结论 |
|------|----------|-------------|------|
| `stream-json --verbose`（无 partial） | 31 | **485 秒（8 分钟）** | 工具调用阶段密集，生成回复阶段完全静默 |
| `stream-json --verbose --include-partial-messages` | **4469** | **9 秒** | 全程持续有事件，可靠心跳 |

事件类型包括：
- `system` — hooks 启停、系统初始化
- `assistant` — 思考过程、工具调用、文本输出的增量 chunk
- `user` — 工具执行结果
- `result` — 最终结果

**实施要点**：
1. 容器内 `claude -p` 改用 `--output-format stream-json --verbose --include-partial-messages`
2. 宿主进程通过 `docker logs --follow` 或 attach stdout 流，监控新事件到达
3. 设活跃度阈值（如 2 分钟无新事件 → 判定卡住），配合宽松硬超时（如 30 分钟）
4. 最终结果从 stdout 流的 `{"type":"result"}` 事件中提取
5. 注意：`--include-partial-messages` 会显著增大输出体积（286KB vs 212KB），监控时只需检测"有新数据到达"即可，不必解析每条消息

**额外发现**：`claude -p` 会继承父进程的 hooks 配置。在 Docker 容器内运行不受影响（容器内无 hooks），但本机嵌套调用时可能因 hooks 卡死。

### 方案 D：可配置超时（最低成本改进）

将硬超时从代码常量改为 YAML 可配置（全局 + 仓库级），让用户根据项目情况自行调整。

这是无论最终选哪个方案都应该做的基础改进。

## 建议实施路径

> 2026-03-28 更新：方案 C（stderr）已验证不可行，方案 E（stream-json）已验证可行

1. **第一步（低成本）**：把 `TaskTimeout` 改为可配置，支持全局和仓库级覆盖（方案 D）
2. **第二步（核心改造）**：容器内 `claude -p` 改用 `--output-format stream-json --verbose --include-partial-messages`，从 stdout 流的 `{"type":"result"}` 事件提取最终结果（方案 E 基础）
3. **第三步（活跃度检测）**：实现 stdout 流式心跳监控，设 2 分钟活跃度阈值 + 30 分钟硬超时上限（方案 E 完整实现）

## 相关代码位置

- 任务超时定义：`internal/queue/options.go:11` — `TaskTimeout()`
- 容器等待超时：`internal/worker/pool.go:217` — `context.WithTimeout(ctx, 30*time.Minute)`
- asynq 入队配置：`internal/queue/client.go:161` — `asynq.Timeout(TaskTimeout(taskType))`
- 容器清理：`internal/worker/pool.go:180-191` — `defer RemoveContainer`
- 重试配置：`internal/queue/options.go:27-46` — `TaskMaxRetry()` / `TaskRetryDelay()`
