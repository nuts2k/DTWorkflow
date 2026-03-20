# DTWorkflow 项目路线图 (ROADMAP)

> 基于 Claude Code 的 Gitea 自动化工作流平台

## 总览

项目分为 **5 个阶段**，采用增量交付策略。每个阶段都产出可独立运行的功能，后续阶段在前者基础上扩展。

```
Phase 1          Phase 2          Phase 3          Phase 4          Phase 5
基础设施          PR 自动评审       Issue 自动修复    集中化测试        AI E2E 测试
& 核心框架        (核心功能)        (核心功能)       (扩展功能)        (扩展功能)
─────────────── ─────────────── ─────────────── ─────────────── ───────────────
  基座搭建          首个业务价值      第二业务价值      测试体系补全      自动化闭环
```

---

## Phase 1：基础设施与核心框架

**目标**：搭建可运行的骨架系统，验证 Claude Code CLI 在 Docker 中的可行性

### 里程碑

#### M1.0 PoC 验证：Claude Code CLI 在 Docker 中的可行性

在正式开发前，先用最小成本验证核心技术假设。PoC 不追求代码质量和架构，目标是快速确认可行性并记录关键参数。

**验证项**：

1. **容器内安装与运行**
   - [x] 构建 Docker 镜像：基础镜像（Debian/Node.js）+ 安装 Claude Code CLI（`npm install -g @anthropic-ai/claude-code`）
   - [x] 容器内执行 `claude -v` 确认安装成功
   - [x] 容器内执行 `claude -p "你好"` 确认非交互模式可用
   - [x] 验证 `--output-format json` 等输出格式控制参数

2. **API Key 认证**
   - [x] 通过环境变量 `ANTHROPIC_API_KEY` 注入容器，验证 Claude API 调用正常
   - [x] 确认 Key 不会泄露到容器日志或文件系统

3. **代码操作能力**
   - [x] 在容器内准备一个示例代码目录（挂载或容器内创建）
   - [x] 用 `claude -p` 让 Claude Code 读取代码文件并输出分析结果
   - [x] 用 `claude -p` 让 Claude Code 修改代码文件，验证文件写入能力
   - [x] 用 `claude -p` 让 Claude Code 执行 Git 操作（init、创建分支、commit）

4. **资源与性能基线**
   - [x] 记录单次 `claude -p` 调用的内存峰值和耗时
   - [ ] 记录容器启动到可执行任务的冷启动耗时
   - [ ] 评估单机可并发运行的 Worker 容器数量上限

5. **跨平台验证**
   - [x] macOS（Docker Desktop）环境以上各项通过
   - [x] Debian（Docker Engine）环境以上各项通过

**产出物**：
- PoC 验证用的 Dockerfile
- docker-compose.yml（含环境变量配置示例）
- 验证结果报告（每项通过/失败、关键参数记录、踩坑记录）

**通过标准**：
以上 1-3 全部通过，4-5 记录基线数据。任一项 1-3 失败需评估替代方案后再决定是否继续。

---

#### M1.1 项目初始化
- [x] Go 项目骨架（Go Modules）
- [x] 项目目录结构规划（cmd / internal / pkg）
- [x] golangci-lint 配置
- [x] Docker / Docker Compose 开发环境

#### M1.2 CLI 框架
- [x] Cobra CLI 骨架搭建
- [x] 子命令结构设计（review-pr / fix-issue / gen-tests / task / serve）
- [x] 全局 flags：`--json`（结构化输出）、`--config`（配置文件路径）、`--verbose`（详细日志）
- [x] 退出码规范：0 成功 / 1 失败 / 2 部分成功
- [x] `serve` 子命令：启动 HTTP 服务（API + Webhook 接收器）
- [x] 统一输出层（PrintResult / PrintError）
- [x] 单元测试（23 个测试，覆盖率 86.9%）

#### M1.3 Gitea API 客户端
- [x] 封装 Gitea REST API 客户端（Go），纯标准库实现，零外部依赖
- [x] 实现 20 个核心 API：PR 操作（8 个）、Issue 操作（7 个）、仓库操作（5 个）
- [x] API Token 认证，Functional Options 模式配置
- [x] 错误处理：类型化 ErrorResponse + IsNotFound/IsUnauthorized/IsForbidden/IsConflict 判断
- [x] URL 路径安全转义，响应体大小限制，分页支持
- [x] 单元测试（53 个测试，覆盖率 85.8%）+ 集成测试框架

#### M1.4 Webhook 接收器
- [ ] Gin 路由接收 Gitea Webhook
- [ ] Webhook 签名验证（安全性）
- [ ] 事件解析：PR 事件（opened / synchronized）、Issue 标签变更事件
- [ ] Gitea 侧 Webhook 配置指南

#### M1.5 任务队列
- [ ] Redis + asynq 集成
- [ ] 任务类型定义（PR 评审 / Issue 修复 / 测试生成）
- [ ] 优先级、重试、超时机制
- [ ] 任务状态持久化到 SQLite
- [ ] CLI 命令：`dtworkflow task status/list/retry`

#### M1.6 Docker Worker 池
- [ ] Claude Code CLI Docker 镜像构建（基础镜像 + Claude Code + Git + 常用工具）
- [ ] Docker SDK for Go 集成，程序化创建/销毁容器
- [ ] Worker 池管理：并发数控制、资源限制（CPU / 内存）
- [ ] 容器内 Git 仓库 clone + worktree 验证
- [ ] Claude Code CLI 非交互模式验证（`claude -p` 在容器内运行）
- [ ] API Key 安全注入（环境变量 / Docker secrets）

#### M1.7 通知框架（骨架）
- [ ] 通知接口定义（Go interface，策略模式）
- [ ] Gitea 评论通知实现（默认通道）
- [ ] 按仓库/事件类型的通知路由配置

#### M1.8 配置管理
- [ ] Viper 集成，全局配置文件格式定义（YAML）
- [ ] 仓库级配置支持
- [ ] 配置热加载（Viper WatchConfig）

### 交付物
- `dtworkflow` 单二进制文件，包含 CLI 命令和 `serve` 模式
- Gitea API 客户端库
- 通知框架骨架
- Docker Compose 一键启动脚本（Redis + dtworkflow serve）

### 验证标准
- Gitea 发送 Webhook → 服务接收 → 创建任务 → 启动 Docker 容器 → 容器内 Claude Code CLI 执行简单 prompt → 结果回写 Gitea 评论
- 端到端流程打通即为通过

### 风险项
- **Claude Code CLI Docker 兼容性**：需尽早验证 CLI 在容器中的行为，包括认证、文件系统访问、Git 操作
- **网络配置**：容器内需能同时访问 Gitea（内网）和 Claude API（外网），可能需要代理配置

---

## Phase 2：PR 自动评审

**目标**：实现完整的 PR 自动评审流程，交付团队首个业务价值

**依赖**：Phase 1 完成

### 里程碑

#### M2.1 PR Diff 提取与预处理
- [ ] 通过 Gitea API 获取 PR diff
- [ ] Diff 解析：变更文件列表、变更行、上下文代码
- [ ] 大型 PR 检测与分批策略（按文件数 / 总行数阈值分组）
- [ ] 变更文件的完整内容获取（非仅 diff 片段）

#### M2.2 评审 Prompt 工程
- [ ] 设计评审 prompt 模板，覆盖四个维度：风格/逻辑/安全/架构
- [ ] 严重程度分级指令（CRITICAL / ERROR / WARNING / INFO）
- [ ] 输出格式定义（结构化 JSON，便于解析）
- [ ] Java 代码评审专项 prompt（Spring Boot 常见问题、MyBatis 等）
- [ ] Vue 代码评审专项 prompt（组件设计、响应式、XSS 等）
- [ ] 项目编码规范文件集成（如仓库中存在 .code-standards 等）

#### M2.3 评审结果解析与回写
- [ ] 解析 Claude 输出的结构化评审结果
- [ ] 逐行 comment：通过 Gitea PR Review API 添加行级评论
- [ ] 整体 summary：生成总结评论并发布
- [ ] 自动 Request Changes：当存在 CRITICAL 或 ERROR 时触发
- [ ] 评审结果持久化到数据库（便于后续统计分析）

#### M2.4 重新评审机制
- [ ] PR 更新（新 push）时自动触发重新评审
- [ ] 增量评审策略：仅评审新变更的文件（可选）
- [ ] 避免重复评审（防抖机制：短时间内多次 push 只触发最后一次）

#### M2.5 评审配置
- [ ] 仓库级评审规则配置：可自定义哪些维度开启、严重程度阈值
- [ ] 文件过滤：可配置忽略特定文件/目录（如生成代码、配置文件）
- [ ] 评审者身份配置：Gitea bot 账户的显示名和头像

### 交付物
- 完整可用的 PR 自动评审功能
- 评审 prompt 模板库（Java / Vue）
- 评审配置文档

### 验证标准
- 创建一个包含已知问题的 PR → 自动评审 → 正确识别问题并分级 → 逐行 comment + summary → 严重问题自动打回
- 更新 PR 后自动重新评审
- Java 和 Vue 仓库各验证一轮

---

## Phase 3：Issue 自动分析与修复

**目标**：实现 Issue 到修复 PR 的自动化闭环

**依赖**：Phase 1 完成（Phase 2 非必须依赖，可并行开发）

### 里程碑

#### M3.1 Issue 信息采集
- [ ] 监听 Issue 标签变更事件（Webhook）
- [ ] `auto-fix` 标签触发任务创建
- [ ] 幂等性保证：同一 Issue 不重复触发
- [ ] 采集 Issue 标题、描述、所有评论
- [ ] 关联信息提取：错误日志、堆栈跟踪、复现步骤识别

#### M3.2 信息充分性判断
- [ ] 设计信息充分性评估 prompt
- [ ] 信息不足时：自动在 Issue 中回复追问模板
- [ ] 自动添加 `waiting-info` 标签
- [ ] 监听 Issue 新评论 → 判断信息是否已补充 → 恢复分析

#### M3.3 根因分析
- [ ] 基于 Issue 关键词的代码库搜索策略
- [ ] 调用链分析（从入口到异常点）
- [ ] 根因分析报告生成：定位文件/方法/行号，解释原因
- [ ] 分析报告作为 Issue 评论发布

#### M3.4 自动修复与 PR 创建
- [ ] 从目标仓库的默认分支创建修复分支（命名规范：`auto-fix/issue-{id}`）
- [ ] Claude Code 在容器内执行代码修改
- [ ] 修复后运行现有测试（如有），确保不引入回归
- [ ] 推送分支并通过 Gitea API 创建 PR
- [ ] PR 描述模板：根因分析 + 修复方案 + 影响范围 + 关联 Issue
- [ ] PR 自动关联 Issue（`fixes #xxx`）

#### M3.5 修复质量保障
- [ ] 修复代码的基础验证（编译通过、测试通过）
- [ ] 修复 PR 自动进入 Phase 2 的评审流程（Claude 自审）
- [ ] 修复失败时自动在 Issue 中报告失败原因

### 交付物
- 完整的 Issue → 分析 → 修复 → PR 自动化流程
- 信息不足追问机制
- 修复 PR 模板

### 验证标准
- 创建一个描述完整的 bug Issue → 添加 `auto-fix` 标签 → 自动分析根因 → 自动创建修复 PR → PR 关联 Issue
- 创建一个描述不足的 Issue → 添加标签 → 自动追问 → 补充信息后自动恢复分析
- Java 和 Vue 仓库各验证一轮

---

## Phase 4：集中化测试（测试补全）

**目标**：补全现有测试缺口，建立变更驱动的测试生成机制

**依赖**：Phase 1 完成

### 里程碑

#### M4.1 测试覆盖率分析
- [ ] Java 项目：集成 JaCoCo 覆盖率报告解析
- [ ] Vue 项目：集成 Istanbul/c8 覆盖率报告解析
- [ ] 识别未覆盖的关键模块/方法（按业务重要性排序）

#### M4.2 测试用例生成
- [ ] Java 单元测试生成（JUnit 5）
  - [ ] Service 层测试（Mock 依赖）
  - [ ] Controller 层测试（MockMvc）
  - [ ] 工具类测试
- [ ] Vue 单元测试生成（Vitest / Jest）
  - [ ] 组件测试
  - [ ] Composable / Store 测试
  - [ ] 工具函数测试
- [ ] 集成测试生成
- [ ] 生成的测试必须能通过运行（编译正确、断言合理）

#### M4.3 测试补全任务
- [ ] 定时任务：周期性扫描覆盖率 → 生成缺失测试 → 创建 PR
- [ ] 手动触发：通过 CLI / API 指定仓库/模块触发测试生成
- [ ] PR 描述包含：新增覆盖的模块、测试策略说明

#### M4.4 变更驱动测试
- [ ] 监听 PR 合并事件
- [ ] 分析合并的代码变更
- [ ] 判断是否有对应测试覆盖
- [ ] 缺失时自动生成测试并创建 PR

### 交付物
- 测试覆盖率分析工具
- Java / Vue 测试生成能力
- 定时补全 + 变更驱动的测试生成流程

### 验证标准
- 指定一个测试覆盖率低的模块 → 自动生成测试 → 测试可运行通过 → 覆盖率提升
- 合并一个无测试覆盖的 PR → 自动检测 → 生成对应测试 PR

---

## Phase 5：AI 驱动 E2E 模拟用户测试

**目标**：实现 AI 规划和执行的端到端测试，形成完整的自动化质量闭环

**依赖**：Phase 1 + Phase 3（Issue 自动创建能力）

### 里程碑

#### M5.1 测试环境对接
- [ ] 对接团队现有的测试环境部署流程
- [ ] 部署状态检测：确认测试环境可用后再执行测试
- [ ] 环境配置管理：测试 URL、账号等

#### M5.2 E2E 测试框架
- [ ] Playwright 集成
- [ ] Docker 镜像扩展：加入浏览器环境
- [ ] 截图 / 录屏能力

#### M5.3 AI 测试规划
- [ ] 基于需求文档/页面结构生成测试场景
- [ ] 测试路径规划：核心业务流程优先
- [ ] 测试数据准备策略

#### M5.4 测试执行与报告
- [ ] AI 驱动 Playwright 执行测试
- [ ] 异常检测：页面错误、接口报错、UI 异常
- [ ] 测试报告生成：成功/失败用例、截图、操作路径
- [ ] 失败时自动创建 Gitea Issue（附完整复现信息）
- [ ] 通知相关人员

#### M5.5 回归测试
- [ ] 记录测试用例为可重复执行的脚本
- [ ] PR 合并到主分支后自动触发回归测试
- [ ] 测试结果趋势分析

### 交付物
- AI 驱动的 E2E 测试能力
- 自动化测试 → Issue 创建的闭环
- 回归测试套件

### 验证标准
- 针对一个 Web 应用 → AI 自动规划测试路径 → 执行 E2E 测试 → 发现已知 bug → 自动创建 Issue（含截图和操作步骤）

---

## 阶段依赖关系

```
Phase 1 (基础设施)
  ├── Phase 2 (PR 评审)
  ├── Phase 3 (Issue 修复) ──┐
  ├── Phase 4 (测试补全)     │
  └──────────────────────────┴── Phase 5 (E2E 测试)
```

- Phase 2、3、4 在 Phase 1 完成后可并行启动（但建议按顺序，逐步交付价值）
- Phase 5 依赖 Phase 1 的基础设施和 Phase 3 的 Issue 自动创建能力

---

## 建议实施顺序与节奏

| 阶段 | 说明 | 建议排期 |
|------|------|---------|
| Phase 1 | 基础设施——所有后续功能的基座 | 第一优先 |
| Phase 2 | PR 评审——最快产出业务价值，团队感知最明显 | 紧接 Phase 1 |
| Phase 3 | Issue 修复——第二业务价值，需要 Phase 2 的评审做质量兜底 | Phase 2 之后 |
| Phase 4 | 测试补全——补足质量基础，为 Phase 5 做准备 | Phase 3 之后 |
| Phase 5 | E2E 测试——最复杂，依赖前面所有基础 | 最后实施 |

---

## 长期演进方向（不在当前范围内）

以下为未来可能的扩展方向，仅做记录，不纳入当前计划：

- **跨仓库关联分析**：Issue 涉及多个仓库时的联动分析与修复
- **知识积累**：评审意见和修复方案的知识库沉淀，提升后续分析效率
- **指标看板**：评审通过率、修复成功率、测试覆盖率趋势等可视化
- **Gitea Actions 集成**：将部分流程迁移到 Gitea Actions 中运行
- **更多通知渠道**：企业微信 / 钉钉 / 飞书深度集成
- **自适应评审**：根据历史评审数据自动调整评审严格度
