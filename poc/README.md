# DTWorkflow PoC - Claude Code CLI Docker 验证

本目录用于验证 Claude Code CLI 在 Docker 容器中运行的可行性，是 DTWorkflow M1.0 PoC 阶段的基础设施组件。

## 前提条件

- Docker 20.10+
- Docker Compose v2+
- 有效的 Anthropic API Key

## 快速开始

### 1. 准备环境变量

```bash
cp .env.example .env
# 编辑 .env，填入真实的 ANTHROPIC_API_KEY
# 如使用代理，还需设置 ANTHROPIC_BASE_URL
```

### 2. 创建测试工作区

```bash
mkdir -p test-workspace
```

### 3. 构建镜像

```bash
docker compose build
```

### 4. 启动容器

```bash
# 交互式进入容器
docker compose run --rm claude-worker -c "bash"

# 或者直接执行单个命令
docker compose run --rm claude-worker -c "claude --version"
```

### 5. 在容器内运行验证

```bash
# 进入容器后，验证 Claude Code CLI 是否可用
claude --version

# 运行一个简单的验证任务（非交互模式）
claude --print "请输出 Hello, DTWorkflow!" --no-permission-check
```

## 文件说明

| 文件 | 说明 |
|------|------|
| `Dockerfile` | 镜像定义：Debian Bookworm + Node.js 20 + Claude Code CLI |
| `docker-compose.yml` | 服务编排：资源限制、环境变量注入、卷挂载 |
| `.dockerignore` | 构建时忽略的文件列表 |
| `.env.example` | 环境变量模板（不含真实密钥） |
| `test-workspace/` | 挂载到容器 /workspace 的本地目录 |

## 资源限制

容器运行时受以下限制约束：
- CPU：最大 2 核
- 内存：最大 4GB

## 注意事项

- `.env` 文件包含敏感信息，已加入 `.gitignore`，**请勿提交到版本控制**
- 容器以非 root 用户 `claude-worker` 运行，提升安全性
- 网络使用自定义 bridge 网络 `dtworkflow-poc`，确保访问内外网
