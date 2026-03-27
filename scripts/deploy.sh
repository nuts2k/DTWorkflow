#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# DTWorkflow 部署脚本
# 用法: scripts/deploy.sh <version> [ssh-host]
# 示例: scripts/deploy.sh v0.2.0 dtworkflow-test
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# --- 参数 ---
VERSION="${1:-}"
DEPLOY_HOST="${2:-${DEPLOY_HOST:-dtworkflow-test}}"
DEPLOY_DIR="/opt/dtworkflow"

if [ -z "$VERSION" ]; then
    echo "用法: $0 <version> [ssh-host]"
    echo "示例: $0 v0.2.0 dtworkflow-test"
    echo ""
    echo "ssh-host 默认值取自环境变量 DEPLOY_HOST，当前: ${DEPLOY_HOST}"
    exit 1
fi

APP_IMAGE="dtworkflow:${VERSION}"
WORKER_IMAGE="dtworkflow-worker:${VERSION}"
RELEASE_DIR="$PROJECT_ROOT/.releases/${VERSION}"

echo "=== DTWorkflow 部署 ==="
echo "版本: $VERSION"
echo "目标主机: $DEPLOY_HOST"
echo "部署目录: $DEPLOY_DIR"
echo ""

# --- 1. 校验本地发布产物 ---
echo "[1/7] 校验发布产物..."
APP_TAR="$RELEASE_DIR/dtworkflow-${VERSION}.tar"
WORKER_TAR="$RELEASE_DIR/dtworkflow-worker-${VERSION}.tar"

for f in "$APP_TAR" "$WORKER_TAR"; do
    if [ ! -f "$f" ]; then
        echo "错误: 发布产物不存在: $f"
        echo "请先运行: scripts/build-release.sh $VERSION"
        exit 1
    fi
done
echo "  产物就绪"

# --- 2. 校验服务器连通性与 dtworkflow.yaml ---
echo "[2/7] 校验服务器..."
ssh "$DEPLOY_HOST" "test -d $DEPLOY_DIR" || {
    echo "错误: 服务器目录 $DEPLOY_DIR 不存在，请先完成首次初始化"
    exit 1
}
ssh "$DEPLOY_HOST" "test -f $DEPLOY_DIR/dtworkflow.yaml" || {
    echo "错误: 服务器配置 $DEPLOY_DIR/dtworkflow.yaml 不存在，请手动创建"
    exit 1
}
ssh "$DEPLOY_HOST" "test -f $DEPLOY_DIR/.env" || {
    echo "错误: 服务器 $DEPLOY_DIR/.env 不存在，请基于 deploy/.env.example 创建"
    exit 1
}
echo "  服务器就绪"

# --- 3. 上传发布产物 ---
echo "[3/7] 上传发布产物..."
ssh "$DEPLOY_HOST" "mkdir -p $DEPLOY_DIR/releases"
scp "$APP_TAR" "$DEPLOY_HOST:$DEPLOY_DIR/releases/"
scp "$WORKER_TAR" "$DEPLOY_HOST:$DEPLOY_DIR/releases/"
scp "$PROJECT_ROOT/deploy/docker-compose.prod.yml" "$DEPLOY_HOST:$DEPLOY_DIR/docker-compose.yml"
echo "  上传完成"

# --- 4. 导入镜像 ---
echo "[4/7] 导入镜像..."
ssh "$DEPLOY_HOST" "docker load -i $DEPLOY_DIR/releases/dtworkflow-${VERSION}.tar"
ssh "$DEPLOY_HOST" "docker load -i $DEPLOY_DIR/releases/dtworkflow-worker-${VERSION}.tar"
echo "  镜像导入完成"

# --- 5. 更新 .env 中的镜像版本（仅更新 APP_IMAGE 和 WORKER_IMAGE） ---
echo "[5/7] 更新镜像版本..."
ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && \
    sed -i 's|^APP_IMAGE=.*|APP_IMAGE=${APP_IMAGE}|' .env && \
    sed -i 's|^WORKER_IMAGE=.*|WORKER_IMAGE=${WORKER_IMAGE}|' .env"
echo "  .env 已更新"

# --- 6. 记录版本并重启服务 ---
echo "[6/7] 重启服务..."
ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && \
    mkdir -p meta && \
    (cat meta/current_version 2>/dev/null > meta/previous_version || true) && \
    docker compose up -d && \
    echo '${VERSION}' > meta/current_version"
echo "  服务已重启"

# --- 7. 健康检查 ---
echo "[7/7] 健康检查..."
echo "  等待服务启动..."
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
    echo "=== 部署成功 ==="
    echo "版本: $VERSION"
    echo "主机: $DEPLOY_HOST"
    ssh "$DEPLOY_HOST" "cd $DEPLOY_DIR && docker compose ps"
else
    echo ""
    echo "=== 健康检查失败 ==="
    echo "服务可能未正常启动，请排查："
    echo "  ssh $DEPLOY_HOST 'cd $DEPLOY_DIR && docker compose ps'"
    echo "  ssh $DEPLOY_HOST 'cd $DEPLOY_DIR && docker compose logs dtworkflow'"
    echo ""
    echo "如需回滚: scripts/rollback.sh $DEPLOY_HOST"
    exit 1
fi
