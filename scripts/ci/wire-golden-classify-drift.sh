#!/usr/bin/env bash
# wire-golden-classify-drift.sh — classify the drift from a live-tier update run
#
# Called by wire-golden-refresh.sh after the -update pass has been run.
# Inspects git diff (staged + unstaged) for testdata/wire_golden/ and outputs
# exactly one of:
#   no-drift          — no changes at all
#   response-drift    — only response.sse files changed
#   request-regression — request.json files changed (provider rejected our body)
#
# Usage:
#   bash scripts/ci/wire-golden-classify-drift.sh [adapter ...]
#
# The optional adapter arguments restrict the diff inspection to those adapter
# directories. When omitted, all of testdata/wire_golden/ is inspected.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GOLDEN_ROOT="$REPO_ROOT/testdata/wire_golden"

# Collect changed files under testdata/wire_golden/.
# git diff HEAD covers both staged and unstaged changes from the -update pass.
declare -a PATHS=()
if [ $# -gt 0 ]; then
  for adapter in "$@"; do
    PATHS+=("$GOLDEN_ROOT/$adapter")
  done
else
  PATHS+=("$GOLDEN_ROOT")
fi

# Build the list of changed fixture files.
changed_files="$(git diff HEAD --name-only -- "${PATHS[@]}" 2>/dev/null || true)"
staged_files="$(git diff --cached --name-only -- "${PATHS[@]}" 2>/dev/null || true)"
all_changed="$(printf '%s\n%s' "$changed_files" "$staged_files" | sort -u | grep -v '^$' || true)"

if [ -z "$all_changed" ]; then
  echo "no-drift"
  exit 0
fi

# Check if any changed file is request.json — that would be a request regression
# (our request body was either rejected causing a rewrite, or something overwrote
# the hand-authored golden).
request_side="$(echo "$all_changed" | grep 'request\.json' || true)"
response_side="$(echo "$all_changed" | grep 'response\.sse' || true)"
other_files="$(echo "$all_changed" | grep -v 'request\.json\|response\.sse' || true)"

if [ -n "$request_side" ] || [ -n "$other_files" ]; then
  # request.json changed or an unexpected file changed — treat as request-side regression.
  echo "request-regression"
  exit 0
fi

if [ -n "$response_side" ]; then
  echo "response-drift"
  exit 0
fi

# Fallback: changes present but not categorised — treat conservatively.
echo "request-regression"
