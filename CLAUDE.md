# CLAUDE.md

## 项目概述

DTWorkflow —— 基于 Claude Code 的 Gitea 自动化工作流平台。功能包括 PR 自动评审、Issue 自动修复、测试补全与 AI E2E 测试。

详细需求见 `docs/PRD.md`，路线图见 `docs/ROADMAP.md`。

## 语言与沟通规范

- 非必要情况下，沟通、代码注释、Git 提交消息、文档均使用**中文**
- 变量名、函数名、类型名等代码标识符使用英文

## 技术栈

- **语言**：Go
- **CLI 框架**：Cobra
- **HTTP 框架**：Gin
- **任务队列**：asynq + Redis
- **数据持久化**：SQLite（modernc.org/sqlite，纯 Go，禁止使用 CGO 依赖）
- **容器管理**：Docker SDK for Go
- **配置管理**：Viper（YAML 格式）
- **Gitea 交互**：Gitea REST API
- **执行引擎**：Claude Code CLI

## 架构约束

- **CLI-first，API 并行**：核心能力必须通过 CLI 命令暴露，HTTP API 作为并行接入层
- **CLI 输出规范**：所有命令支持 `--json` 标志输出结构化 JSON；默认人类可读格式；退出码：0 成功 / 1 失败 / 2 部分成功
- **初期不做 Web 界面**：配置通过 YAML 文件管理，状态查询通过 CLI 或 API
- **Docker 隔离**：每个任务在独立 Docker 容器中运行 Claude Code CLI，容器间互不影响

## 跨平台要求

- 开发环境：macOS
- 生产环境：Debian (Linux amd64)
- **禁止使用 CGO**：所有依赖必须为纯 Go 实现，确保 `GOOS=linux GOARCH=amd64 go build` 可直接交叉编译
- 文件路径使用 `filepath.Join()`，不硬编码分隔符

## 项目结构

```
cmd/
  dtworkflow/   # 服务端 CLI 入口
  dtw/          # 瘦客户端 CLI 入口（远程操作）
internal/       # 内部包（不对外暴露）
  api/          # REST API 层（handlers/middleware/router）
  cmd/          # 服务端 Cobra 命令定义
  gitea/        # Gitea API 客户端
  worker/       # Docker Worker 池管理
  queue/        # 任务队列
  review/       # PR 评审逻辑
  fix/          # Issue 分析与修复逻辑（analyze_issue 只读分析 / fix_issue 写权限修复）
  notify/       # 通知框架
  config/       # 配置管理
  dtw/          # dtw 客户端核心库（HTTP 客户端/配置/Wait 轮询/输出）
    cmd/        # dtw Cobra 命令定义
  store/        # SQLite 数据持久化
  model/        # 共享数据模型
  webhook/      # Webhook 事件解析
  report/       # 报告生成
pkg/            # 可复用的公共包
docs/           # 项目文档（PRD、ROADMAP 等）
configs/        # 配置文件模板
```

## 代码搜索规则

- **探索性问题必须先语义搜索**：当用户提问包含"…是什么"、"怎么实现的"、"机制"、"流程"、"逻辑"、"策略"等探索性关键词时 → **必须**先用 `mcp__fast-context__fast_context_search`，禁止直接 Grep
- **宽泛语义搜索**（"找所有涉及 Y 的文件"、探索陌生模块、需要超过 2 次 Grep 才能定位的问题）→ 优先用 `mcp__fast-context__fast_context_search`
- **精确定位**（已知文件名、函数名、字符串）→ 用 Grep / Glob
- 并行读取多个文件时无需等待，直接同时发起所有 Read 调用

## Issue 自动修复功能（M3.5）

- **触发方式**：
  - 标签触发：Issue 添加 `auto-fix` 标签 → 只读分析（`analyze_issue`）；添加 `fix-to-pr` 标签 → 修复 + PR 创建（`fix_issue`）
  - CLI 触发：`./bin/dtw fix-issue --owner <owner> --repo <repo> --issue <N> --fix`
- **两级镜像**：`analyze_issue` 用轻量镜像（只读），`fix_issue` 用执行镜像（`worker-full`，含 JDK + Maven）
- **修复流程**：`fix.Service.executeFix` 12 步 — 前置校验 → 信息不足检查 → 采集上下文 → 构造 prompt → 容器执行 → 解析 FixOutput → 创建 PR → Issue 评论
- **PR 创建**：容器内 Claude push 分支 `auto-fix/issue-{id}`，容器外 `fix.Service` 通过 Gitea API 创建 PR（`fixes #{id}` 关联 Issue）
- **Tag-as-Ref**：Issue 关联 tag 时，PR Base 改用仓库默认分支，PR 描述中注明
- **失败处理**：信息不足 / Claude 返回失败 → SkipRetry + Issue 评论；Push 成功但 PR 创建失败 → 允许重试

## 测试服务器

- SSH Host 别名：`companytest`（对应 `~/.ssh/config` 中的 Host 条目）
- 连接命令：`ssh companytest`
- 本机可创建 `deploy/local.env`（已被 `.gitignore` 排除）覆盖默认 Host，参考 `deploy/local.env.example`
- 部署目录：`/opt/dtworkflow`

## 编码规范

- 遵循 Go 官方代码规范和 Effective Go
- 使用 golangci-lint 进行静态检查
- 错误处理：不吞错误，逐层返回或包装（`fmt.Errorf("xxx: %w", err)`）
- 日志使用结构化日志库（如 slog）
