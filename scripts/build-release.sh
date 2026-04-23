#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# DTWorkflow 构建发布脚本
# 用法: scripts/build-release.sh <version>
# 示例: scripts/build-release.sh v0.2.0
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$PROJECT_ROOT"

# --- 参数校验 ---
VERSION="${1:-}"
if [ -z "$VERSION" ]; then
    echo "用法: $0 <version>"
    echo "示例: $0 v0.2.0"
    exit 1
fi

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "错误: 版本号格式不正确，应为 vX.Y.Z（如 v0.2.0）"
    exit 1
fi

APP_IMAGE="dtworkflow:${VERSION}"
WORKER_IMAGE="dtworkflow-worker:${VERSION}"
WORKER_FULL_IMAGE="dtworkflow-worker-full:${VERSION}"
RELEASE_DIR="$PROJECT_ROOT/.releases/${VERSION}"

echo "=== DTWorkflow 构建发布 ==="
echo "版本: $VERSION"
echo "App 镜像: $APP_IMAGE"
echo "Worker 镜像: $WORKER_IMAGE"
echo "Worker-Full 镜像: $WORKER_FULL_IMAGE"
echo ""

# --- 1. 校验 git 工作区干净 ---
echo "[1/9] 校验 git 工作区..."
if [ -n "$(git status --porcelain)" ]; then
    echo "错误: git 工作区不干净，请先提交或暂存所有更改"
    git status --short
    exit 1
fi
echo "  工作区干净"

# --- 2. 校验版本 tag 不存在 ---
echo "[2/9] 校验版本 tag..."
if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "错误: tag $VERSION 已存在"
    exit 1
fi
echo "  tag $VERSION 可用"

# --- 3. 运行测试 ---
echo "[3/9] 运行测试..."
make test
echo "  测试通过"

# --- 4. 构建 linux/amd64 二进制验证 ---
echo "[4/9] 验证 linux/amd64 交叉编译..."
make build-linux
echo "  交叉编译成功"

# --- 5. 构建 App 镜像 ---
echo "[5/9] 构建 App 镜像 ($APP_IMAGE)..."

# 计算 LDFLAGS
MODULE="$(go list -m)"
GIT_COMMIT="$(git rev-parse --short HEAD)"
BUILD_TIME="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
LDFLAGS="-X '${MODULE}/internal/cmd.version=${VERSION}' -X '${MODULE}/internal/cmd.gitCommit=${GIT_COMMIT}' -X '${MODULE}/internal/cmd.buildTime=${BUILD_TIME}'"

docker buildx build \
    --platform linux/amd64 \
    --load \
    --build-arg LDFLAGS="$LDFLAGS" \
    -f build/Dockerfile \
    -t "$APP_IMAGE" \
    .
echo "  App 镜像构建完成"

# --- 6. 构建 Worker 镜像 ---
echo "[6/9] 构建 Worker 镜像 ($WORKER_IMAGE)..."
docker buildx build \
    --platform linux/amd64 \
    --load \
    -f build/docker/Dockerfile.worker \
    -t "$WORKER_IMAGE" \
    .
echo "  Worker 镜像构建完成"

# --- 7. 构建 Worker-Full 镜像 ---
echo "[7/9] 构建 Worker-Full 镜像 ($WORKER_FULL_IMAGE)..."
docker buildx build \
    --platform linux/amd64 \
    --load \
    -f build/docker/Dockerfile.worker-full \
    -t "$WORKER_FULL_IMAGE" \
    .
echo "  Worker-Full 镜像构建完成"

# --- 8. 校验镜像平台 ---
echo "[8/9] 校验镜像平台..."
for img in "$APP_IMAGE" "$WORKER_IMAGE" "$WORKER_FULL_IMAGE"; do
    PLATFORM="$(docker inspect --format='{{.Os}}/{{.Architecture}}' "$img")"
    if [ "$PLATFORM" != "linux/amd64" ]; then
        echo "错误: 镜像 $img 平台为 $PLATFORM，预期 linux/amd64"
        exit 1
    fi
    echo "  $img -> $PLATFORM"
done

# --- 9. 导出 tar 包 ---
echo "[9/9] 导出镜像 tar 包..."
mkdir -p "$RELEASE_DIR"
docker save "$APP_IMAGE" -o "$RELEASE_DIR/dtworkflow-${VERSION}.tar"
docker save "$WORKER_IMAGE" -o "$RELEASE_DIR/dtworkflow-worker-${VERSION}.tar"
docker save "$WORKER_FULL_IMAGE" -o "$RELEASE_DIR/dtworkflow-worker-full-${VERSION}.tar"

echo ""
echo "=== 构建完成 ==="
echo "发布产物目录: $RELEASE_DIR"
ls -lh "$RELEASE_DIR"
echo ""
echo "下一步:"
echo "  1. 创建 tag:  git tag $VERSION && git push origin $VERSION"
echo "  2. 部署到服务器: scripts/deploy.sh $VERSION [host]"
