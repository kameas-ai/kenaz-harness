#!/usr/bin/env bash
# check-served-mode-topic-forwarding.sh — a NARROWLY-SCOPED completeness
# gate: every `Topic* = "…"` broker-topic const with a real frontend
# `useEventStream(...)` subscriber must also appear in
# core/serve/wsstream.go's passthroughTopics (by VALUE), or be named in a
# dated allowlist line.
#
# WHY THIS IS NARROWER THAN check-broker-topic-consumers.sh (I14)
# -----------------------------------------------------------------
# I14 asks "does ANY consumer exist" (frontend, Go, publish, passthrough,
# or allowlist) — it never asked whether a topic reachable from a
# `useEventStream` call is ALSO reachable when running under `--serve`.
# PR #336 review MUST FIX 1 found exactly that gap for mcp:health-changed:
# I14 reported it "covered" (frontend_hit=1 was enough), while a served
# workbench never received the frame at all, because passthroughTopics is
# a SEPARATE list I14 never required frontend-subscribed topics to be in.
# wsstream_topics_parity_test.go's header records FIVE more topics in
# exactly that shape, found by the same cross-reference and deliberately
# not gated at the time — each needs its own session-scoping disposition
# (does the payload carry a session id, or does it need a
# processWideTopics exemption like TopicMigrationDriftDetected /
# mcp.TopicMCPHealthChanged?) before it can be wired, which is real
# per-topic research, not a mechanical fix. This gate freezes today's five
# known gaps in a dated allowlist and fails on anything NEW — so the
# five stay a tracked, bounded backlog instead of an invisible one, and
# nobody adds a sixth by accident.
#
# WHY "useEventStream" ONLY (not EventsOn / onServedEvent, unlike I14's
# pass 1)
# -----------------------------------------------------------------
# `useEventStream` (frontend/src/lib/useEventStream.ts) is the ONLY
# frontend call that actually depends on passthroughTopics: under
# `window.runtime` (desktop) it wires the native Wails bridge directly;
# under served mode it falls back to `onServedEvent`, which is fed
# exclusively by frames wsstream.go forwards for topics in
# passthroughTopics. A bare `EventsOn(...)` call is the RAW native Wails
# bridge — it never runs in a browser and never goes anywhere near
# passthroughTopics, so a topic reachable ONLY that way (e.g.
# `session.list_changed` in useHarnessAPI.ts, `sites:deploy:progress` in
# SitesView.vue) cannot have this gate's defect: there is no served-mode
# expectation to disappoint. `onServedEvent(...)` is useEventStream's OWN
# served-mode implementation detail — matching it here would be circular
# (it is downstream of passthroughTopics, not a second way of reaching a
# topic that bypasses it).
#
# WHY THREE PACKAGES ARE EXCLUDED FROM THE CHECKED SET
# -----------------------------------------------------------------
# core/menu/**, core/rpc/views/update/** and core/update/** declare
# Topic* consts for the native OS menu bar (menu:search:open,
# menu:about:open, …) and the desktop self-update pump
# (update:available, update:download-{progress,complete,failed}).
# `main.go`'s `runServeMode` — the entire `--serve` code path — never
# constructs `menu.Handlers` or wires `core/update.Service` /
# `updateview.Manager` at all (verified: neither package is referenced
# anywhere in `runServeMode`'s body). These publishes structurally
# cannot occur under `--serve` no matter what passthroughTopics contains
# — there is no gap to freeze, because there is no server-side event to
# miss. (Their frontend `useEventStream` calls are real — App.vue and
# UpdatesPanel.vue both use it — but they degrade to "nothing arrives,
# because Go never tried to send anything," which is the OS-menu-bar /
# self-update features being desktop-only by nature, not this gate's
# defect class.) core/serve/wsstream.go ITSELF is also excluded,
# self-referentially: `TopicStreamTruncated` is a synthetic notice
# wsstream.go writes directly via its own `writeFrame` helper when THIS
# connection cannot keep up — it never goes through `EventBus.Publish`,
# so "is it in the list this same file defines" is not a coherent
# question for it (mirrors the G-0 self-exclusion precedent in I14: a
# file's own declaration doesn't count as evidence about itself).
#
# PASS 2 (#69): THE REVERSE DIRECTION
# -----------------------------------------------------------------
# Pass 1 above asks "does every useEventStream-subscribed topic reach
# passthroughTopics". It says nothing about the other direction: a topic
# IN passthroughTopics that NO useEventStream call subscribes to — dead
# weight forwarded to every served connection's frame stream, and more
# importantly a passthroughTopics entry that no longer states real
# intent. This matters more than it used to: passthroughTopics recently
# became the single hand-authored source of truth (the TS
# SERVED_STREAM_TOPICS list is generated from it, per
# wsstream_topics_parity_test.go), so an orphan entry is read as a
# statement of intent nobody meant, not a harmless leftover.
#
# Pass 2 reuses this same file's discovery machinery: PASSTHROUGH_BLOCK /
# IDENT_TO_VALUE to walk passthroughTopics' own elements (this time
# keeping the identifier, not collapsing straight to a value set) and
# FRONTEND_WINDOW to ask the identical "does a real useEventStream call
# mention this value" question pass 1 already asks — just with the two
# sides swapped. No new discovery machinery.
#
# Legitimate exceptions go in a SEPARATE dated allowlist
# (served-mode-topic-forwarding-orphans.txt), not the pass-1 one — "not
# forwarded despite a subscriber" and "forwarded despite no subscriber"
# are different claims about the same value, and conflating the two
# files would let a pass-1 exception silently launder a pass-2 violation
# for an unrelated topic that happens to share a line-matching quirk.
#
# Exit codes:
#   0 — every useEventStream-subscribed Topic* const (outside the
#       excluded desktop-only/self-referential packages) is forwarded via
#       passthroughTopics, or explicitly allowlisted (pass 1) AND every
#       passthroughTopics entry has a real useEventStream consumer, or is
#       explicitly allowlisted (pass 2).
#   2 — at least one topic fails either pass.
set -euo pipefail

source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[served-mode-topic-forwarding]"
ALLOWLIST="scripts/ci/allowlists/served-mode-topic-forwarding-gaps.txt"
PASSTHROUGH_FILE="core/serve/wsstream.go"

REPORT_MODE=0
if [[ "${1:-}" == "--report" ]]; then
  REPORT_MODE=1
fi

ci_require_file "$PASSTHROUGH_FILE" "$GATE" \
  "passthroughTopics is declared there; without it there is nothing to check membership against."

# --- Discover every `<Ident> = "<value>"` Topic* const, same discovery
# pass as check-broker-topic-consumers.sh (kept byte-identical so the two
# gates never silently diverge on what counts as a topic). Unfiltered —
# used both to build the checked set below AND to resolve
# passthroughTopics' identifiers back to their literal values.
mapfile -t ALL_DEFS < <(
  grep -rnE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z0-9_]*[Tt]opic[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*"[^"]+"' \
    core --include='*.go' 2>/dev/null \
    | grep -v '_test\.go' \
    | sed -E 's/^([^:]+):([0-9]+):[[:space:]]*(const[[:space:]]+)?([A-Za-z0-9_]*[Tt]opic[A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*"([^"]+)".*/\1:\2:\4:\5/'
)

if [[ ${#ALL_DEFS[@]} -eq 0 ]]; then
  echo "${GATE} no Topic* consts found — nothing to check (unexpected; verify the glob)." >&2
  exit 2
fi

# --- Build the ident -> value map (used to resolve passthroughTopics'
# identifiers, e.g. `rpc.TopicSessionUsageUpdated`, back to the literal
# string they carry). Last writer wins, which only matters for the
# handful of values declared under two different names on purpose
# (e.g. "cost.threshold.crossed" via both ThresholdEventTopic and
# rpc.TopicCostThresholdCrossed) — either resolves to the same value.
declare -A IDENT_TO_VALUE
for def in "${ALL_DEFS[@]}"; do
  rest="${def#*:}"    # drop file
  rest="${rest#*:}"   # drop line
  ident="${rest%%:*}"
  value="${rest#*:}"
  IDENT_TO_VALUE["$ident"]="$value"
done

# --- Excluded packages (see header for why): declaring file starts with
# one of these prefixes, OR is passthroughTopics' own declaring file.
is_excluded_file() {
  local f="$1"
  case "$f" in
    core/menu/*) return 0 ;;
    core/rpc/views/update/*) return 0 ;;
    core/update/*) return 0 ;;
    "$PASSTHROUGH_FILE") return 0 ;;
    *) return 1 ;;
  esac
}

# --- CHECK_DEFS: ALL_DEFS minus the excluded-package entries.
CHECK_DEFS=()
for def in "${ALL_DEFS[@]}"; do
  file="${def%%:*}"
  if ! is_excluded_file "$file"; then
    CHECK_DEFS+=("$def")
  fi
done

# --- Pass 1 (frontend): production .ts/.vue files under frontend/src,
# useEventStream call sites ONLY (see header for why not EventsOn /
# onServedEvent too). Window = call line + next 3 lines, same rationale
# as I14: a generic type param (`useEventStream<Payload>(`) pushes the
# topic literal onto its own line under this repo's formatting.
ci_require_dir "frontend/src" "$GATE" \
  "Pass 1 (frontend subscribers) cannot run without it, and a frontend-only subscriber would be reported dead."

mapfile -t FRONTEND_FILES < <(
  find frontend/src -type f \( -name '*.ts' -o -name '*.vue' \) \
    ! -path '*__tests__*' ! -name '*.spec.ts' ! -name '*.test.ts'
)
if [[ ${#FRONTEND_FILES[@]} -eq 0 ]]; then
  echo "$GATE FAIL: frontend/src has no production .ts/.vue files to scan." >&2
  exit 1
fi

FRONTEND_WINDOW=$(grep -A3 -E 'useEventStream(<[^>]*>)?\(' "${FRONTEND_FILES[@]}") || {
  rc=$?
  if [[ $rc -ge 2 ]]; then
    echo "${GATE} ERROR: frontend subscriber scan failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
    exit 1
  fi
}
FRONTEND_WINDOW=$(grep -A3 -E 'useEventStream(<[^>]*>)?\(' "${FRONTEND_FILES[@]}" 2>/dev/null || true)

# --- passthroughTopics block, resolved to a VALUE set (not an identifier
# regex like I14 uses per-topic — matching by value is what makes a
# topic declared under one name but forwarded under a same-valued alias
# (e.g. ThresholdEventTopic / rpc.TopicCostThresholdCrossed, both
# "cost.threshold.crossed") correctly count as covered).
PASSTHROUGH_BLOCK=$(awk '/var passthroughTopics = \[\]string\{/{flag=1} flag{print} /^\}/{if(flag)exit}' "$PASSTHROUGH_FILE")

# Only real identifier tokens on lines that are actual slice ELEMENTS
# (start with whitespace + an identifier + a comma), not prose: the
# block's own comments are full of English sentences ("Chat streaming —
# the reason this file exists."), and matching every word in them
# against IDENT_TO_VALUE would be harmless-but-wrong noise at best, and
# at worst a bare "." at a sentence's end reduces to an empty subscript.
declare -A PASSTHROUGH_VALUES
while IFS= read -r tok; do
  [[ -z "$tok" ]] && continue
  bare="${tok##*.}"  # strip an optional "pkg." qualifier
  [[ -z "$bare" ]] && continue
  if [[ -n "${IDENT_TO_VALUE[$bare]+set}" ]]; then
    PASSTHROUGH_VALUES["${IDENT_TO_VALUE[$bare]}"]=1
  fi
done < <(printf '%s\n' "$PASSTHROUGH_BLOCK" | grep -vE '^[[:space:]]*//' | grep -oE '[A-Za-z_][A-Za-z0-9_.]*' || true)

# --- Allowlist (dated "<value>" DATA lines, comments stripped).
ALLOWLIST_DATA=""
if [[ -f "$ALLOWLIST" ]]; then
  ALLOWLIST_DATA=$(grep -v '^[[:space:]]*#' "$ALLOWLIST") || {
    rc=$?
    if [[ $rc -ge 2 ]]; then
      echo "${GATE} ERROR: allowlist read failed (grep exit ${rc})." >&2
      exit 1
    fi
  }
fi

fail=0
candidates=0
allowlisted_count=0

for def in "${CHECK_DEFS[@]}"; do
  file="${def%%:*}"
  rest="${def#*:}"
  line="${rest%%:*}"
  rest="${rest#*:}"
  ident="${rest%%:*}"
  value="${rest#*:}"

  frontend_hit=0
  if [[ "$FRONTEND_WINDOW" == *"\"${value}\""* || "$FRONTEND_WINDOW" == *"'${value}'"* ]]; then
    frontend_hit=1
  fi

  # Only useEventStream-subscribed topics are candidates at all — see
  # header for why a topic with no such subscriber is out of scope for
  # this gate (I14 already covers "does anything consume it").
  if [[ $frontend_hit -ne 1 ]]; then
    if [[ $REPORT_MODE -eq 1 ]]; then
      printf '  %-32s = %-40s (no useEventStream subscriber — not a candidate)\n' "$ident" "$value"
    fi
    continue
  fi

  candidates=$((candidates + 1))

  passthrough_hit=0
  if [[ -n "${PASSTHROUGH_VALUES[$value]+set}" ]]; then
    passthrough_hit=1
  fi

  allow_hit=0
  if [[ -n "$ALLOWLIST_DATA" && "$ALLOWLIST_DATA" == *"\"${value}\""* ]]; then
    allow_hit=1
  fi

  if [[ $passthrough_hit -eq 1 ]]; then
    if [[ $REPORT_MODE -eq 1 ]]; then
      printf '  %-32s = %-40s -> forwarded (passthroughTopics)\n' "$ident" "$value"
    fi
    continue
  fi

  if [[ $allow_hit -eq 1 ]]; then
    allowlisted_count=$((allowlisted_count + 1))
    if [[ $REPORT_MODE -eq 1 ]]; then
      printf '  %-32s = %-40s -> ALLOWLISTED (not forwarded, dated blocker on file)\n' "$ident" "$value"
    fi
    continue
  fi

  fail=1
  if [[ $REPORT_MODE -eq 1 ]]; then
    printf '  %-32s = %-40s -> FAIL (not forwarded, not allowlisted)\n' "$ident" "$value"
  fi
  echo "" >&2
  echo "${GATE} FAIL: ${ident} = \"${value}\" (${file}:${line}) has a real frontend useEventStream subscriber but is missing from ${PASSTHROUGH_FILE}'s passthroughTopics and has no dated allowlist line." >&2
  echo "  A served-mode client that subscribes to this topic will silently never receive it. Fix: add the value to passthroughTopics (and harnessClient.ts's SERVED_STREAM_TOPICS — see wsstream_topics_parity_test.go), deciding along the way whether it needs a processWideTopics entry (payload carries no session id), or add a dated line to ${ALLOWLIST} naming the blocker and an owner." >&2
done

if [[ $REPORT_MODE -eq 1 ]]; then
  echo ""
  echo "${GATE} pass 1: ${candidates} useEventStream-subscribed candidates checked, ${allowlisted_count} allowlisted."
fi

# --- Pass 2 (reverse direction, #69). See header. Reuses PASSTHROUGH_BLOCK
# and IDENT_TO_VALUE (built above for pass 1) and FRONTEND_WINDOW (built
# above for pass 1's frontend scan) — no new discovery machinery.
ORPHAN_ALLOWLIST="scripts/ci/allowlists/served-mode-topic-forwarding-orphans.txt"

ORPHAN_ALLOWLIST_DATA=""
if [[ -f "$ORPHAN_ALLOWLIST" ]]; then
  ORPHAN_ALLOWLIST_DATA=$(grep -v '^[[:space:]]*#' "$ORPHAN_ALLOWLIST") || {
    rc=$?
    if [[ $rc -ge 2 ]]; then
      echo "${GATE} ERROR: orphan allowlist read failed (grep exit ${rc})." >&2
      exit 1
    fi
  }
fi

orphan_fail=0
forwarded=0
orphan_allowlisted_count=0

# Walk passthroughTopics' own slice-element tokens (same extraction
# regex as the PASSTHROUGH_VALUES build above — comments stripped first,
# then real identifier tokens only), this time keeping the identifier
# for reporting instead of collapsing straight to a value set.
while IFS= read -r tok; do
  [[ -z "$tok" ]] && continue
  bare="${tok##*.}"
  [[ -z "$bare" ]] && continue
  value="${IDENT_TO_VALUE[$bare]:-}"
  # Not a resolvable Topic* const (e.g. a stray identifier in a comment
  # the "strip full-comment lines" filter missed) — not a real slice
  # element, skip rather than false-fail on it.
  [[ -z "$value" ]] && continue

  forwarded=$((forwarded + 1))

  frontend_hit=0
  if [[ "$FRONTEND_WINDOW" == *"\"${value}\""* || "$FRONTEND_WINDOW" == *"'${value}'"* ]]; then
    frontend_hit=1
  fi

  if [[ $frontend_hit -eq 1 ]]; then
    if [[ $REPORT_MODE -eq 1 ]]; then
      printf '  %-32s = %-40s <- consumed (useEventStream)\n' "$tok" "$value"
    fi
    continue
  fi

  if [[ -n "$ORPHAN_ALLOWLIST_DATA" && "$ORPHAN_ALLOWLIST_DATA" == *"\"${value}\""* ]]; then
    orphan_allowlisted_count=$((orphan_allowlisted_count + 1))
    if [[ $REPORT_MODE -eq 1 ]]; then
      printf '  %-32s = %-40s <- ALLOWLISTED (no useEventStream consumer, dated blocker on file)\n' "$tok" "$value"
    fi
    continue
  fi

  orphan_fail=1
  if [[ $REPORT_MODE -eq 1 ]]; then
    printf '  %-32s = %-40s <- FAIL (no useEventStream consumer, not allowlisted)\n' "$tok" "$value"
  fi
  echo "" >&2
  echo "${GATE} FAIL: passthroughTopics entry ${tok} = \"${value}\" (${PASSTHROUGH_FILE}) is forwarded to every served-mode client but no frontend useEventStream(...) call subscribes to it." >&2
  echo "  This is dead weight on every served connection's frame stream, and passthroughTopics is the single hand-authored source of truth the frontend's SERVED_STREAM_TOPICS list is generated from — an orphan entry reads as a statement of intent nobody meant. Fix: add a real useEventStream(...) subscriber, remove the entry from passthroughTopics if the feature is retired or desktop-only, or add a dated line to ${ORPHAN_ALLOWLIST} naming the blocker and an owner (e.g. the payload is delivered by a mechanism this gate cannot see — see that file's header for why TopicStreamTruncated is NOT an example of this)." >&2
done < <(printf '%s\n' "$PASSTHROUGH_BLOCK" | grep -vE '^[[:space:]]*//' | grep -oE '[A-Za-z_][A-Za-z0-9_.]*' || true)

if [[ $REPORT_MODE -eq 1 ]]; then
  echo ""
  echo "${GATE} pass 2: ${forwarded} passthroughTopics entries checked, ${orphan_allowlisted_count} allowlisted."
fi

if [[ $fail -ne 0 || $orphan_fail -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see offending topics above (pass 1: subscribed but not forwarded; pass 2: forwarded but not subscribed)." >&2
  exit 2
fi

echo "${GATE} clean — pass 1: every useEventStream-subscribed Topic* const is forwarded via passthroughTopics or explicitly allowlisted (${candidates} candidates, ${allowlisted_count} allowlisted). pass 2: every passthroughTopics entry has a useEventStream consumer or is explicitly allowlisted (${forwarded} entries, ${orphan_allowlisted_count} allowlisted)."
exit 0
