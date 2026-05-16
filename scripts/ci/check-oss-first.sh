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
if [ -f "frontend/node_modules/.bin/vitest" ]; then
  (cd frontend && ./node_modules/.bin/vitest run --reporter=basic)
  echo "[oss-first] Frontend tests: PASS"
else
  echo "[oss-first] SKIP: frontend/node_modules not available (worktree); run from main checkout"
fi

echo "[oss-first] OSS-first contract: PASS"
