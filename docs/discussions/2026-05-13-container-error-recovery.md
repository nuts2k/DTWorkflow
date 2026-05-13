# 容器执行错误的运行时纠正方案讨论

**日期**: 2026-05-13
**状态**: 讨论完成，待实施

## 问题背景

容器执行过程中有两种错误会导致全量重试（销毁容器 → 重新克隆仓库 → 重跑 Claude）：

1. **返回解析错误**（Parse Failure）：Claude 输出的 JSON 不符合预期格式
2. **活跃度超时**（Activity Timeout）：容器 stdout 长时间无新输出，stream monitor 判定卡住

全量重试代价高：浪费 Claude 已完成的所有工作（克隆、分析、推理），讨论是否有办法在容器执行过程中纠正这两种错误。

## 分析结论

### 活跃度超时：Docker exec 原地恢复（明确可行）

超时触发时容器仍存活，可以通过 Docker exec 介入而非直接销毁。

**推荐方案：两阶段超时**

```
Phase 1 (soft timeout - 首次超时):
  1. docker exec 发 SIGINT 给 claude 进程
  2. 等待原进程优雅退出（session 持久化到 ~/.claude/）
  3. docker exec 启动新 claude: --resume <session_id> -p "继续完成任务并输出结果"
  4. stream monitor 重置 timer，继续监控新进程的 stdout

Phase 2 (hard timeout - 二次超时):
  resume 也无响应 → cancel(ErrActivityTimeout) → 走现有全量重试路径
```

**优势**：
- 容器未销毁，仓库/分支/环境全部就绪，零开销
- 会话数据在容器 tmpfs 里，不需要 Docker volume
- resume 恢复 Claude 对话上下文，不重复已完成的工作

**需要改造的代码**：
- `streamMonitorLoop`（pool.go）：增加 soft/hard 两阶段状态机
- `DockerClient` 接口：新增 `ExecInContainer(ctx, containerID, cmd)` 方法
- Session ID 捕获：从 stream-json 事件中提取 session_id（扩展 `tryExtractResultCLIJSON`）

### 返回解析错误：当前机制已基本合理

解析错误分两种子情况：

| 子类型 | 说明 | 对应代码分支 | 纠正方案 |
|-------|------|------------|---------|
| Case 1：格式被污染 | 内容正确但 JSON 格式不对（code fence、未转义引号等） | 内层业务 JSON 解析失败 | Host 侧轻量 API 修复（可选优化） |
| Case 2：内容已出错 | 执行过程中已偏离（CLI error、不变量违反、Claude 幻觉） | `IsExecutionError` / 不变量校验失败 | 全量重试是正确做法 |

**为什么 resume 对解析错误无效**：
- Case 1（格式问题）：Host 侧 API 修复更轻量，不需要 resume
- Case 2（内容问题）：session 上下文已被污染，resume 大概率重复同样的错误

**为什么 Docker exec 对解析错误不适用**：
解析错误发生在 `parseResult` 阶段，此时容器已正常退出并被 defer 清理，无从介入。

**可选优化（方案 A）**：
对 Case 1 在 `parseResult` 失败后、触发全量重试前，插入一次轻量 Claude API 调用修复 JSON 格式。但适用面较窄，优先级低于活跃度超时的改进。

### 排除的方案

| 方案 | 排除原因 |
|------|---------|
| Stdin 直接注入 | stdin 在写入 prompt 后已关闭，`-p` 模式不支持交互续写 |
| Entrypoint 内验证循环 | 与 stream-json 模式冲突，实现复杂 |
| Volume 持久化 + 新容器 resume | Docker exec 原地 resume 更简单，无需 volume 管理；仅作为 Docker exec 失败后的兜底 |

## 实施建议

**优先做**：活跃度超时的 Docker exec + 两阶段超时方案。这是唯一有明确改进空间的点。

**预防优于纠正**：解析错误方面，改进方向应聚焦于降低发生率（优化 prompt 格式约束、强化 `--output-format json` 信封控制），而非事后修复。

## 总结

| 错误类型 | 有效应对 | 优先级 |
|---------|---------|-------|
| 活跃度超时 | Docker exec + resume（两阶段超时） | 高 — 明确可行且收益大 |
| 解析错误 Case 1（格式问题） | Host 侧 API 修复 | 低 — 适用面窄 |
| 解析错误 Case 2（内容问题） | 当前全量重试已正确 | 无需改动 |
