#!/usr/bin/env bash
# 验证项4：资源与性能基线
# 测量 Claude Code CLI 在 Docker 中的性能基线数据
# 用法：./verify-performance.sh [IMAGE_NAME] [ITERATIONS]

set -euo pipefail

# 默认参数
IMAGE_NAME="${1:-dtworkflow-poc:latest}"
ITERATIONS="${2:-3}"
CLAUDE_API_KEY="${CLAUDE_API_KEY:-}"

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

# 检查依赖
check_deps() {
    local missing=()
    for cmd in docker jq bc; do
        if ! command -v "$cmd" &>/dev/null; then
            missing+=("$cmd")
        fi
    done
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "缺少依赖命令：${missing[*]}"
        exit 1
    fi
}

# 检查镜像是否存在
check_image() {
    if ! docker image inspect "$IMAGE_NAME" &>/dev/null; then
        log_error "镜像 $IMAGE_NAME 不存在，请先运行 docker build"
        exit 1
    fi
    log_ok "镜像 $IMAGE_NAME 存在"
}

# 测量容器冷启动耗时（毫秒）
measure_cold_start() {
    local start_ms end_ms elapsed_ms
    # 使用 date +%s%3N 获取毫秒时间戳
    start_ms=$(date +%s%3N)

    # 运行容器并执行 claude --version（最轻量的验证命令）
    docker run --rm \
        -e ANTHROPIC_API_KEY="${CLAUDE_API_KEY:-placeholder}" \
        "$IMAGE_NAME" \
        claude --version &>/dev/null || true

    end_ms=$(date +%s%3N)
    elapsed_ms=$((end_ms - start_ms))
    echo "$elapsed_ms"
}

# 测量单次 claude -p 执行耗时（毫秒）
measure_inference() {
    if [[ -z "$CLAUDE_API_KEY" ]]; then
        echo "-1"
        return
    fi

    local start_ms end_ms elapsed_ms
    start_ms=$(date +%s%3N)

    docker run --rm \
        -e ANTHROPIC_API_KEY="$CLAUDE_API_KEY" \
        "$IMAGE_NAME" \
        claude -p "回复OK" --output-format text &>/dev/null || true

    end_ms=$(date +%s%3N)
    elapsed_ms=$((end_ms - start_ms))
    echo "$elapsed_ms"
}

# 计算平均值（输入为换行分隔的整数列表）
calc_average() {
    local values=("$@")
    local sum=0 count=${#values[@]}
    for v in "${values[@]}"; do
        sum=$((sum + v))
    done
    if [[ $count -eq 0 ]]; then
        echo "0"
    else
        echo $((sum / count))
    fi
}

# 计算最小值
calc_min() {
    local min="$1"
    shift
    for v in "$@"; do
        if [[ $v -lt $min ]]; then min=$v; fi
    done
    echo "$min"
}

# 计算最大值
calc_max() {
    local max="$1"
    shift
    for v in "$@"; do
        if [[ $v -gt $max ]]; then max=$v; fi
    done
    echo "$max"
}

# 获取容器内存限制（通过 docker inspect）
get_memory_limit() {
    local limit
    limit=$(docker inspect "$IMAGE_NAME" --format '{{.HostConfig.Memory}}' 2>/dev/null || echo "0")
    echo "$limit"
}

# 获取运行容器的峰值内存使用（通过 docker stats --no-stream）
measure_peak_memory() {
    if [[ -z "$CLAUDE_API_KEY" ]]; then
        echo "-1"
        return
    fi

    # 后台运行容器
    local cid
    cid=$(docker run -d \
        -e ANTHROPIC_API_KEY="$CLAUDE_API_KEY" \
        "$IMAGE_NAME" \
        claude -p "回复OK" --output-format text 2>/dev/null || echo "")

    if [[ -z "$cid" ]]; then
        echo "-1"
        return
    fi

    # 采样内存使用（最多等待 30 秒）
    local peak_mem=0
    local timeout=30
    local elapsed=0
    while docker ps -q --filter "id=$cid" | grep -q .; do
        local mem_str
        mem_str=$(docker stats --no-stream --format "{{.MemUsage}}" "$cid" 2>/dev/null | awk '{print $1}' || echo "0B")
        local mem_bytes
        mem_bytes=$(parse_size "$mem_str")
        if [[ $mem_bytes -gt $peak_mem ]]; then
            peak_mem=$mem_bytes
        fi
        elapsed=$((elapsed + 1))
        if [[ $elapsed -ge $timeout ]]; then
            docker stop "$cid" &>/dev/null || true
            break
        fi
        sleep 1
    done
    docker rm -f "$cid" &>/dev/null || true
    echo "$peak_mem"
}

# 解析 docker stats 内存字符串（如 "123.4MiB"）为字节数
parse_size() {
    local size_str="$1"
    local num unit
    num=$(echo "$size_str" | sed 's/[^0-9.]//g')
    unit=$(echo "$size_str" | sed 's/[0-9.]//g' | tr '[:lower:]' '[:upper:]')

    case "$unit" in
        B)   echo "${num%.*}" ;;
        KB|KIB) echo "$(echo "$num * 1024" | bc | cut -d. -f1)" ;;
        MB|MIB) echo "$(echo "$num * 1048576" | bc | cut -d. -f1)" ;;
        GB|GIB) echo "$(echo "$num * 1073741824" | bc | cut -d. -f1)" ;;
        *)   echo "0" ;;
    esac
}

# 字节转人类可读格式
bytes_to_human() {
    local bytes="$1"
    if [[ "$bytes" -lt 0 ]]; then
        echo "N/A（需要 API Key）"
        return
    fi
    if [[ "$bytes" -lt 1048576 ]]; then
        echo "$(echo "scale=1; $bytes / 1024" | bc) KB"
    elif [[ "$bytes" -lt 1073741824 ]]; then
        echo "$(echo "scale=1; $bytes / 1048576" | bc) MB"
    else
        echo "$(echo "scale=2; $bytes / 1073741824" | bc) GB"
    fi
}

main() {
    log_info "=== 性能基线测量开始 ==="
    log_info "镜像：$IMAGE_NAME"
    log_info "迭代次数：$ITERATIONS"
    [[ -z "$CLAUDE_API_KEY" ]] && log_warn "未设置 CLAUDE_API_KEY，将跳过推理耗时和内存测量"

    check_deps
    check_image

    # ---- 冷启动测量 ----
    log_info "正在测量容器冷启动耗时（共 $ITERATIONS 次）..."
    local cold_start_times=()
    for i in $(seq 1 "$ITERATIONS"); do
        log_info "  冷启动第 $i 次..."
        local t
        t=$(measure_cold_start)
        cold_start_times+=("$t")
        log_info "  第 $i 次：${t}ms"
    done

    local cs_avg cs_min cs_max
    cs_avg=$(calc_average "${cold_start_times[@]}")
    cs_min=$(calc_min "${cold_start_times[@]}")
    cs_max=$(calc_max "${cold_start_times[@]}")

    # ---- 推理耗时测量 ----
    log_info "正在测量推理耗时（claude -p '回复OK'）..."
    local inference_times=()
    local inf_skipped=false
    if [[ -z "$CLAUDE_API_KEY" ]]; then
        inf_skipped=true
        inference_times=(-1)
    else
        for i in $(seq 1 "$ITERATIONS"); do
            log_info "  推理第 $i 次..."
            local t
            t=$(measure_inference)
            inference_times+=("$t")
            log_info "  第 $i 次：${t}ms"
        done
    fi

    local inf_avg inf_min inf_max
    if $inf_skipped; then
        inf_avg=-1; inf_min=-1; inf_max=-1
    else
        inf_avg=$(calc_average "${inference_times[@]}")
        inf_min=$(calc_min "${inference_times[@]}")
        inf_max=$(calc_max "${inference_times[@]}")
    fi

    # ---- 内存测量 ----
    log_info "正在测量峰值内存使用..."
    local peak_memory
    peak_memory=$(measure_peak_memory)

    # ---- 获取镜像大小 ----
    local image_size
    image_size=$(docker image inspect "$IMAGE_NAME" --format '{{.Size}}' 2>/dev/null || echo "0")
    local image_size_human
    image_size_human=$(bytes_to_human "$image_size")

    # ---- 获取 Node.js 版本（容器内）----
    local node_version
    node_version=$(docker run --rm "$IMAGE_NAME" node --version 2>/dev/null || echo "unknown")

    local timestamp
    timestamp=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

    # ---- 输出 JSON ----
    local json_output
    json_output=$(jq -n \
        --arg ts "$timestamp" \
        --arg image "$IMAGE_NAME" \
        --argjson iterations "$ITERATIONS" \
        --argjson cs_avg "$cs_avg" \
        --argjson cs_min "$cs_min" \
        --argjson cs_max "$cs_max" \
        --argjson cs_p1 "${cold_start_times[0]:-0}" \
        --argjson cs_p2 "${cold_start_times[1]:-0}" \
        --argjson cs_p3 "${cold_start_times[2]:-0}" \
        --argjson inf_avg "$inf_avg" \
        --argjson inf_min "$inf_min" \
        --argjson inf_max "$inf_max" \
        --argjson peak_memory "$peak_memory" \
        --argjson image_size "$image_size" \
        --arg node_version "$node_version" \
        --argjson api_key_available "$([ -n "$CLAUDE_API_KEY" ] && echo true || echo false)" \
        '{
            timestamp: $ts,
            image: $image,
            iterations: $iterations,
            api_key_available: $api_key_available,
            cold_start: {
                unit: "ms",
                average: $cs_avg,
                min: $cs_min,
                max: $cs_max,
                samples: [$cs_p1, $cs_p2, $cs_p3]
            },
            inference: {
                unit: "ms",
                note: (if $api_key_available then "claude -p \"回复OK\" --output-format text" else "skipped: no API key" end),
                average: $inf_avg,
                min: $inf_min,
                max: $inf_max
            },
            memory: {
                unit: "bytes",
                peak_usage: $peak_memory,
                image_size: $image_size
            },
            environment: {
                node_version: $node_version
            }
        }')

    # ---- 输出人类可读表格 ----
    echo "" >&2
    echo "┌─────────────────────────────────────────────────┐" >&2
    echo "│           性能基线测量结果                       │" >&2
    echo "├─────────────────────────┬───────────────────────┤" >&2
    echo "│ 指标                    │ 数值                  │" >&2
    echo "├─────────────────────────┼───────────────────────┤" >&2
    printf "│ %-23s │ %-21s │\n" "冷启动平均耗时" "${cs_avg}ms" >&2
    printf "│ %-23s │ %-21s │\n" "冷启动最小耗时" "${cs_min}ms" >&2
    printf "│ %-23s │ %-21s │\n" "冷启动最大耗时" "${cs_max}ms" >&2
    echo "├─────────────────────────┼───────────────────────┤" >&2
    if $inf_skipped; then
        printf "│ %-23s │ %-21s │\n" "推理平均耗时" "N/A（无API Key）" >&2
    else
        printf "│ %-23s │ %-21s │\n" "推理平均耗时" "${inf_avg}ms" >&2
        printf "│ %-23s │ %-21s │\n" "推理最小耗时" "${inf_min}ms" >&2
        printf "│ %-23s │ %-21s │\n" "推理最大耗时" "${inf_max}ms" >&2
    fi
    echo "├─────────────────────────┼───────────────────────┤" >&2
    printf "│ %-23s │ %-21s │\n" "峰值内存使用" "$(bytes_to_human "$peak_memory")" >&2
    printf "│ %-23s │ %-21s │\n" "镜像大小" "$image_size_human" >&2
    printf "│ %-23s │ %-21s │\n" "Node.js 版本" "$node_version" >&2
    echo "└─────────────────────────┴───────────────────────┘" >&2
    echo "" >&2

    # JSON 输出到 stdout
    echo "$json_output"

    log_ok "性能基线测量完成"
}

main "$@"
