#!/usr/bin/env bash
# check-agentgraph-convergence.sh — CI gate for
# kitty-specs/agentgraph-total-convergence-01PMGX01/spec.md §6 invariants
# I3, I6, I7 (WP01: `fix(ci): WP01 — convergence invariant gates`).
#
# I3 — every callable node kind is exercised end-to-end by at least one
#      shipped graph (core/rpc/views/agentgraph/library/*.yaml), activity
#      (core/agentgraph/activities/yaml/*.yaml), or GOLDEN RUN FIXTURE
#      (see "Kernel-run fixtures" below). "Exercised" is computed
#      after kind-alias resolution (core/agentgraph/wire_gen.go
#      kindAliases: fork->branch, branch->decision, llm->model,
#      plan->planner) — a YAML author writing `kind: branch` today invokes
#      the `decision` executor, not the real `branch` (sub-graph spawn)
#      kind. See core/agentgraph/nodes/manifests/decision.yaml: "Was named
#      `branch` in v0; kept as alias."
#
# ---- Kernel-run fixtures (WP17) -------------------------------------------
#
# Spec §6 I3 says "shipped graph, activity, OR GOLDEN RUN FIXTURE". Only the
# first two were counted until WP17, which made the list unfairly long: a
# kind with a real NewKernel()+Run() test genuinely IS exercised end to end,
# and several were. The gap mattered in the other direction too — it created
# pressure to jam kinds into shipped YAML purely to clear a line, which is
# how you get a bundled activity with a hardcoded absolute path in it
# (the reason WP13 declined to do exactly that for read_file).
#
# The convention. In any _test.go file under core/, a line-comment:
#
#     // convergence:exercised <kind>
#
# credits <kind> to the exercised set. Put it in the doc comment of the test
# that runs it, not at the top of the file — the marker is a claim about a
# specific test, and a reviewer has to be able to check it against the code
# immediately below.
#
# ANTI-LAUNDER RULES. A marker is not a free pass:
#
#   1. The file containing it MUST also contain `NewKernel` and `.Run(`.
#      A marker in a file with no kernel in it fails the gate.
#   2. It must name a kind that exists (a manifest stem). A typo fails.
#   3. It must still be NEEDED. A marker for a kind that shipped YAML
#      already exercises is STALE and fails, exactly like a stale allowlist
#      line — markers shrink monotonically too.
#
# THE LIMIT, STATED PLAINLY. Rule 1 is grep-level and file-scoped. It proves
# a real kernel lives in that file; it does NOT prove that this test runs
# that kernel, nor that the kernel ran THIS kind through its PRODUCTION
# executor rather than a scripted stub (core/agentgraph/kernel_liveness_test.go
# substitutes stub executors for every kind but its subject-under-test —
# a marker there is only honest for the un-stubbed kinds, which is why the
# `join` marker sits on the test where joinExecutor is deliberately real).
# Closing that gap needs Go-level analysis, not a shell gate. The marker is
# therefore a REVIEWED assertion with a mechanical floor under it: cheap
# mistakes fail automatically, and dishonest ones are visible at the marker.
#
# I6 — no kind is dead-in-production-but-alive-in-a-template.
#      "Production" = core/rpc/views/agentgraph/library/chat_default.yaml
#      (the only graph the chat runner actually attaches) plus the
#      activities directory. toolloop_default.yaml is a read-only editor
#      starting template that nothing runs (spec §1.4) — a kind reachable
#      ONLY through it is exactly the failure mode I6 forbids.
#
# I5 — exactly one elicitation primitive. `core/asks` is gone, and no
#      package other than core/elicitation declares elicitation storage:
#      an AskBus read half (`LookupAnswer`), a deferral sweep
#      (`SweepExpired`), or the system-reminder renderer
#      (`SystemReminderText`). A package may declare those only if it is
#      core/elicitation itself or it imports core/elicitation (i.e. it
#      is an adapter over the shared registry, not a second store).
#
# I7 — no orphan packages: every non-test package under core/ has a
#      non-test importer, or is itself an entry point (a `main` package,
#      or a package imported directly by the repo-root main.go).
#
# Allowlists (one per invariant, plain text, `#` comments, blank lines
# ignored) live under scripts/ci/allowlists/. Each run of this script:
#   - FAILS if a violation is found that is NOT in the matching allowlist
#   - FAILS if an allowlist line is STALE (no longer a violation), so the
#     lists are forced to shrink monotonically as WPs land (spec §4.1,
#     §6.1)
#
# Usage: bash scripts/ci/check-agentgraph-convergence.sh (from anywhere —
# resolves the repo root itself). No flags. Requires `go` and `jq` (jq is
# already a CI dependency; see check-release-integrity.sh).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[agentgraph-convergence]"

MANIFEST_DIR="core/agentgraph/nodes/manifests"
LIBRARY_DIR="core/rpc/views/agentgraph/library"
ACTIVITIES_DIR="core/agentgraph/activities/yaml"
ALLOW_DIR="scripts/ci/allowlists"
I3_ALLOW="${ALLOW_DIR}/i3-unexercised-kinds.txt"
I5_ALLOW="${ALLOW_DIR}/i5-elicitation-stores.txt"
I6_ALLOW="${ALLOW_DIR}/i6-template-only-kinds.txt"
I7_ALLOW="${ALLOW_DIR}/i7-orphan-packages.txt"

ci_require_dir "$MANIFEST_DIR" "$GATE"
ci_require_dir "$LIBRARY_DIR" "$GATE"
ci_require_dir "$ACTIVITIES_DIR" "$GATE"
ci_require_file "$I3_ALLOW" "$GATE"
ci_require_file "$I5_ALLOW" "$GATE"
ci_require_file "$I6_ALLOW" "$GATE"
ci_require_file "$I7_ALLOW" "$GATE"

if ! command -v jq >/dev/null 2>&1; then
  echo "$GATE FAIL: jq is required (already installed on ci-medium; see check-release-integrity.sh)." >&2
  exit 1
fi

fail=0

# ---- helpers --------------------------------------------------------------

# load_allowlist <file> — strips comments/blanks, one entry per line.
load_allowlist() {
  grep -vE '^[[:space:]]*(#|$)' "$1" || true
}

# resolve_alias <kind> — must track core/agentgraph/aliases.go's
# `lookupAlias` behaviour exactly, not just wire_gen.go's `kindAliases`
# table. Drift here silently reopens the loophole I3/I6 exist to close
# (an alias'd kind would count as "referenced" under its own name
# instead of its canonical target).
#
# CANONICAL-FIRST RULE (agentgraph-total-convergence-01PMGX01 WP05):
# lookupAlias returns early — no rewrite at all — when the name already
# IS a callable kind in the live catalog. `kindAliases` contains
# `branch -> decision`, but `branch` is itself a callable kind (the
# sub-graph spawn primitive), so a YAML author writing `kind: branch`
# gets the `branch` manifest, NOT `decision`. WP01 seeded this function
# from the raw table and inverted that one case; toolloop_default.yaml's
# `continue_check` node was the only YAML affected and now spells
# `kind: decision` outright, so correcting the rule here changes no
# result today — it stops the next reader inheriting the wrong model.
#
# The remaining three entries are genuine aliases: `fork`, `llm` and
# `plan` are not callable kinds, so they do get rewritten.
resolve_alias() {
  case "$1" in
    fork) echo "branch" ;;
    llm) echo "model" ;;
    plan) echo "planner" ;;
    *) echo "$1" ;;
  esac
}

# check_against_allowlist <label> <allow-file> <violations (newline list)>
# Sets `fail=1` on either an unlisted violation or a stale allowlist entry.
check_against_allowlist() {
  local label="$1" allow_file="$2" violations="$3"
  local allow unlisted stale

  allow=$(load_allowlist "$allow_file")

  unlisted=$(comm -23 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
  if [[ -n "$unlisted" ]]; then
    echo "" >&2
    echo "${GATE} FAIL (${label}): violation(s) not covered by ${allow_file}:" >&2
    printf '%s\n' "$unlisted" | sed 's/^/    /' >&2
    fail=1
  fi

  stale=$(comm -13 <(printf '%s\n' "$violations" | sort -u) <(printf '%s\n' "$allow" | sort -u) | grep -v '^$' || true)
  if [[ -n "$stale" ]]; then
    echo "" >&2
    echo "${GATE} FAIL (${label}): STALE entries in ${allow_file} (no longer violations — delete these lines; spec §4.1 requires allow-lists to shrink monotonically):" >&2
    printf '%s\n' "$stale" | sed 's/^/    /' >&2
    fail=1
  fi
}

# ============================================================
# I3 + I6: node-kind reachability
# ============================================================

# Every callable kind = manifest filename stems, minus the archetype
# manifests (which start with "_" and are not callable).
all_kinds=$(find "$MANIFEST_DIR" -maxdepth 1 -name '*.yaml' ! -name '_archetype.*' -exec basename {} .yaml \; | sort -u)

# extract_kinds <file...> — every `kind: <value>` scalar in the given
# graph/activity YAML file(s), alias-resolved, one per line.
extract_kinds() {
  local f
  for f in "$@"; do
    [[ -f "$f" ]] || continue
    grep -E '^[[:space:]]*kind:[[:space:]]*' "$f" | sed -E 's/^[[:space:]]*kind:[[:space:]]*"?([A-Za-z0-9_]+)"?.*/\1/'
  done | while read -r k; do resolve_alias "$k"; done
}

shopt -s nullglob
activity_files=("${ACTIVITIES_DIR}"/*.yaml)
shopt -u nullglob

production_kinds=$( { extract_kinds "${LIBRARY_DIR}/chat_default.yaml"; extract_kinds "${activity_files[@]}"; } | sort -u)
template_kinds=$(extract_kinds "${LIBRARY_DIR}/toolloop_default.yaml" | sort -u)
yaml_kinds=$( { printf '%s\n' "$production_kinds"; printf '%s\n' "$template_kinds"; } | sort -u)

# ---- kernel-run fixture markers (see the header) --------------------------
#
# `// convergence:exercised <kind>` in a _test.go file whose file also
# contains a real kernel (NewKernel + .Run(). Validated, not trusted:
# unknown kind, kernel-less file, or a marker made redundant by shipped
# YAML all fail the gate.
marker_kinds=""
marker_fail=0
while IFS=: read -r mfile _ mrest; do
  [[ -z "$mfile" ]] && continue
  mkind=$(printf '%s' "$mrest" | sed -E 's@.*convergence:exercised[[:space:]]+([A-Za-z0-9_]+).*@\1@')
  if [[ -z "$mkind" ]]; then
    echo "${GATE} FAIL (I3 marker): malformed marker in ${mfile}: ${mrest}" >&2
    marker_fail=1
    continue
  fi
  # Rule 2 — the kind must exist.
  if ! printf '%s\n' "$all_kinds" | grep -qx "$mkind"; then
    echo "${GATE} FAIL (I3 marker): ${mfile} claims kind '${mkind}', which is not a manifest under ${MANIFEST_DIR}." >&2
    marker_fail=1
    continue
  fi
  # Rule 1 — a real kernel must live in the same file.
  if ! grep -q 'NewKernel' "$mfile" || ! grep -q '\.Run(' "$mfile"; then
    echo "${GATE} FAIL (I3 marker): ${mfile} claims kind '${mkind}' but contains no NewKernel + .Run( — a fixture marker must sit with the kernel run it describes." >&2
    marker_fail=1
    continue
  fi
  # Rule 3 — redundant markers are stale.
  if printf '%s\n' "$yaml_kinds" | grep -qx "$mkind"; then
    echo "${GATE} FAIL (I3 marker): ${mfile} claims kind '${mkind}', but a shipped graph/activity already exercises it. Delete the marker (markers shrink monotonically, like the allow-lists)." >&2
    marker_fail=1
    continue
  fi
  marker_kinds="${marker_kinds}${mkind}"$'\n'
done < <(grep -rn 'convergence:exercised' --include='*_test.go' core 2>/dev/null || true)
marker_kinds=$(printf '%s' "$marker_kinds" | grep -v '^$' | sort -u || true)
if [[ "$marker_fail" -ne 0 ]]; then
  fail=1
fi

exercised_kinds=$( { printf '%s\n' "$yaml_kinds"; printf '%s\n' "$marker_kinds"; } | grep -v '^$' | sort -u)

# I3: callable kinds referenced by nothing shipped at all — no production
# graph, no template, and no kernel-run fixture.
i3_violations=$(comm -23 <(printf '%s\n' "$all_kinds") <(printf '%s\n' "$exercised_kinds"))
check_against_allowlist "I3 unexercised kind" "$I3_ALLOW" "$i3_violations"

# I6: kinds reachable ONLY via the dead-in-production template.
i6_violations=$(comm -23 <(printf '%s\n' "$template_kinds") <(printf '%s\n' "$production_kinds"))
check_against_allowlist "I6 template-only kind" "$I6_ALLOW" "$i6_violations"

# ============================================================
# I5: exactly one elicitation primitive
# ============================================================

ELICITATION_PKG="core/elicitation"

ci_require_dir "$ELICITATION_PKG" "$GATE"

# (a) core/asks must be gone. It was the third elicitation surface
#     (spec §1.3) and WP06 deleted it; a reappearance is an outright
#     failure with no allow-list escape.
if compgen -G "core/asks/*.go" > /dev/null; then
  echo "" >&2
  echo "${GATE} FAIL (I5): core/asks/ is back. It was deleted by 01PMGX01 WP06;" >&2
  echo "${GATE}   deferred asks, expiry and the system-reminder text belong to" >&2
  echo "${GATE}   ${ELICITATION_PKG}. Fold the change in there instead." >&2
  fail=1
fi

# (b) Nobody else declares elicitation storage. The three symbols below
#     are the load-bearing halves of an ask store: the AskBus read leg,
#     the deferral sweep, and the reminder renderer. Declaring one while
#     NOT importing core/elicitation means a private registry was forked.
#
#     `grep -n ') LookupAnswer('` matches a method declaration, not a
#     call site or a doc comment, which is what keeps legitimate
#     consumers (the chat runner calls NewMemAskBus; core/rpc mentions
#     the AskBus in prose) out of the violation set.
i5_violations=""
while IFS= read -r f; do
  case "$f" in
    "${ELICITATION_PKG}"/*) continue ;;
  esac
  symbols=$(grep -oE '\) (LookupAnswer)\(|func (SweepExpired|SystemReminderText)\(|\) (SweepExpired|SystemReminderText)\(' "$f" \
    | grep -oE 'LookupAnswer|SweepExpired|SystemReminderText' | sort -u || true)
  [[ -n "$symbols" ]] || continue
  # An adapter over the shared registry is fine; a fork is not. The
  # exemption is SAME-FILE only: the file that declares an ask-store
  # half must itself import core/elicitation.
  #
  # It used to fall back to a package-level check (any *.go in the same
  # directory importing core/elicitation exempted every file in it).
  # The elicitation review (F1) planted a `zzForkedAskStore` with a
  # `LookupAnswer` method in a NEW file inside an already-importing
  # package and the gate passed it — which is the whole failure mode
  # I5 exists to catch, since a second store is precisely the thing you
  # would add as a new file next to the first one. Dropped by 01PMGX01
  # WP17; verified by re-planting that case, which now fails.
  if grep -q "kenaz-harness/${ELICITATION_PKG}\"" "$f"; then
    continue
  fi
  pkgdir=$(dirname "$f")
  while IFS= read -r sym; do
    i5_violations="${i5_violations}${pkgdir} (${sym})"$'\n'
  done <<< "$symbols"
done < <(find core -name '*.go' ! -name '*_test.go' | sort)

i5_violations=$(printf '%s' "$i5_violations" | sort -u)
check_against_allowlist "I5 elicitation store" "$I5_ALLOW" "$i5_violations"

# ============================================================
# I7: orphan packages under core/
# ============================================================

TMP_JSON="$(mktemp)"
TMP_MAININC="$(mktemp)"
trap 'rm -f "${TMP_JSON}" "${TMP_MAININC}"' EXIT

# `go list ./...` at the repo root cannot resolve main.go's
# `//go:embed all:frontend/dist` on a checkout that has not run a frontend
# build (frontend/dist does not exist). Scope the listing to ./core/...
# and ./cmd/... (neither has an embed dependency) and separately extract
# main.go's own import block by grep — this is what keeps packages that
# are ONLY imported from main.go (e.g. core/menu, core/serve,
# core/update/bootswap) from being misreported as orphans.
go list -json ./core/... ./cmd/... > "${TMP_JSON}"
grep -oE '"github.com/kameas-ai/kenaz-harness/core/[A-Za-z0-9_./]*"' main.go \
  | tr -d '"' > "${TMP_MAININC}" || true

i7_violations=$(jq -r -s --slurpfile mainimp <(jq -R -s -c 'split("\n") | map(select(length > 0))' "${TMP_MAININC}") '
  map(select(.ImportPath | startswith("github.com/kameas-ai/kenaz-harness/"))) as $pkgs
  | ($pkgs | map(.Imports // []) | add | unique) as $fromPkgs
  | ($fromPkgs + $mainimp[0]) as $imported
  | $pkgs
  | map(select(.ImportPath | startswith("github.com/kameas-ai/kenaz-harness/core/")))
  | map(select(.Name != "main"))
  | map(select(([.ImportPath] - $imported) | length > 0))
  | map(.ImportPath | sub("^github.com/kameas-ai/kenaz-harness/"; ""))
  | .[]
' "${TMP_JSON}" | sort -u)

check_against_allowlist "I7 orphan package" "$I7_ALLOW" "$i7_violations"

# ============================================================
if [[ "$fail" -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

kind_count=$(printf '%s\n' "$all_kinds" | grep -c . || true)
echo "${GATE} clean — I3/I5/I6/I7 pass (${kind_count} callable kinds checked, allow-listed exceptions honored)."
