#!/usr/bin/env bash
# check-no-model-family-literals.sh — versioned-model-profile-01PMDL04 WP04
#
# Bans hardcoded model-family name literals in core logic outside
# core/llm/** — closes the frozen-core violation this WP fixes
# (core/agentgraph's tierFromModelID string-matched "haiku"/"opus"/etc.
# directly on model IDs to guess a size tier). Tier/capability data now
# lives in core/llm/capabilities/data/*.yaml; core code asks a question
# through a data seam (Catalog.Tier/TierAny, agentgraph.TierSource)
# instead of pattern-matching model-family names itself.
#
# A same-line `// model-lit-allow: <reason>` comment opts a line out —
# for genuine non-classification references this lint isn't targeting
# (a cost-estimation table, a context-window budget table awaiting its
# own capability-data migration, UI help text, an unrelated "Claude
# Desktop" import-config feature name). Every existing opt-out is
# reviewed inline in its own commit; new ones get the same scrutiny in
# review — this is an escape hatch, not a way to silence the lint.
#
# Usage: bash scripts/ci/check-no-model-family-literals.sh [root]
set -euo pipefail

ROOT="${1:-core}"

# claude requires a trailing "-" or "." (real model-id shape, e.g.
# "claude-3-opus" / bedrock's "anthropic.claude-3-sonnet") so it doesn't
# trip on the unrelated "ClaudeDesktop" MCP-config-import feature name.
# Every other family word is word-bounded (underscore counts as a word
# character for this purpose) so none of them trip on an unrelated word
# or identifier substring — e.g. "minimum"/"optional" for mini, or
# ReasonNetworkTierNotPermitted, whose lowercased "...sonNETwork..." would
# otherwise coincidentally spell "sonnet" across the "Reason"/"Network"
# word boundary (a real false positive this pattern hit before bounding).
PATTERN='claude[-.]|(^|[^A-Za-z0-9_])gemini([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])haiku([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])opus([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])sonnet([^A-Za-z0-9_]|$)|gpt-|(^|[^A-Za-z0-9_])o1([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])mini([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])nano([^A-Za-z0-9_]|$)|(^|[^A-Za-z0-9_])ultra([^A-Za-z0-9_]|$)'

fail=0
violations=""

while IFS= read -r -d '' f; do
  case "$f" in
    "$ROOT"/llm/*) continue ;;
    *_test.go) continue ;;
  esac

  matches=$(grep -inE "$PATTERN" "$f" 2>/dev/null || true)
  [[ -z "$matches" ]] && continue

  while IFS= read -r line; do
    lineno="${line%%:*}"
    rest="${line#*:}"
    # Drop a full-line comment or trailing "//..." text before
    # re-checking — a comment mentioning a model name in prose isn't a
    # classification-logic violation. Coarse: doesn't handle a literal
    # "//" inside a string, same tradeoff documented in
    # check-no-credential-in-ui.sh's own grep-based approach.
    code_part="${rest%%//*}"
    if ! echo "$code_part" | grep -qiE "$PATTERN"; then
      continue # the only match was inside a comment
    fi
    if echo "$rest" | grep -q 'model-lit-allow:'; then
      continue
    fi
    violations="${violations}${f}:${lineno}:${rest}"$'\n'
    fail=1
  done <<< "$matches"
done < <(find "$ROOT" -name '*.go' -print0 2>/dev/null)

if [[ $fail -ne 0 ]]; then
  echo "[model-family-literal] FAIL — hardcoded model-family literal(s) outside ${ROOT}/llm/**:" >&2
  printf '%s' "$violations" >&2
  echo "" >&2
  echo "Route classification/routing decisions through core/llm/capabilities (Catalog.Tier/" >&2
  echo "TierAny) or an equivalent data seam instead of string-matching model-family names in" >&2
  echo "core logic. A genuine non-classification reference (UI text, a cost/budget table, an" >&2
  echo "unrelated feature name) can opt out with a same-line '// model-lit-allow: <reason>'." >&2
  exit 1
fi

echo "[model-family-literal] clean — no unexempted model-family literals outside ${ROOT}/llm/**."
