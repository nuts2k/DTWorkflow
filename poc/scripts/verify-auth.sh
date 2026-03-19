#!/usr/bin/env bash
# 用途：验证 Claude Code CLI 的 API Key 认证机制
# 测试项：环境变量检查、API 认证有效性、Key 安全性（未泄露到文件系统）

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

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

# 测试1：检查 ANTHROPIC_API_KEY 环境变量是否已设置
test_api_key_env_set() {
  if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    # 不输出 Key 值，只显示前4位用于确认
    local key_prefix="${ANTHROPIC_API_KEY:0:4}"
    print_result "PASS" "ANTHROPIC_API_KEY 环境变量已设置" "Key 前缀: ${key_prefix}*** (长度: ${#ANTHROPIC_API_KEY})"
  else
    print_result "FAIL" "ANTHROPIC_API_KEY 环境变量已设置" "ANTHROPIC_API_KEY 未设置，请通过 -e ANTHROPIC_API_KEY=... 传入"
  fi
}

# 测试1.5：检查 ANTHROPIC_BASE_URL 配置（可选）
test_base_url_config() {
  if [ -n "${ANTHROPIC_BASE_URL:-}" ]; then
    print_result "PASS" "ANTHROPIC_BASE_URL 已配置" "Base URL: ${ANTHROPIC_BASE_URL}"
  else
    print_result "PASS" "ANTHROPIC_BASE_URL 未配置（使用默认）" "将使用默认 API 端点"
  fi
}

# 测试2：验证 API 认证正常（执行一次简单调用）
test_api_auth_works() {
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    print_result "FAIL" "API 认证有效" "跳过：ANTHROPIC_API_KEY 未设置"
    return
  fi

  local output
  local exit_code=0
  output=$(claude -p "回复数字1" 2>&1) || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -n "$output" ]; then
    print_result "PASS" "API 认证有效" "claude -p 调用成功，响应: ${output:0:50}"
  else
    # 检查是否是认证错误
    if echo "$output" | grep -qi "authentication\|unauthorized\|invalid.*key\|api.*key"; then
      print_result "FAIL" "API 认证有效" "认证失败: ${output:0:200}"
    else
      print_result "FAIL" "API 认证有效" "调用失败（退出码: $exit_code）: ${output:0:200}"
    fi
  fi
}

# 测试3：检查常见日志路径中是否存在 API Key 泄露
test_key_not_in_logs() {
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    print_result "FAIL" "API Key 未泄露到日志文件" "跳过：ANTHROPIC_API_KEY 未设置"
    return
  fi

  local log_dirs=(
    "/var/log"
    "/tmp"
    "/root/.npm"
    "/root/.config/claude"
    "/home"
  )

  local leaked=false
  local leaked_in=""

  for log_dir in "${log_dirs[@]}"; do
    if [ -d "$log_dir" ]; then
      # 搜索包含 API Key 的文件（限制搜索范围防止超时）
      if grep -r --include="*.log" --include="*.txt" -l "$ANTHROPIC_API_KEY" "$log_dir" 2>/dev/null | head -1 | grep -q .; then
        leaked=true
        leaked_in="$log_dir"
        break
      fi
    fi
  done

  if [ "$leaked" = false ]; then
    print_result "PASS" "API Key 未泄露到日志文件" "检查路径: ${log_dirs[*]}"
  else
    print_result "FAIL" "API Key 未泄露到日志文件" "在 $leaked_in 中发现 API Key"
  fi
}

# 测试4：检查配置目录中是否存在 API Key 写入
test_key_not_in_config_files() {
  if [ -z "${ANTHROPIC_API_KEY:-}" ]; then
    print_result "FAIL" "API Key 未写入配置文件" "跳过：ANTHROPIC_API_KEY 未设置"
    return
  fi

  local config_dirs=(
    "$HOME/.config"
    "$HOME/.anthropic"
    "$HOME/.claude"
    "/etc"
  )

  local found=false
  local found_in=""

  for config_dir in "${config_dirs[@]}"; do
    if [ -d "$config_dir" ]; then
      if grep -r -l "$ANTHROPIC_API_KEY" "$config_dir" 2>/dev/null | head -1 | grep -q .; then
        found=true
        found_in="$config_dir"
        break
      fi
    fi
  done

  if [ "$found" = false ]; then
    print_result "PASS" "API Key 未写入配置文件" "检查路径: ${config_dirs[*]}"
  else
    print_result "FAIL" "API Key 未写入配置文件" "在 $found_in 中发现 API Key 明文存储"
  fi
}

# 测试5：检查 ~/.config 目录确认 Key 未明文存储
test_config_directory_safety() {
  local config_path="$HOME/.config/claude"

  if [ -d "$config_path" ]; then
    local file_list
    file_list=$(ls -la "$config_path" 2>/dev/null || echo "（无法列出）")
    if [ "$QUIET" = false ]; then
      echo "       ~/.config/claude 目录内容:"
      echo "$file_list" | while IFS= read -r line; do echo "         $line"; done
    fi

    # 检查是否有敏感文件
    if ls "$config_path" 2>/dev/null | grep -qi "key\|secret\|token\|credential"; then
      print_result "FAIL" "~/.config/claude 无敏感文件名" "发现可疑文件名，请检查"
    else
      print_result "PASS" "~/.config/claude 无敏感文件名" "目录存在但无敏感文件名"
    fi
  else
    print_result "PASS" "~/.config/claude 无敏感文件名" "目录不存在（Key 通过环境变量传递）"
  fi
}

# 主函数
main() {
  if [ "$QUIET" = false ]; then
    echo "========================================"
    echo "  验证项2：API Key 认证"
    echo "========================================"
    echo ""
  fi

  test_api_key_env_set
  test_base_url_config
  test_api_auth_works
  test_key_not_in_logs
  test_key_not_in_config_files
  test_config_directory_safety

  # 输出汇总
  echo ""
  echo "----------------------------------------"
  if [ "$QUIET" = true ]; then
    echo "验证项2（API认证）: PASS=$PASS_COUNT FAIL=$FAIL_COUNT"
  else
    echo -e "汇总: ${GREEN}PASS: $PASS_COUNT${NC} | ${RED}FAIL: $FAIL_COUNT${NC}"
    echo "----------------------------------------"
  fi

  [ $FAIL_COUNT -eq 0 ] && exit 0 || exit 1
}

main "$@"
