#!/usr/bin/env bash
# 用途：验证 Claude Code CLI 在容器内的安装和基本运行能力
# 测试项：版本检查、非交互模式、JSON 输出格式、流式 JSON 输出

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# 计数器
PASS_COUNT=0
FAIL_COUNT=0

# 静默模式标志
QUIET=false

# 解析参数
for arg in "$@"; do
  case "$arg" in
    --quiet) QUIET=true ;;
  esac
done

# 打印测试结果
print_result() {
  local status="$1"
  local name="$2"
  local detail="${3:-}"

  if [ "$status" = "PASS" ]; then
    PASS_COUNT=$((PASS_COUNT + 1))
    if [ "$QUIET" = false ]; then
      echo -e "${GREEN}[PASS]${NC} $name"
      [ -n "$detail" ] && echo "       $detail"
    fi
  else
    FAIL_COUNT=$((FAIL_COUNT + 1))
    if [ "$QUIET" = false ]; then
      echo -e "${RED}[FAIL]${NC} $name"
      [ -n "$detail" ] && echo "       $detail"
    fi
  fi
}

# 测试1：验证 claude 命令存在且可执行
test_claude_exists() {
  if command -v claude &>/dev/null; then
    print_result "PASS" "claude 命令存在" "路径: $(command -v claude)"
  else
    print_result "FAIL" "claude 命令存在" "未找到 claude 命令，请确认已正确安装"
  fi
}

# 测试2：验证 claude --version 输出
test_claude_version() {
  local version_output
  if version_output=$(claude --version 2>&1); then
    print_result "PASS" "claude --version 执行成功" "$version_output"
  else
    print_result "FAIL" "claude --version 执行成功" "退出码: $?, 输出: $version_output"
  fi
}

# 测试3：验证非交互模式 claude -p
test_noninteractive_mode() {
  local output
  local exit_code=0
  output=$(claude -p "回复OK，不要输出其他内容" 2>&1) || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -n "$output" ]; then
    print_result "PASS" "非交互模式 claude -p 可用" "响应: ${output:0:100}"
  else
    print_result "FAIL" "非交互模式 claude -p 可用" "退出码: $exit_code, 输出: ${output:0:200}"
  fi
}

# 测试4：验证 --output-format json 输出
test_json_output_format() {
  local output
  local exit_code=0
  output=$(claude -p "回复OK" --output-format json 2>&1) || exit_code=$?

  if [ $exit_code -eq 0 ] && echo "$output" | python3 -c "import sys,json; json.load(sys.stdin)" &>/dev/null 2>&1; then
    print_result "PASS" "--output-format json 输出有效 JSON" "输出前100字节: ${output:0:100}"
  elif [ $exit_code -eq 0 ] && echo "$output" | python3 -c "
import sys
data = sys.stdin.read()
# 检查是否包含 JSON 对象
import re
if re.search(r'\{.*\}', data, re.DOTALL):
    sys.exit(0)
sys.exit(1)
" &>/dev/null 2>&1; then
    print_result "PASS" "--output-format json 输出包含 JSON 内容" "输出前100字节: ${output:0:100}"
  else
    print_result "FAIL" "--output-format json 输出有效 JSON" "退出码: $exit_code, 输出: ${output:0:200}"
  fi
}

# 测试5：验证 --output-format stream-json 流式输出
test_stream_json_output_format() {
  local output
  local exit_code=0
  output=$(claude -p "回复OK" --output-format stream-json 2>&1) || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -n "$output" ]; then
    # 流式 JSON 每行是独立的 JSON 对象
    local first_line
    first_line=$(echo "$output" | head -1)
    if echo "$first_line" | python3 -c "import sys,json; json.loads(sys.stdin.read())" &>/dev/null 2>&1; then
      print_result "PASS" "--output-format stream-json 流式输出可用" "首行: ${first_line:0:100}"
    else
      # 可能是 NDJSON 格式，检查是否有 JSON 内容
      if echo "$output" | python3 -c "
import sys
for line in sys.stdin:
    line = line.strip()
    if line:
        import json
        json.loads(line)
        sys.exit(0)
sys.exit(1)
" &>/dev/null 2>&1; then
        print_result "PASS" "--output-format stream-json 流式输出可用（NDJSON格式）" "输出行数: $(echo "$output" | wc -l)"
      else
        print_result "FAIL" "--output-format stream-json 流式输出可用" "输出不是有效的流式JSON: ${output:0:200}"
      fi
    fi
  else
    print_result "FAIL" "--output-format stream-json 流式输出可用" "退出码: $exit_code, 输出: ${output:0:200}"
  fi
}

# 主函数
main() {
  if [ "$QUIET" = false ]; then
    echo "========================================"
    echo "  验证项1：Claude Code CLI 安装与运行"
    echo "========================================"
    echo ""
  fi

  test_claude_exists
  test_claude_version
  test_noninteractive_mode
  test_json_output_format
  test_stream_json_output_format

  # 输出汇总
  echo ""
  echo "----------------------------------------"
  if [ "$QUIET" = true ]; then
    echo "验证项1（安装与运行）: PASS=$PASS_COUNT FAIL=$FAIL_COUNT"
  else
    echo -e "汇总: ${GREEN}PASS: $PASS_COUNT${NC} | ${RED}FAIL: $FAIL_COUNT${NC}"
    echo "----------------------------------------"
  fi

  # 退出码
  [ $FAIL_COUNT -eq 0 ] && exit 0 || exit 1
}

main "$@"
