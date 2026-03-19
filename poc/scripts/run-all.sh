#!/usr/bin/env bash
# 用途：汇总执行所有 Claude Code CLI 功能验证脚本
# 依次运行：verify-install.sh / verify-auth.sh / verify-code-ops.sh
# 退出码：0 全部通过 / 1 有失败项

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BOLD='\033[1m'
NC='\033[0m'

# 静默模式标志
QUIET=false

# 解析参数
for arg in "$@"; do
  case "$arg" in
    --quiet) QUIET=true ;;
  esac
done

# 脚本所在目录
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# 验证脚本列表：[脚本文件, 验证项名称]
declare -a SCRIPTS=(
  "verify-install.sh:验证项1：安装与运行"
  "verify-auth.sh:验证项2：API Key 认证"
  "verify-code-ops.sh:验证项3：代码操作能力"
)

# 结果记录
declare -a RESULTS=()
TOTAL_PASS=0
TOTAL_FAIL=0

# 执行单个验证脚本并捕获结果
run_script() {
  local script_entry="$1"
  local script_file="${script_entry%%:*}"
  local script_name="${script_entry##*:}"
  local script_path="$SCRIPT_DIR/$script_file"

  if [ ! -f "$script_path" ]; then
    if [ "$QUIET" = false ]; then
      echo -e "${RED}[ERROR]${NC} 脚本不存在: $script_path"
    fi
    RESULTS+=("MISSING:$script_name")
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
    return
  fi

  if [ ! -x "$script_path" ]; then
    chmod +x "$script_path"
  fi

  if [ "$QUIET" = false ]; then
    echo ""
    echo -e "${BOLD}>>> 执行: $script_name${NC}"
    echo "    脚本: $script_path"
    echo ""
  fi

  local exit_code=0

  if [ "$QUIET" = true ]; then
    # 静默模式：仍然运行脚本但只捕获汇总行
    bash "$script_path" --quiet 2>&1 || exit_code=$?
  else
    bash "$script_path" 2>&1 || exit_code=$?
  fi

  if [ $exit_code -eq 0 ]; then
    RESULTS+=("PASS:$script_name")
    TOTAL_PASS=$((TOTAL_PASS + 1))
  else
    RESULTS+=("FAIL:$script_name")
    TOTAL_FAIL=$((TOTAL_FAIL + 1))
  fi
}

# 输出汇总表格
print_summary() {
  echo ""
  echo "========================================"
  echo -e "  ${BOLD}M1.0 PoC 功能验证汇总报告${NC}"
  echo "========================================"
  printf "%-35s %s\n" "验证项" "结果"
  echo "----------------------------------------"

  for result in "${RESULTS[@]}"; do
    local status="${result%%:*}"
    local name="${result##*:}"

    if [ "$status" = "PASS" ]; then
      printf "%-35s ${GREEN}%s${NC}\n" "$name" "[PASS]"
    elif [ "$status" = "FAIL" ]; then
      printf "%-35s ${RED}%s${NC}\n" "$name" "[FAIL]"
    else
      printf "%-35s ${YELLOW}%s${NC}\n" "$name" "[MISSING]"
    fi
  done

  echo "----------------------------------------"
  echo ""
  echo -e "总计: ${GREEN}通过: $TOTAL_PASS${NC} | ${RED}失败: $TOTAL_FAIL${NC} | 共: $((TOTAL_PASS + TOTAL_FAIL))"
  echo ""

  if [ $TOTAL_FAIL -eq 0 ]; then
    echo -e "${GREEN}${BOLD}所有验证项全部通过！Claude Code CLI 在 Docker 中运行可行。${NC}"
  else
    echo -e "${RED}${BOLD}有 $TOTAL_FAIL 个验证项失败，请检查上方详情。${NC}"
  fi
  echo "========================================"
}

# 主函数
main() {
  if [ "$QUIET" = false ]; then
    echo "========================================"
    echo -e "  ${BOLD}DTWorkflow M1.0 PoC 验证套件${NC}"
    echo "  Claude Code CLI 可行性验证"
    echo "========================================"
    echo "  执行时间: $(date '+%Y-%m-%d %H:%M:%S')"
    echo "  脚本目录: $SCRIPT_DIR"
    echo "========================================"
  fi

  # 依次执行各验证脚本
  for script_entry in "${SCRIPTS[@]}"; do
    run_script "$script_entry"
  done

  # 输出汇总
  print_summary

  # 退出码：0 全部通过 / 1 有失败项
  [ $TOTAL_FAIL -eq 0 ] && exit 0 || exit 1
}

main "$@"
