# DTWorkflow PoC - Claude Code CLI 运行环境
# 基础镜像：Debian Bookworm Slim + Node.js 20 LTS
FROM debian:bookworm-slim

# 设置非交互式安装，避免 apt 弹出交互提示
ENV DEBIAN_FRONTEND=noninteractive

# 安装基础工具和依赖
RUN apt-get update && apt-get install -y --no-install-recommends \
    curl \
    wget \
    git \
    jq \
    ca-certificates \
    gnupg \
    && rm -rf /var/lib/apt/lists/*

# 安装 Node.js 20 LTS（通过 NodeSource 官方脚本）
RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
    && apt-get install -y --no-install-recommends nodejs \
    && rm -rf /var/lib/apt/lists/*

# 安装 Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# 创建非 root 用户 claude-worker，提升安全性
RUN groupadd --gid 1001 claude-worker \
    && useradd --uid 1001 --gid claude-worker --shell /bin/bash --create-home claude-worker

# 设置工作目录
WORKDIR /workspace

# 将工作目录所有权交给 claude-worker 用户
RUN chown claude-worker:claude-worker /workspace

# 切换到非 root 用户运行
USER claude-worker

# 使用 bash 作为入口点，保持灵活性以便执行各种命令
ENTRYPOINT ["/bin/bash"]
