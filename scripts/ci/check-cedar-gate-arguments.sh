#!/usr/bin/env bash
# check-cedar-gate-arguments.sh — I13: a Cedar gate is only as real as the
# ARGUMENT passed at the wiring site.
#
# THE DEFECT CLASS (unwired sweep, 2026-08-16 — audit findings A1 + A2)
# --------------------------------------------------------------------
# Five production wiring sites consulted no policy at all:
#
#   core/rpc/api.go            gs.SetGate(&memoryGateAdapter{gate: cedar.AllowAll{}})
#   core/rpc/api.go            cedar.NewLLMPolicyGuard(cedar.AllowAll{})
#   core/rpc/builtins_wiring.go  constructWebSearch: PolicyGate: cedar.AllowAll{}
#   core/rpc/api.go            workflowsview.Config{...}      — `Cedar` omitted ⇒ nil
#   core/rpc/api.go            scheduledchatview.Config{...}  — `Cedar` omitted ⇒ nil
#
# A sixth (tools.Config.Gate, the recipe-spawn gate the shipped
# mcp-no-npx.cedar template targets) was found by this gate while it was
# being written.
#
# A user could author a Cedar policy, watch the editor validate it, save
# it, see it listed as loaded — and none of these sites read it. A nil
# Gate is not "off"; every gate helper short-circuits it to
# Allow("no engine wired (default-allow)").
#
# WHY NO EXISTING GATE COULD SEE IT
# ---------------------------------
# check-no-unwired-gates.sh (I10) looks for exported control-flow
# functions with zero non-test CALL SITES. All nine `Gate*` / `Check*`
# helpers HAVE call sites — they are called faithfully, with a gate that
# cannot deny. The defect is one argument at the call, which is exactly
# the shape I10's vocabulary cannot express.
#
# WHAT THIS CHECKS
# ----------------
# Clause 1 — AllowAll consumed on the spot.
#   `cedar.AllowAll{}` appearing as a call ARGUMENT or a composite-literal
#   FIELD VALUE in non-test Go under core/rpc/ is a violation. Such a value
#   can never be replaced: it is consumed at the point it is written.
#
#   The legitimate fallback idiom is NOT this shape and is not flagged:
#
#       var g cedar.Gate = cedar.AllowAll{}   // placeholder…
#       if cedarEngine != nil { g = cedarEngine }   // …conditionally replaced
#
#   nor is `return cedar.AllowAll{}` inside buildCedarGate, the single
#   designated fallback builder.
#
# Clause 2 — a placeholder nobody replaces.
#   A variable initialised to `cedar.AllowAll{}` must be reassigned
#   somewhere later in the same file. `var g cedar.Gate = cedar.AllowAll{}`
#   with no subsequent `g = …` is clause 1 wearing a variable name.
#
# Clause 3 — a Gate-typed Config field the wiring site omits.
#   Every `<Field> cedar.Gate` declared in a `Config` struct under
#   core/rpc/views/ must be assigned in the `<alias>.Config{…}` literal in
#   core/rpc/api.go. An omitted field is the zero value, i.e. a nil gate,
#   i.e. unconditional permit — the A2 half of the finding, and the half
#   that leaves no `AllowAll` string to grep for.
#
# Violations must appear in scripts/ci/allowlists/i13-cedar-gate-arguments.txt
# with a DATED justification naming the blocker — same contract as I10/I11:
# a line that stays must say why it stays, and lines shrink monotonically.
# An entry that no longer corresponds to a violation is STALE and fails.
#
# Usage: bash scripts/ci/check-cedar-gate-arguments.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[cedar-gate-arguments]"
RPC_ROOT="core/rpc"
VIEWS_ROOT="core/rpc/views"
API_FILE="core/rpc/api.go"
ALLOW_FILE="scripts/ci/allowlists/i13-cedar-gate-arguments.txt"

ci_require_dir "$RPC_ROOT" "$GATE"
ci_require_dir "$VIEWS_ROOT" "$GATE"
ci_require_file "$API_FILE" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0

load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

# ---------------------------------------------------------------------------
# Vacuous-pass guard.
#
# Every clause below is a grep for a pattern that MUST exist in a healthy
# tree. If the Gate interface were renamed, or core/rpc restructured, the
# greps would match nothing and this gate would print "clean" while
# inspecting an empty set — the exact failure scripts/ci/lib/ci-gate.sh and
# gates_can_fail_test.go exist to prevent. So: assert the raw material is
# there before drawing any conclusion from its absence.
# ---------------------------------------------------------------------------
gate_type_refs=$(grep -rlE 'cedar\.Gate' --include='*.go' "$RPC_ROOT" 2>/dev/null || true)
if [[ -z "$gate_type_refs" ]]; then
  echo "${GATE} FAIL: no reference to 'cedar.Gate' anywhere under ${RPC_ROOT}." >&2
  echo "${GATE} A gate cannot pass by having nothing to look at. Either the Gate" >&2
  echo "${GATE} interface was renamed or the wiring layer moved — update this script" >&2
  echo "${GATE} in the same commit." >&2
  exit 1
fi

if ! grep -qE 'func buildCedarGate\(' "$API_FILE"; then
  echo "${GATE} FAIL: buildCedarGate not found in ${API_FILE}." >&2
  echo "${GATE} It is the designated production gate builder this check assumes" >&2
  echo "${GATE} every wiring site routes through. If it was renamed, rename it here" >&2
  echo "${GATE} too; if it was deleted, this gate's premise no longer holds." >&2
  exit 1
fi

violations=""

# ---------------------------------------------------------------------------
# Clause 1: cedar.AllowAll{} consumed as an argument or a field value.
#
# Matched shapes (AllowAll preceded by '(' or ':' on the same line):
#     f(cedar.AllowAll{})            call argument
#     &T{gate: cedar.AllowAll{}}     composite-literal field value
#     Field: cedar.AllowAll{},       ditto, multi-line literal
#
# Deliberately NOT matched:
#     var g cedar.Gate = cedar.AllowAll{}
#     g = cedar.AllowAll{}
#     return cedar.AllowAll{}
# ---------------------------------------------------------------------------
while IFS= read -r hit; do
  [[ -z "$hit" ]] && continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  line="${rest%%:*}"
  violations="${violations}${file}:${line}: AllowAll consumed at the call (clause 1)"$'\n'
done < <(
  grep -rnE '[(:][[:space:]]*cedar\.AllowAll\{\}' --include='*.go' "$RPC_ROOT" 2>/dev/null \
    | grep -v '_test\.go' || true
)

# ---------------------------------------------------------------------------
# Clause 2: a placeholder variable nobody replaces.
#
# For each `var <name> cedar.Gate = cedar.AllowAll{}` (or `<name> :=`),
# require a later plain assignment to <name> in the same file that is not
# itself AllowAll. Without one the placeholder IS the final value.
# ---------------------------------------------------------------------------
while IFS= read -r hit; do
  [[ -z "$hit" ]] && continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  line="${rest%%:*}"
  text="${rest#*:}"
  # Extract the variable name from `var NAME cedar.Gate = ...` or `NAME := ...`
  name=$(printf '%s' "$text" | sed -nE 's/^[[:space:]]*var[[:space:]]+([A-Za-z_][A-Za-z0-9_]*).*/\1/p')
  if [[ -z "$name" ]]; then
    name=$(printf '%s' "$text" | sed -nE 's/^[[:space:]]*([A-Za-z_][A-Za-z0-9_]*)[[:space:]]*:=.*/\1/p')
  fi
  [[ -z "$name" ]] && continue
  # A replacement is `name = <something other than AllowAll>` after this line.
  if ! awk -v n="$name" -v start="$line" 'NR>start {
        pat = "^[[:space:]]*" n "[[:space:]]*=[^=]"
        if ($0 ~ pat && $0 !~ /cedar\.AllowAll\{\}/) { found=1; exit }
      } END { exit !found }' "$file"; then
    violations="${violations}${file}:${line}: '${name}' initialised to AllowAll and never replaced (clause 2)"$'\n'
  fi
done < <(
  grep -rnE '(var[[:space:]]+[A-Za-z_][A-Za-z0-9_]*[[:space:]]+cedar\.Gate[[:space:]]*=|[A-Za-z_][A-Za-z0-9_]*[[:space:]]*:=)[[:space:]]*cedar\.AllowAll\{\}' \
    --include='*.go' "$RPC_ROOT" 2>/dev/null | grep -v '_test\.go' || true
)

# ---------------------------------------------------------------------------
# Clause 3: a Gate-typed Config field the api.go wiring site omits.
#
# For each core/rpc/views/<pkg> declaring `<Field> cedar.Gate` inside a
# `type Config struct` block, resolve the import alias api.go uses for that
# package, extract the `<alias>.Config{ … }` literal, and require `<Field>:`
# inside it.
# ---------------------------------------------------------------------------
config_fields=$(
  for f in $(find "$VIEWS_ROOT" -name '*.go' ! -name '*_test.go' | sort); do
    awk -v file="$f" '
      /^type[[:space:]]+Config[[:space:]]+struct[[:space:]]*\{/ { inblock=1; next }
      inblock && /^\}/ { inblock=0; next }
      inblock && $0 ~ /^[[:space:]]*[A-Z][A-Za-z0-9_]*[[:space:]]+cedar\.Gate[[:space:]]*$/ {
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", $0)
        split($0, parts, /[[:space:]]+/)
        print file "\t" parts[1]
      }
    ' "$f"
  done
)

if [[ -z "$config_fields" ]]; then
  echo "${GATE} FAIL: no 'Config' struct under ${VIEWS_ROOT} declares a cedar.Gate field." >&2
  echo "${GATE} Clause 3 has nothing to inspect, which is indistinguishable from" >&2
  echo "${GATE} passing. Either the view Configs were restructured (update the awk" >&2
  echo "${GATE} block above) or the Gate fields were removed deliberately." >&2
  exit 1
fi

while IFS=$'\t' read -r file field; do
  [[ -z "$file" ]] && continue
  pkgdir=$(dirname "$file")
  pkgname=$(basename "$pkgdir")
  # Resolve the alias api.go imports this package under. Both aliased
  # (`workflowsview "…/views/workflows"`) and bare (`"…/views/tools"`)
  # import forms appear in api.go.
  impalias=$(grep -oE "[A-Za-z0-9_]+[[:space:]]+\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE" \
    | awk '{print $1}' | head -1 || true)
  if [[ -z "$impalias" ]]; then
    if grep -qE "\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE"; then
      impalias="$pkgname"
    else
      violations="${violations}${file}: Config.${field} is cedar.Gate-typed but ${API_FILE} does not import ${pkgdir} — nothing constructs it with a gate (clause 3)"$'\n'
      continue
    fi
  fi
  # Extract every `<alias>.Config{ … }` literal by brace counting and
  # require the field to be assigned in at least one of them.
  found=$(awk -v a="${impalias}.Config{" -v fld="${field}:" '
    index($0, a) > 0 { depth=1; if (index($0, fld) > 0) { print "yes"; exit } ; collecting=1; next }
    collecting {
      n = gsub(/\{/, "{"); m = gsub(/\}/, "}")
      depth += n - m
      if (index($0, fld) > 0) { print "yes"; exit }
      if (depth <= 0) { collecting=0 }
    }
  ' "$API_FILE")
  if [[ "$found" != "yes" ]]; then
    violations="${violations}${file}: Config.${field} is never assigned in ${API_FILE}'s ${impalias}.Config literal — the zero value is a nil gate, i.e. unconditional permit (clause 3)"$'\n'
  fi
done <<< "$config_fields"

violations=$(printf '%s' "$violations" | grep -v '^$' | sort -u || true)
allow=$(load_allowlist "$ALLOW_FILE")

# ---- violations not covered by the allowlist ----
unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
if [[ -n "$unlisted" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: Cedar gate wired to an unconditional permit, not in ${ALLOW_FILE}:" >&2
  printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
  echo "" >&2
  echo "${GATE} A gate that cannot deny is worse than no gate: the policy editor still" >&2
  echo "${GATE} validates the rule, still lists it as loaded, and still reports the check" >&2
  echo "${GATE} as covered. Pass buildCedarGate(c.DataDir()) (or the *cedar.Engine), or" >&2
  echo "${GATE} add a DATED justification naming the blocker." >&2
  fail=1
fi

# ---- stale allowlist entries ----
while IFS= read -r entry; do
  [[ -z "$entry" ]] && continue
  if ! printf '%s\n' "$violations" | grep -qxF "$entry"; then
    echo "" >&2
    echo "${GATE} FAIL: STALE entry in ${ALLOW_FILE} — no longer a violation:" >&2
    echo "    ${entry}" >&2
    echo "${GATE} Delete the line (allow-lists shrink monotonically)." >&2
    fail=1
  fi
done <<< "$allow"

if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — every cedar.Gate argument and Config field at a production wiring site resolves to a real engine (or is listed in ${ALLOW_FILE})."
