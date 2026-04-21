#!/bin/bash
# Maven CLI 包装器 —— 强制使用持久化 volume 作为本地仓库路径。
#
# 设计目的：
#   fix_issue / gen_tests 任务容器把 named volume 挂载到 /workspace/.m2/repository，
#   实现跨任务的 Maven 依赖缓存。为防止 Claude Code 生成的命令传入
#   `-Dmaven.repo.local=/tmp/...` 等覆盖参数导致缓存失效，包装器在所有 mvn 调用中：
#     1) 过滤掉任何 `-Dmaven.repo.local=*` 与 `--define=maven.repo.local=*` 参数；
#     2) 在参数列表前强制追加 `-Dmaven.repo.local=/workspace/.m2/repository`。
#
# 安装位置（由 Dockerfile.worker-full 烘焙到镜像）：
#   /usr/local/bin/mvn       —— 本包装器（PATH 命中的入口）
#   /usr/local/bin/mvn.real  —— 指向 /opt/apache-maven-*/bin/mvn 的符号链接
#
# 为什么放在镜像里而不是运行时写入：
#   容器 /tmp 为 noexec tmpfs（见 internal/worker/docker.go HostConfig.Tmpfs），
#   entrypoint 在容器启动后无法把包装器写到任何受 noexec 约束之外的可执行位置。
#   把包装器烘焙进镜像的 /usr/local/bin 可完全规避该限制。
#
# 兼容性：
#   仅过滤合并形式 `-Dkey=value`。分离形式 `-D key value` / `--define key=value`
#   在 Claude 生成的命令中未观察到，若未来出现再扩展过滤规则。
set -euo pipefail

REAL_MVN="/usr/local/bin/mvn.real"
FORCED_LOCAL_REPO="/workspace/.m2/repository"

ARGS=()
for arg in "$@"; do
    case "$arg" in
        -Dmaven.repo.local=*) ;;
        --define=maven.repo.local=*) ;;
        *) ARGS+=("$arg") ;;
    esac
done

# bash 对空数组 + set -u 的兼容写法：${arr[@]+"${arr[@]}"} 在 arr 空时展开为空。
exec "${REAL_MVN}" "-Dmaven.repo.local=${FORCED_LOCAL_REPO}" ${ARGS[@]+"${ARGS[@]}"}
