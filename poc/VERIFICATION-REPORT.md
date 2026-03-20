# M1.0 PoC 验证报告

> 本报告记录 DTWorkflow 项目 M1.0 PoC 阶段的验证结果。
> 目标：确认 Claude Code CLI 在 Docker 容器中可行，并建立基线性能数据。

---

## 执行环境

| 项目 | 值 |
|------|-----|
| 验证日期 | 2026-03-20 |
| 宿主机操作系统 | macOS (Darwin 25.3.0, arm64) |
| 容器操作系统 | Debian GNU/Linux 12 (bookworm) |
| CPU 架构 | aarch64 |
| Docker 版本 | Docker Desktop 4.65.0, Engine 29.2.1 |
| Docker 镜像 | `dtworkflow-claude-worker:poc` (727MB) |
| API 接入方式 | 代理 (ANTHROPIC_BASE_URL) |

---

## 验证项 1：容器内安装与运行

**验证脚本**：`poc/scripts/verify-install.sh`

### 状态

- [x] 通过 (5/5 PASS)

### 测试详情

| 子项 | 预期结果 | 实际结果 | 状态 |
|------|----------|----------|------|
| `claude` 命令存在 | 路径可找到 | /usr/bin/claude | PASS |
| `claude --version` 输出版本号 | 版本字符串 | 2.1.80 (Claude Code) | PASS |
| 非交互模式 `claude -p` 可用 | 正确返回响应 | 响应: OK | PASS |
| `--output-format json` 输出有效 JSON | JSON 格式 | 有效 JSON，含 type/duration_ms 等字段 | PASS |
| `--output-format stream-json --verbose` 流式输出 | 逐行 JSON | 首行含 type:system, subtype:init | PASS |

### 关键参数

```
Docker 基础镜像：debian:bookworm-slim
Node.js 版本：v20.20.1
npm 版本：10.8.2
Claude Code CLI 版本：2.1.80
镜像最终大小：727MB
```

### 踩坑记录

- 问题：`--output-format stream-json` 必须搭配 `--verbose` 使用，否则报错
  解决：在自动化脚本中统一添加 `--verbose` 参数

---

## 验证项 2：API Key 认证

**验证脚本**：`poc/scripts/verify-auth.sh`

### 状态

- [x] 通过 (6/6 PASS)

### 测试详情

| 子项 | 预期结果 | 实际结果 | 状态 |
|------|----------|----------|------|
| ANTHROPIC_API_KEY 环境变量已设置 | 变量可读 | Key 前缀: ptx_***（长度: 77） | PASS |
| ANTHROPIC_BASE_URL 已配置 | URL 可读 | https://api.portunex.gewulabs.group | PASS |
| API 认证有效 | claude -p 返回结果 | 调用成功 | PASS |
| API Key 未泄露到日志文件 | 日志中无明文 Key | 检查 /var/log /tmp /home 等路径，未发现 | PASS |
| API Key 未写入配置文件 | 配置目录无 Key | 检查 ~/.config ~/.anthropic ~/.claude /etc，未发现 | PASS |
| ~/.config/claude 无敏感文件名 | 无敏感文件 | 目录不存在（Key 通过环境变量传递） | PASS |

### 关键参数

```
认证方式：环境变量 ANTHROPIC_API_KEY + ANTHROPIC_BASE_URL
API 接入：通过代理网关（非直连 api.anthropic.com）
Key 注入方式：docker-compose env_file 指令
```

### 踩坑记录

- 问题：宿主机的 `ANTHROPIC_BASE_URL` 环境变量（本地代理 127.0.0.1:15800）通过 docker-compose `${VAR}` 语法覆盖了 .env 文件中的值，导致容器内指向 127.0.0.1（容器自身）而连接被拒绝
  解决：改用 `env_file` 指令直接加载 .env 到容器，不受宿主机同名环境变量干扰

---

## 验证项 3：代码操作能力

**验证脚本**：`poc/scripts/verify-code-ops.sh`

### 状态

- [x] 通过 (7/7 PASS)

### 测试详情

| 子项 | 预期结果 | 实际结果 | 状态 |
|------|----------|----------|------|
| 创建测试 Go 项目 | 文件存在 | /tmp/test-repo 下 main.go, go.mod | PASS |
| Claude 读取并分析代码 | 返回分析结果 | "简单的 Go 程序，打印 Hello World 并定义了加法函数" | PASS |
| Claude 修改代码文件（添加 multiply 函数） | 文件被修改 | 文件已修改，multiply 函数已添加 | PASS |
| Claude 执行 Git init | .git 目录创建 | .git 目录已创建 | PASS |
| Git add 和 commit 成功 | 提交记录存在 | 提交: f048d18 初始提交 | PASS |
| Claude 创建并切换 Git 分支 | 分支切换成功 | 当前分支: feature/test-branch | PASS |
| Git 仓库状态验证 | 提交和分支正确 | 提交数: 1, 分支: feature/test-branch, master | PASS |

### 关键参数

```
代码操作成功率：7 / 7
权限模式：--dangerously-skip-permissions（自动化场景必需）
运行用户：claude-worker（非 root）
```

### 踩坑记录

- 问题：Claude Code CLI 默认要求用户交互式确认文件写入和 Git 操作权限，导致自动化场景下修改文件和执行 Git 命令失败（提示"需要用户批准"）
  解决：在自动化调用中添加 `--dangerously-skip-permissions` 参数跳过权限确认。生产环境中应使用更细粒度的权限控制（如 `--allowedTools`）

---

## 验证项 4：资源与性能基线

**验证脚本**：`poc/scripts/verify-performance.sh`

### 状态

- [x] 通过（满足基线要求）

### 测试详情

| 指标 | 预期基线 | 实际测量值（平均） | 最小值 | 最大值 | 状态 |
|------|----------|-------------------|--------|--------|------|
| 推理耗时（简单 prompt） | < 30000ms | 2620ms | 2431ms | 2731ms | PASS |
| 容器内存使用 | < 1GB | ~5MB（cgroup） | - | - | PASS |
| 镜像大小 | < 2GB | 727MB | - | - | PASS |

### 原始数据

```json
{
  "iterations": 3,
  "inference_time_ms": {
    "avg": 2620,
    "min": 2431,
    "max": 2731,
    "values": [2731, 2431, 2700]
  },
  "memory_mb": {
    "final": 5,
    "values": [5, 6, 5]
  }
}
```

### 关键参数

```
测试迭代次数：3
测试时间：2026-03-20
prompt：回复OK
输出格式：--output-format json
```

### 踩坑记录

- 问题：性能测量脚本原设计为宿主机运行（依赖 docker 和 bc 命令），在容器内无法执行
  解决：重写脚本适配容器内运行环境，使用 cgroup 接口读取内存，bash 算术替代 bc

---

## 验证项 5：跨平台验证

**验证脚本**：`poc/scripts/verify-platform.sh`

### 状态

- [x] macOS (arm64) - 通过
- [ ] Linux (amd64) - 待验证

### 测试详情（macOS arm64）

| 项目 | macOS arm64 | Linux amd64 | 备注 |
|------|------------|-------------|------|
| Docker 版本 | 29.2.1 | 待验证 | |
| 内核版本 | 6.12.76-linuxkit | 待验证 | Docker Desktop 使用 LinuxKit |
| Node.js 版本 | v20.20.1 | 待验证 | |
| Claude CLI 版本 | 2.1.80 | 待验证 | |
| api.anthropic.com 连通 | 不可达（使用代理） | 待验证 | |
| ANTHROPIC_BASE_URL 连通 | 可达 | 待验证 | |
| 镜像构建成功 | PASS | 待验证 | |
| 容器运行正常 | PASS | 待验证 | |

### 原始数据

```json
{
  "os": "Debian GNU/Linux 12 (bookworm)",
  "kernel": "6.12.76-linuxkit",
  "arch": "aarch64",
  "cpu_cores": "8",
  "memory_total_mb": "7836",
  "memory_available_mb": "7238",
  "node_version": "v20.20.1",
  "npm_version": "10.8.2",
  "claude_cli_version": "2.1.80 (Claude Code)",
  "git_version": "git version 2.39.5",
  "user": "claude-worker",
  "network": {
    "api_anthropic_reachable": false,
    "base_url_reachable": true,
    "base_url": "https://api.portunex.gewulabs.group"
  }
}
```

### 踩坑记录

- 问题：平台信息脚本原设计依赖宿主机 docker 和 bc 命令，容器内不可用
  解决：重写脚本在容器内收集信息，使用 /proc/meminfo、nproc 等 Linux 原生接口

---

## 结论与建议

### 总体结论

| 验证项 | 结论 |
|--------|------|
| 1. 容器内安装与运行 | **通过** (5/5) |
| 2. API Key 认证 | **通过** (6/6) |
| 3. 代码操作能力 | **通过** (7/7) |
| 4. 资源与性能基线 | **通过** |
| 5. 跨平台验证 | **部分通过**（macOS 通过，Linux 待验证） |
| **M1.0 PoC 整体** | **通过** |

### M1.0 可行性评估

Claude Code CLI 在 Docker 容器中运行**完全可行**。主要发现：

1. **安装与运行**：基于 Debian + Node.js 20 的镜像可正常安装和运行 Claude Code CLI，镜像体积 727MB 可接受
2. **API 认证**：通过环境变量注入 API Key 和 Base URL 安全可靠，Key 不会泄露到容器文件系统
3. **代码操作**：Claude Code 可在容器内读取、修改代码文件并执行 Git 操作，自动化场景需使用 `--dangerously-skip-permissions`
4. **性能**：简单 prompt 平均响应 ~2.6s，容器内存占用极低（~5MB），单机可并发运行多个 Worker 容器
5. **代理支持**：通过 `ANTHROPIC_BASE_URL` 可灵活对接代理网关，适应内网环境

### 后续建议

- [ ] 在 Debian (Linux amd64) 生产环境中验证跨平台兼容性
- [ ] 评估复杂任务（大型 PR 评审、代码修复）的推理耗时和内存占用
- [ ] 建立容器复用机制（warm pool）减少冷启动开销
- [ ] 评估并发容器数量对宿主机资源的影响
- [ ] 研究 `--allowedTools` 替代 `--dangerously-skip-permissions` 实现细粒度权限控制
- [ ] 优化 Dockerfile 减小镜像体积（多阶段构建、清理缓存）

### 风险与缓解

| 风险 | 严重程度 | 缓解措施 |
|------|----------|----------|
| `--dangerously-skip-permissions` 带来安全风险 | 中 | 生产环境使用 `--allowedTools` 白名单机制，限制可执行的工具 |
| 代理网关单点故障影响所有 Worker | 中 | 配置代理高可用或直连 API 作为降级方案 |
| 复杂任务内存占用可能大幅增长 | 低 | docker-compose 已设置 4GB 内存上限，超出时自动 OOM Kill |
| Linux amd64 平台尚未验证 | 低 | 镜像基于 Debian 标准基础镜像，预期兼容性良好，需实际验证 |

---

*报告生成日期：2026-03-20*
*验证工具：`poc/scripts/verify-*.sh`*
