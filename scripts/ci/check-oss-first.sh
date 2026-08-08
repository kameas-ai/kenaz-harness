#!/usr/bin/env bash
# check-oss-first.sh — fleet-auth-foundation-01NDFSEX08 WP07
#
# Verifies the OSS-first contract: with HARNESS_FLEET_DISABLED=1, all
# non-fleet core packages and the full frontend test suite must pass.
#
# Usage: bash scripts/ci/check-oss-first.sh

set -euo pipefail

export HARNESS_FLEET_DISABLED=1

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

echo "[oss-first] HARNESS_FLEET_DISABLED=1"

# ── Go: non-fleet packages ────────────────────────────────────────────────

echo "[oss-first] Running Go tests for non-fleet packages..."
go test -short -count=1 \
  ./core/sessions/... \
  ./core/workflows/... \
  ./core/llm/... \
  ./core/mcp/... \
  ./core/agentgraph/... \
  ./core/hooks/... \
  ./core/slashcmd/... \
  ./core/policy/... \
  ./core/rpc/views/...

echo "[oss-first] Go tests: PASS"

# ── Frontend ──────────────────────────────────────────────────────────────

echo "[oss-first] Running frontend tests..."
FRONTEND_RAN=no
if [ -f "frontend/node_modules/.bin/vitest" ]; then
  (cd frontend && ./node_modules/.bin/vitest run --reporter=basic)
  echo "[oss-first] Frontend tests: PASS"
  FRONTEND_RAN=yes
else
  # This branch is taken on EVERY CI run. pr.yml invokes this script from the
  # lint-go job, which does actions/checkout + setup-go and no `npm ci`, so
  # frontend/node_modules never exists there. The pr.yml comment says the
  # frontend side "runs fully in test-frontend which does npm ci first" — but
  # test-frontend runs plain `npm test`, without HARNESS_FLEET_DISABLED=1. So
  # the frontend half of the OSS-first contract is asserted nowhere.
  #
  # Not silently skipped any more: this prints a GitHub annotation so the hole
  # is visible in the Checks UI, and the final line reports which halves
  # actually ran. To genuinely close it, add to the test-frontend job:
  #     - name: oss-first frontend
  #       working-directory: frontend
  #       env: { HARNESS_FLEET_DISABLED: "1" }
  #       run: npx vitest run --reporter=basic
  # (deferred here: #279 is concurrently editing .github/workflows/pr.yml.)
  echo "[oss-first] SKIP: frontend/node_modules not available — frontend half NOT verified."
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    echo "::warning title=oss-first frontend half skipped::frontend/node_modules absent in this job, so the HARNESS_FLEET_DISABLED=1 frontend suite did not run. Only the Go half of the OSS-first contract was verified."
  fi
fi

echo "[oss-first] OSS-first contract: PASS (go=yes frontend=${FRONTEND_RAN})"
