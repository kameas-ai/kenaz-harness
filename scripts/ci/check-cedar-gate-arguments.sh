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
# Matched shapes:
#     f(cedar.AllowAll{})            call argument
#     &T{gate: cedar.AllowAll{}}     composite-literal field value
#     Field: cedar.AllowAll{},       ditto, multi-line literal
#     []cedar.Gate{cedar.AllowAll{}} slice/map element
#     f(                             gofmt's wrapping of a long call —
#         cedar.AllowAll{},          the argument lands on its own line,
#     )                              with no '(' to anchor on
#
# Deliberately NOT matched (the replaceable-placeholder idiom, checked by
# clause 2 instead):
#     var g cedar.Gate = cedar.AllowAll{}
#     g = cedar.AllowAll{}
#     return cedar.AllowAll{}
#
# Implemented by exclusion rather than by an allowlist of preceding
# characters: any line mentioning cedar.AllowAll{} is a violation UNLESS
# the mention is an assignment RHS or a return. Anchoring on '(' or ':'
# was silenced by `gofmt` wrapping the exact call the gate was written
# for (`cedar.NewLLMPolicyGuard(cedar.AllowAll{})`).
# ---------------------------------------------------------------------------
while IFS= read -r hit; do
  [[ -z "$hit" ]] && continue
  file="${hit%%:*}"
  rest="${hit#*:}"
  line="${rest%%:*}"
  violations="${violations}${file}:${line}: AllowAll consumed at the call (clause 1)"$'\n'
done < <(
  grep -rnE 'cedar\.AllowAll\{\}' --include='*.go' "$RPC_ROOT" 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -vE ':[[:space:]]*//' \
    | grep -vE '(=|return)[[:space:]]*cedar\.AllowAll\{\}[[:space:]]*$' || true
)

# ---------------------------------------------------------------------------
# Clause 2: a placeholder variable nobody replaces.
#
# For each `var <name> cedar.Gate = cedar.AllowAll{}` (or `<name> :=`),
# require a later plain assignment to <name> in the same file that is
# neither AllowAll nor nil. Without one the placeholder IS the final
# value.
#
# KNOWN HOLES, deliberately not closed here (a shell gate cannot do
# reachability or scope analysis; closing them needs a Go AST tool):
#   - a dead replacement — `if false { g = engine }` — counts.
#   - a replacement in a DIFFERENT function later in the same file, on a
#     variable that merely shares the name, counts.
# Both require someone to write the replacement deliberately, which is a
# different failure mode from the accidental omissions above.
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
  # A replacement is `name = <something other than AllowAll or nil>`
  # after this line. `g = nil` is not a replacement: a nil Gate is the
  # same unconditional permit AllowAll is.
  if ! awk -v n="$name" -v start="$line" 'NR>start {
        pat = "^[[:space:]]*" n "[[:space:]]*=[^=]"
        nilpat = "^[[:space:]]*" n "[[:space:]]*=[[:space:]]*nil[[:space:]]*$"
        if ($0 ~ pat && $0 !~ /cedar\.AllowAll\{\}/ && $0 !~ nilpat) { found=1; exit }
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
# to be assigned a non-nil value inside it.
#
# Field discovery strips line comments and struct tags before matching.
# Anchoring the type at end-of-line meant a single trailing `// comment`
# on the declaration removed the field from the gate's view entirely —
# and adding such a comment is a natural thing to do *while* introducing
# the omission.
#
# The assignment scan also strips comments: `// Cedar: tracked in TICKET`
# left where the assignment used to be otherwise satisfied a bare
# substring test, and so did a `Cedar:` mention inside an unrelated
# field's trailing comment.
# ---------------------------------------------------------------------------
config_fields=$(
  for f in $(find "$VIEWS_ROOT" -name '*.go' ! -name '*_test.go' | sort); do
    awk -v file="$f" '
      /^type[[:space:]]+Config[[:space:]]+struct[[:space:]]*\{/ { inblock=1; next }
      inblock && /^\}/ { inblock=0; next }
      inblock {
        line = $0
        sub(/\/\/.*$/, "", line)          # drop a trailing line comment
        sub(/`[^`]*`/, "", line)          # drop a struct tag
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
        if (line ~ /^[A-Z][A-Za-z0-9_]*[[:space:]]+cedar\.Gate$/) {
          split(line, parts, /[[:space:]]+/)
          print file "\t" parts[1]
        }
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
  #
  # `Field: nil` counts as NOT assigned: an explicit nil is the same
  # unconditional permit as the omission, so clause 3 checks the value,
  # not merely the presence of the key.
  found=$(awk -v a="${impalias}.Config{" -v fld="${field}:" '
    function code(s) { sub(/\/\/.*$/, "", s); return s }
    function assigned(s,   i, rest) {
      i = index(s, fld); if (i == 0) return 0
      rest = substr(s, i + length(fld))
      gsub(/^[[:space:]]+/, "", rest)
      if (rest ~ /^nil[[:space:]]*,?[[:space:]]*$/) return 0
      return 1
    }
    { c = code($0) }
    index(c, a) > 0 { depth=1; if (assigned(c)) { print "yes"; exit } ; collecting=1; next }
    collecting {
      n = gsub(/\{/, "{"); m = gsub(/\}/, "}")
      depth += n - m
      if (assigned(c)) { print "yes"; exit }
      if (depth <= 0) { collecting=0 }
    }
  ' "$API_FILE")
  if [[ "$found" != "yes" ]]; then
    violations="${violations}${file}: Config.${field} is never assigned a non-nil gate in ${API_FILE}'s ${impalias}.Config literal — the zero value is a nil gate, i.e. unconditional permit (clause 3)"$'\n'
  fi
done <<< "$config_fields"

# ---------------------------------------------------------------------------
# Clause 4: a cedar.Gate wired through a functional option, not a Config
# struct field.
#
# C-010 (model-authored-graphs-01PMGA01 UNIT-8(b)): the agentgraph
# Manager has no `Config` struct — NewManager takes ...ManagerOption
# (manager.go:153) — so clause 3's `type Config struct` discovery has
# nothing to see WithGraphCedarGate with. Reverting that one argument at
# its api.go wiring site loses the whole graph.author/graph.run
# property and breaks no listing test, because the nil-gate contract is
# default-allow (GateGraphAuthor/GateGraphRun's own documented
# behaviour) — exactly the shape clause 3 exists to catch, just behind
# a functional option instead of a struct field.
#
# For every `func With<Name>(<param> cedar.Gate) …Option` declared
# under core/rpc/views/** (non-test), require `<alias>.With<Name>(` to
# appear in api.go with an argument that is textually neither `nil` nor
# `cedar.AllowAll{}`. A variable name (e.g. `graphCedarGate`, itself
# reassigned from AllowAll{} to a real engine — see clause 2's
# placeholder idiom) counts as wired; clauses 1/2 already audit that
# variable's own assignment history, so clause 4 does not re-walk it.
#
# KNOWN HOLE (a shell gate cannot do full parsing): the argument is
# extracted with `[^()]*`, which assumes the call's own argument
# contains no nested parentheses. Every gate argument in this codebase
# today is a bare identifier or a literal (`nil`, `cedar.AllowAll{}`);
# an argument shaped like `buildSomething(x)` would defeat the
# extraction. Closing that needs a Go AST tool, same caveat clause 2
# already carries for a different reason.
# ---------------------------------------------------------------------------
with_gate_funcs=$(
  for f in $(find "$VIEWS_ROOT" -name '*.go' ! -name '*_test.go' | sort); do
    # `|| true` on EACH file's pipeline, not just the whole loop: with
    # `set -eo pipefail`, a plain `grep | sed` that matches nothing in
    # one file exits 1, and pipefail propagates that as the exit status
    # of the `for` loop's last iteration — which then aborts the
    # ENTIRE `with_gate_funcs=$(...)` assignment via set -e, discarding
    # every file found in earlier iterations. This is exactly the
    # "grep exits 1 on no match" trap clause 1's `|| true` already
    # guards against at the top level; it applies per-iteration here
    # because the loop, not just the substitution, is what set -e sees.
    { grep -nE '^func With[A-Za-z0-9_]+\([a-zA-Z_][a-zA-Z0-9_]*[[:space:]]+cedar\.Gate\)' "$f" \
      | sed -nE "s#^[0-9]+:func (With[A-Za-z0-9_]+)\(.*#${f}\t\1#p"; } || true
  done
)

if [[ -z "$with_gate_funcs" ]]; then
  echo "${GATE} FAIL: no 'func With<Name>(<param> cedar.Gate) ...Option' found under ${VIEWS_ROOT}." >&2
  echo "${GATE} Clause 4 has nothing to inspect, which is indistinguishable from" >&2
  echo "${GATE} passing. Either every such option was renamed/removed, or the grep" >&2
  echo "${GATE} pattern above needs updating for a new shape." >&2
  exit 1
fi

# Comments stripped per-line, then the whole file collapsed to one line
# so a call site wrapped across multiple physical lines (gofmt's
# wrapping of a long argument list) is still one contiguous match for
# the `[^()]*` extraction below.
api_collapsed=$(sed -E 's#//.*$##' "$API_FILE" | tr '\n' ' ')

while IFS=$'\t' read -r file name; do
  [[ -z "$file" ]] && continue
  pkgdir=$(dirname "$file")
  pkgname=$(basename "$pkgdir")
  impalias=$(grep -oE "[A-Za-z0-9_]+[[:space:]]+\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE" \
    | awk '{print $1}' | head -1 || true)
  if [[ -z "$impalias" ]]; then
    if grep -qE "\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE"; then
      impalias="$pkgname"
    else
      violations="${violations}${file}: ${name} is a cedar.Gate-typed option but ${API_FILE} does not import ${pkgdir} — nothing constructs it with a gate (clause 4)"$'\n'
      continue
    fi
  fi
  # PRESENCE first, ARGUMENT second. These are different questions and
  # conflating them produced a false positive the day this clause shipped.
  #
  # `[^()]*` cannot match an argument that is itself a call. The gate's own
  # header documents that hole — and bundle-download-and-verify-01PMZ909
  # walked straight into it hours later with
  #   bundle.WithGate(a.cedarGate())
  # which is CORRECTLY wired. The old single-regex form found no match and
  # reported "declared but never called", i.e. it accused a live Cedar gate
  # of being absent. A gate that fires on correct code gets deleted, taking
  # its real coverage with it.
  #
  # So: first ask whether the option is called AT ALL (argument shape
  # irrelevant). Only if it is do we try to read the argument, and a nested
  # call is treated as "not a literal permit" — which is right, since
  # `nil` and `cedar.AllowAll{}` are the shapes clause 4 exists to catch and
  # neither contains parentheses.
  called=$(printf '%s' "$api_collapsed" | grep -cE "${impalias}\.${name}\(" || true)
  call=$(printf '%s' "$api_collapsed" | grep -oE "${impalias}\.${name}\([^()]*\)" | head -1 || true)
  if [[ "${called:-0}" -gt 0 && -z "$call" ]]; then
    # Called with a nested-call argument, e.g. `WithGate(a.cedarGate())`.
    #
    # The nesting itself is fine — that is the legitimate production shape
    # this branch exists to stop mis-flagging. But "not a literal permit"
    # is NOT the same as "not a permit": the approving review of PR #302
    # planted `WithGate(helper())` where helper's body is
    # `return cedar.AllowAll{}` and every clause missed it. Skipping here
    # unconditionally would trade the false positive for a false negative,
    # which is the worse of the two — a gate that cannot see an
    # unconditional permit is the exact lie this gate exists to end.
    #
    # So: resolve the callee one level and check whether its body returns
    # an unconditional permit. One level is not full call-graph analysis
    # (that needs a Go AST tool, same caveat clause 2 carries), but it
    # closes the shape an adversary or a careless refactor actually
    # produces.
    callee=$(printf '%s' "$api_collapsed" \
      | grep -oE "${impalias}\.${name}\([A-Za-z0-9_.]+\(" \
      | head -1 | sed -E "s#.*\(([A-Za-z0-9_.]+)\($#\1#" || true)
    if [[ -n "$callee" ]]; then
      shortname="${callee##*.}"
      # Body of `func (recv) shortname(...) ... { ... }` up to the next
      # column-0 close brace, then look for an unconditional permit.
      permit=$(awk -v fn="$shortname" '
        $0 ~ ("^func .*[ \t(]" fn "\(") {inside=1}
        inside {print}
        inside && /^}/ {exit}
      ' $(find core -name '*.go' ! -name '*_test.go') 2>/dev/null \
        | grep -cE "return[[:space:]]+(cedar\.)?AllowAll\{\}" || true)
      if [[ "${permit:-0}" -gt 0 ]]; then
        violations="${violations}${file}: ${name} is called as ${impalias}.${name}(${callee}(...)), and ${shortname} returns an unconditional cedar.AllowAll{} — the gate can never deny (clause 4, nested-call permit)"$'\n'
      fi
    fi
    continue
  fi
  if [[ -z "$call" ]]; then
    violations="${violations}${file}: ${name} is declared but ${API_FILE} never calls ${impalias}.${name}(...) — the option is never applied, so the field it sets keeps its zero value (clause 4)"$'\n'
    continue
  fi
  arg="${call#*(}"
  arg="${arg%)}"
  arg="$(printf '%s' "$arg" | sed -E 's/^[[:space:]]+|[[:space:]]+$//g')"
  if [[ "$arg" == "nil" || "$arg" == "cedar.AllowAll{}" || -z "$arg" ]]; then
    violations="${violations}${file}: ${name} is called with '${arg}' at ${API_FILE}'s ${impalias}.${name}(...) call site — a nil/AllowAll/empty argument is the same unconditional permit as an omitted Config field (clause 4)"$'\n'
  fi
done <<< "$with_gate_funcs"

# ---------------------------------------------------------------------------
# Clause 5 (B4, unwired sweep, release/v0.72.0): a Cedar-Engine-backed
# collaborator field that clause 3 is structurally blind to because it
# violates one of clause 3's two hardcoded assumptions:
#
#   core/rpc/views/permissions/impl.go   Config.Engine   type Engine
#     (permissions.Engine, NOT literally cedar.Gate — api.go's
#     permissionsview.Config{...} assigns a.cedarEngine to it directly)
#   core/rpc/views/contextsync/impl.go   Impl.Gate       type cedar.Gate
#     (correctly typed, but the struct is named Impl, not Config —
#     api.go's contextsyncview.Impl{...} assigns a.cedarGate())
#
# Reverting either assignment left every existing test green: the
# permissions package's own impl_test.go constructs the engine ITSELF
# (newRealEngineAPI) rather than driving api.go's real wiring, and
# nothing drove the contextsync Gate's reach at all before this clause.
#
# TWO NARROW WIDENINGS of clause 3's exact mechanism — narrow on purpose,
# to avoid the false-positive blast radius a fully generic version would
# have (see below):
#
#   (a) STRUCT NAME: scan `type Impl struct` blocks in addition to
#       `type Config struct` — the only two struct-literal-at-api.go
#       idioms this codebase uses (Config for options-style views, Impl
#       for directly-constructed ones; contextsync.Impl's own doc
#       comment confirms the idiom: "All fields may be nil").
#
#   (b) FIELD TYPE: in a Config struct specifically, also accept a bare
#       identifier type (declared in the SAME FILE, hence guaranteed
#       same package) that carries a compile-time witness
#       `var _ <TypeName> = (*cedar.Engine)(nil)` — i.e. an interface
#       *cedar.Engine is declared to satisfy. This is deliberately NOT
#       "any witnessed interface": that idiom appears 33 times under
#       core/rpc/views/ for the ordinary "struct implements its own API
#       interface" assertion (var _ FooAPI = (*API)(nil)), which would
#       make this clause pure noise. Restricting the witness's RHS to
#       exactly `(*cedar.Engine)(nil)` matches precisely two
#       declarations today (cedarpolicy.Engine, permissions.Engine) —
#       cedarpolicy's is wired through a plain constructor call, not a
#       Config literal, so it never enters this clause's field list.
#
# Then, exactly like clause 3: resolve the api.go import alias, locate
# the `<alias>.<StructName>{...}` literal (a leading `&` on a pointer
# literal does not break the substring search below), and require the
# field to be assigned something other than a bare `nil`.
# ---------------------------------------------------------------------------
cedar_engine_witness_types_by_file=$(
  for f in $(find "$VIEWS_ROOT" -name '*.go' ! -name '*_test.go' | sort); do
    grep -oE '^var _ [A-Za-z_][A-Za-z0-9_]* = \(\*cedar\.Engine\)\(nil\)' "$f" \
      | sed -E "s#^var _ ([A-Za-z_][A-Za-z0-9_]*) =.*#${f}\t\1#" || true
  done
)

if [[ -z "$cedar_engine_witness_types_by_file" ]]; then
  echo "${GATE} FAIL: no 'var _ <Type> = (*cedar.Engine)(nil)' witness found under ${VIEWS_ROOT}." >&2
  echo "${GATE} Clause 5's type-widening half has nothing to anchor on, which is" >&2
  echo "${GATE} indistinguishable from passing. Either the witness idiom was removed" >&2
  echo "${GATE} (permissions.Engine, cedarpolicy.Engine) or renamed — update this" >&2
  echo "${GATE} script in the same commit." >&2
  exit 1
fi

clause5_fields=$(
  for f in $(find "$VIEWS_ROOT" -name '*.go' ! -name '*_test.go' | sort); do
    file_witnesses=$(printf '%s\n' "$cedar_engine_witness_types_by_file" | awk -F'\t' -v file="$f" '$1==file{print $2}')
    awk -v file="$f" -v witnesses="$file_witnesses" '
      BEGIN {
        n = split(witnesses, w, "\n")
        for (i = 1; i <= n; i++) if (w[i] != "") wset[w[i]] = 1
      }
      /^type[[:space:]]+(Config|Impl)[[:space:]]+struct[[:space:]]*\{/ {
        inblock = 1
        split($0, parts, /[[:space:]]+/)
        structname = parts[2]
        next
      }
      inblock && /^\}/ { inblock = 0; next }
      inblock {
        line = $0
        sub(/\/\/.*$/, "", line)
        sub(/`[^`]*`/, "", line)
        gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
        if (structname == "Impl" && line ~ /^[A-Z][A-Za-z0-9_]*[[:space:]]+cedar\.Gate$/) {
          split(line, fp, /[[:space:]]+/)
          print file "\t" structname "\t" fp[1]
        } else if (structname == "Config" && line ~ /^[A-Z][A-Za-z0-9_]*[[:space:]]+[A-Za-z_][A-Za-z0-9_]*$/) {
          split(line, fp, /[[:space:]]+/)
          if (fp[2] in wset) print file "\t" structname "\t" fp[1]
        }
      }
    ' "$f"
  done
)

if [[ -z "$clause5_fields" ]]; then
  echo "${GATE} FAIL: clause 5 found no Impl.<cedar.Gate> or witnessed-Config field under ${VIEWS_ROOT}." >&2
  echo "${GATE} Clause 5 has nothing to inspect, which is indistinguishable from passing." >&2
  echo "${GATE} Either contextsync.Impl.Gate / permissions.Config.Engine were removed or" >&2
  echo "${GATE} restructured — update this script in the same commit." >&2
  exit 1
fi

while IFS=$'\t' read -r file structname field; do
  [[ -z "$file" ]] && continue
  pkgdir=$(dirname "$file")
  pkgname=$(basename "$pkgdir")
  impalias=$(grep -oE "[A-Za-z0-9_]+[[:space:]]+\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE" \
    | awk '{print $1}' | head -1 || true)
  if [[ -z "$impalias" ]]; then
    if grep -qE "\"github\.com/kameas-ai/kenaz-harness/${pkgdir}\"" "$API_FILE"; then
      impalias="$pkgname"
    else
      violations="${violations}${file}: ${structname}.${field} is a Cedar-Engine-backed field but ${API_FILE} does not import ${pkgdir} — nothing constructs it with the shared engine (clause 5)"$'\n'
      continue
    fi
  fi
  found=$(awk -v a="${impalias}.${structname}{" -v fld="${field}:" '
    function code(s) { sub(/\/\/.*$/, "", s); return s }
    function assigned(s,   i, rest) {
      i = index(s, fld); if (i == 0) return 0
      rest = substr(s, i + length(fld))
      gsub(/^[[:space:]]+/, "", rest)
      if (rest ~ /^nil[[:space:]]*,?[[:space:]]*$/) return 0
      return 1
    }
    { c = code($0) }
    index(c, a) > 0 { depth=1; if (assigned(c)) { print "yes"; exit } ; collecting=1; next }
    collecting {
      n = gsub(/\{/, "{"); m = gsub(/\}/, "}")
      depth += n - m
      if (assigned(c)) { print "yes"; exit }
      if (depth <= 0) { collecting=0 }
    }
  ' "$API_FILE")
  if [[ "$found" != "yes" ]]; then
    violations="${violations}${file}: ${structname}.${field} is never assigned a non-nil value in ${API_FILE}'s ${impalias}.${structname} literal — the zero value is a nil collaborator, i.e. no Cedar engine reaches it (clause 5)"$'\n'
  fi
done <<< "$clause5_fields"

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
