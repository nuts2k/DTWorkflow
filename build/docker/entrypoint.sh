#!/bin/bash
set -euo pipefail

# ============================================================
# DTWorkflow Worker 容器入口脚本
# 负责：运行环境准备 -> Git 仓库 clone -> PR 分支 checkout -> 执行 Claude Code CLI
# ============================================================

log() { echo "[entrypoint] $*" >&2; }

# --- 准备可写的 HOME 目录 ---
# 容器使用 ReadonlyRootfs，/home/worker 不可写。
# Claude Code CLI 需要 ~/.claude/ 目录存储运行时配置。
# 将 HOME 迁移到 /tmp/.home（tmpfs 可写），并复制 .gitconfig。
export HOME=/tmp/.home
mkdir -p "${HOME}/.claude"
cp /home/worker/.gitconfig "${HOME}/.gitconfig" 2>/dev/null || true
log "HOME 已迁移到 ${HOME}"

# 处理自签名证书场景
if [ "${GIT_SSL_NO_VERIFY:-}" = "true" ]; then
    log "已禁用 Git SSL 证书验证"
fi

# 如果未配置 clone 信息，直接执行传入命令
if [ -z "${REPO_CLONE_URL:-}" ] || [ -z "${GITEA_TOKEN:-}" ]; then
    log "未配置仓库 clone 信息，跳过 clone 阶段"
    exec "$@"
fi

# 构建带认证的 clone URL（token 内嵌方式）
# 格式：https://token:<GITEA_TOKEN>@<host>/<repo>.git
# 使用 bash 字符串操作代替 sed，避免 token 中特殊字符引发问题
PROTO="${REPO_CLONE_URL%%://*}"
REST="${REPO_CLONE_URL#*://}"
AUTH_URL="${PROTO}://token:${GITEA_TOKEN}@${REST}"

# Clone 仓库到工作目录
REPO_DIR="/workspace/repo"
log "正在 clone 仓库: ${REPO_CLONE_URL} -> ${REPO_DIR}"
# 过滤 git 输出中可能包含的 token（认证失败时 git 可能回显完整 URL）
# 所有 git 输出重定向到 stderr，保持 stdout 纯净（供 Claude CLI JSON 输出使用）
if ! git clone "${AUTH_URL}" "${REPO_DIR}" 2>&1 | sed "s|${GITEA_TOKEN}|***|g" >&2; then
    log "clone 失败，退出"
    exit 1
fi
cd "${REPO_DIR}"

# 根据任务类型处理分支
case "${TASK_TYPE:-}" in
    review_pr)
        if [ -n "${PR_NUMBER:-}" ]; then
            # 方式一：通过 Gitea 的 PR ref 获取（refs/pull/<number>/head）
            log "正在获取 PR #${PR_NUMBER} 的代码..."
            if git fetch origin "pull/${PR_NUMBER}/head:pr-${PR_NUMBER}" >&2 2>&1; then
                git checkout "pr-${PR_NUMBER}" >&2 2>&1
                log "通过 PR ref 获取成功"
            elif [ -n "${HEAD_REF:-}" ]; then
                # 方式二：回退到直接 checkout HEAD_REF 分支
                log "PR ref 不可用，回退到分支: ${HEAD_REF}"
                git fetch origin "${HEAD_REF}" >&2 2>&1
                git checkout "FETCH_HEAD" >&2 2>&1
            else
                log "警告：无法获取 PR 分支，使用默认分支"
            fi

            # 获取 base 分支用于 diff 对比
            if [ -n "${BASE_REF:-}" ]; then
                git fetch origin "${BASE_REF}" >&2 2>&1 || true
                log "已获取 base 分支: ${BASE_REF}"
            fi

            log "PR #${PR_NUMBER} 分支已就绪 (HEAD: $(git rev-parse --short HEAD))"
        elif [ -n "${HEAD_REF:-}" ]; then
            log "正在 checkout 分支: ${HEAD_REF}"
            git fetch origin "${HEAD_REF}" >&2 2>&1
            git checkout "FETCH_HEAD" >&2 2>&1
            log "分支已就绪"
        fi
        ;;
    fix_issue)
        log "Issue 修复任务，使用默认分支"
        ;;
    gen_tests)
        log "测试生成任务，使用默认分支"
        ;;
    *)
        log "任务类型: ${TASK_TYPE:-<empty>}，使用默认分支"
        ;;
esac

log "仓库准备完成 ($(git log --oneline -1))"

# --- 评审模式安全加固：锁定 Git 写操作 ---
if [ "${TASK_TYPE:-}" = "review_pr" ]; then
    # P0: 将 push URL 置为无效，防止任何 push 操作
    git remote set-url --push origin no-push-allowed

    # P1: 设置 pre-commit hook 拦截所有 commit 尝试
    mkdir -p .git/hooks
    cat > .git/hooks/pre-commit << 'HOOK'
#!/bin/sh
echo "ERROR: commits are disabled in review mode" >&2
exit 1
HOOK
    chmod +x .git/hooks/pre-commit

    # P1: 清除 Git 凭证，防止 Claude Code 通过 Bash 读取 token 后手动 push
    git remote set-url origin "${REPO_CLONE_URL}"
    git config credential.helper ''

    log "评审模式安全加固已启用（push 已禁用，commit 已拦截，凭证已清除）"
fi

# 清除敏感环境变量，防止 Claude Code 通过 Bash 读取
unset GITEA_TOKEN
unset AUTH_URL
unset GITEA_URL
unset REPO_CLONE_URL

log "开始执行任务命令..."

# 执行传入的命令（通常是 claude -p "..."）
exec "$@"
