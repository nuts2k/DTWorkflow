#!/usr/bin/env bash
# 验证项5：跨平台信息收集
# 检测运行环境并输出平台信息
# 用法：./verify-platform.sh [IMAGE_NAME]

set -euo pipefail

IMAGE_NAME="${1:-dtworkflow-poc:latest}"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()  { echo -e "${BLUE}[INFO]${NC} $*" >&2; }
log_ok()    { echo -e "${GREEN}[OK]${NC} $*" >&2; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC} $*" >&2; }
log_error() { echo -e "${RED}[ERROR]${NC} $*" >&2; }

# 检测宿主机平台类型
detect_host_platform() {
    local os arch
    os=$(uname -s)
    arch=$(uname -m)
    case "$os" in
        Darwin)
            # macOS：通常使用 Docker Desktop
            echo "macOS Docker Desktop ($arch)"
            ;;
        Linux)
            # Linux：通常使用 Docker Engine
            echo "Linux Docker Engine ($arch)"
            ;;
        *)
            echo "Unknown ($os $arch)"
            ;;
    esac
}

# 获取 Docker 版本信息
get_docker_info() {
    local version engine_version api_version
    version=$(docker version --format '{{.Server.Version}}' 2>/dev/null || echo "unknown")
    api_version=$(docker version --format '{{.Server.APIVersion}}' 2>/dev/null || echo "unknown")
    echo "{\"version\": \"$version\", \"api_version\": \"$api_version\"}"
}

# 获取宿主机内核版本
get_kernel_version() {
    uname -r 2>/dev/null || echo "unknown"
}

# 获取宿主机 CPU 信息
get_cpu_info() {
    local cpu_count cpu_model
    case "$(uname -s)" in
        Darwin)
            cpu_count=$(sysctl -n hw.logicalcpu 2>/dev/null || echo "unknown")
            cpu_model=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "unknown")
            ;;
        Linux)
            cpu_count=$(nproc 2>/dev/null || grep -c ^processor /proc/cpuinfo 2>/dev/null || echo "unknown")
            cpu_model=$(grep "model name" /proc/cpuinfo 2>/dev/null | head -1 | cut -d: -f2 | xargs || echo "unknown")
            ;;
        *)
            cpu_count="unknown"
            cpu_model="unknown"
            ;;
    esac
    # 转义引号用于 JSON
    cpu_model=$(echo "$cpu_model" | sed 's/"/\\"/g')
    echo "{\"count\": \"$cpu_count\", \"model\": \"$cpu_model\"}"
}

# 获取宿主机内存信息（字节）
get_memory_info() {
    local total_bytes
    case "$(uname -s)" in
        Darwin)
            total_bytes=$(sysctl -n hw.memsize 2>/dev/null || echo "0")
            ;;
        Linux)
            local total_kb
            total_kb=$(grep MemTotal /proc/meminfo 2>/dev/null | awk '{print $2}' || echo "0")
            total_bytes=$((total_kb * 1024))
            ;;
        *)
            total_bytes=0
            ;;
    esac
    echo "$total_bytes"
}

# 字节转人类可读
bytes_to_human() {
    local bytes="$1"
    if [[ "$bytes" -ge 1073741824 ]]; then
        printf "%.1f GB" "$(echo "scale=1; $bytes / 1073741824" | bc)"
    elif [[ "$bytes" -ge 1048576 ]]; then
        printf "%.1f MB" "$(echo "scale=1; $bytes / 1048576" | bc)"
    else
        printf "%d KB" $((bytes / 1024))
    fi
}

# 获取容器内 Node.js 信息
get_container_node_info() {
    if ! docker image inspect "$IMAGE_NAME" &>/dev/null; then
        echo "{\"node\": \"image_not_found\", \"npm\": \"image_not_found\"}"
        return
    fi

    local node_ver npm_ver
    node_ver=$(docker run --rm "$IMAGE_NAME" node --version 2>/dev/null || echo "unknown")
    npm_ver=$(docker run --rm "$IMAGE_NAME" npm --version 2>/dev/null || echo "unknown")
    echo "{\"node\": \"$node_ver\", \"npm\": \"$npm_ver\"}"
}

# 检测 Claude Code CLI 版本
get_claude_version() {
    if ! docker image inspect "$IMAGE_NAME" &>/dev/null; then
        echo "image_not_found"
        return
    fi
    docker run --rm \
        -e ANTHROPIC_API_KEY="placeholder" \
        "$IMAGE_NAME" \
        claude --version 2>/dev/null | head -1 || echo "unknown"
}

# 检测网络连通性（能否访问 api.anthropic.com）
check_network_connectivity() {
    local reachable=false
    local latency_ms=-1
    local error_msg=""

    # 先从宿主机测试
    if command -v curl &>/dev/null; then
        local start_ms end_ms
        start_ms=$(date +%s%3N)
        if curl -sf --connect-timeout 5 --max-time 10 \
            "https://api.anthropic.com" -o /dev/null 2>/dev/null; then
            end_ms=$(date +%s%3N)
            latency_ms=$((end_ms - start_ms))
            reachable=true
        else
            local exit_code=$?
            case $exit_code in
                6)  error_msg="DNS 解析失败" ;;
                7)  error_msg="无法连接到主机" ;;
                28) error_msg="连接超时" ;;
                60) error_msg="SSL 证书验证失败" ;;
                *)  error_msg="curl 错误码 $exit_code" ;;
            esac
        fi
    else
        # 尝试 nc
        if nc -z -w5 api.anthropic.com 443 2>/dev/null; then
            reachable=true
        else
            error_msg="curl 不可用，nc 测试也失败"
        fi
    fi

    local json="{\"reachable\": $reachable, \"latency_ms\": $latency_ms"
    if [[ -n "$error_msg" ]]; then
        json="$json, \"error\": \"$error_msg\""
    fi
    json="$json}"
    echo "$json"
}

# 检查 Docker 是否支持跨平台构建（buildx）
check_buildx_support() {
    if docker buildx version &>/dev/null; then
        local buildx_version
        buildx_version=$(docker buildx version 2>/dev/null | head -1 || echo "unknown")
        echo "{\"available\": true, \"version\": \"$buildx_version\"}"
    else
        echo "{\"available\": false, \"version\": null}"
    fi
}

main() {
    log_info "=== 跨平台信息收集开始 ==="

    local timestamp
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    local host_platform
    host_platform=$(detect_host_platform)
    log_info "宿主机平台：$host_platform"

    local docker_info
    docker_info=$(get_docker_info)
    log_info "Docker 版本：$(echo "$docker_info" | jq -r '.version')"

    local kernel_version
    kernel_version=$(get_kernel_version)
    log_info "内核版本：$kernel_version"

    local cpu_info
    cpu_info=$(get_cpu_info)
    log_info "CPU：$(echo "$cpu_info" | jq -r '.model') x$(echo "$cpu_info" | jq -r '.count')"

    local memory_bytes
    memory_bytes=$(get_memory_info)
    log_info "内存：$(bytes_to_human "$memory_bytes")"

    log_info "正在获取容器内 Node.js 信息..."
    local node_info
    node_info=$(get_container_node_info)
    log_info "Node.js：$(echo "$node_info" | jq -r '.node')，npm：$(echo "$node_info" | jq -r '.npm')"

    log_info "正在获取 Claude Code CLI 版本..."
    local claude_version
    claude_version=$(get_claude_version)
    log_info "Claude Code CLI：$claude_version"

    log_info "正在检测网络连通性（api.anthropic.com）..."
    local network_info
    network_info=$(check_network_connectivity)
    local net_reachable
    net_reachable=$(echo "$network_info" | jq -r '.reachable')
    if [[ "$net_reachable" == "true" ]]; then
        log_ok "api.anthropic.com 可达，延迟 $(echo "$network_info" | jq -r '.latency_ms')ms"
    else
        log_warn "api.anthropic.com 不可达：$(echo "$network_info" | jq -r '.error // "未知原因"')"
    fi

    local buildx_info
    buildx_info=$(check_buildx_support)
    log_info "Docker buildx：$(echo "$buildx_info" | jq -r 'if .available then "可用 \(.version)" else "不可用" end')"

    # ---- 镜像是否存在 ----
    local image_exists=false
    local image_size=0
    if docker image inspect "$IMAGE_NAME" &>/dev/null; then
        image_exists=true
        image_size=$(docker image inspect "$IMAGE_NAME" --format '{{.Size}}' 2>/dev/null || echo "0")
    fi

    # ---- 输出 JSON ----
    local json_output
    json_output=$(jq -n \
        --arg ts "$timestamp" \
        --arg host_platform "$host_platform" \
        --argjson docker_info "$docker_info" \
        --arg kernel_version "$kernel_version" \
        --argjson cpu_info "$cpu_info" \
        --argjson memory_bytes "$memory_bytes" \
        --argjson node_info "$node_info" \
        --arg claude_version "$claude_version" \
        --argjson network_info "$network_info" \
        --argjson buildx_info "$buildx_info" \
        --arg image_name "$IMAGE_NAME" \
        --argjson image_exists "$image_exists" \
        --argjson image_size "$image_size" \
        '{
            timestamp: $ts,
            host: {
                platform: $host_platform,
                kernel: $kernel_version,
                cpu: $cpu_info,
                memory_bytes: $memory_bytes,
                docker: $docker_info,
                buildx: $buildx_info
            },
            container: {
                image: $image_name,
                image_exists: $image_exists,
                image_size_bytes: $image_size,
                runtime: $node_info,
                claude_cli_version: $claude_version
            },
            network: $network_info
        }')

    # ---- 输出人类可读摘要 ----
    echo "" >&2
    echo "┌─────────────────────────────────────────────────────┐" >&2
    echo "│              跨平台环境信息摘要                      │" >&2
    echo "├─────────────────────────┬───────────────────────────┤" >&2
    echo "│ 项目                    │ 信息                      │" >&2
    echo "├─────────────────────────┼───────────────────────────┤" >&2
    printf "│ %-23s │ %-25s │\n" "宿主机平台" "$host_platform" >&2
    printf "│ %-23s │ %-25s │\n" "内核版本" "$kernel_version" >&2
    printf "│ %-23s │ %-25s │\n" "Docker 版本" "$(echo "$docker_info" | jq -r '.version')" >&2
    printf "│ %-23s │ %-25s │\n" "内存总量" "$(bytes_to_human "$memory_bytes")" >&2
    echo "├─────────────────────────┼───────────────────────────┤" >&2
    printf "│ %-23s │ %-25s │\n" "Node.js（容器内）" "$(echo "$node_info" | jq -r '.node')" >&2
    printf "│ %-23s │ %-25s │\n" "npm（容器内）" "$(echo "$node_info" | jq -r '.npm')" >&2
    printf "│ %-23s │ %-25s │\n" "Claude CLI" "$claude_version" >&2
    echo "├─────────────────────────┼───────────────────────────┤" >&2
    local net_status
    if [[ "$net_reachable" == "true" ]]; then
        net_status="可达 ($(echo "$network_info" | jq -r '.latency_ms')ms)"
    else
        net_status="不可达"
    fi
    printf "│ %-23s │ %-25s │\n" "api.anthropic.com" "$net_status" >&2
    echo "└─────────────────────────┴───────────────────────────┘" >&2
    echo "" >&2

    # JSON 输出到 stdout
    echo "$json_output"

    log_ok "跨平台信息收集完成"
}

main "$@"
