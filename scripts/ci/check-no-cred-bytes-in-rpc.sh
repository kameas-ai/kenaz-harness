#!/usr/bin/env bash
# Credential hygiene CI guard (WP08 / credential-store-01KQ8TDD).
#
# Check 1 — cred []byte literal
#   Prevents raw credential bytes from leaking into NEW RPC/handler packages.
#   The pattern `cred []byte` is legitimate inside packages that own credential
#   storage, opaque-secret handling, or the existing LLM provider transport
#   boundary (which WP01 of credential-store-01KQ8TDD will migrate away from):
#     - core/credstore/**   (the canonical credential store — mission target)
#     - core/secrets/**     (opaque secret primitives)
#     - core/llm/**         (pre-existing LLM adapter interface boundary;
#                            being migrated to credstore.RoundTrip by WP01)
#   Test files are also allowlisted: *_test.go may use the literal in fixtures.
#
#   Allowlist path patterns (grep -v):
#     core/credstore/    — credstore package directory
#     core/secrets/      — secrets package directory
#     core/llm/          — pre-existing LLM adapter boundary (WP01 migration)
#     _test.go           — test files (any package)
#
# Check 2 — hard-coded API key prefixes
#   Matches Anthropic (sk-ant-…) and generic OpenAI-style (sk-…) keys.
#   Regexes:
#     sk-ant-[A-Za-z0-9]{16,}   — Anthropic API key prefix + ≥16 alphanum chars
#     sk-[A-Za-z0-9]{20,}       — generic sk- key prefix + ≥20 alphanum chars
#                                  (20-char floor avoids false positives on
#                                  short identifiers like sk-learn, sk-schema)
#   Allowlisted paths (grep -v):
#     _test.go           — test fixtures / example keys in tests
#     kitty-specs/       — mission spec files may quote example keys
#     testdata/          — fixture directories
#     vendor/            — vendored third-party code
#
# Exit codes:
#   0  — clean, no violations
#   2  — violations found (human-readable listing on stderr)
#
set -euo pipefail

WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
cd "$WORKTREE_ROOT"

fail=0

# ---------------------------------------------------------------------------
# Check 1: cred []byte literal outside credstore / secrets / test files
# ---------------------------------------------------------------------------
echo "[cred-hygiene] Checking for 'cred []byte' outside allowlisted paths..."

cred_hits=$(
  grep -rn 'cred \[\]byte' --include='*.go' core/ 2>/dev/null \
    | grep -v 'core/credstore/' \
    | grep -v 'core/secrets/' \
    | grep -v 'core/llm/' \
    | grep -v '_test\.go' \
    || true
)

if [[ -n "$cred_hits" ]]; then
  echo "" >&2
  echo "[cred-hygiene] FAIL: 'cred []byte' found outside allowlisted packages." >&2
  echo "  Allowlist: core/credstore/, core/secrets/, core/llm/, *_test.go" >&2
  echo "  Use credstore.Use / credstore.RoundTrip instead of passing raw bytes." >&2
  echo "" >&2
  echo "  Offending files:" >&2
  while IFS= read -r line; do
    echo "    $line" >&2
  done <<< "$cred_hits"
  fail=1
fi

# ---------------------------------------------------------------------------
# Check 2: hard-coded API key literals outside test/fixture/spec paths
# ---------------------------------------------------------------------------
echo "[cred-hygiene] Checking for hard-coded API key literals (sk-ant-*, sk-*)..."

# Combined grep: match either key pattern in .go files, then filter allowlisted paths.
apikey_hits=$(
  grep -rEn 'sk-ant-[A-Za-z0-9]{16,}|sk-[A-Za-z0-9]{20,}' --include='*.go' . 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -v 'kitty-specs/' \
    | grep -v '/testdata/' \
    | grep -v '/vendor/' \
    || true
)

if [[ -n "$apikey_hits" ]]; then
  echo "" >&2
  echo "[cred-hygiene] FAIL: hard-coded API key literal found." >&2
  echo "  Regex: sk-ant-[A-Za-z0-9]{16,} | sk-[A-Za-z0-9]{20,}" >&2
  echo "  Allowlist: *_test.go, kitty-specs/, testdata/, vendor/" >&2
  echo "  Load credentials at runtime via credstore.Use or environment variables." >&2
  echo "" >&2
  echo "  Offending files:" >&2
  while IFS= read -r line; do
    echo "    $line" >&2
  done <<< "$apikey_hits"
  fail=1
fi

# ---------------------------------------------------------------------------
# Result
# ---------------------------------------------------------------------------
if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "[cred-hygiene] FAIL — see offending lines above. Fix before merging." >&2
  exit 2
fi

echo "[cred-hygiene] clean — no 'cred []byte' regressions or hard-coded API keys found."
exit 0
