#!/usr/bin/env bash
# check-hook-event-fire-sites.sh — CI gate for
# kitty-specs/trust-surfaces-that-fire-01PMZ202 WP08 (UNIT-7, G-2).
#
# WP08 introduces FIRING_HOOK_EVENTS (frontend/src/lib/hooks.ts) — the
# declared subset of ALL_HOOK_EVENTS whose events actually reach a
# production Fire/FireAsync/Run<X> call. Before WP08, the event picker
# offered all 18 events in ALL_HOOK_EVENTS; UNIT-1 (WP01) established
# empirically that at most ONE of them (pre_send) actually fires in a
# shipped build. Restricting the picker without a mechanism that keeps it
# restricted would just be a one-time cleanup that drifts stale again the
# next time someone edits ALL_HOOK_EVENTS or forgets to grow
# FIRING_HOOK_EVENTS alongside a new producer. This gate is that mechanism.
#
# THREE LEGS (spec.md §14.3 / tasks.md WP08 "G-2 gate"):
#
#   (a) every entry of FIRING_HOOK_EVENTS has a non-test
#       Fire(/FireAsync(/Run<X> reference outside core/hooks/hooks.go.
#       Stops the picker drifting AHEAD of the backend — an event added to
#       FIRING_HOOK_EVENTS with no real producer is exactly the lie this
#       mission exists to close.
#   (b) FIRING_HOOK_EVENTS ⊆ ALL_HOOK_EVENTS ⊆ AllEvents (core/hooks.go).
#       A firing event that is not even offered, or an offered event the
#       backend does not recognise, are both drift.
#   (c) every event in AllEvents \ FIRING_HOOK_EVENTS has a dated,
#       owner-named entry in
#       scripts/ci/allowlists/i17-eventless-hook-events.txt. Stops the
#       backend drifting AHEAD of the picker silently — every event that
#       does not fire yet must say, in writing, why not and who owns
#       closing the gap. This is what makes the allowlist shrink
#       monotonically as WP09-WP21 land their producers.
#
# DISCOVERY HEURISTIC for leg (a): each event's snake_case name is
# converted to PascalCase (e.g. pre_tool_use -> PreToolUse) — the exact
# convention core/hooks/hooks.go's own Event* constants already follow
# (EventPreToolUse = "pre_tool_use"). A "real" fire site is either:
#   - a method-call site `.Run<Pascal>(` or `.Fire<Pascal>(` (the specific
#     per-event Run*/Fire* methods on hooks.Runner / its adapters) — the
#     leading `.` is what distinguishes an actual call from a bare `func
#     (r *Runner) Run<Pascal>(` declaration or an interface method
#     signature, neither of which is preceded by a receiver dot; or
#   - a line calling the generic `.Fire(` / `.FireAsync(` dispatch that
#     also names the event's own Go constant `Event<Pascal>` (the shape
#     core/fswatch/watcher.go uses: `w.runner.Fire(ctx, hooks.EventFileChanged, ev)`).
# Both patterns exclude core/hooks/hooks.go itself and every _test.go file.
#
# Usage: bash scripts/ci/check-hook-event-fire-sites.sh (from anywhere).

set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[hook-event-fire-sites]"
HOOKS_TS="frontend/src/lib/hooks.ts"
HOOKS_GO="core/hooks/hooks.go"
ALLOW_FILE="scripts/ci/allowlists/i17-eventless-hook-events.txt"

ci_require_file "$HOOKS_TS" "$GATE"
ci_require_file "$HOOKS_GO" "$GATE"
ci_require_file "$ALLOW_FILE" "$GATE"

fail=0

# ---- extract FIRING_HOOK_EVENTS and ALL_HOOK_EVENTS from hooks.ts ----
extract_ts_array() {
  local const_name="$1"
  # Handles both single-line (`export const X = ['a'] as const;`) and
  # multi-line declarations: grabbing turns on at the declaration line
  # (which is also printed) and turns off after the line containing
  # `] as const;`, whether that is the same line or a later one. An
  # earlier cut used `next` on the trigger line, which meant a
  # single-line declaration never tripped the close condition and the
  # scan ran on into the NEXT `] as const;` in the file (HOOK_KINDS'),
  # silently vacuuming up an unrelated array's contents.
  awk -v name="export const ${const_name} = \\[" \
    'index($0, name) { grabbing=1 } grabbing { print } grabbing && /\] as const;/ { grabbing=0 }' \
    "$HOOKS_TS" | grep -oE "'[a-z_]+'" | tr -d "'"
}

firing_events=$(extract_ts_array "FIRING_HOOK_EVENTS")
all_fe_events=$(extract_ts_array "ALL_HOOK_EVENTS")

if [[ -z "$firing_events" ]]; then
  echo "${GATE} FAIL: derived zero entries from FIRING_HOOK_EVENTS in ${HOOKS_TS}." >&2
  echo "${GATE} A gate that finds nothing cannot distinguish a clean tree from a broken parse." >&2
  exit 1
fi
if [[ -z "$all_fe_events" ]]; then
  echo "${GATE} FAIL: derived zero entries from ALL_HOOK_EVENTS in ${HOOKS_TS}." >&2
  exit 1
fi

# ---- extract core/hooks.AllEvents (Go), resolved through its Event* const map ----
# The const map is scanned across all of core/hooks (non-test), not just
# hooks.go, so a planted violation does not need to land inside the
# hand-maintained literal block to be visible to this gate.
const_map_file="$(mktemp)"
trap 'rm -f "$const_map_file"' EXIT

grep -rnoE '\bEvent[A-Za-z0-9]+[[:space:]]*=[[:space:]]*"[a-z_]+"' --include='*.go' "$(dirname "$HOOKS_GO")" 2>/dev/null \
  | grep -v '_test\.go:' \
  | sed -E 's/^[^:]+:[0-9]+://' \
  | sed -E 's/^([A-Za-z0-9]+)[[:space:]]*=[[:space:]]*"([a-z_]+)"$/\1 \2/' \
  > "$const_map_file"

go_all_events=""
unresolved=""
while IFS= read -r ident; do
  ident="$(printf '%s' "$ident" | sed -E 's/^[[:space:]]*([A-Za-z0-9]+),?[[:space:]]*$/\1/')"
  [[ -z "$ident" ]] && continue
  val="$(grep -E "^${ident} " "$const_map_file" | awk '{print $2}' | head -1)"
  if [[ -z "$val" ]]; then
    unresolved="${unresolved}${ident}"$'\n'
    continue
  fi
  go_all_events="${go_all_events}${val}"$'\n'
done < <(awk '/^var AllEvents = \[\]string\{/,/^\}/' "$HOOKS_GO" | grep -E '^[[:space:]]*Event[A-Za-z0-9]+,?[[:space:]]*$')

# Also honour any `AllEvents = append(AllEvents, ...)` reassignment
# anywhere in the package (non-test) — the literal block above is the
# hand-maintained source of truth today, but the gate should not go blind
# just because a future entry (or a planted violation) arrives via an
# append call instead of the literal. Each argument may be a quoted
# string or an Event* identifier resolved through the same const map.
while IFS= read -r args; do
  [[ -z "$args" ]] && continue
  while IFS= read -r arg; do
    arg="$(printf '%s' "$arg" | sed -E 's/^[[:space:]]*//; s/[[:space:]]*$//')"
    [[ -z "$arg" ]] && continue
    if [[ "$arg" =~ ^\"([a-z_]+)\"$ ]]; then
      go_all_events="${go_all_events}${BASH_REMATCH[1]}"$'\n'
    else
      val="$(grep -E "^${arg} " "$const_map_file" | awk '{print $2}' | head -1)"
      if [[ -z "$val" ]]; then
        unresolved="${unresolved}${arg} (from AllEvents=append(...))"$'\n'
      else
        go_all_events="${go_all_events}${val}"$'\n'
      fi
    fi
  done < <(printf '%s\n' "$args" | tr ',' '\n')
done < <(grep -rnoE 'AllEvents[[:space:]]*=[[:space:]]*append\(AllEvents,[^)]*\)' --include='*.go' "$(dirname "$HOOKS_GO")" 2>/dev/null \
  | grep -v '_test\.go:' \
  | sed -E 's/^[^:]+:[0-9]+://' \
  | sed -E 's/^AllEvents[[:space:]]*=[[:space:]]*append\(AllEvents,(.*)\)$/\1/')

if [[ -n "$unresolved" ]]; then
  echo "${GATE} FAIL: AllEvents references identifier(s) with no resolvable Event*=\"...\" constant:" >&2
  printf '%s\n' "$unresolved" | sed 's/^/    /' >&2
  exit 1
fi
go_all_events="$(printf '%s\n' "$go_all_events" | grep -v '^$' | sort -u)"

if [[ -z "$go_all_events" ]]; then
  echo "${GATE} FAIL: derived zero entries from ${HOOKS_GO}'s AllEvents." >&2
  exit 1
fi

firing_sorted="$(printf '%s\n' "$firing_events" | sort -u)"
all_fe_sorted="$(printf '%s\n' "$all_fe_events" | sort -u)"

# ---- leg (b): FIRING_HOOK_EVENTS ⊆ ALL_HOOK_EVENTS ⊆ AllEvents ----
not_in_all_fe=$(comm -23 <(printf '%s\n' "$firing_sorted") <(printf '%s\n' "$all_fe_sorted") || true)
if [[ -n "$not_in_all_fe" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: FIRING_HOOK_EVENTS entries not present in ALL_HOOK_EVENTS (leg b):" >&2
  printf '%s\n' "$not_in_all_fe" | sed 's/^/    /' >&2
  fail=1
fi

not_in_go=$(comm -23 <(printf '%s\n' "$all_fe_sorted") <(printf '%s\n' "$go_all_events") || true)
if [[ -n "$not_in_go" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: ALL_HOOK_EVENTS entries not present in core/hooks.AllEvents (leg b):" >&2
  printf '%s\n' "$not_in_go" | sed 's/^/    /' >&2
  fail=1
fi

# ---- leg (a): every FIRING_HOOK_EVENTS entry has a real, non-test fire site ----
to_pascal_case() {
  # snake_case -> PascalCase, e.g. post_tool_use_failure -> PostToolUseFailure
  local IFS=_
  local out=""
  for word in $1; do
    out="${out}$(tr '[:lower:]' '[:upper:]' <<<"${word:0:1}")${word:1}"
  done
  printf '%s' "$out"
}

has_fire_site() {
  local event="$1" pascal="$2"
  # Real method-call site: preceded by a receiver dot, which excludes both
  # `func (r *Runner) Run<Pascal>(` declarations and bare interface method
  # signatures (neither has a preceding '.').
  local method_hits
  method_hits=$(grep -rnE "\.(Run|Fire)${pascal}\(" --include='*.go' core 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -v "^${HOOKS_GO}:" \
    || true)
  if [[ -n "$method_hits" ]]; then
    return 0
  fi
  # Generic-dispatch call site: a .Fire( / .FireAsync( call on a line that
  # also names the event's own Go constant (core/fswatch/watcher.go's shape).
  local generic_hits
  generic_hits=$(grep -rnE '\.(Fire|FireAsync)\(' --include='*.go' core 2>/dev/null \
    | grep -v '_test\.go' \
    | grep -v "^${HOOKS_GO}:" \
    | grep -E "Event${pascal}\b" \
    || true)
  [[ -n "$generic_hits" ]]
}

checked_count=0
firing_without_site=""
while IFS= read -r event; do
  [[ -z "$event" ]] && continue
  checked_count=$((checked_count + 1))
  pascal=$(to_pascal_case "$event")
  if ! has_fire_site "$event" "$pascal"; then
    firing_without_site="${firing_without_site}${event} (derived candidate: ${pascal})"$'\n'
  fi
done <<< "$firing_sorted"

if [[ -n "$firing_without_site" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: FIRING_HOOK_EVENTS entries with no non-test Fire(/FireAsync(/Run<X> reference outside ${HOOKS_GO} (leg a):" >&2
  printf '%s\n' "$firing_without_site" | sed 's/^/    /' >&2
  fail=1
fi

# ---- leg (c): every AllEvents \ FIRING_HOOK_EVENTS entry has an allowlist row ----
eventless=$(comm -23 <(printf '%s\n' "$go_all_events") <(printf '%s\n' "$firing_sorted") || true)
allow_events=$(grep -vE '^[[:space:]]*(#|$)' "$ALLOW_FILE" | awk '{print $1}' | sort -u || true)

missing_justification=""
while IFS= read -r event; do
  [[ -z "$event" ]] && continue
  row=$(grep -E "^${event}[[:space:]]" "$ALLOW_FILE" || true)
  if [[ -z "$row" ]]; then
    missing_justification="${missing_justification}${event}"$'\n'
    continue
  fi
  if [[ "$row" != *"owner:"* || "$row" != *"date:"* ]]; then
    missing_justification="${missing_justification}${event} (row present but not dated/owner-named)"$'\n'
  fi
done <<< "$eventless"

if [[ -n "$missing_justification" ]]; then
  echo "" >&2
  echo "${GATE} FAIL: event(s) not in FIRING_HOOK_EVENTS with no dated, owner-named entry in ${ALLOW_FILE} (leg c):" >&2
  printf '%s\n' "$missing_justification" | sed 's/^/    /' >&2
  fail=1
fi

echo ""
echo "${GATE} derived candidates — FIRING_HOOK_EVENTS: ${checked_count}, ALL_HOOK_EVENTS(frontend): $(printf '%s\n' "$all_fe_sorted" | grep -c .), AllEvents(go): $(printf '%s\n' "$go_all_events" | grep -c .), allowlisted eventless: $(printf '%s\n' "$allow_events" | grep -c .)."

if [[ "$fail" -ne 0 ]]; then
  echo "${GATE} FAIL — see violations above." >&2
  exit 1
fi

echo "${GATE} clean — every FIRING_HOOK_EVENTS entry fires, the three lists nest correctly, and every non-firing event is justified."
