#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
ENTRYPOINT="${ROOT}/build/docker/entrypoint.sh"
TMPDIR="$(mktemp -d)"
trap 'rm -rf "${TMPDIR}"' EXIT

mkdir -p "${TMPDIR}/fakebin" "${TMPDIR}/home"

cat > "${TMPDIR}/fakebin/git" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
echo "git $*" >> "${GIT_LOG:?}"
case "${1:-}" in
  clone)
    mkdir -p "${@: -1}/.git"
    ;;
  fetch|checkout)
    echo "stdout:${1}"
    echo "stderr:${1}" >&2
    ;;
  remote|config)
    ;;
  rev-parse)
    echo "abc123"
    ;;
  log)
    echo "abc123 test"
    ;;
esac
EOF
chmod +x "${TMPDIR}/fakebin/git"

# 辅助：sed 也需要能找到
cat > "${TMPDIR}/fakebin/sed" <<'SEDEOF'
#!/usr/bin/env bash
exec /usr/bin/sed "$@"
SEDEOF
chmod +x "${TMPDIR}/fakebin/sed"

PASS=0
FAIL=0

run_case() {
  local desc="$1" task_type="$2" issue_ref="$3" expect_fetch="$4"
  local repo_dir="${TMPDIR}/repo-$$"
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
    if echo "${log}" | grep -q "git fetch origin" | head -1 && ! echo "${log}" | grep -q "git fetch origin$"; then
      # 检查是否有带参数的 fetch（不算 clone 过程的 fetch）
      if echo "${log}" | grep -qE "git fetch origin [^ ]"; then
        echo "FAIL: ${desc} — unexpected fetch with ref in log:"
        echo "${log}"
        (( FAIL++ ))
      else
        echo "PASS: ${desc} — no ref fetch"
        (( PASS++ ))
      fi
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
  local repo_dir="${TMPDIR}/repo-stdout-$$"
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

run_gen_tests_credentials_case() {
  local repo_dir="${TMPDIR}/repo-gentests-$$"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"

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

  if echo "${log}" | grep -q "git config --global credential.helper /tmp/.git-credential-helper"; then
    echo "PASS: gen_tests — credential helper 已配置"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 credential.helper 指向 /tmp/.git-credential-helper，实际 log:"
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

  if [ -x "/tmp/.git-credential-helper" ]; then
    echo "PASS: gen_tests — credential helper 脚本已创建并可执行"
    (( PASS++ ))
  else
    echo "FAIL: gen_tests — 预期 /tmp/.git-credential-helper 已创建，实际未找到或不可执行"
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

echo "=== Entrypoint Behavior Tests ==="

run_case "fix_issue with ISSUE_REF=feature/auth" "fix_issue" "feature/auth" "yes"
run_case "fix_issue with empty ISSUE_REF" "fix_issue" "" "no"
run_case "analyze_issue with ISSUE_REF=feature/auth" "analyze_issue" "feature/auth" "yes"
run_stdout_case "fix_issue should not leak git output to stdout" "fix_issue"
run_stdout_case "analyze_issue should not leak git output to stdout" "analyze_issue"
run_gen_tests_credentials_case
run_stdout_case "gen_tests should not leak git output to stdout" "gen_tests"

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
if [ "${FAIL}" -gt 0 ]; then
  exit 1
fi
echo "ALL PASS"
