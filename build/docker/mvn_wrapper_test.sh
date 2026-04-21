#!/usr/bin/env bash
# mvn 包装器单元测试 —— 纯 bash 层验证参数过滤逻辑。
# 不依赖真实 Maven：把 /usr/local/bin/mvn.real 替换为假脚本，直接捕获其收到的参数。
#
# 运行：bash build/docker/mvn_wrapper_test.sh
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WRAPPER_SRC="${SCRIPT_DIR}/mvn-wrapper.sh"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

# 把 wrapper 复制一份并把硬编码的 REAL_MVN 路径重定向到 fake 脚本。
# 这样既保留 wrapper 的过滤逻辑，又能在宿主机无 Maven 环境下运行。
FAKE_REAL="${TMPDIR}/mvn.real"
WRAPPER="${TMPDIR}/mvn"

cat > "${FAKE_REAL}" <<'EOF'
#!/bin/bash
# 把收到的参数逐行写入 ARGS_FILE，便于断言
: > "${ARGS_FILE:?}"
for a in "$@"; do
    printf '%s\n' "$a" >> "${ARGS_FILE}"
done
EOF
chmod +x "${FAKE_REAL}"

sed "s|^REAL_MVN=.*|REAL_MVN=\"${FAKE_REAL}\"|" "${WRAPPER_SRC}" > "${WRAPPER}"
chmod +x "${WRAPPER}"

PASS=0
FAIL=0

run_case() {
  local desc="$1"
  shift
  local args_file="${TMPDIR}/args.$$"
  # 兼容零参数调用：${arr[@]+"${arr[@]}"} 在数组未设置或为空时展开为空
  local -a input=()
  if [ "$#" -gt 0 ]; then
    input=("$@")
  fi
  ARGS_FILE="${args_file}" bash "${WRAPPER}" ${input[@]+"${input[@]}"}
  # 第一行必须是强制 -Dmaven.repo.local=/workspace/.m2/repository
  local first
  first="$(head -n1 "${args_file}" || true)"
  if [ "${first}" != "-Dmaven.repo.local=/workspace/.m2/repository" ]; then
    echo "FAIL: ${desc} — 首参数不是强制 repo.local，实际为: ${first}"
    cat "${args_file}"
    FAIL=$((FAIL+1))
    return
  fi
  # 后续参数不得包含任何 maven.repo.local 覆盖
  if tail -n +2 "${args_file}" | grep -E '(^-Dmaven\.repo\.local=|^--define=maven\.repo\.local=)' >/dev/null; then
    echo "FAIL: ${desc} — 过滤后仍存在 maven.repo.local 覆盖参数"
    cat "${args_file}"
    FAIL=$((FAIL+1))
    return
  fi
  echo "PASS: ${desc}"
  PASS=$((PASS+1))
}

assert_contains() {
  local desc="$1" args_file="$2" needle="$3"
  if ! grep -Fx -- "${needle}" "${args_file}" >/dev/null; then
    echo "FAIL: ${desc} — 丢失期望参数: ${needle}"
    cat "${args_file}"
    FAIL=$((FAIL+1))
    return 1
  fi
  return 0
}

echo "=== mvn wrapper tests ==="

# 用例 1：无参数
run_case "无参数调用，仅强制注入 repo.local"

# 用例 2：普通参数（test、-DskipTests）保留
args_file="${TMPDIR}/args.case2"
ARGS_FILE="${args_file}" bash "${WRAPPER}" test -DskipTests=true
first="$(head -n1 "${args_file}")"
if [ "${first}" = "-Dmaven.repo.local=/workspace/.m2/repository" ]; then
  assert_contains "test 参数保留" "${args_file}" "test" &&
  assert_contains "-DskipTests 参数保留" "${args_file}" "-DskipTests=true" &&
  { echo "PASS: 普通参数保留，repo.local 强制注入"; PASS=$((PASS+1)); }
else
  echo "FAIL: 普通参数用例 — 首参数错误: ${first}"
  FAIL=$((FAIL+1))
fi

# 用例 3：Claude 传入覆盖路径，必须被过滤
args_file="${TMPDIR}/args.case3"
ARGS_FILE="${args_file}" bash "${WRAPPER}" test "-Dmaven.repo.local=/tmp/malicious-cache"
if grep -Fx -- '-Dmaven.repo.local=/tmp/malicious-cache' "${args_file}" >/dev/null; then
  echo "FAIL: 覆盖路径未被过滤"
  cat "${args_file}"
  FAIL=$((FAIL+1))
elif ! grep -Fx -- '-Dmaven.repo.local=/workspace/.m2/repository' "${args_file}" >/dev/null; then
  echo "FAIL: 强制 repo.local 缺失"
  cat "${args_file}"
  FAIL=$((FAIL+1))
else
  echo "PASS: 覆盖路径 -Dmaven.repo.local=/tmp/... 被过滤"
  PASS=$((PASS+1))
fi

# 用例 4：--define=maven.repo.local=... 长选项合并形式也被过滤
args_file="${TMPDIR}/args.case4"
ARGS_FILE="${args_file}" bash "${WRAPPER}" test "--define=maven.repo.local=/tmp/x"
if grep -Fx -- '--define=maven.repo.local=/tmp/x' "${args_file}" >/dev/null; then
  echo "FAIL: --define=maven.repo.local=... 未被过滤"
  cat "${args_file}"
  FAIL=$((FAIL+1))
else
  echo "PASS: --define=maven.repo.local=... 被过滤"
  PASS=$((PASS+1))
fi

# 用例 5：含空格的 goal 参数顺序保持（保护位置敏感参数）
args_file="${TMPDIR}/args.case5"
ARGS_FILE="${args_file}" bash "${WRAPPER}" clean install -U -Pdev
if [ "$(sed -n '2p' "${args_file}")" = "clean" ] &&
   [ "$(sed -n '3p' "${args_file}")" = "install" ] &&
   [ "$(sed -n '4p' "${args_file}")" = "-U" ] &&
   [ "$(sed -n '5p' "${args_file}")" = "-Pdev" ]; then
  echo "PASS: 位置参数顺序保持（clean install -U -Pdev）"
  PASS=$((PASS+1))
else
  echo "FAIL: 位置参数顺序错乱"
  cat "${args_file}"
  FAIL=$((FAIL+1))
fi

# 用例 6：参数中含空格 / 等号等特殊字符（Maven 不常见但验证数组传递）
args_file="${TMPDIR}/args.case6"
ARGS_FILE="${args_file}" bash "${WRAPPER}" -Dsome.prop="value with space"
if grep -Fx -- '-Dsome.prop=value with space' "${args_file}" >/dev/null; then
  echo "PASS: 含空格参数被原样保留"
  PASS=$((PASS+1))
else
  echo "FAIL: 含空格参数丢失"
  cat "${args_file}"
  FAIL=$((FAIL+1))
fi

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
if [ "${FAIL}" -gt 0 ]; then
  exit 1
fi
echo "ALL PASS"
