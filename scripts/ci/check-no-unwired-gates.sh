#!/usr/bin/env bash
# check-no-unwired-gates.sh — CI gate for
# kitty-specs/agentgraph-total-convergence-01PMGX01/spec.md §6 I10 (WP01:
# `fix(ci): WP01 — convergence invariant gates`).
#
# I10: no exported function with a documented control-flow role (a gate,
# an enforcement check, an authorization step) has zero production call
# sites. This is the general form of the `EvaluateToolGate` defect (spec
# §1.7): a 331-line Cedar "prompt_on_first_use" gate with a doc comment
# describing exactly how callers are supposed to invoke it, and zero
# non-test call sites anywhere in core/ — code that reads as load-bearing
# but never actually runs in production.
#
# DISCOVERY HEURISTIC (deliberately narrow, not a general dead-code
# scanner): a candidate is any EXPORTED free function under core/ whose
# name starts with Evaluate/Enforce/Authorize/Check/Verify/Guard/Permit,
# or ends with Gate — the naming vocabulary this codebase uses for
# gate/authorization/enforcement functions. Broadening this to catch
# every unreachable exported symbol is a different, much bigger check
# (dead-code elimination) that does not belong in a 30s CI gate; this
# one stays scoped to the specific "control-flow role that nothing
# calls" defect class I10 names.
#
# VOCABULARY WIDENING, 2026-08-14 (unwired sweep). Check/Verify/Guard/
# Permit were added because the original four could not see the defect
# this allowlist was already DOCUMENTING: the entry for
# EnforceManifestDriftPolicy states that its producer CheckManifestDrift
# "also has zero non-test callers", and `Check*` was not in the
# vocabulary, so the gate never flagged it. A gate whose allowlist
# describes violations its own scanner cannot detect is only half a gate.
#
# The widening surfaced eight symbols, all real, all seeded below with a
# per-symbol verdict from the sweep's triage. The most consequential is
# cedar.CheckLLMFallback: core/llm/fallback's Runner is constructed on
# the live chat path with no options, so `checkPolicy` is nil and every
# fallback hop runs unevaluated, while runner.go's own doc says "fn
# should call cedar.CheckLLMFallback".
#
# For each candidate this script counts REAL non-test call/reference
# sites: occurrences of `Symbol(` in non-test .go files, minus pure
# comment lines (`^\s*//`) and minus the function's own declaration line.
# Zero real sites = a violation, which must appear in the allowlist.
#
# scripts/ci/allowlists/i10-unwired-gates.txt lines are
# `<package-import-path>.<Symbol>`. Two special rules (spec-mandated):
#   - An allowlist entry naming a symbol that no longer exists ANYWHERE
#     under core/ is NOT an error — deleting the dead code (rather than
#     wiring it up) is an equally valid resolution (see WP02a, which
#     deletes EvaluateToolGate outright), and the check must not force a
#     synchronized two-file edit to do so.
#   - An allowlist entry naming a symbol that still exists AND now has a
#     real call site is STALE and must be deleted (spec §4.1 — allow-lists
#     shrink monotonically).
#
# Usage: bash scripts/ci/check-no-unwired-gates.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[no-unwired-gates]"
SCAN_ROOT="core"
ALLOW_FILE="scripts/ci/allowlists/i10-unwired-gates.txt"
MODULE="github.com/kameas-ai/kenaz-harness"

ci_require_dir "$SCAN_ROOT" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

# has_real_callsite <symbol> — true (exit 0) iff `<symbol>(` appears in a
# non-test .go file under core/ outside of a comment line and outside the
# symbol's own `func <symbol>(` declaration line.
# has_real_callsite_in_pkg is the PACKAGE-AWARE form, used only by the
# stale-entry check (section 3). has_real_callsite below matches a bare
# symbol name anywhere in the tree, which is right for DISCOVERY (a
# same-named helper is still a call worth noticing) and wrong for
# staleness.
#
# Found 2026-08-21 during v0.70.0 integration. The allowlist carries
# `core/credstore.WithCedarGate` — genuinely unwired, with a written
# justification. approval-node-01PMZC12 then added an identically named
# `graphview.WithCedarGate` in core/rpc/views/agentgraph and wired it at
# core/rpc/api.go. The bare-symbol check saw "WithCedarGate(" with a real
# call site and declared the credstore entry STALE, demanding deletion of
# a justification that is still true. Deleting it would have removed the
# only record that credstore's gate is unreached — the gate would have
# caused exactly the blindness it exists to prevent.
#
# Two accepted call shapes for `<import path>.<Symbol>`:
#   - qualified, from outside:  <pkgname>.<Symbol>(
#   - unqualified, from inside: <Symbol>( within the package's own dir
has_real_callsite_in_pkg() {
  local import_path="$1" symbol="$2"
  local pkgdir pkgname hits
  # import path -> repo-relative dir (MODULE/core/credstore -> core/credstore)
  pkgdir="${import_path#"${MODULE}/"}"
  pkgname="${pkgdir##*/}"

  # Qualified calls from anywhere outside the package itself.
  hits=$(grep -rn "${pkgname}\.${symbol}(" --include='*.go' "$SCAN_ROOT" 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -vE ':[0-9]+:[[:space:]]*//' \
    || true)
  if [[ -n "$hits" ]]; then
    return 0
  fi

  # Unqualified calls from inside the declaring package.
  if [[ -d "$pkgdir" ]]; then
    hits=$(grep -rn "${symbol}(" --include='*.go' "$pkgdir" 2>/dev/null \
      | grep -v '_test\.go' \
      | grep -vE ':[0-9]+:[[:space:]]*//' \
      | grep -vE ":[0-9]+:func ${symbol}\(" \
      | grep -vE ":[0-9]+:func \([^)]*\) ${symbol}\(" \
      || true)
    if [[ -n "$hits" ]]; then
      return 0
    fi
  fi
  return 1
}

has_real_callsite() {
  local symbol="$1"
  local hits
  hits=$(grep -rn "${symbol}(" --include='*.go' "$SCAN_ROOT" 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -vE ':[0-9]+:[[:space:]]*//' \
    | grep -vE ":[0-9]+:func ${symbol}\(" \
    | grep -vE ":[0-9]+:func \([^)]*\) ${symbol}\(" \
    || true)
  [[ -n "$hits" ]]
}

# import_path_for <go-file> — the module import path of the package
# containing the given file (directory-based; no build introspection
# needed since core/ has no non-canonical package-directory mismatches).
import_path_for() {
  local dir
  dir="$(dirname "$1")"
  echo "${MODULE}/${dir}"
}

# ---- 1. discover candidates + compute today's violation set ----
# Both alternatives require the symbol to start with an uppercase letter
# (EXPORTED, per the header's contract) — the *Gate( branch previously
# matched unexported functions too (e.g. buildCedarGate).
candidates=$(grep -rnE '^func (Evaluate|Enforce|Authorize|Check|Verify|Guard|Permit)[A-Za-z0-9_]*\(|^func [A-Z][A-Za-z0-9_]*Gate\(' \
  --include='*.go' "$SCAN_ROOT" 2>/dev/null | grep -v '_test\.go' || true)

violations=""
while IFS=: read -r file line rest; do
  [[ -z "$file" ]] && continue
  # The receiver group `(\([^)]*\) )?` must be atomic (not three
  # independently-optional pieces) or greedy backtracking lets it consume
  # the whole function name, leaving only the LAST character captured
  # (e.g. "AuthorizeDevice" -> "e"). That silently broke discovery: every
  # candidate collapsed to a 1-char symbol that has_real_callsite matches
  # everywhere, so no new violation could ever be reported. Verified fix:
  # capture group 2 explicitly, receiver group as a single optional unit.
  symbol=$(printf '%s' "$rest" | sed -E 's/^func (\([^)]*\) )?([A-Za-z0-9_]+)\(.*/\2/')
  [[ -z "$symbol" ]] && continue
  if ! has_real_callsite "$symbol"; then
    qualified="$(import_path_for "$file").${symbol}"
    violations="${violations}${qualified}"$'\n'
  fi
done <<< "$candidates"
violations=$(printf '%s' "$violations" | grep -v '^$' | sort -u || true)

# ---- 2. violations not covered by the allowlist ----
allow=$(load_allowlist "$ALLOW_FILE")
unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
if [[ -n "$unlisted" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: unwired-gate-shaped function(s) with zero non-test call sites, not in ${ALLOW_FILE}:" >&2
  printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
  fail=1
fi

# ---- 3. stale allowlist entries (symbol still exists AND now has a real call site) ----
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  symbol="${entry##*.}"
  entry_pkg="${entry%.*}"
  # Tolerate an entry naming a symbol that no longer exists anywhere —
  # deletion is a valid resolution and must not require a synchronized edit.
  if ! grep -rqE "^func (\([^)]*\) )?${symbol}\(" --include='*.go' "$SCAN_ROOT" 2>/dev/null; then
    continue
  fi
  if has_real_callsite_in_pkg "$entry_pkg" "$symbol"; then
    echo "" >&2
    echo "${GATE} FAIL: STALE entry in ${ALLOW_FILE} — ${entry} now has a real non-test call site. Delete the line (spec §4.1 requires allow-lists to shrink monotonically)." >&2
    fail=1
  fi
done <<< "$allow"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — no unwired gate-shaped function outside ${ALLOW_FILE}."
