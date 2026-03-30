# 测试服务器部署与发布方案 实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 基于设计文档 `docs/superpowers/specs/2026-03-27-test-server-deployment-design.md`，实现一套构建与部署分离的测试服务器发布机制，支撑 Phase 2 PR 自动评审的内测上线。

**Architecture:** 本地构建层生成 `linux/amd64` 镜像 tar 包，本地部署层通过 SSH+SCP 将产物传输到测试服务器并触发 `docker load` + `docker compose up -d`，服务器运行层只负责运行不承担构建。版本通过 `meta/current_version` 和 `meta/previous_version` 文件追踪，回滚通过切换镜像 tag 实现。

**Tech Stack:** Bash (脚本), Docker Buildx (跨平台镜像构建), Docker Compose (服务编排), SSH/SCP (远程传输), Make (任务入口)

**设计文档:** `docs/superpowers/specs/2026-03-27-test-server-deployment-design.md`

---

### Task 1: 创建测试服务器专用 Compose 文件

**Files:**
- Create: `deploy/docker-compose.prod.yml`

**Step 1: 创建 deploy 目录和 Compose 文件**

`deploy/docker-compose.prod.yml` 与开发用 `docker-compose.yml` 的关键区别：
- 使用 `image:` 而非 `build:`
- 通过 `${APP_IMAGE}` / `${WORKER_IMAGE}` 变量引用镜像
- 通过 `${DOCKER_GID}` 动态注入 Docker socket GID
- 通过 `${DEPLOY_DIR}` 引用服务器部署目录
- 显式将 `WORKER_IMAGE` 接线为容器环境变量 `DTWORKFLOW_WORKER_IMAGE`
- 不包含 `worker-image` 构建 profile
- 数据卷使用 bind mount 到 `${DEPLOY_DIR}/shared/` 下

```yaml
services:
  redis:
    image: redis:7-alpine
    ports:
      - "127.0.0.1:6379:6379"
    volumes:
      - ${DEPLOY_DIR}/shared/redis:/data
    command: redis-server --appendonly yes
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 5s
      timeout: 3s
      retries: 5
    networks:
      - dtworkflow-net
    restart: unless-stopped

  dtworkflow:
    image: ${APP_IMAGE}
    command: ["serve", "--config", "/app/dtworkflow.yaml"]
    ports:
      - "0.0.0.0:8080:8080"
    depends_on:
      redis:
        condition: service_healthy
    healthcheck:
      test: ["CMD", "curl", "-sf", "http://localhost:8080/readyz"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 10s
    group_add:
      - "${DOCKER_GID}"
    environment:
      DTWORKFLOW_REDIS_ADDR: redis:6379
      DTWORKFLOW_DATABASE_PATH: /app/data/dtworkflow.db
      DTWORKFLOW_WORKER_IMAGE: ${WORKER_IMAGE}
    volumes:
      - ${DEPLOY_DIR}/dtworkflow.yaml:/app/dtworkflow.yaml:ro
      - ${DEPLOY_DIR}/shared/data:/app/data
      - /var/run/docker.sock:/var/run/docker.sock
    networks:
      - dtworkflow-net
    restart: unless-stopped

networks:
  dtworkflow-net:
    driver: bridge
```

**Step 2: 验证 Compose 文件语法**

Run: `docker compose -f deploy/docker-compose.prod.yml config --quiet 2>&1 || echo "语法检查结果见上方"`

注意：由于变量未设置会有警告，这是预期行为。关键是不能有 YAML 语法错误。可以设置临时变量验证：

Run: `APP_IMAGE=test WORKER_IMAGE=test DOCKER_GID=999 DEPLOY_DIR=/tmp docker compose -f deploy/docker-compose.prod.yml config --quiet && echo "PASS"`

Expected: PASS

**Step 3: Commit**

```bash
git add deploy/docker-compose.prod.yml
git commit -m "deploy: 新增测试服务器专用 Compose 编排文件

使用 image 引用而非 build，通过 .env 变量注入镜像版本、Docker GID、
部署目录，显式接线 DTWORKFLOW_WORKER_IMAGE 环境变量。"
```

---

### Task 2: 创建服务器端环境变量模板

**Files:**
- Create: `deploy/.env.example`

**Step 1: 创建 .env.example**

该文件是服务器端 `/opt/dtworkflow/.env` 的模板，定义 Compose 变量契约。

```bash
# DTWorkflow 测试服务器部署变量
# 复制此文件到服务器 /opt/dtworkflow/.env 并修改

# 部署目录（与服务器实际路径一致）
DEPLOY_DIR=/opt/dtworkflow

# Docker socket GID（运行 stat -c '%g' /var/run/docker.sock 获取）
DOCKER_GID=999

# 镜像版本（由 deploy.sh 自动更新，首次需手动填写）
APP_IMAGE=dtworkflow:v0.2.0
WORKER_IMAGE=dtworkflow-worker:v0.2.0
```

**Step 2: Commit**

```bash
git add deploy/.env.example
git commit -m "deploy: 新增服务器端 .env 模板文件

定义 Compose 变量契约：DEPLOY_DIR、DOCKER_GID、APP_IMAGE、WORKER_IMAGE。"
```

---

### Task 3: 创建构建发布脚本

**Files:**
- Create: `scripts/build-release.sh`

**Step 1: 编写 build-release.sh**

脚本职责：校验工作区 -> 校验版本 -> 运行测试 -> 构建双镜像 -> 校验平台 -> 导出 tar 包 -> 创建 Git tag。

```bash
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
RELEASE_DIR="$PROJECT_ROOT/.releases/${VERSION}"

echo "=== DTWorkflow 构建发布 ==="
echo "版本: $VERSION"
echo "App 镜像: $APP_IMAGE"
echo "Worker 镜像: $WORKER_IMAGE"
echo ""

# --- 1. 校验 git 工作区干净 ---
echo "[1/8] 校验 git 工作区..."
if [ -n "$(git status --porcelain)" ]; then
    echo "错误: git 工作区不干净，请先提交或暂存所有更改"
    git status --short
    exit 1
fi
echo "  工作区干净"

# --- 2. 校验版本 tag 不存在 ---
echo "[2/8] 校验版本 tag..."
if git rev-parse "$VERSION" >/dev/null 2>&1; then
    echo "错误: tag $VERSION 已存在"
    exit 1
fi
echo "  tag $VERSION 可用"

# --- 3. 运行测试 ---
echo "[3/8] 运行测试..."
make test
echo "  测试通过"

# --- 4. 构建 linux/amd64 二进制验证 ---
echo "[4/8] 验证 linux/amd64 交叉编译..."
make build-linux
echo "  交叉编译成功"

# --- 5. 构建 App 镜像 ---
echo "[5/8] 构建 App 镜像 ($APP_IMAGE)..."

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
echo "[6/8] 构建 Worker 镜像 ($WORKER_IMAGE)..."
docker buildx build \
    --platform linux/amd64 \
    --load \
    -f build/docker/Dockerfile.worker \
    -t "$WORKER_IMAGE" \
    .
echo "  Worker 镜像构建完成"

# --- 7. 校验镜像平台 ---
echo "[7/8] 校验镜像平台..."
for img in "$APP_IMAGE" "$WORKER_IMAGE"; do
    PLATFORM="$(docker inspect --format='{{.Os}}/{{.Architecture}}' "$img")"
    if [ "$PLATFORM" != "linux/amd64" ]; then
        echo "错误: 镜像 $img 平台为 $PLATFORM，预期 linux/amd64"
        exit 1
    fi
    echo "  $img -> $PLATFORM"
done

# --- 8. 导出 tar 包 ---
echo "[8/8] 导出镜像 tar 包..."
mkdir -p "$RELEASE_DIR"
docker save "$APP_IMAGE" -o "$RELEASE_DIR/dtworkflow-${VERSION}.tar"
docker save "$WORKER_IMAGE" -o "$RELEASE_DIR/dtworkflow-worker-${VERSION}.tar"

echo ""
echo "=== 构建完成 ==="
echo "发布产物目录: $RELEASE_DIR"
ls -lh "$RELEASE_DIR"
echo ""
echo "下一步:"
echo "  1. 创建 tag:  git tag $VERSION && git push origin $VERSION"
echo "  2. 部署到服务器: scripts/deploy.sh $VERSION [host]"
```

**Step 2: 添加可执行权限**

Run: `chmod +x scripts/build-release.sh`

**Step 3: 验证脚本语法**

Run: `bash -n scripts/build-release.sh && echo "语法 OK"`

Expected: 语法 OK

**Step 4: Commit**

```bash
git add scripts/build-release.sh
git commit -m "scripts: 新增构建发布脚本 build-release.sh

校验工作区和版本 tag，运行测试，构建 linux/amd64 双镜像，
校验平台，导出 tar 包到 .releases/ 目录。"
```

---

### Task 4: 创建部署脚本

**Files:**
- Create: `scripts/deploy.sh`

**Step 1: 编写 deploy.sh**

脚本职责：传输产物 -> docker load -> 更新 .env 镜像版本 -> 更新 Compose -> 重启服务 -> 记录版本 -> 健康检查。

```bash
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
```

**Step 2: 添加可执行权限**

Run: `chmod +x scripts/deploy.sh`

**Step 3: 验证脚本语法**

Run: `bash -n scripts/deploy.sh && echo "语法 OK"`

Expected: 语法 OK

**Step 4: Commit**

```bash
git add scripts/deploy.sh
git commit -m "scripts: 新增部署脚本 deploy.sh

传输 tar 包到服务器，docker load 导入镜像，仅更新 .env 中
APP_IMAGE 和 WORKER_IMAGE，重启服务并执行健康检查。"
```

---

### Task 5: 创建回滚脚本

**Files:**
- Create: `scripts/rollback.sh`

**Step 1: 编写 rollback.sh**

```bash
#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# DTWorkflow 回滚脚本
# 用法: scripts/rollback.sh [ssh-host]
# 示例: scripts/rollback.sh dtworkflow-test
# ============================================================

DEPLOY_HOST="${1:-${DEPLOY_HOST:-dtworkflow-test}}"
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
```

**Step 2: 添加可执行权限**

Run: `chmod +x scripts/rollback.sh`

**Step 3: 验证脚本语法**

Run: `bash -n scripts/rollback.sh && echo "语法 OK"`

Expected: 语法 OK

**Step 4: Commit**

```bash
git add scripts/rollback.sh
git commit -m "scripts: 新增回滚脚本 rollback.sh

读取 previous_version，检查目标镜像是否存在（不存在则从 tar 恢复），
切换 .env 镜像版本并重启服务。"
```

---

### Task 6: 更新 Makefile 添加发布目标

**Files:**
- Modify: `Makefile`

**Step 1: 在 Makefile 中添加 release 和 deploy 目标**

在 `docker-build` 目标之后、`help` 目标之前添加：

```makefile
## release: 构建发布包（用法：make release VERSION=v0.2.0）
release:
ifndef VERSION
	$(error 请指定版本号: make release VERSION=v0.2.0)
endif
	scripts/build-release.sh $(VERSION)

## deploy: 部署到测试服务器（用法：make deploy VERSION=v0.2.0 HOST=dtworkflow-test）
deploy:
ifndef VERSION
	$(error 请指定版本号: make deploy VERSION=v0.2.0)
endif
	scripts/deploy.sh $(VERSION) $(HOST)

## rollback: 回滚测试服务器（用法：make rollback HOST=dtworkflow-test）
rollback:
	scripts/rollback.sh $(HOST)
```

同时更新 `.PHONY` 行，追加 `release deploy rollback`。

**Step 2: 验证 Makefile**

Run: `make help`

Expected: 输出中包含 `release`、`deploy`、`rollback` 三个新目标

**Step 3: Commit**

```bash
git add Makefile
git commit -m "build: Makefile 新增 release/deploy/rollback 目标

提供 make release、make deploy、make rollback 快捷入口。"
```

---

### Task 7: 更新 .gitignore 排除发布产物

**Files:**
- Modify: `.gitignore`

**Step 1: 在 .gitignore 中添加发布产物排除**

追加以下行：

```
# 发布产物（镜像 tar 包）
.releases/
```

**Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: .gitignore 排除 .releases/ 发布产物目录"
```

---

### Task 8: 验证完整构建流程（本地 dry-run）

**Step 1: 确认脚本可执行**

Run: `ls -la scripts/build-release.sh scripts/deploy.sh scripts/rollback.sh`

Expected: 三个文件都有 `x` 执行权限

**Step 2: 验证 build-release.sh 参数校验**

Run: `scripts/build-release.sh`

Expected: 输出用法提示并退出码 1

Run: `scripts/build-release.sh bad-version`

Expected: 输出版本号格式错误并退出码 1

**Step 3: 验证 deploy.sh 参数校验**

Run: `scripts/deploy.sh`

Expected: 输出用法提示并退出码 1

**Step 4: 验证 Compose 文件完整性**

Run: `APP_IMAGE=dtworkflow:v0.2.0 WORKER_IMAGE=dtworkflow-worker:v0.2.0 DOCKER_GID=999 DEPLOY_DIR=/opt/dtworkflow docker compose -f deploy/docker-compose.prod.yml config`

Expected: 输出完整渲染后的 Compose 配置，无错误。确认：
- `dtworkflow` 服务 image 为 `dtworkflow:v0.2.0`
- environment 中包含 `DTWORKFLOW_WORKER_IMAGE: dtworkflow-worker:v0.2.0`
- `group_add` 包含 `"999"`
- 卷挂载使用 `/opt/dtworkflow/` 前缀

**Step 5（可选）: 完整构建测试**

如果当前环境支持 Docker Buildx 且想做完整验证：

Run: `scripts/build-release.sh v0.0.0-test`

Expected: 完整通过 8 个步骤，在 `.releases/v0.0.0-test/` 下生成两个 tar 文件

清理测试产物：

```bash
rm -rf .releases/v0.0.0-test
docker rmi dtworkflow:v0.0.0-test dtworkflow-worker:v0.0.0-test 2>/dev/null || true
```

---

## 附录：服务器首次初始化步骤（人工操作）

以下步骤在测试服务器上人工执行一次，不属于自动化脚本范围：

```bash
# 1. 创建目录结构
sudo mkdir -p /opt/dtworkflow/{releases,shared/data,shared/redis,meta}
sudo chown -R $USER:$USER /opt/dtworkflow

# 2. 基于模板创建 .env
# 从本地 scp deploy/.env.example 到服务器后：
cp .env.example .env
# 编辑 .env，确认 DOCKER_GID：
stat -c '%g' /var/run/docker.sock
# 将结果写入 .env 的 DOCKER_GID

# 3. 创建 dtworkflow.yaml（手动填写敏感配置）
# 参考 configs/dtworkflow.example.yaml 模板，重点确认以下字段：
#
# worker:
#   timeouts:
#     review_pr: "15m"   # 默认硬编码仅 10m，大型 PR 评审容易超时
#     fix_issue: "45m"
#     gen_tests: "30m"
#   stream_monitor:
#     enabled: true      # 启用流式心跳监控，检测容器卡住并自动触发重试
#     activity_timeout: "2m"

# 4. 验证 Docker 环境
docker --version
docker compose version
```
