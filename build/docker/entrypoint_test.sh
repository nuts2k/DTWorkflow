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
  fetch|checkout|remote|config)
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
  local desc="$1" issue_ref="$2" expect_fetch="$3"
  local repo_dir="${TMPDIR}/repo-$$"
  rm -rf "${repo_dir}"
  : > "${TMPDIR}/git.log"

  PATH="${TMPDIR}/fakebin:/usr/bin:/bin" \
  HOME="${TMPDIR}/home" \
  GIT_LOG="${TMPDIR}/git.log" \
  REPO_DIR="${repo_dir}" \
  REPO_CLONE_URL="https://gitea.example.com/owner/repo.git" \
  GITEA_TOKEN="token" \
  TASK_TYPE="fix_issue" \
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

echo "=== Entrypoint Behavior Tests ==="

run_case "fix_issue with ISSUE_REF=feature/auth" "feature/auth" "yes"
run_case "fix_issue with empty ISSUE_REF" "" "no"

echo ""
echo "Results: ${PASS} passed, ${FAIL} failed"
if [ "${FAIL}" -gt 0 ]; then
  exit 1
fi
echo "ALL PASS"
