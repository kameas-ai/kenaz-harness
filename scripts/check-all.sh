#!/usr/bin/env bash
# Local-dev gate: run all CI lint checks without requiring a full CI run.
# Mirrors the steps in .github/workflows/pr.yml (lint-go job).
# Usage: bash scripts/check-all.sh
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$ROOT"

ok=0
fail=0

run_check() {
  local name="$1"
  local script="$2"
  printf "[check-all] %-45s " "$name..."
  if bash "$script" > /dev/null 2>&1; then
    echo "OK"
  else
    echo "FAIL"
    bash "$script" || true
    fail=1
  fi
}

run_check "credential hygiene lint"             scripts/ci/check-no-cred-bytes-in-rpc.sh
run_check "codegen idempotent"                  scripts/ci/check-codegen.sh
run_check "bindings-name guard"                 scripts/ci/check-binding-names.sh
run_check "emitter-isolation guard"             scripts/ci/check-emitter-isolation.sh
run_check "wailsjs-isolation guard"             scripts/ci/check-wailsjs-isolation.sh
run_check "single-persistence-file guard"       scripts/ci/check-single-persistence-file.sh
run_check "test-only-symbols guard"             scripts/ci/check-test-only-symbols.sh
run_check "no-credential-in-ui guard"           scripts/ci/check-no-credential-in-ui.sh
run_check "no-user-content-in-slog guard"       scripts/ci/check-no-user-content-in-slog.sh
run_check "csp guard"                           scripts/ci/check-csp.sh

echo ""
if [[ $fail -ne 0 ]]; then
  echo "[check-all] FAIL — one or more checks failed. See output above."
  exit 1
fi

echo "[check-all] All checks passed."
exit 0
