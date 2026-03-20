#!/usr/bin/env bash
# 用途：验证 Claude Code CLI 的代码操作能力
# 测试项：读取分析代码、修改代码文件、执行 Git 操作（init/branch/commit）

set -euo pipefail

# 颜色常量
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

# 计数器
PASS_COUNT=0
FAIL_COUNT=0

# 测试工作目录
TEST_REPO="/tmp/test-repo"

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

# 准备：创建测试 Go 项目
setup_test_repo() {
  # 清理旧目录
  rm -rf "$TEST_REPO"
  mkdir -p "$TEST_REPO"

  # 创建 main.go
  cat > "$TEST_REPO/main.go" << 'GOEOF'
package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}

func add(a, b int) int {
	return a + b
}
GOEOF

  # 创建 go.mod
  cat > "$TEST_REPO/go.mod" << 'MODEOF'
module test-repo

go 1.21
MODEOF

  if [ "$QUIET" = false ]; then
    echo "       已创建测试项目: $TEST_REPO"
    echo "       文件列表: $(ls $TEST_REPO)"
  fi
}

# 测试1：创建测试 Go 项目
test_create_test_project() {
  local exit_code=0
  setup_test_repo || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -f "$TEST_REPO/main.go" ] && [ -f "$TEST_REPO/go.mod" ]; then
    print_result "PASS" "创建测试 Go 项目" "路径: $TEST_REPO, 文件: main.go, go.mod"
  else
    print_result "FAIL" "创建测试 Go 项目" "退出码: $exit_code"
  fi
}

# 测试2：用 claude -p --dangerously-skip-permissions 读取并分析代码
test_claude_read_code() {
  if [ ! -f "$TEST_REPO/main.go" ]; then
    print_result "FAIL" "Claude 读取并分析代码" "测试项目未创建，跳过"
    return
  fi

  local output
  local exit_code=0
  output=$(cd "$TEST_REPO" && claude -p --dangerously-skip-permissions "请读取 main.go 文件并告诉我这个程序的功能，用一句话回答" 2>&1) || exit_code=$?

  if [ $exit_code -eq 0 ] && [ -n "$output" ]; then
    print_result "PASS" "Claude 读取并分析代码" "响应: ${output:0:150}"
  else
    print_result "FAIL" "Claude 读取并分析代码" "退出码: $exit_code, 输出: ${output:0:200}"
  fi
}

# 测试3：用 claude -p --dangerously-skip-permissions 修改代码文件
test_claude_modify_code() {
  if [ ! -f "$TEST_REPO/main.go" ]; then
    print_result "FAIL" "Claude 修改代码文件" "测试项目未创建，跳过"
    return
  fi

  # 记录原始内容哈希
  local original_hash
  original_hash=$(md5sum "$TEST_REPO/main.go" | cut -d' ' -f1)

  # 请求 Claude 添加一个新函数
  local output
  local exit_code=0
  output=$(cd "$TEST_REPO" && claude -p --dangerously-skip-permissions "在 main.go 文件末尾添加一个名为 multiply 的函数，接受两个 int 参数并返回它们的乘积。直接修改文件，不要输出解释。" 2>&1) || exit_code=$?

  # 检查文件是否被修改
  local new_hash
  new_hash=$(md5sum "$TEST_REPO/main.go" | cut -d' ' -f1)

  if [ "$original_hash" != "$new_hash" ]; then
    # 验证 multiply 函数是否真的被添加
    if grep -q "multiply" "$TEST_REPO/main.go"; then
      print_result "PASS" "Claude 修改代码文件（添加 multiply 函数）" "文件已修改，multiply 函数已添加"
    else
      print_result "FAIL" "Claude 修改代码文件（添加 multiply 函数）" "文件被修改但未找到 multiply 函数"
    fi
  else
    if [ $exit_code -ne 0 ]; then
      print_result "FAIL" "Claude 修改代码文件（添加 multiply 函数）" "claude 调用失败（退出码: $exit_code）: ${output:0:200}"
    else
      print_result "FAIL" "Claude 修改代码文件（添加 multiply 函数）" "文件未被修改（原始哈希: $original_hash）"
    fi
  fi
}

# 测试4：用 claude -p --dangerously-skip-permissions 执行 Git init 操作
test_claude_git_init() {
  if [ ! -d "$TEST_REPO" ]; then
    print_result "FAIL" "Claude 执行 Git init" "测试目录不存在，跳过"
    return
  fi

  local output
  local exit_code=0
  output=$(cd "$TEST_REPO" && claude -p --dangerously-skip-permissions "在当前目录执行 git init，然后设置 user.email 为 test@test.com，user.name 为 TestUser。只执行命令，不要解释。" 2>&1) || exit_code=$?

  if [ -d "$TEST_REPO/.git" ]; then
    print_result "PASS" "Claude 执行 Git init" ".git 目录已创建"
  else
    print_result "FAIL" "Claude 执行 Git init" "退出码: $exit_code, .git 目录未创建, 输出: ${output:0:200}"
  fi
}

# 测试5：Git add + commit（直接使用 git 命令，验证代码操作结果可以提交）
test_git_commit() {
  if [ ! -d "$TEST_REPO/.git" ]; then
    # 如果 git init 失败，手动初始化
    cd "$TEST_REPO" && git init -q && git config user.email "test@test.com" && git config user.name "TestUser"
  fi

  local exit_code=0
  cd "$TEST_REPO" && git add -A && git commit -m "初始提交：添加 Go hello world 项目" -q 2>&1 || exit_code=$?

  if [ $exit_code -eq 0 ]; then
    local commit_hash
    commit_hash=$(cd "$TEST_REPO" && git log --oneline -1)
    print_result "PASS" "Git add 和 commit 成功" "提交: $commit_hash"
  else
    print_result "FAIL" "Git add 和 commit 成功" "退出码: $exit_code"
  fi
}

# 测试6：用 claude -p --dangerously-skip-permissions 创建 Git 分支
test_claude_git_branch() {
  if [ ! -d "$TEST_REPO/.git" ]; then
    print_result "FAIL" "Claude 创建 Git 分支" "Git 仓库未初始化，跳过"
    return
  fi

  # 确保有至少一个提交（分支需要 HEAD 存在）
  if ! cd "$TEST_REPO" && git log &>/dev/null 2>&1; then
    cd "$TEST_REPO" && git add -A && git commit -m "初始提交" -q 2>/dev/null || true
  fi

  local output
  local exit_code=0
  output=$(cd "$TEST_REPO" && claude -p --dangerously-skip-permissions "在当前 git 仓库创建一个名为 feature/test-branch 的新分支并切换到该分支。只执行命令。" 2>&1) || exit_code=$?

  # 验证分支是否创建
  local current_branch
  current_branch=$(cd "$TEST_REPO" && git branch --show-current 2>/dev/null || echo "unknown")

  if [ "$current_branch" = "feature/test-branch" ]; then
    print_result "PASS" "Claude 创建并切换 Git 分支" "当前分支: $current_branch"
  else
    # 检查分支是否存在（即使没有切换过去）
    if cd "$TEST_REPO" && git branch | grep -q "feature/test-branch"; then
      print_result "PASS" "Claude 创建 Git 分支（存在但未切换）" "分支已创建，当前分支: $current_branch"
    else
      print_result "FAIL" "Claude 创建并切换 Git 分支" "当前分支: $current_branch, 退出码: $exit_code, 输出: ${output:0:200}"
    fi
  fi
}

# 测试7：验证 Git 操作结果（提交记录和分支）
test_verify_git_state() {
  if [ ! -d "$TEST_REPO/.git" ]; then
    print_result "FAIL" "验证 Git 仓库状态" "Git 仓库未初始化，跳过"
    return
  fi

  local commit_count
  commit_count=$(cd "$TEST_REPO" && git log --oneline 2>/dev/null | wc -l | tr -d ' ')

  local branch_count
  branch_count=$(cd "$TEST_REPO" && git branch 2>/dev/null | wc -l | tr -d ' ')

  if [ "$commit_count" -ge 1 ]; then
    local branch_list
    branch_list=$(cd "$TEST_REPO" && git branch 2>/dev/null | tr '\n' ',' | sed 's/,$//')
    print_result "PASS" "Git 仓库状态验证" "提交数: $commit_count, 分支: $branch_list"
  else
    print_result "FAIL" "Git 仓库状态验证" "未找到提交记录（commit_count=$commit_count）"
  fi
}

# 清理测试目录
cleanup() {
  if [ "$QUIET" = false ]; then
    echo ""
    echo "清理测试目录: $TEST_REPO"
  fi
  rm -rf "$TEST_REPO"
}

# 主函数
main() {
  if [ "$QUIET" = false ]; then
    echo "========================================"
    echo "  验证项3：代码操作能力"
    echo "========================================"
    echo ""
  fi

  test_create_test_project
  test_claude_read_code
  test_claude_modify_code
  test_claude_git_init
  test_git_commit
  test_claude_git_branch
  test_verify_git_state

  # 清理
  cleanup

  # 输出汇总
  echo ""
  echo "----------------------------------------"
  if [ "$QUIET" = true ]; then
    echo "验证项3（代码操作）: PASS=$PASS_COUNT FAIL=$FAIL_COUNT"
  else
    echo -e "汇总: ${GREEN}PASS: $PASS_COUNT${NC} | ${RED}FAIL: $FAIL_COUNT${NC}"
    echo "----------------------------------------"
  fi

  [ $FAIL_COUNT -eq 0 ] && exit 0 || exit 1
}

main "$@"
