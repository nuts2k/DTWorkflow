#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# DTWorkflow 回滚脚本
# 用法: scripts/rollback.sh [ssh-host]
# 示例: scripts/rollback.sh companytest
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# 加载本机部署配置（如存在，设置 DEPLOY_HOST 等本机变量）
[ -f "$PROJECT_ROOT/deploy/local.env" ] && source "$PROJECT_ROOT/deploy/local.env"

DEPLOY_HOST="${1:-${DEPLOY_HOST:-companytest}}"
DEPLOY_DIR="/opt/dtworkflow"

echo "=== DTWorkflow 回滚 ==="
echo "目标主机: $DEPLOY_HOST"
echo ""

# --- 1. 读取版本信息 ---
echo "[1/5] 读取版本信息..."
CURRENT="$(ssh "$DEPLOY_HOST" "cat $DEPLOY_DIR/meta/current_version 2>/dev/null" || true)"
PREVIOUS="$(ssh "$DEPLOY_HOST" "cat $DEPLOY_DIR/meta/previous_version 2>/dev/null" || true)"

if [ -z "$PREVIOUS" ]; then
    echo "错误: 无可回滚版本（meta/previous_version 为空）"
    exit 1
fi

echo "  当前版本: ${CURRENT:-<未知>}"
echo "  回滚目标: $PREVIOUS"
echo ""
read -p "确认回滚到 $PREVIOUS？(y/N) " -n 1 -r
echo ""
if [[ ! $REPLY =~ ^[Yy]$ ]]; then
    echo "已取消"
    exit 0
fi

TARGET_APP="dtworkflow:${PREVIOUS}"
TARGET_WORKER="dtworkflow-worker:${PREVIOUS}"

# --- 2. 检查目标镜像是否存在 ---
echo "[2/5] 检查目标镜像..."
for img in "$TARGET_APP" "$TARGET_WORKER"; do
    if ! ssh "$DEPLOY_HOST" "docker image inspect '$img' >/dev/null 2>&1"; then
        # 尝试从 releases 目录重新导入
        BASENAME="$(echo "$img" | tr ':' '-')"
        TAR_PATH="$DEPLOY_DIR/releases/${BASENAME}.tar"
        echo "  镜像 $img 不存在，尝试从 $TAR_PATH 恢复..."
        if ssh "$DEPLOY_HOST" "test -f '$TAR_PATH'"; then
            ssh "$DEPLOY_HOST" "docker load -i '$TAR_PATH'"
            echo "  已恢复 $img"
        else
            echo "错误: 镜像 $img 不存在且 tar 包也不存在: $TAR_PATH"
            exit 1
        fi
    else
        echo "  $img 已存在"
    fi
done

# --- 3. 更新 .env ---
echo "[3/5] 更新 .env..."
ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && \
    sed -i 's|^APP_IMAGE=.*|APP_IMAGE=${TARGET_APP}|' .env && \
    sed -i 's|^WORKER_IMAGE=.*|WORKER_IMAGE=${TARGET_WORKER}|' .env"
echo "  .env 已更新"

# --- 4. 重启服务 ---
echo "[4/5] 重启服务..."
ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && \
    echo '${CURRENT}' > meta/previous_version && \
    docker compose up -d && \
    echo '${PREVIOUS}' > meta/current_version"
echo "  服务已重启"

# --- 5. 健康检查 ---
echo "[5/5] 健康检查..."
sleep 5

get_remote_dtworkflow_health() {
    ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && \
        container_id=\$(docker compose ps -q dtworkflow) && \
        if [ -z \"\$container_id\" ]; then \
            echo missing; \
        else \
            docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{else}}{{.State.Status}}{{end}}' \"\$container_id\"; \
        fi" 2>/dev/null || echo unreachable
}

HEALTH_OK=false
for i in $(seq 1 6); do
    HEALTH_STATUS="$(get_remote_dtworkflow_health)"
    if [ "$HEALTH_STATUS" = "healthy" ]; then
        HEALTH_OK=true
        break
    fi
    echo "  重试 ($i/6)，当前状态: $HEALTH_STATUS"
    sleep 5
done

if [ "$HEALTH_OK" = true ]; then
    echo ""
    echo "=== 回滚成功 ==="
    echo "当前版本: $PREVIOUS"
    ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && docker compose ps"
else
    echo ""
    echo "=== 回滚后健康检查失败 ==="
    echo "请手动排查："
    echo "  ssh $DEPLOY_HOST 'cd $DEPLOY_DIR && docker compose logs dtworkflow'"
    exit 1
fi
