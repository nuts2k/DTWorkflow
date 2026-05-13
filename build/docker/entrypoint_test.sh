#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ENTRYPOINT="${ROOT}/build/docker/entrypoint.sh"
TMPDIR="$(mktemp -d)"
export CRED_HELPER_SCRIPT="${TMPDIR}/cred-helper"
WARNINGS_FILE="/tmp/.gen_tests_warnings"
trap 'chmod -R u+w "${TMPDIR}" 2>/dev/null || true; rm -rf "${TMPDIR}" "${WARNINGS_FILE}" "${CRED_HELPER_SCRIPT}"' EXIT

mkdir -p "${TMPDIR}/fakebin" "${TMPDIR}/home"

# ------------------------------------------------------------
# 可参数化的 git mock：通过环境变量控制各子命令行为
#   MOCK_AUTO_TEST_FETCH_RESULT  : auto-test/* 分支 fetch 结果，success|fail，默认 fail（分支不存在）
#   MOCK_MERGE_BASE_RESULT       : merge-base --is-ancestor 结果，ancestor|not-ancestor，默认 ancestor
#   MOCK_LOG_AUTHORS             : log base..branch --pretty=%ae 输出（换行分隔作者），默认空（仅 bot）
#   MOCK_GIT_PUSH_RESULT         : push 结果，success|fail，默认 success
# rev-parse 返回：origin/auto-test/* -> branchsha；origin/* -> basesha；其它 -> abc123
# ------------------------------------------------------------
cat > "${TMPDIR}/fakebin/git" <<'EOF'
#!/usr/bin/env bash
set -uo pipefail
echo "git $*" >> "${GIT_LOG:?}"
case "${1:-}" in
  clone)
    mkdir -p "${@: -1}/.git"
    ;;
  fetch)
    ref="${3:-}"
    case "${ref}" in
      auto-test/*)
        if [ "${MOCK_AUTO_TEST_FETCH_RESULT:-fail}" = "fail" ]; then
          echo "fatal: couldn't find remote ref ${ref}" >&2
          exit 1
        fi
        ;;
    esac
    echo "stdout:fetch"
    echo "stderr:fetch" >&2
    ;;
  checkout)
    echo "stdout:checkout"
    echo "stderr:checkout" >&2
    ;;
  remote|config)
    ;;
  rev-parse)
    case "${2:-}" in
      origin/auto-test/*)
        if [ "${MOCK_REV_PARSE_BRANCH_FAIL:-}" = "1" ]; then
          exit 1
        fi
        echo "branchsha"
        ;;
      origin/*) echo "basesha" ;;
      *)        echo "abc123" ;;
    esac
    ;;
  merge-base)
    if [ "${2:-}" = "--is-ancestor" ]; then
      if [ "${MOCK_MERGE_BASE_RESULT:-ancestor}" = "not-ancestor" ]; then
        exit 1
      fi
      exit 0
    fi
    ;;
  log)
    # 仅当请求 %ae 格式时返回 mock 作者列表；其它 log 调用（如 --oneline -1）回落默认串
    case "$*" in
      *--pretty=format:%ae*)
        if [ -n "${MOCK_LOG_AUTHORS:-}" ]; then
          printf '%s\n' "${MOCK_LOG_AUTHORS}"
        fi
        ;;
      *)
        echo "abc123 test"
        ;;
    esac
    ;;
  push)
    # 无论成功失败都向 stderr 输出含 token 的认证串（供脱敏断言）
    echo "push attempt to https://token:${GITEA_TOKEN:-__none__}@host/repo.git" >&2
    if [ "${MOCK_GIT_PUSH_RESULT:-success}" = "fail" ]; then
      echo "fatal: Updates were rejected because the tip of your current branch is behind" >&2
      exit 1
    fi
    ;;
esac
EOF
chmod +x "${TMPDIR}/fakebin/git"

# sed 透传（某些 PATH 精简环境下需要）
cat > "${TMPDIR}/fakebin/sed" <<'SEDEOF'
#!/usr/bin/env bash
exec /usr/bin/sed "$@"
SEDEOF
chmod +x "${TMPDIR}/fakebin/sed"

PASS=0
FAIL=0

assert_contains() {
  local desc="$1" haystack="$2" needle="$3"
  if echo "${haystack}" | grep -qF -- "${needle}"; then
    echo "PASS: ${desc}"
    (( PASS++ ))
  else
    echo "FAIL: ${desc} — 期望包含: ${needle}"
    echo "---- 实际 ----"
    echo "${haystack}"
    echo "--------------"
    (( FAIL++ ))
  fi
}

assert_not_contains() {
  local desc="$1" haystack="$2" needle="$3"
  if echo "${haystack}" | grep -qF -- "${needle}"; then
    echo "FAIL: ${desc} — 期望不包含: ${needle}"
    echo "---- 实际 ----"
    echo "${haystack}"
    echo "--------------"
    (( FAIL++ ))
  else
    echo "PASS: ${desc}"
    (( PASS++ ))
  fi
}

# ==================== 既有 M4.1 用例 ====================

run_case() {
  local desc="$1" task_type="$2" issue_ref="$3" expect_fetch="$4"
  local repo_dir="${TMPDIR}/repo-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="${task_type}" \
  ISSUE_REF="${issue_ref}" \
  bash "${ENTRYPOINT}" true >/dev/null 2>&1 || true

  local log
  log="$(cat "${TMPDIR}/git.log")"

  if [ "${expect_fetch}" = "yes" ]; then
    if echo "${log}" | grep -q "git fetch origin ${issue_ref}"; then
      echo "PASS: ${desc} — fetch found"
      (( PASS++ ))
    else
      echo "FAIL: ${desc} — expected 'git fetch origin ${issue_ref}' in log:"
      echo "${log}"
      (( FAIL++ ))
    fi
    if echo "${log}" | grep -q "git checkout FETCH_HEAD"; then
      echo "PASS: ${desc} — checkout found"
      (( PASS++ ))
    else
      echo "FAIL: ${desc} — expected 'git checkout FETCH_HEAD' in log:"
      echo "${log}"
      (( FAIL++ ))
    fi
  else
    if echo "${log}" | grep -qE "git fetch origin [^ ]"; then
      echo "FAIL: ${desc} — unexpected fetch with ref in log:"
      echo "${log}"
      (( FAIL++ ))
    else
      echo "PASS: ${desc} — no ref fetch"
      (( PASS++ ))
    fi
    if echo "${log}" | grep -q "git checkout FETCH_HEAD"; then
      echo "FAIL: ${desc} — unexpected checkout FETCH_HEAD in log:"
      echo "${log}"
      (( FAIL++ ))
    else
      echo "PASS: ${desc} — no checkout FETCH_HEAD"
      (( PASS++ ))
    fi
  fi
}

run_stdout_case() {
  local desc="$1" task_type="$2"
  local repo_dir="${TMPDIR}/repo-stdout-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  local stdout_file="${TMPDIR}/stdout-${task_type}.log"
  local stderr_file="${TMPDIR}/stderr-${task_type}.log"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="${task_type}" \
  ISSUE_REF="feature/auth" \
  bash "${ENTRYPOINT}" true >"${stdout_file}" 2>"${stderr_file}" || true

  if [ -s "${stdout_file}" ]; then
    echo "FAIL: ${desc} — stdout should be empty, got:"
    cat "${stdout_file}"
    (( FAIL++ ))
  else
    echo "PASS: ${desc} — stdout clean"
    (( PASS++ ))
  fi
}

run_build_cache_case() {
  local desc="$1" task_type="$2"
  local repo_dir="${TMPDIR}/repo-cache-${task_type}-$$"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  local stdout_file="${TMPDIR}/cache-${task_type}.out"
  local stderr_file="${TMPDIR}/cache-${task_type}.err"

  # mvn 包装器不在宿主机 /usr/local/bin 内（由 worker-full 镜像烘焙提供），
  # 此处仅断言环境变量被正确导出——包装器本身的行为由 mvn_wrapper_test.sh 单独覆盖，
  # 构建期由 Dockerfile.worker-full 的 `mvn --version` 烟囱测试把关。
  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="${task_type}" \
  ISSUE_REF="feature/auth" \
  BASE_REF="main" \
  bash "${ENTRYPOINT}" bash -lc 'printf "%s\n" "$MAVEN_OPTS"; printf "%s\n" "$GRADLE_USER_HOME"; test -z "${GITEA_TOKEN:-}"; test -z "${DTWORKFLOW_FIX_REVIEW_PUSH_TOKEN:-}"' >"${stdout_file}" 2>"${stderr_file}" || true

  if grep -qx -- '-Dmaven.repo.local=/workspace/.m2/repository' "${stdout_file}" &&
     grep -qx -- '/workspace/.gradle' "${stdout_file}"; then
    echo "PASS: ${desc} — build cache env exported"
    (( PASS++ ))
  else
    echo "FAIL: ${desc} — expected build cache env in stdout:"
    cat "${stdout_file}"
    (( FAIL++ ))
  fi
}

run_gen_tests_credentials_case() {
  local repo_dir="${TMPDIR}/repo-gentests-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  rm -f "${WARNINGS_FILE}"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="gen_tests" \
  bash "${ENTRYPOINT}" true >/dev/null 2>&1 || true

  local log
  log="$(cat "${TMPDIR}/git.log")"

  if echo "${log}" | grep -q "git remote set-url origin https://gitea.example.com/owner/repo.git"; then
    echo "PASS: gen_tests — origin URL 已脱敏（不含 token）"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 origin URL 已 set-url 为不含 token，实际 log:"
    echo "${log}"
    (( FAIL++ ))
  fi

  if echo "${log}" | grep -q "git config --global credential.helper ${CRED_HELPER_SCRIPT}"; then
    echo "PASS: gen_tests — credential helper 已配置"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 credential.helper 指向 ${CRED_HELPER_SCRIPT}，实际 log:"
    echo "${log}"
    (( FAIL++ ))
  fi

  if echo "${log}" | grep -q "git config --global user.name DTWorkflow Bot"; then
    echo "PASS: gen_tests — git identity name 已设置"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 user.name=DTWorkflow Bot，实际 log:"
    echo "${log}"
    (( FAIL++ ))
  fi

  if echo "${log}" | grep -q "git config --global user.email dtworkflow-bot@noreply.local"; then
    echo "PASS: gen_tests — git identity email 已设置"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 user.email=dtworkflow-bot@noreply.local，实际 log:"
    echo "${log}"
    (( FAIL++ ))
  fi



  if [ -x "${repo_dir}/.git/hooks/pre-push" ] && grep -q "refs/heads/auto-test/\*" "${repo_dir}/.git/hooks/pre-push"; then
    echo "PASS: gen_tests — pre-push hook 已限制仅允许推送 auto-test/*"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 pre-push hook 限制 auto-test/*，实际缺失或内容不符"
    (( FAIL++ ))
  fi
}

run_fix_review_case() {
  local repo_dir="${TMPDIR}/repo-fix-review-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  local stdout_file="${TMPDIR}/fix-review.out"
  local stderr_file="${TMPDIR}/fix-review.err"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="fix_review" \
  PR_NUMBER="42" \
  HEAD_REF="feature/review-fix" \
  BASE_REF="main" \
  bash "${ENTRYPOINT}" bash -lc 'printf "%s\n" "$MAVEN_OPTS"; printf "%s\n" "$GRADLE_USER_HOME"' >"${stdout_file}" 2>"${stderr_file}" || true

  local log
  log="$(cat "${TMPDIR}/git.log")"

  assert_contains "fix_review — fetch PR head 到 remote tracking" \
    "${log}" "git fetch origin feature/review-fix:refs/remotes/origin/feature/review-fix"
  assert_contains "fix_review — checkout 到 PR head 分支" \
    "${log}" "git checkout -B feature/review-fix origin/feature/review-fix"
  assert_contains "fix_review — 设置 upstream 以支持普通 git push" \
    "${log}" "git branch --set-upstream-to=origin/feature/review-fix feature/review-fix"
  assert_contains "fix_review — 获取 base 分支供 diff/上下文使用" \
    "${log}" "git fetch origin main"
  assert_contains "fix_review — origin URL 已脱敏" \
    "${log}" "git remote set-url origin https://gitea.example.com/owner/repo.git"
  assert_contains "fix_review — 全局 credential helper 已禁用" \
    "${log}" "git config --global credential.helper "
  assert_contains "fix_review — git identity name 已设置" \
    "${log}" "git config --global user.name DTWorkflow Bot"

  if [ -x "${repo_dir}/.git/hooks/pre-push" ] &&
     grep -q "DTWORKFLOW_FIX_REVIEW_HEAD_REF" "${repo_dir}/.git/hooks/pre-push"; then
    echo "PASS: fix_review — pre-push hook 限制当前 PR head 分支"
    (( PASS++ ))
  else
    echo "FAIL: fix_review — 预期 pre-push hook 限制当前 PR head 分支"
    (( FAIL++ ))
  fi

  assert_contains "fix_review — 任务命令成功后由入口脚本受控 push" \
    "${log}" "push origin HEAD:refs/heads/feature/review-fix"

  if [ -x "${repo_dir}/../.dtworkflow-bin/git" ]; then
    echo "PASS: fix_review — git wrapper 已安装"
    (( PASS++ ))
  elif [ -x "/workspace/.dtworkflow-bin/git" ]; then
    echo "PASS: fix_review — git wrapper 已安装"
    (( PASS++ ))
  elif [ -x "${repo_dir}/../.dtworkflow-bin/dtworkflow-fix-review-push" ]; then
    echo "PASS: fix_review — safe push helper 已安装"
    (( PASS++ ))
  else
    # 脚本实际写入 /workspace；宿主测试环境无法可靠访问时，退化检查 stdout 中 PATH 行不可用，故检查日志即可。
    if grep -q "迭代评审修复模式已启用" "${stderr_file}"; then
      echo "PASS: fix_review — safe push helper 安装流程已执行"
      (( PASS++ ))
    else
      echo "FAIL: fix_review — 未检测到 safe push helper 安装流程"
      (( FAIL++ ))
    fi
  fi

  if grep -qx -- '-Dmaven.repo.local=/workspace/.m2/repository' "${stdout_file}" &&
     grep -qx -- '/workspace/.gradle' "${stdout_file}"; then
    echo "PASS: fix_review — build cache env exported"
    (( PASS++ ))
  else
    echo "FAIL: fix_review — expected build cache env in stdout:"
    cat "${stdout_file}"
    (( FAIL++ ))
  fi

  assert_not_contains "fix_review — push stderr 不泄漏 token" \
    "$(cat "${stderr_file}")" "token"
}

run_fix_review_missing_head_case() {
  local repo_dir="${TMPDIR}/repo-fix-review-missing-head-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  local stdout_file="${TMPDIR}/fix-review-missing.out"
  local stderr_file="${TMPDIR}/fix-review-missing.err"

  set +e
  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="fix_review" \
  PR_NUMBER="42" \
  BASE_REF="main" \
  bash "${ENTRYPOINT}" true >"${stdout_file}" 2>"${stderr_file}"
  local code=$?
  set -e

  if [ "${code}" -eq 2 ]; then
    echo "PASS: fix_review — HEAD_REF 缺失时确定性失败"
    (( PASS++ ))
  else
    echo "FAIL: fix_review — HEAD_REF 缺失时应 exit 2，实际 ${code}"
    cat "${stderr_file}"
    (( FAIL++ ))
  fi
}

run_fix_review_invalid_head_case() {
  local repo_dir="${TMPDIR}/repo-fix-review-invalid-head-$$-${RANDOM}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"
  local stdout_file="${TMPDIR}/fix-review-invalid.out"
  local stderr_file="${TMPDIR}/fix-review-invalid.err"

  set +e
  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="fix_review" \
  PR_NUMBER="42" \
  HEAD_REF='feature/foo";id;"' \
  BASE_REF="main" \
  bash "${ENTRYPOINT}" true >"${stdout_file}" 2>"${stderr_file}"
  local code=$?
  set -e

  if [ "${code}" -eq 2 ]; then
    echo "PASS: fix_review — 非法 HEAD_REF 确定性失败"
    (( PASS++ ))
  else
    echo "FAIL: fix_review — 非法 HEAD_REF 应 exit 2，实际 ${code}"
    cat "${stderr_file}"
    (( FAIL++ ))
  fi
}

# ==================== 运行 M4.1 既有用例 ====================

echo "=== Entrypoint Behavior Tests ==="

run_case "fix_issue with ISSUE_REF=feature/auth" "fix_issue" "feature/auth" "yes"
run_case "fix_issue with empty ISSUE_REF" "fix_issue" "" "no"
run_case "analyze_issue with ISSUE_REF=feature/auth" "analyze_issue" "feature/auth" "yes"
run_stdout_case "fix_issue should not leak git output to stdout" "fix_issue"
run_stdout_case "analyze_issue should not leak git output to stdout" "analyze_issue"
run_build_cache_case "fix_issue should enable build cache redirect" "fix_issue"
run_build_cache_case "gen_tests should enable build cache redirect" "gen_tests"
run_gen_tests_credentials_case
run_stdout_case "gen_tests should not leak git output to stdout" "gen_tests"
run_fix_review_case
run_fix_review_missing_head_case
run_fix_review_invalid_head_case

# ==================== M4.2 新增：gen_tests 断点续传用例 ====================

echo ""
echo "--- M4.2 gen_tests 断点续传断言 ---"

# 运行 gen_tests 场景并收集输出；返回 log / stderr / warnings
# 用法：run_gen_tests_resume <repo_tag> <module_san> <base_ref> <fetch_result> <merge_base> <log_authors> <push_result> [rev_parse_branch_fail]
# 约定：将 git.log、stderr、warnings 分别写入 TMPDIR 下带 tag 的文件
# rev_parse_branch_fail：可选，"1" 表示 rev-parse origin/auto-test/* 返回非零（降级路径）
run_gen_tests_resume() {
  local tag="$1" module_san="$2" base_ref="$3"
  local fetch_result="$4" merge_base="$5" log_authors="$6" push_result="$7"
  local rev_parse_branch_fail="${8:-}"
  local repo_dir="${TMPDIR}/repo-resume-${tag}"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git-${tag}.log"
  rm -f "${WARNINGS_FILE}"
  local stderr_file="${TMPDIR}/stderr-${tag}.log"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git-${tag}.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="secr3tT0kenXYZ" \
  TASK_TYPE="gen_tests" \
  MODULE_SANITIZED="${module_san}" \
  BASE_REF="${base_ref}" \
  MOCK_AUTO_TEST_FETCH_RESULT="${fetch_result}" \
  MOCK_MERGE_BASE_RESULT="${merge_base}" \
  MOCK_LOG_AUTHORS="${log_authors}" \
  MOCK_GIT_PUSH_RESULT="${push_result}" \
  MOCK_REV_PARSE_BRANCH_FAIL="${rev_parse_branch_fail}" \
  bash "${ENTRYPOINT}" true >/dev/null 2>"${stderr_file}" || true
}

# ---- S1：BASE_REF 前置 checkout ----
run_gen_tests_resume "s1" "mymod" "release-1.2" "fail" "ancestor" "" "success"
LOG_S1="$(cat "${TMPDIR}/git-s1.log")"
# 断言 1：BASE_REF 先 fetch
assert_contains "S1 — BASE_REF 前置 fetch (git fetch origin release-1.2)" \
  "${LOG_S1}" "git fetch origin release-1.2"
# 断言 2：fetch BASE_REF 之后紧跟 checkout FETCH_HEAD（BASE_REF 对齐完成再进入断点续传）
if echo "${LOG_S1}" | awk '
  /git fetch origin release-1.2/ { f=1; next }
  f && /git checkout FETCH_HEAD/ { ok=1; exit }
  END { exit ok?0:1 }
'; then
  echo "PASS: S1 — BASE_REF fetch 后紧跟 checkout FETCH_HEAD"
  (( PASS++ ))
else
  echo "FAIL: S1 — 期望 fetch release-1.2 后紧跟 checkout FETCH_HEAD"
  echo "${LOG_S1}"
  (( FAIL++ ))
fi

# ---- S2：首次新建（auto-test/* fetch 失败） ----
run_gen_tests_resume "s2" "mymod" "main" "fail" "ancestor" "" "success"
LOG_S2="$(cat "${TMPDIR}/git-s2.log")"
# 断言 3：fetch auto-test/mymod 失败后走新建分支路径
assert_contains "S2 — 首次新建：checkout -B auto-test/mymod origin/main" \
  "${LOG_S2}" "git checkout -B auto-test/mymod origin/main"
# 断言 4：首次新建不应出现 force-with-lease
assert_not_contains "S2 — 首次新建不触发 force-with-lease" \
  "${LOG_S2}" "git push --force-with-lease"

# ---- S3：复用（分支存在 + 含 base + 仅 bot） ----
run_gen_tests_resume "s3" "mymod" "main" "success" "ancestor" "" "success"
LOG_S3="$(cat "${TMPDIR}/git-s3.log")"
# 断言 5：复用走 checkout -B <branch> origin/<branch>
assert_contains "S3 — 复用：checkout -B auto-test/mymod origin/auto-test/mymod" \
  "${LOG_S3}" "git checkout -B auto-test/mymod origin/auto-test/mymod"
# 断言 6：复用不触发 force-with-lease
assert_not_contains "S3 — 复用不触发 force-with-lease" \
  "${LOG_S3}" "git push --force-with-lease"

# ---- S4：落后 base 重置 + force-with-lease 成功 ----
run_gen_tests_resume "s4" "mymod" "main" "success" "not-ancestor" "" "success"
LOG_S4="$(cat "${TMPDIR}/git-s4.log")"
# 断言 7：重置到 BASE_SHA
assert_contains "S4 — 落后 base：checkout -B auto-test/mymod basesha" \
  "${LOG_S4}" "git checkout -B auto-test/mymod basesha"
# 断言 8：主动 force-with-lease 对齐远程（绑定 BRANCH_SHA）
assert_contains "S4 — 主动 force-with-lease=auto-test/mymod:branchsha origin auto-test/mymod" \
  "${LOG_S4}" "git push --force-with-lease=auto-test/mymod:branchsha origin auto-test/mymod"
# 断言 9：warnings 写入 RESET_PUSHED=1
if [ -f "${WARNINGS_FILE}" ] && grep -q "^AUTO_TEST_BRANCH_RESET_PUSHED=1$" "${WARNINGS_FILE}"; then
  echo "PASS: S4 — /tmp/.gen_tests_warnings 含 AUTO_TEST_BRANCH_RESET_PUSHED=1"
  (( PASS++ ))
else
  echo "FAIL: S4 — 预期 ${WARNINGS_FILE} 含 AUTO_TEST_BRANCH_RESET_PUSHED=1"
  [ -f "${WARNINGS_FILE}" ] && cat "${WARNINGS_FILE}" || echo "(warnings 文件不存在)"
  (( FAIL++ ))
fi

# ---- S5：作者污染重置 + force-with-lease 成功 ----
run_gen_tests_resume "s5" "mymod" "main" "success" "ancestor" "evil@example.com" "success"
LOG_S5="$(cat "${TMPDIR}/git-s5.log")"
assert_contains "S5 — 污染重置：checkout -B auto-test/mymod basesha" \
  "${LOG_S5}" "git checkout -B auto-test/mymod basesha"
assert_contains "S5 — 污染重置触发 force-with-lease" \
  "${LOG_S5}" "git push --force-with-lease=auto-test/mymod:branchsha origin auto-test/mymod"

# ---- S6：远程对齐失败 ----
run_gen_tests_resume "s6" "mymod" "main" "success" "not-ancestor" "" "fail"
if [ -f "${WARNINGS_FILE}" ] && grep -q "^AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=1$" "${WARNINGS_FILE}"; then
  echo "PASS: S6 — 远程对齐失败写入 AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=1"
  (( PASS++ ))
else
  echo "FAIL: S6 — 预期 ${WARNINGS_FILE} 含 AUTO_TEST_BRANCH_RESET_REMOTE_FAILED=1"
  [ -f "${WARNINGS_FILE}" ] && cat "${WARNINGS_FILE}" || echo "(warnings 文件不存在)"
  (( FAIL++ ))
fi

# ---- S7：MODULE_SANITIZED 为空 → 回落 all ----
run_gen_tests_resume "s7" "" "main" "fail" "ancestor" "" "success"
LOG_S7="$(cat "${TMPDIR}/git-s7.log")"
assert_contains "S7 — MODULE_SANITIZED 空值回落：auto-test/all 出现在 fetch" \
  "${LOG_S7}" "git fetch origin auto-test/all"
assert_contains "S7 — MODULE_SANITIZED 空值回落：checkout -B auto-test/all origin/main" \
  "${LOG_S7}" "git checkout -B auto-test/all origin/main"

# ---- S8：pre-push hook 回归（M4.1 不退化） ----
REPO_S2="${TMPDIR}/repo-resume-s2"
if [ -x "${REPO_S2}/.git/hooks/pre-push" ] && grep -q "refs/heads/auto-test/\*" "${REPO_S2}/.git/hooks/pre-push"; then
  echo "PASS: S8 — pre-push hook 仍限制 auto-test/* (M4.1 不退化)"
  (( PASS++ ))
else
  echo "FAIL: S8 — pre-push hook 缺失或未限制 auto-test/*"
  (( FAIL++ ))
fi

# ---- S9：Token 脱敏 ----
# S4 场景下 force-with-lease push 执行；mock git 在 stderr 输出了完整 token
# entrypoint 的 `git push ... 2>&1 | sed "s|${GITEA_TOKEN}|***|g" >&2` 应将其替换为 ***
STDERR_S4="$(cat "${TMPDIR}/stderr-s4.log")"
assert_not_contains "S9 — S4 stderr 不含原始 token (sed 脱敏生效)" \
  "${STDERR_S4}" "secr3tT0kenXYZ"
# 进一步：脱敏后应含 *** 占位
assert_contains "S9 — S4 stderr 含脱敏占位 ***" \
  "${STDERR_S4}" "***"

# ---- S10：Warnings 路径约定 ----
# S4 结束后立刻断言文件路径恰为 /tmp/.gen_tests_warnings（不是其它目录）
run_gen_tests_resume "s10" "mymod" "main" "success" "not-ancestor" "" "success"
if [ -f "/tmp/.gen_tests_warnings" ]; then
  echo "PASS: S10 — warnings 写入路径为 /tmp/.gen_tests_warnings"
  (( PASS++ ))
else
  echo "FAIL: S10 — 预期 /tmp/.gen_tests_warnings 存在"
  (( FAIL++ ))
fi

# ---- S11：rev-parse 失败降级 + force-with-lease 无 SHA 绑定 ----
# 验证 Critical #1 修复：BRANCH_SHA="" 时不使用 refname: 空 expected SHA 语义
run_gen_tests_resume "s11" "mymod" "main" "success" "not-ancestor" "" "success" "1"
LOG_S11="$(cat "${TMPDIR}/git-s11.log")"
# 重置应当发生（RESET_REASON="分支 SHA 解析失败"）
assert_contains "S11 — rev-parse 失败：checkout -B auto-test/mymod basesha（重置发生）" \
  "${LOG_S11}" "git checkout -B auto-test/mymod basesha"
# force-with-lease 应不带 :SHA（仅绑定 refname，git 自动以远程跟踪值为期望）
assert_contains "S11 — rev-parse 失败：force-with-lease 仅绑定 refname（无 :SHA）" \
  "${LOG_S11}" "git push --force-with-lease=auto-test/mymod origin auto-test/mymod"
assert_not_contains "S11 — rev-parse 失败：不应出现空 SHA 绑定（refname: 语义）" \
  "${LOG_S11}" "git push --force-with-lease=auto-test/mymod: origin"
# push 成功时应写入 AUTO_TEST_BRANCH_RESET_PUSHED=1
if [ -f "${WARNINGS_FILE}" ] && grep -q "^AUTO_TEST_BRANCH_RESET_PUSHED=1$" "${WARNINGS_FILE}"; then
  echo "PASS: S11 — rev-parse 失败降级写入 AUTO_TEST_BRANCH_RESET_PUSHED=1"
  (( PASS++ ))
else
  echo "FAIL: S11 — 预期 ${WARNINGS_FILE} 含 AUTO_TEST_BRANCH_RESET_PUSHED=1"
  [ -f "${WARNINGS_FILE}" ] && cat "${WARNINGS_FILE}" || echo "(warnings 文件不存在)"
  (( FAIL++ ))
fi

# ==================== 汇总 ====================

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
if [ "${FAIL}" -gt 0 ]; then
  exit 1
fi
echo "ALL PASS"
