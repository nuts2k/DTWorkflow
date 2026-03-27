# 容器执行超时与可观测性讨论

> 日期：2026-03-27
> 状态：待技术验证
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

### 方案 C：stderr 输出流作为心跳信号（待验证，最有前景）

核心思路：不预测"该给多长时间"，而是检测"它还在干活吗"。

设一个宽松的硬超时（如 30 分钟）作为绝对上限，同时通过 stderr 输出流检测活跃度。如果长时间（如 5 分钟）没有新输出，判定卡住并提前终止。

**待验证的关键问题**：

1. **`claude -p` 非交互模式下 stderr 输出什么？**
   - 交互式 TUI 用 ANSI 转义码原地更新行（"Thinking..." 转圈等），这些不是逐行输出
   - `-p` 模式没有 TTY，理论上不会用 ANSI 转义码，但具体 stderr 输出内容和频率未知
   - 可能的情况：
     - 有逐行进度信息（理想）
     - 几乎静默，只有错误时输出（不能当心跳用）
     - 输出间隔不规律，深度思考时可能几分钟无输出

2. **验证方法**：
   ```bash
   # 方法一：本机直接测试
   claude -p "review this code..." --output-format json 2>/tmp/claude-stderr.log
   # 观察 stderr 内容和时间间隔

   # 方法二：容器内测试
   docker logs -f <container_id>  # 实时观察 stderr
   ```

3. **如果 stderr 不可靠**，备选方案：
   - Docker Stats CPU 监控（方案 B）
   - 定期 `docker exec` 检查进程状态
   - 在 entrypoint 中包装一个心跳脚本

### 方案 D：可配置超时（最低成本改进）

将硬超时从代码常量改为 YAML 可配置（全局 + 仓库级），让用户根据项目情况自行调整。

这是无论最终选哪个方案都应该做的基础改进。

## 建议实施路径

1. **第一步（低成本）**：把 `TaskTimeout` 改为可配置，支持全局和仓库级覆盖
2. **第二步（技术验证）**：实际测试 `claude -p` 的 stderr 输出行为，确认方案 C 是否可行
3. **第三步（根据验证结果）**：
   - stderr 可靠 → 实现 stderr 流式心跳检测 + 宽松硬超时
   - stderr 不可靠 → 实现 Docker Stats 活跃度检测，或退回方案 D

## 相关代码位置

- 任务超时定义：`internal/queue/options.go:11` — `TaskTimeout()`
- 容器等待超时：`internal/worker/pool.go:217` — `context.WithTimeout(ctx, 30*time.Minute)`
- asynq 入队配置：`internal/queue/client.go:161` — `asynq.Timeout(TaskTimeout(taskType))`
- 容器清理：`internal/worker/pool.go:180-191` — `defer RemoveContainer`
- 重试配置：`internal/queue/options.go:27-46` — `TaskMaxRetry()` / `TaskRetryDelay()`
