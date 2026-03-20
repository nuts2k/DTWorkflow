#!/usr/bin/env bash
# 用途：收集容器内的平台和环境信息，用于 PoC 验证报告
# 在容器内运行，收集运行时环境信息

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m'

# 静默模式标志
QUIET=false
for arg in "$@"; do
  case "$arg" in
    --quiet) QUIET=true ;;
  esac
done

log() {
  [ "$QUIET" = false ] && echo -e "${BLUE}[INFO]${NC} $1"
}

log "=== 平台信息收集开始 ==="

# 操作系统信息
os_name=$(grep "^PRETTY_NAME" /etc/os-release 2>/dev/null | cut -d'"' -f2 || echo "unknown")
kernel=$(uname -r 2>/dev/null || echo "unknown")
arch=$(uname -m 2>/dev/null || echo "unknown")
log "操作系统：$os_name"
log "内核版本：$kernel"
log "架构：$arch"

# CPU 信息
cpu_count=$(nproc 2>/dev/null || echo "unknown")
cpu_model=$(grep "model name" /proc/cpuinfo 2>/dev/null | head -1 | cut -d':' -f2 | xargs || echo "unknown")
log "CPU：$cpu_count 核，$cpu_model"

# 内存信息
total_mb="unknown"
avail_mb="unknown"
if [ -f /proc/meminfo ]; then
  total_kb=$(grep MemTotal /proc/meminfo | awk '{print $2}')
  total_mb=$((total_kb / 1024))
  avail_kb=$(grep MemAvailable /proc/meminfo | awk '{print $2}')
  avail_mb=$((avail_kb / 1024))
  log "内存：总计 ${total_mb}MB，可用 ${avail_mb}MB"
fi

# Node.js 信息
node_version=$(node --version 2>/dev/null || echo "not installed")
npm_version=$(npm --version 2>/dev/null || echo "not installed")
log "Node.js：$node_version"
log "npm：$npm_version"

# Claude Code CLI 版本
claude_version=$(claude --version 2>/dev/null || echo "not installed")
log "Claude Code CLI：$claude_version"

# Git 版本
git_version=$(git --version 2>/dev/null || echo "not installed")
log "Git：$git_version"

# 网络连通性
log "检测网络连通性..."

api_reachable="false"
if curl -s --connect-timeout 5 -o /dev/null -w "%{http_code}" https://api.anthropic.com 2>/dev/null | grep -qE "^[23]"; then
  api_reachable="true"
  log "  api.anthropic.com：可达"
else
  log "  api.anthropic.com：不可达（可能使用代理）"
fi

base_url_reachable="false"
base_url="${ANTHROPIC_BASE_URL:-}"
if [ -n "$base_url" ]; then
  if curl -s --connect-timeout 5 -o /dev/null -w "%{http_code}" "$base_url" 2>/dev/null | grep -qE "^[23]"; then
    base_url_reachable="true"
    log "  ANTHROPIC_BASE_URL ($base_url)：可达"
  else
    log "  ANTHROPIC_BASE_URL ($base_url)：不可达"
  fi
fi

# 当前用户
current_user=$(whoami 2>/dev/null || echo "unknown")
log "运行用户：$current_user"

# 输出表格
echo ""
echo "========================================"
echo "  平台信息汇总"
echo "========================================"
printf "%-25s %s\n" "项目" "值"
echo "----------------------------------------"
printf "%-25s %s\n" "操作系统" "$os_name"
printf "%-25s %s\n" "内核版本" "$kernel"
printf "%-25s %s\n" "架构" "$arch"
printf "%-25s %s\n" "CPU" "$cpu_count 核"
printf "%-25s %s\n" "内存总计" "${total_mb}MB"
printf "%-25s %s\n" "内存可用" "${avail_mb}MB"
printf "%-25s %s\n" "Node.js" "$node_version"
printf "%-25s %s\n" "npm" "$npm_version"
printf "%-25s %s\n" "Claude Code CLI" "$claude_version"
printf "%-25s %s\n" "Git" "$git_version"
printf "%-25s %s\n" "运行用户" "$current_user"
printf "%-25s %s\n" "API 直连" "$api_reachable"
printf "%-25s %s\n" "Base URL 可达" "$base_url_reachable"
echo "========================================"

# 输出 JSON
echo ""
log "JSON 格式输出："
cat <<JSONEOF
{
  "os": "$os_name",
  "kernel": "$kernel",
  "arch": "$arch",
  "cpu_cores": "$cpu_count",
  "memory_total_mb": "$total_mb",
  "memory_available_mb": "$avail_mb",
  "node_version": "$node_version",
  "npm_version": "$npm_version",
  "claude_cli_version": "$claude_version",
  "git_version": "$git_version",
  "user": "$current_user",
  "network": {
    "api_anthropic_reachable": $api_reachable,
    "base_url_reachable": $base_url_reachable,
    "base_url": "$base_url"
  }
}
JSONEOF

echo ""
echo -e "${GREEN}[PASS]${NC} 平台信息收集完成"
