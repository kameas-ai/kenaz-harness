#!/usr/bin/env bash
# wire-golden-refresh.sh — live-tier fixture refresh + auto-PR generator
#
# Called by .github/workflows/wire-golden-live.yml.
# For each adapter whose TEST_<ADAPTER>_KEY env var is set:
#   1. Re-runs the WireGolden tests with -update to record fresh response.sse.
#   2. Runs the scrubber over the recorded fixtures.
#   3. Calls wire-golden-classify-drift.sh to decide the drift category.
# Based on the aggregate drift category, opens one of:
#   - Auto-PR on rolling branch auto/wire-golden-refresh (response-only drift).
#   - Fresh regression-labelled PR (request-side failure).
#   - No PR (no drift).
#
# Outputs (via $GITHUB_OUTPUT when running under Actions):
#   drift_outcome   — one of: no-drift | response-drift | request-regression | skipped
#   auto_pr_url     — URL of auto-PR (response-drift case only)
#   regression_pr_url — URL of regression PR (request-regression case only)
#
# Environment variables consumed:
#   TEST_ANTHROPIC_KEY, TEST_OPENAI_KEY, TEST_OPENROUTER_KEY, TEST_BEDROCK_KEY
#   ADAPTER_FILTER   — optional single adapter name to restrict run
#   DRY_RUN          — set to "true" to detect drift without opening PRs
#   GH_TOKEN         — GitHub token (for gh pr create)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT_DIR="$REPO_ROOT/scripts/ci"
DATE="$(date -u +%Y-%m-%d)"
RUN_BRANCH="auto/wire-golden-refresh"
DRIFT_REPORT_DIR="$REPO_ROOT/docs"

# ---- helpers ----------------------------------------------------------------

log() { echo "[wire-golden-refresh] $*"; }

set_output() {
  if [ -n "${GITHUB_OUTPUT:-}" ]; then
    echo "$1=$2" >> "$GITHUB_OUTPUT"
  fi
}

# ---- adapter list -----------------------------------------------------------

ALL_ADAPTERS=(anthropic openai openrouter bedrock)

if [ -n "${ADAPTER_FILTER:-}" ]; then
  ADAPTERS=("$ADAPTER_FILTER")
else
  ADAPTERS=("${ALL_ADAPTERS[@]}")
fi

# ---- per-adapter live-update ------------------------------------------------

declare -a ADAPTERS_UPDATED=()
declare -a ADAPTERS_SKIPPED=()
declare -a ADAPTERS_REQUEST_FAILED=()
OVERALL_DRIFT="no-drift"

for adapter in "${ADAPTERS[@]}"; do
  upper_adapter="${adapter^^}"
  # Accept both WIRE_GOLDEN_<ADAPTER>_KEY and TEST_<ADAPTER>_KEY (the latter
  # is what wirecheck.RequireUpdateKey reads).
  key_var="TEST_${upper_adapter}_KEY"
  key_val="${!key_var:-}"

  if [ -z "$key_val" ]; then
    log "SKIP $adapter: $key_var not set"
    ADAPTERS_SKIPPED+=("$adapter")
    continue
  fi

  log "Running live-update for $adapter ..."

  # Run the WireGolden tests with the real API key.
  # -update mode records fresh response.sse; request.json is never touched.
  # If the test exits non-zero, the adapter either failed a request (request-side
  # regression) or a network error occurred. We capture the exit code and classify
  # separately.
  update_exit=0
  go test \
    ./core/llm/... ./core/rpc/views/agentgraph/chat/... \
    -run "WireGolden" \
    -count=1 -short \
    -args -update -adapter="$adapter" \
    2>&1 | tee "/tmp/wire-golden-update-${adapter}.log" || update_exit=$?

  if [ "$update_exit" -ne 0 ]; then
    # Non-zero could be a request rejection or a network failure.
    # wire-golden-classify-drift.sh will inspect the log to distinguish.
    log "WARN $adapter: test exited with code $update_exit — classifying as potential request regression"
    ADAPTERS_REQUEST_FAILED+=("$adapter")
    OVERALL_DRIFT="request-regression"
    continue
  fi

  ADAPTERS_UPDATED+=("$adapter")
done

if [ ${#ADAPTERS_UPDATED[@]} -eq 0 ] && [ ${#ADAPTERS_REQUEST_FAILED[@]} -eq 0 ]; then
  log "No adapters had keys set and no request failures — live tier skipped."
  set_output "drift_outcome" "skipped"
  exit 0
fi

# ---- classify drift ---------------------------------------------------------

log "Classifying drift for updated adapters: ${ADAPTERS_UPDATED[*]:-none}"

# The classify script inspects git diff for the updated fixtures and decides
# whether the diff touches only response.sse (response drift) or also request.json
# (request-side regression).
CLASSIFY_OUTCOME="no-drift"
if [ ${#ADAPTERS_UPDATED[@]} -gt 0 ]; then
  CLASSIFY_OUTCOME="$(bash "$SCRIPT_DIR/wire-golden-classify-drift.sh" "${ADAPTERS_UPDATED[@]}")"
fi

# If there were request failures, they dominate.
if [ ${#ADAPTERS_REQUEST_FAILED[@]} -gt 0 ]; then
  CLASSIFY_OUTCOME="request-regression"
fi

log "Drift classification: $CLASSIFY_OUTCOME"
set_output "drift_outcome" "$CLASSIFY_OUTCOME"

# ---- open PR ----------------------------------------------------------------

if [ "${DRY_RUN:-false}" = "true" ]; then
  log "DRY_RUN=true — skipping PR creation (drift_outcome=$CLASSIFY_OUTCOME)"
  exit 0
fi

case "$CLASSIFY_OUTCOME" in
  no-drift)
    log "No fixture drift detected — no PR needed."
    ;;

  response-drift)
    # ----------------------------------------------------------------
    # Response-side drift: open/update a single rolling auto-PR.
    # The PR diff is constrained to testdata/wire_golden/*/response.sse.
    # ----------------------------------------------------------------
    log "Opening/updating auto-PR on branch $RUN_BRANCH ..."

    git config user.email "wire-golden-bot@kameas.ai"
    git config user.name  "wire-golden-bot"

    # Create/reset the rolling branch.
    git checkout -B "$RUN_BRANCH"
    git add -- testdata/wire_golden/

    # Check if there's actually anything staged.
    if git diff --cached --quiet; then
      log "Staged diff is empty — no PR needed despite response-drift classification."
      set_output "drift_outcome" "no-drift"
      exit 0
    fi

    git commit -m "chore(wire-golden): refresh recorded responses ($DATE)"

    # Force-push so the rolling branch always has exactly one new commit.
    git push --force origin "$RUN_BRANCH"

    # Open PR (or update if branch already has an open PR).
    EXISTING_PR="$(gh pr list --head "$RUN_BRANCH" --json number,url --jq '.[0].url' 2>/dev/null || true)"

    if [ -n "$EXISTING_PR" ]; then
      log "Updating existing auto-PR: $EXISTING_PR"
      # Force-push already updated the branch; no need to re-create the PR.
      AUTO_PR_URL="$EXISTING_PR"
    else
      AUTO_PR_URL="$(gh pr create \
        --base main \
        --head "$RUN_BRANCH" \
        --title "chore(wire-golden): refresh recorded responses ($DATE)" \
        --body "$(cat <<'EOF'
## Auto-generated fixture refresh

The nightly live-tier run detected response-side drift between the locked-tier
fixtures and the real upstream API responses. This PR updates **only**
\`response.sse\` files to match the current upstream behaviour.

### What changed
Provider-side fields (e.g. finish-reason vocabulary, event ordering, or metadata
fields) drifted since the fixtures were last recorded. The scrubber normalised
ids and timestamps so the diff captures only meaningful structural changes.

### Diff constraints
This PR **must not** modify \`request.json\` files or any Go source. CI on this PR
runs the locked tier against the updated fixtures; if green and a maintainer
approves, it can merge.

### Review guidance
- Check each \`response.sse\` diff for unexpected changes beyond provider-side evolution.
- If the diff includes \`request.json\` or \`.go\` files, **do not merge** — something
  went wrong and this needs human investigation.

_Auto-opened by \`wire-golden-live.yml\` at $(date -u +%Y-%m-%dT%H:%M:%SZ)_
EOF
)" \
        --label "auto-fixture-refresh" 2>&1 | grep "^https://")"
    fi

    log "Auto-PR: $AUTO_PR_URL"
    set_output "auto_pr_url" "$AUTO_PR_URL"
    ;;

  request-regression)
    # ----------------------------------------------------------------
    # Request-side failure: open a fresh, non-auto-merge regression PR.
    # ----------------------------------------------------------------
    REG_BRANCH="regression/wire-golden-${DATE}-$(date -u +%H%M%S)"
    log "Opening regression PR on branch $REG_BRANCH ..."

    git config user.email "wire-golden-bot@kameas.ai"
    git config user.name  "wire-golden-bot"

    git checkout -B "$REG_BRANCH"

    # Stage whatever drift we have (may be empty if only failures, no updates).
    git add -- testdata/wire_golden/ 2>/dev/null || true
    if ! git diff --cached --quiet; then
      git commit -m "fix(wire-golden): capture partial drift before request regression ($DATE)"
    fi

    # Include the update logs as reference artefacts.
    for adapter in "${ADAPTERS_REQUEST_FAILED[@]}"; do
      log_file="/tmp/wire-golden-update-${adapter}.log"
      if [ -f "$log_file" ]; then
        mkdir -p "$REPO_ROOT/testdata/wire_golden/regression-logs"
        cp "$log_file" "$REPO_ROOT/testdata/wire_golden/regression-logs/${adapter}-${DATE}.log"
        git add -- "testdata/wire_golden/regression-logs/"
      fi
    done
    if ! git diff --cached --quiet; then
      git commit -m "chore(wire-golden): add regression log artefacts ($DATE)"
    fi

    git push origin "$REG_BRANCH"

    FAILED_LIST="$(IFS=', '; echo "${ADAPTERS_REQUEST_FAILED[*]}")"

    REGRESSION_PR_URL="$(gh pr create \
      --base main \
      --head "$REG_BRANCH" \
      --title "fix(wire-golden): request regression detected in live tier ($DATE)" \
      --body "$(cat <<EOF
## Wire-golden request-side regression — human investigation required

The nightly live-tier run detected that one or more provider APIs **rejected our
request body**. This is a hard regression — it means our request serialisation
is no longer accepted by the provider.

### Failing adapters
$FAILED_LIST

### Next steps
1. Review the update log artefacts in \`testdata/wire_golden/regression-logs/\`.
2. Run \`go test ./core/llm/<adapter>/... -run WireGolden -v\` locally with
   \`TEST_<ADAPTER>_KEY\` set to reproduce.
3. Fix the request serialisation in \`core/llm/<adapter>/\`.
4. Update the hand-authored \`request.json\` fixture to match.
5. Close this PR (it is a diagnosis artifact, not a merge target).

**Do not merge this PR — it is a regression diagnosis, not a fix.**

_Auto-opened by \`wire-golden-live.yml\` at $(date -u +%Y-%m-%dT%H:%M:%SZ)_
EOF
)" \
      --label "regression" \
      --no-maintainer-edit \
      2>&1 | grep "^https://")"

    log "Regression PR: $REGRESSION_PR_URL"
    set_output "regression_pr_url" "$REGRESSION_PR_URL"
    ;;
esac

log "Done."
