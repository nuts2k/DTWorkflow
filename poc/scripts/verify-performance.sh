#!/usr/bin/env bash
# 用途：测量 Claude Code CLI 在 Docker 容器内的性能基线
# 测试项：推理耗时、内存使用、多次取均值
# 在容器内运行

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
RED='\033[0;31m'
BLUE='\033[0;34m'
NC='\033[0m'

# 默认迭代次数
ITERATIONS=3

# 静默模式标志
QUIET=false
for arg in "$@"; do
  case "$arg" in
    --quiet) QUIET=true ;;
    [0-9]*) ITERATIONS="$arg" ;;
  esac
done

log() {
  [ "$QUIET" = false ] && echo -e "${BLUE}[INFO]${NC} $1"
}

# 检查 ANTHROPIC_API_KEY
if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
  echo -e "${RED}[ERROR]${NC} ANTHROPIC_API_KEY 未设置，无法测量推理性能"
  exit 1
fi

log "=== 性能基线测量开始 ==="
log "迭代次数：$ITERATIONS"

# 测量单次 claude -p 调用耗时（毫秒）
measure_inference_time() {
  local start end duration_ms
  start=$(date +%s%N 2>/dev/null || python3 -c "import time; print(int(time.time()*1000000000))")
  claude -p "回复OK" --output-format json > /dev/null 2>&1 || true
  end=$(date +%s%N 2>/dev/null || python3 -c "import time; print(int(time.time()*1000000000))")
  duration_ms=$(( (end - start) / 1000000 ))
  echo "$duration_ms"
}

# 获取当前内存使用（MB）
get_memory_usage_mb() {
  # 尝试 cgroup v2
  if [ -f /sys/fs/cgroup/memory.current ]; then
    local bytes
    bytes=$(cat /sys/fs/cgroup/memory.current)
    echo $(( bytes / 1024 / 1024 ))
    return
  fi
  # 尝试 cgroup v1
  if [ -f /sys/fs/cgroup/memory/memory.usage_in_bytes ]; then
    local bytes
    bytes=$(cat /sys/fs/cgroup/memory/memory.usage_in_bytes)
    echo $(( bytes / 1024 / 1024 ))
    return
  fi
  # 回退到 /proc/meminfo
  if [ -f /proc/meminfo ]; then
    local total_kb free_kb
    total_kb=$(grep MemTotal /proc/meminfo | awk '{print $2}')
    free_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
    echo $(( (total_kb - free_kb) / 1024 ))
    return
  fi
  echo "0"
}

# 多次测量推理耗时
declare -a TIMES=()
declare -a MEMORIES=()

log "开始测量推理耗时（$ITERATIONS 次）..."
for i in $(seq 1 "$ITERATIONS"); do
  mem_before=$(get_memory_usage_mb)
  duration=$(measure_inference_time)
  mem_after=$(get_memory_usage_mb)
  mem_delta=$((mem_after - mem_before))

  TIMES+=("$duration")
  MEMORIES+=("$mem_after")
  log "  第 $i 次：耗时 ${duration}ms，内存 ${mem_after}MB（+${mem_delta}MB）"
done

# 计算统计值
total_time=0
min_time=999999999
max_time=0
for t in "${TIMES[@]}"; do
  total_time=$((total_time + t))
  [ "$t" -lt "$min_time" ] && min_time=$t
  [ "$t" -gt "$max_time" ] && max_time=$t
done
avg_time=$((total_time / ITERATIONS))

# 取最后一次内存值作为稳态值
final_memory=${MEMORIES[-1]}

# 输出人类可读表格
echo ""
echo "========================================"
echo "  性能基线测量结果"
echo "========================================"
printf "%-25s %s\n" "指标" "值"
echo "----------------------------------------"
printf "%-25s %s\n" "测量次数" "$ITERATIONS"
printf "%-25s %s\n" "平均推理耗时" "${avg_time}ms"
printf "%-25s %s\n" "最小推理耗时" "${min_time}ms"
printf "%-25s %s\n" "最大推理耗时" "${max_time}ms"
printf "%-25s %s\n" "容器内存使用" "${final_memory}MB"
echo "========================================"

# 输出 JSON 格式
echo ""
log "JSON 格式输出："
cat <<JSONEOF
{
  "iterations": $ITERATIONS,
  "inference_time_ms": {
    "avg": $avg_time,
    "min": $min_time,
    "max": $max_time,
    "values": [$(IFS=,; echo "${TIMES[*]}")]
  },
  "memory_mb": {
    "final": $final_memory,
    "values": [$(IFS=,; echo "${MEMORIES[*]}")]
  }
}
JSONEOF

echo ""
echo -e "${GREEN}[PASS]${NC} 性能基线数据收集完成"
