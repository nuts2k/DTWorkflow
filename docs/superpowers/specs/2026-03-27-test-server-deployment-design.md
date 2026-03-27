# DTWorkflow 测试服务器部署与发布方案设计

**日期**：2026-03-27
**状态**：评审中
**范围**：测试服务器部署、镜像发布、版本管理、回滚机制、配置边界
**适用阶段**：Phase 2 PR 自动评审能力内测上线

---

## 1. 背景与目标

当前 DTWorkflow 已基本完成 Phase 2 的自动 PR 评审主链路，具备部署到公司内部测试服务器进行持续验证和真实仓库试运行的条件。

本次设计的目标不是一次性建设完整 CI/CD 平台，而是在**最小复杂度**前提下，提供一套可落地、可重复、可回滚的测试环境发布机制，支撑后续内部试运行与迭代。

### 1.1 设计目标

1. **一键发布**：在开发机执行标准命令，完成构建、传输、部署。
2. **版本可追踪**：每次部署都有明确版本号，对应 Git tag 与镜像 tag。
3. **可回滚**：新版本异常时，可快速切回上一版本。
4. **配置边界清晰**：敏感配置保留在服务器本地，不被发布流程覆盖。
5. **适配当前现状**：复用现有 Docker、Compose、Go 构建体系，不引入私有镜像仓库、CI/CD 等额外基础设施。
6. **适配目标平台**：发布产物必须明确面向 `linux/amd64`，避免开发机架构与测试服务器架构不一致导致部署失败。

### 1.2 已知约束

| 项目 | 约束 |
|------|------|
| 服务器环境 | Debian 裸机，已安装 Docker 和 Docker Compose |
| 连接方式 | SSH 直连，用户通过 `~/.ssh/config` 中的别名访问 |
| 镜像分发 | 本地构建后通过 `docker save` + `scp` + `docker load` 分发 |
| 配置管理 | `dtworkflow.yaml` 仅保存在服务器本地 |
| Compose 管理 | 仓库内维护测试服务器专用 `docker-compose` 文件 |
| 自动化范围 | 第一版需要版本管理与回滚能力，不建设完整 CI/CD |
| 部署目录 | `/opt/dtworkflow` |
| 目标平台 | Debian Linux amd64 |

---

## 2. 方案对比与结论

### 2.1 方案 A：单脚本打包所有职责

使用一个 `deploy.sh` 同时完成构建、版本管理、传输、部署与回滚。

**优点**：文件少，上手快。
**缺点**：脚本会快速膨胀，本地逻辑与远程逻辑耦合，调试困难。

### 2.2 方案 B：构建与部署分离（推荐）

将流程拆分为：
- 本地构建发布包
- 本地触发远程部署
- 独立回滚脚本
- 仓库内维护测试环境专用 Compose 文件

**优点**：
- 构建与部署职责清晰
- 支持“构建一次，部署多次”
- 易于定位构建失败、传输失败、部署失败
- 后续平滑升级到 CI/CD 或 Registry 时改造成本低

**缺点**：文件会比单脚本方案多 2~3 个。

### 2.3 方案 C：服务器直接构建

将源码同步到服务器后，在服务器执行 `docker compose build`。

**排除理由**：
- 服务器需要具备 Go、npm 构建依赖访问能力
- 构建环境与开发环境漂移风险更高
- 构建失败与部署失败混在一起，不利于回滚

### 2.4 结论

采用**方案 B：构建与部署分离**。

---

## 3. 总体架构设计

部署体系分为四层：本地构建层、本地部署层、服务器运行层、版本回滚层。

### 3.1 本地构建层

职责：
- 校验工作区状态
- 生成发布版本号
- 构建主应用镜像与 Worker 镜像
- 导出为可传输的 tar 包
- 验证镜像平台为 `linux/amd64`

输出产物：
- `dtworkflow:<version>`
- `dtworkflow-worker:<version>`
- 对应 `tar` 文件

该层不直接修改服务器状态，只负责生成稳定的发布产物。

### 3.2 本地部署层

职责：
- 将镜像 tar 包与部署文件同步到服务器
- 通过 SSH 触发服务器上的镜像导入与服务重启
- 更新服务器侧版本记录
- 执行健康检查并输出结果

该层只负责“发布动作”，不承担构建职责。

### 3.3 服务器运行层

职责：
- 保存服务器本地配置（`dtworkflow.yaml`）
- 保存部署编排文件（`docker-compose.yml`）
- 运行 Redis 与 DTWorkflow 主服务
- 使用已加载到本地 Docker 的镜像启动服务
- 持久化运行数据（SQLite / Redis）

服务器不依赖 Go 或 npm 构建链路，也不需要访问外部依赖源。

### 3.4 版本与回滚层

职责：
- 每次部署记录当前版本与上一版本
- 回滚时切换镜像 tag 并重启服务
- 保证回滚不需要重新构建镜像
- 当目标镜像不存在时，可从服务器保留的 tar 包重新导入

核心原则：
- 每次发布必须有统一版本号
- app 与 worker 使用同一版本号
- 回滚是“切换版本”而不是“重新构建”

---

## 4. 仓库结构设计

建议在仓库中新增如下结构：

```text
deploy/
  docker-compose.prod.yml
  .env.example
scripts/
  build-release.sh
  deploy.sh
  rollback.sh
```

### 4.1 `deploy/docker-compose.prod.yml`

职责：
- 测试服务器专用编排文件
- 使用 `image:` 而非 `build:`
- 通过服务器 `.env` 注入镜像版本、部署目录、Docker GID 等变量
- 将服务器 `.env` 中的镜像版本变量与容器进程环境明确接线

说明：
- 与当前开发用 `docker-compose.yml` 分离
- 避免把 macOS 开发环境兼容逻辑混入测试服务器部署逻辑

### 4.2 `deploy/.env.example`

职责：
- 作为**服务器端 `.env` 文件模板**，定义 Compose 变量契约
- 供首次初始化服务器目录时复制生成 `/opt/dtworkflow/.env`

建议变量：

```bash
DEPLOY_DIR=/opt/dtworkflow
DOCKER_GID=999
APP_IMAGE=dtworkflow:v0.2.0
WORKER_IMAGE=dtworkflow-worker:v0.2.0
```

说明：
- 该文件只描述**服务器端 Compose 变量**。
- SSH 主机别名不写入服务器 `.env`，而由本地 `scripts/deploy.sh` 通过参数或本地环境变量传入，例如 `DEPLOY_HOST=dtworkflow-test`。
- 不应包含 Gitea token、Claude API key、Webhook secret 等敏感信息。

### 4.3 `scripts/build-release.sh`

职责：
- 校验发布前置条件
- 使用 `docker buildx build --platform linux/amd64 --load` 构建两个镜像
- 校验两个镜像的 `Os/Architecture` 均为 `linux/amd64`
- 导出 tar 包
- 生成本地发布产物目录

### 4.4 `scripts/deploy.sh`

职责：
- 将发布产物传输到服务器
- 在服务器执行 `docker load`
- **只更新服务器 `.env` 中的 `APP_IMAGE` 与 `WORKER_IMAGE`**
- 覆盖更新服务器 `docker-compose.yml`
- 启动或更新服务
- 记录当前版本与上一个版本
- 执行健康检查

说明：
- `scripts/deploy.sh` 不得覆盖服务器 `.env` 中长期持有的 `DEPLOY_DIR`、`DOCKER_GID`。
- `scripts/deploy.sh` 也不得覆盖服务器本地 `dtworkflow.yaml`。

### 4.5 `scripts/rollback.sh`

职责：
- 读取服务器上的上一版本记录
- 将 `.env` 中的镜像版本切换回上一版本
- 若目标镜像在本地 Docker 中不存在，则优先从 `releases/` 重新 `docker load`
- 重新执行 `docker compose up -d`

---

## 5. 服务器目录布局设计

建议固定部署目录为 `/opt/dtworkflow`，结构如下：

```text
/opt/dtworkflow/
  docker-compose.yml
  dtworkflow.yaml
  .env
  releases/
    dtworkflow-v0.2.0.tar
    dtworkflow-worker-v0.2.0.tar
  shared/
    data/
    redis/
  meta/
    current_version
    previous_version
```

### 5.1 文件职责说明

#### `docker-compose.yml`
- 来自仓库维护的测试环境 Compose 文件副本
- 可由部署脚本覆盖更新
- 不包含敏感信息

#### `dtworkflow.yaml`
- 服务器本地真实运行配置
- 包含 Gitea token、Claude API key、Webhook secret 等敏感信息
- 首次初始化时人工创建
- 后续部署脚本只检查是否存在，**不得覆盖**

#### `.env`
- 服务器本地部署变量
- 用于给 Compose 注入镜像 tag、部署目录、Docker GID 等
- 只保存 Compose 变量，不保存敏感业务配置

示例：

```bash
DEPLOY_DIR=/opt/dtworkflow
DOCKER_GID=999
APP_IMAGE=dtworkflow:v0.2.0
WORKER_IMAGE=dtworkflow-worker:v0.2.0
```

#### `releases/`
- 保存发布镜像 tar 包
- 用于排障与必要时的重新导入
- 回滚时若本地镜像已被清理，可从这里恢复
- 可按策略仅保留最近若干版本

#### `shared/data/`
- 用于持久化 SQLite 数据
- 避免容器重建导致数据丢失

#### `shared/redis/`
- 用于持久化 Redis 数据
- 避免 Redis 容器重建导致队列状态完全丢失

#### `meta/current_version` / `meta/previous_version`
- 用于记录当前已切换版本与上一版本
- `current_version` 表示当前 Compose 已切换到的目标版本
- `previous_version` 表示回滚目标版本
- 版本文件主要服务于回滚，而不是表示“最后一次健康验证成功版本”

---

## 6. 版本管理设计

### 6.1 版本号规则

采用统一语义化版本：
- `v0.1.1`
- `v0.2.0`
- `v1.0.0`

同一次发布中：
- 主应用镜像：`dtworkflow:vX.Y.Z`
- Worker 镜像：`dtworkflow-worker:vX.Y.Z`

**约束**：主应用与 Worker 不拆分独立版本，以降低部署与回滚复杂度。

### 6.2 Git tag 策略

建议流程：
1. 本地构建成功
2. 再创建 Git tag
3. 再执行部署

这样可以避免“tag 已创建，但镜像构建失败”的脏状态。

### 6.3 版本记录策略

每次部署时：
1. 读取旧的 `current_version`
2. `docker compose up -d` 切换到新版本
3. 写入：
   - `previous_version = 旧 current_version`
   - `current_version = 新版本`
4. 再执行健康检查

这样设计的原因是：
- 一旦 Compose 已切换到新版本，`previous_version` 就必须立即成为可回滚目标
- 即使健康检查失败，回滚脚本也能准确切回上一版本

说明：
- 健康检查失败时，不自动改回版本文件，而是保留现场并提示执行回滚脚本。
- 因此，版本文件表达的是“已切换版本关系”，不是“最后一次健康版本关系”。

---

## 7. 发布流程设计

### 7.1 首次初始化流程

首次部署与日常发布必须分离。

首次初始化只做一次，建议包括：
1. 创建 `/opt/dtworkflow` 目录结构
2. 上传 `docker-compose.yml`
3. 基于 `deploy/.env.example` 创建服务器 `.env`
4. 人工创建 `dtworkflow.yaml`
5. 检查 Docker、Compose 是否可用
6. 检查端口占用
7. 检测 `/var/run/docker.sock` 的 GID 并写入服务器 `.env`
8. 首次启动并校验 `/healthz`

首次初始化不要求完整自动化，但应有明确脚本或标准步骤。

### 7.2 日常发布流程

#### 阶段 A：本地构建发布包

执行顺序：
1. 校验 git 工作区干净
2. 校验目标版本 tag 不存在
3. 运行测试与基础构建验证
4. 使用 `docker buildx build --platform linux/amd64 --load` 构建 app 镜像
5. 使用 `docker buildx build --platform linux/amd64 --load` 构建 worker 镜像
6. 校验两个镜像的平台均为 `linux/amd64`
7. 导出两个 tar 包
8. 创建版本 tag

#### 阶段 B：部署到服务器

执行顺序：
1. 上传 tar 包与最新 Compose 文件
2. SSH 登录服务器
3. `docker load` 导入镜像
4. 仅更新服务器 `.env` 中的 `APP_IMAGE` 与 `WORKER_IMAGE`
5. 执行 `docker compose up -d`
6. 写入 `meta/current_version` 与 `meta/previous_version`
7. 执行健康检查

### 7.3 回滚流程

回滚时不重新构建：
1. 读取 `meta/previous_version`
2. 检查目标版本镜像是否存在
3. 若镜像不存在，则从 `releases/` 中对应 tar 包重新 `docker load`
4. 修改 `.env` 中的 `APP_IMAGE` / `WORKER_IMAGE`
5. 执行 `docker compose up -d`
6. 交换版本记录

回滚本质上是“切换到旧镜像版本”。

---

## 8. 配置边界设计

### 8.1 `dtworkflow.yaml` 的边界

`dtworkflow.yaml` 仅保存在服务器本地，原因如下：
- 包含敏感信息
- 同时承载运行时业务配置（如评审等级、忽略规则、Repo 覆盖配置）
- 不应由构建或发布脚本覆盖

部署脚本只能：
- 检查它是否存在
- 不存在时报错并停止部署

部署脚本不能：
- 覆盖真实配置
- 从仓库同步真实配置
- 自动生成带敏感值的配置

### 8.2 Compose 文件的边界

Compose 文件由仓库维护，职责是：
- 定义服务拓扑
- 定义卷挂载与端口映射
- 定义镜像引用方式
- 将服务器 `.env` 中的 Compose 变量接线到容器运行环境

Compose 文件不应承载：
- 敏感业务配置
- 运行时 token
- 服务器专属密钥

### 8.3 Worker 镜像版本的管理

当前项目中，应用层已经具备通过环境变量覆盖 `worker.image` 的能力：
- `internal/config/config.go:176-180` 启用了环境变量覆盖
- `internal/config/config.go:204` 已注册 `worker.image` 默认值
- `internal/cmd/serve.go:473` 运行时直接读取 `cfg.Worker.Image`

因此，本次部署设计**不需要改造应用配置系统本身**，需要补齐的是：

**服务器 `.env` → Compose → 容器进程环境** 的接线链路。

推荐目标形态：
- `dtworkflow.yaml` 中仍可保留默认值
- Compose 通过服务器 `.env` 将 `WORKER_IMAGE` 映射为容器环境变量 `DTWORKFLOW_WORKER_IMAGE`
- 部署时只需要更新服务器 `.env` 中的 `WORKER_IMAGE`，无需编辑 `dtworkflow.yaml`

---

## 9. Compose 设计要点

### 9.1 使用 `image:` 而非 `build:`

测试服务器部署文件必须引用本地已加载镜像：

```yaml
services:
  dtworkflow:
    image: ${APP_IMAGE}
```

原因：
- 服务器不承担构建职责
- 避免部署时重新拉取依赖
- 保证部署版本与本地构建产物完全一致

### 9.2 必须显式接入 `DTWORKFLOW_*` 环境变量

Compose 自动读取 `.env` 只用于**变量替换**，不会自动把 `.env` 内容注入到容器进程环境。

因此，部署文件中必须显式声明：

```yaml
services:
  dtworkflow:
    image: ${APP_IMAGE}
    environment:
      DTWORKFLOW_REDIS_ADDR: redis:6379
      DTWORKFLOW_DATABASE_PATH: /app/data/dtworkflow.db
      DTWORKFLOW_WORKER_IMAGE: ${WORKER_IMAGE}
```

其中：
- `DTWORKFLOW_REDIS_ADDR` 让容器内服务访问 Compose 内部的 Redis 服务名
- `DTWORKFLOW_DATABASE_PATH` 让 SQLite 指向容器内挂载的数据目录
- `DTWORKFLOW_WORKER_IMAGE` 将服务器 `.env` 中的 `WORKER_IMAGE` 接入应用运行时配置

这是本次部署设计的关键接线点。

### 9.3 Docker socket GID 动态注入

当前开发环境中使用 `group_add: ["0"]` 适配 macOS Docker Desktop。测试服务器为 Debian，应改为：

```yaml
group_add:
  - "${DOCKER_GID}"
```

由部署或初始化脚本通过 `stat` 等方式自动探测 `/var/run/docker.sock` 的 GID，并写入服务器 `.env`。

### 9.4 固定挂载路径

建议统一挂载：
- `${DEPLOY_DIR}/dtworkflow.yaml:/app/dtworkflow.yaml:ro`
- `${DEPLOY_DIR}/shared/data:/app/data`
- `${DEPLOY_DIR}/shared/redis:/data`
- `/var/run/docker.sock:/var/run/docker.sock`

这样可以避免路径依赖当前登录用户目录，提升可维护性。

### 9.5 运行服务范围

测试服务器上长期运行的服务仅应包括：
- `redis`
- `dtworkflow`

Worker 镜像不需要作为常驻服务启动；它只作为 DTWorkflow 在运行时创建任务容器时使用的镜像。

---

## 10. 发布前检查与发布后验证

### 10.1 发布前检查

为避免明显有问题的版本进入测试服务器，第一版建议至少执行：

1. git 工作区干净
2. 目标版本 tag 不存在
3. `make test` 通过
4. `make build-linux` 成功
5. app 与 worker 镜像构建成功
6. 两个镜像的平台均为 `linux/amd64`

### 10.2 发布后验证

部署完成后，脚本应自动执行：
- `docker compose ps`
- 检查主容器状态
- 请求 `/healthz`
- 请求 `/readyz`

如果验证失败：
- 脚本明确提示部署失败
- 提示用户执行回滚脚本
- 保留现场供排障，不做自动强制回滚

### 10.3 不自动回滚的原因

第一版不建议自动回滚，原因如下：
1. 失败可能来自环境或配置，而非镜像版本本身
2. 自动回滚会隐藏现场，增加排障成本
3. 测试服阶段更适合“自动验证 + 人工决定是否回滚”

---

## 11. 排障与运维建议

建议统一排障入口：

- 服务状态：`docker compose ps`
- 应用日志：`docker compose logs dtworkflow`
- Redis 日志：`docker compose logs redis`
- 当前版本：`meta/current_version`
- 上一版本：`meta/previous_version`

建议在部署文档或脚本输出中固定这些指令，降低维护成本。

---

## 12. 第一版明确不做的事项

为了保持方案聚焦，第一版明确不做：

- 私有镜像仓库
- CI/CD 自动发布流水线
- 多环境矩阵管理
- 自动失败回滚
- 配置中心 / Secret Manager
- 蓝绿部署 / 滚动发布
- 高可用 Redis / 外部数据库

理由：当前目标是**支撑内部测试服稳定上线和持续迭代**，而非建设完整发布平台。

---

## 13. 实施后预期效果

方案落地后，应达到以下效果：

1. 开发机可通过标准命令完成构建与发布。
2. 测试服务器只负责运行，不承担构建职责。
3. 每次上线都有清晰的 Git tag 与镜像版本对应关系。
4. `dtworkflow.yaml` 始终只保存在服务器本地，不会被覆盖。
5. 新版本异常时，可通过单独命令快速回滚。
6. Worker 镜像版本可通过部署变量切换，无需手工改 `dtworkflow.yaml`。
7. 后续如需升级到 Registry 或 CI/CD，可在现有边界上平滑演进。

---

## 14. 后续实施建议

建议按以下顺序落地：

1. 增加测试服务器专用 `deploy/docker-compose.prod.yml`
2. 增加部署目录初始化脚本或初始化步骤说明
3. 实现 `scripts/build-release.sh`
4. 实现 `scripts/deploy.sh`
5. 实现 `scripts/rollback.sh`
6. 在 Compose 中补齐 `WORKER_IMAGE -> DTWORKFLOW_WORKER_IMAGE` 接线
7. 本地验证发布包构建流程
8. 在测试服务器完成首次初始化与试部署
9. 用真实发布版本完成一次完整“发布 → 验证 → 回滚演练”

---

## 15. 决策摘要

| 决策项 | 结论 |
|------|------|
| 部署模式 | 本地构建 + SSH 传输 + 服务器加载镜像 |
| 方案形态 | 构建与部署分离 |
| 部署目录 | `/opt/dtworkflow` |
| 敏感配置 | `dtworkflow.yaml` 仅保存在服务器本地 |
| Compose 管理 | 仓库维护测试环境专用 Compose 文件 |
| 版本管理 | 统一语义化版本，app 与 worker 共用版本号 |
| 镜像平台 | 统一构建为 `linux/amd64` |
| 回滚策略 | 手动触发回滚，不自动失败回滚 |
| worker 版本切换 | 通过服务器 `.env` + Compose 显式传递到 `DTWORKFLOW_WORKER_IMAGE` |
| 第一版边界 | 不引入 Registry、CI/CD、自动回滚 |

---

## 16. 待实施阶段确认项

以下内容在实施前仍需再次确认：

1. 测试服务器是否存在额外网络限制（如访问 Gitea、Claude 代理地址、Docker Hub/GHCR）。
2. SQLite 与 Redis 数据目录是否需要额外备份策略。
3. 是否需要在发布脚本中加入更详细的部署审计输出（发布时间、执行人、目标主机）。
4. 是否需要在服务器端限制 `releases/` 保留版本数量，避免磁盘持续增长。

---

**结论**：该方案满足当前内部测试服务器上线需求，复杂度适中，具备清晰边界、可操作性和后续演进空间，适合作为 DTWorkflow Phase 2 内测部署基线方案。
