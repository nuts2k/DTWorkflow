# `claude -p` stderr 输出行为验证测试设计

> 日期：2026-03-28
> 关联：docs/discussions/2026-03-27-container-timeout-observability.md 方案 C

## 目标

验证 `claude -p`（非交互模式）的 stderr 输出是否可作为容器活跃度心跳信号。

需回答三个问题：

1. stderr 是否有输出？内容是什么？
2. 输出频率如何？最大静默间隔多长？
3. "正常工作"和"卡住"时 stderr 行为有无可区分差异？

## 测试环境

- 本机 macOS 直接运行
- 不涉及 Docker 容器

## 测试轮次

### 轮次一：快速摸底（简单 prompt）

```bash
claude -p "用 Go 写一个冒泡排序，并解释时间复杂度" --output-format json 2>/tmp/claude-stderr-simple.log
```

- 目的：确认 stderr 是否有输出、基本格式
- 预期耗时：10-30 秒

### 轮次二：真实场景（代码评审 prompt）

```bash
claude -p "评审以下 Go 文件的代码质量、潜在 bug 和改进建议：$(cat internal/worker/pool.go)" --output-format json 2>/tmp/claude-stderr-review.log
```

- 目的：观察较长执行时间下 stderr 的输出频率和间隔
- 预期耗时：1-5 分钟

## 观测方法

给 stderr 每行加时间戳，实时观察：

```bash
claude -p "..." --output-format json 2>&1 1>/tmp/claude-stdout.json \
  | while IFS= read -r line; do
      echo "$(date '+%H:%M:%S.%N') $line"
    done \
  | tee /tmp/claude-stderr-ts.log
```

## 分析维度

- 每行内容是什么（进度信息？工具调用？ANSI 转义码？纯文本？）
- 行与行之间的时间间隔
- 是否有长时间（>1 分钟）完全静默的时段
- 有无可识别的"开始/结束"标记

## 结论判定

| stderr 行为 | 结论 |
|---|---|
| 持续有输出，间隔 < 2 分钟 | 方案 C 可行，可作为心跳 |
| 有输出但间隔不稳定，偶尔 > 5 分钟 | 方案 C 需要宽松阈值，勉强可用 |
| 几乎无输出或只有首尾两行 | 方案 C 不可行，退回方案 B/D |

## 产出物

- `/tmp/claude-stderr-simple.log` — 轮次一原始 stderr
- `/tmp/claude-stderr-review.log` — 轮次二原始 stderr
- `/tmp/claude-stderr-ts.log` — 带时间戳的 stderr
- `/tmp/claude-stdout.json` — stdout JSON 输出（供参考）
- 更新讨论文档 `docs/discussions/2026-03-27-container-timeout-observability.md` 记录验证结论
