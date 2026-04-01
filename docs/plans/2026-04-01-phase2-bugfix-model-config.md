# Phase 2 Bug Fix 与 Claude 模型配置增强

> 日期：2026-04-01
> 涉及里程碑：M1.4 / M1.6 / M1.8 / M2.2

---

## 背景

本轮包含四个独立修复/增强，均来自实际使用中发现的问题。

---

## 1. feat(review)：支持配置 Claude 模型与推理强度

### 问题

`buildCommand` 固定调用默认模型与推理强度，无法针对不同仓库使用性价比更优的模型组合。

### 方案

**配置层**（`internal/config`）：
- `ClaudeConfig` 新增 `Model` / `Effort` 字段，默认值 `claude-sonnet-4-6` / `high`
- `ReviewOverride` 新增同名字段，`ResolveReviewConfig` 将全局值作为基础，仓库级可覆盖

**命令构造层**（`internal/review/prompt.go`）：
- `ReviewConfig` 新增 `Model` / `Effort` 字段
- `buildCommand(cfg ReviewConfig)` 按需追加 `--model` / `--effort` 参数
- `normalizeEffort` 函数规范化推理强度值（trim + lowercase）

**配置示例**（`configs/dtworkflow.example.yaml`）：
```yaml
claude:
  model: "claude-sonnet-4-6"
  effort: "high"

repos:
  - name: "org/expensive-repo"
    review:
      model: "claude-opus-4-6"   # 高价值仓库用更强模型
      effort: "medium"
```

### 继承规则

```
全局 claude.model/effort
  ↓ (若仓库未覆盖则继承)
repos[].review.model/effort
  ↓
ReviewConfig.Model/Effort
  ↓
buildCommand() → --model / --effort
```

---

## 2. fix(security)：清除容器内 Gitea 环境变量

### 问题

`GITEA_URL` 和 `REPO_CLONE_URL` 仅供 `entrypoint.sh` 的 clone 阶段使用，但此前未在 clone 完成后清除，Claude Code 执行阶段仍可通过环境读取仓库克隆凭证。

### 方案

`build/docker/entrypoint.sh` 在 clone 完成后新增：
```sh
unset GITEA_URL
unset REPO_CLONE_URL
```

结合既有的 `unset GITEA_TOKEN` / `unset AUTH_URL`，形成完整的凭证清理链。

> **安全设计原则**：容器内存在两个执行阶段（clone 阶段 / Claude Code 阶段），两阶段所需权限不同。凭证仅在最小必要时段可见，执行阶段看不到克隆凭证。

同步在 `container.go` 添加注释说明此设计意图，防止未来误删 unset 逻辑。

---

## 3. fix(review)：extractJSON 兼容前导文本

### 问题

Claude 有时在 JSON 结构之前输出自然语言说明（如"Here is the review result:"），导致 `extractJSON` 无法匹配 code fence，直接返回原始文本，后续 JSON 解析失败。

### 方案

在 `extractJSON` 的 code fence 路径之后，增加兜底路径：
```go
start := strings.Index(text, "{")
end := strings.LastIndex(text, "}")
if start >= 0 && end > start {
    return text[start : end+1]
}
```

处理顺序：
1. 优先尝试 code fence 提取（```json ... ``` 或 ``` ... ```）
2. 无 code fence 时，定位首个 `{` 到末尾 `}` 之间的内容
3. 兜底返回原始文本（保持原有行为）

---

## 4. fix(webhook)：支持 PR reopened 事件

### 问题

PR 被关闭后重新打开（`reopened` 动作）应触发评审，但 `parser.go` 原条件仅允许 `opened` / `synchronized`，导致 reopen 后不触发评审。

### 方案

`parsePullRequest` 白名单新增 `reopened`：
```go
if payload.Action != "opened" && payload.Action != "synchronized" && payload.Action != "reopened" {
    return nil, ErrUnsupportedAction
}
```

---

## 5. fix(review)：prompt 前置 READ-ONLY 约束

### 问题

部分场景下 Claude Code 会尝试调用外部 API 或向 Gitea 提交评论，与 `--disallowedTools` 工具层限制形成冲突，行为不可预期。

### 方案

在 `buildPrompt` 最开头注入明确的只读模式约束文本（意图层）：
```
IMPORTANT: You are in READ-ONLY code analysis mode.
- Do NOT call any external APIs, HTTP endpoints, or network services
- Do NOT attempt to submit reviews, comments, or status updates to Gitea or any other platform
- Do NOT run curl, wget, python requests, or any other network commands
- Your ONLY task is to analyze the code and output the JSON result to stdout
```

**双层防御设计**（有意为之，请勿删除任何一层）：
| 层次 | 机制 | 作用 |
|------|------|------|
| 意图层 | Prompt 前置约束文本 | 约束 Claude 的行为意图，减少工具调用尝试 |
| 工具层 | `--disallowedTools Edit,Write,NotebookEdit` | 强制禁止文件写工具，兜底保障 |

---

## 配置校验增强

`internal/config/validate.go` 同步新增对上述字段的校验：

| 字段 | 校验规则 |
|------|---------|
| `claude.effort` | 枚举：`low` / `medium` / `high`，空值跳过 |
| `claude.model` | 正则 `^[a-zA-Z0-9._-]+$`，空值跳过 |
| `review.effort` | 同上 |
| `review.model` | 同上 |
| `repos[N].review.effort` | 同上 |
| `repos[N].review.model` | 同上 |

---

## 测试覆盖

- `internal/config/repo_config_test.go`：新增 model/effort 继承与覆盖场景
- `internal/config/validate_test.go`：新增 effort 非法值、model 格式非法值校验
- `internal/review/prompt_test.go`：新增 buildCommand model/effort 参数透传、extractJSON 前导文本兼容
