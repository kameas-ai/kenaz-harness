#!/usr/bin/env bash
# check-builtin-tool-registration.sh — I11: every implemented builtin tool
# is reachable from the model's catalog, or is allowlisted with a dated
# justification.
#
# THE DEFECT CLASS (unwired sweep, 2026-08-14)
# --------------------------------------------
# `core/tools/monitor` is a complete, tested, 469-line builtin tool
# (`kenaz__monitor`) with its own Cedar action constant, shipped in
# v0.11.0 and registered NOWHERE ever since. Nothing imports the package
# outside its own test file. No model has ever been able to call it.
#
# No existing gate could see this:
#   - I7 (orphan packages) catches it only as one line in a 36-line bulk
#     list of "packages with no non-test importer", most of which are
#     legitimate test-support libraries. A tool the model is supposed to
#     be able to CALL is a different, louder problem than an unimported
#     helper, and it was buried.
#   - The builtin-tools tripwire (core/rpc/builtins_wiring_test.go
#     TestBuiltinEnabledPredicate_AllRegisteredToolsHaveExplicitCase)
#     iterates `Builtins().Names()` — the REGISTERED set. A tool that
#     never registers is invisible to it by construction; it can only
#     catch registered-but-no-predicate-case.
#
# So: a whole tool can be built, tested, gated, documented and shipped
# without one line of CI noticing that no model can reach it.
#
# WHAT THIS CHECKS
# ----------------
# A package under core/tools/ that declares a builtin tool name — a
# `Name`/`ToolName`/`<X>ToolName` const whose value starts with `kenaz__`
# — must be imported by core/rpc/builtins_wiring.go, the single wiring
# site for the builtin registry (core/rpc/api.go calls
# registerBuiltinTools / registerFSBuiltinTools / registerFSRequestTool /
# registerReadContextFileTool and nothing else registers).
#
# Import, not registration, is the bar on purpose. Several tools register
# conditionally and SHOULD (`save_artifact` needs an artifacts manager;
# `subagent_dispatch` is deliberately withheld while its BranchSeam
# cannot spawn a child run — crash-recovery-tool-gating-0XQTC4RK FR-007).
# Requiring the wiring file to at least *know about* the package catches
# the "nobody ever hooked this up" case without arguing with every
# deliberate conditional. A package that is imported but whose
# registration is dead code is a code-review question, not a grep one.
#
# Violations must appear in
# scripts/ci/allowlists/i11-unregistered-builtin-tools.txt with a dated
# justification naming the blocker — same contract as the I10 allowlist:
# a line that stays must say why it stays, and lines shrink monotonically.
# An entry naming a package that no longer exists is fine (deletion is a
# valid resolution). An entry naming a package that IS now imported is
# STALE and fails.
#
# Usage: bash scripts/ci/check-builtin-tool-registration.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[builtin-tool-registration]"
TOOLS_ROOT="core/tools"
WIRING_FILE="core/rpc/builtins_wiring.go"
ALLOW_FILE="scripts/ci/allowlists/i11-unregistered-builtin-tools.txt"

ci_require_dir "$TOOLS_ROOT" "$GATE"
ci_require_file "$WIRING_FILE" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

# ---- 1. discover tool-declaring packages ----
# A declaration looks like `Name = "kenaz__x"` or `ToolName = "kenaz__x"`
# or `EnterToolName = "kenaz__x"`, with or without a leading `const`.
decls=$(grep -rnE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z0-9_]*(Name|ToolName)[[:space:]]*=[[:space:]]*"kenaz__' \
  --include='*.go' "$TOOLS_ROOT" 2>/dev/null | grep -v '_test\.go' || true)

if [[ -z "$decls" ]]; then
  echo "${GATE} FAIL: found no kenaz__ tool-name declarations under ${TOOLS_ROOT}." >&2
  echo "${GATE} A gate cannot pass by having nothing to look at — the naming convention" >&2
  echo "${GATE} this scan depends on has changed. Update the pattern in the same commit." >&2
  exit 1
fi

pkgs=$(printf '%s\n' "$decls" | cut -d: -f1 | xargs -n1 dirname | sort -u)

# ---- 2. which of them the wiring file imports ----
violations=""
while IFS= read -r pkg; do
  [[ -z "$pkg" ]] && continue
  if ! grep -qE "\"github\.com/kameas-ai/kenaz-harness/${pkg}\"" "$WIRING_FILE"; then
    violations="${violations}${pkg}"$'\n'
  fi
done <<< "$pkgs"
violations=$(printf '%s' "$violations" | grep -v '^$' | sort -u || true)

allow=$(load_allowlist "$ALLOW_FILE")

# ---- 3. violations not covered by the allowlist ----
unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
if [[ -n "$unlisted" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: builtin tool package(s) never imported by ${WIRING_FILE}, not in ${ALLOW_FILE}:" >&2
  printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A tool the model cannot call is not shipped, it is dead. Register it," >&2
  echo "${GATE} delete it, or add a DATED justification naming the blocker." >&2
  fail=1
fi

# ---- 4. stale allowlist entries ----
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  # A line naming a package that no longer exists is fine — deleting the
  # dead tool is a valid resolution and must not require a paired edit.
  [[ -d "$entry" ]] || continue
  if ! printf '%s\n' "$violations" | grep -qx "$entry"; then
    echo "" >&2
    echo "${GATE} FAIL: STALE entry in ${ALLOW_FILE} — ${entry} is now imported by ${WIRING_FILE}. Delete the line (allow-lists shrink monotonically)." >&2
    fail=1
  fi
done <<< "$allow"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — every builtin tool package under ${TOOLS_ROOT} is wired into ${WIRING_FILE} or listed in ${ALLOW_FILE}."
