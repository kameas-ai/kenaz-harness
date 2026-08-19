#!/usr/bin/env bash
# check-broker-topic-consumers.sh — every `Topic* = "…"` broker-topic
# const in core/** must have at least one real consumer: a frontend
# subscriber, a Go subscriber, a passthroughTopics entry (served mode),
# or a dated allowlist line.
#
# Mission: self-update-repair-01PMUP01 WP06, spec §6. This is the gate
# audit §5's "an RPC's async contract vs. its caller's await sequence"
# was originally asked for; the spec DECLINES that gate (it cannot be
# made non-vacuous — see docs/unwired-ledger.md) and builds this one
# instead. It would have caught A8 (this mission's own root cause) plus
# audit findings B9/B16/B17 — four registration-vs-consumption misses,
# one gate.
#
# THE INPUT SET IS DERIVED, NOT DECLARED — that is what makes this
# non-vacuous. A new `Topic* = "…"` const nobody annotates FAILS rather
# than silently passing; there is no opt-in list of "topics to check".
#
# Multi-pass, inheriting check-output-ports.sh's discipline (see that
# script's header for the methodology this mirrors): a Go-only pass
# would false-positive `update:available`, whose only consumer before
# this mission was TypeScript (`useUpdateStore.ts`'s
# `rt.EventsOn('update:available', ...)`). A topic may be referenced in
# Go call sites either by its literal string or by the const's own Go
# identifier (main.go's WP03 wiring uses
# `updateview.TopicDownloadProgress`, not the literal) — both count.
#
# Bias toward under-reporting (treating an ambiguous case as covered),
# same risk asymmetry check-output-ports.sh documents: a live topic
# wrongly flagged dead is worse than a genuinely-dead topic slipping
# through once, because the allowlist is the honest, reviewable escape
# hatch for the latter and there is no escape hatch for the former
# short of editing this script.
#
# PERFORMANCE: the naive form of this script (one `grep` invocation per
# topic per candidate file) took minutes on this tree's file count. The
# two hot passes below instead run ONE `grep -A3` over the whole
# candidate file set, once, and hold the result in memory; each topic
# then does an in-memory substring check against that single blob.
#
# Exit codes:
#   0 — every Topic* const is consumed, or explicitly allowlisted
#   2 — at least one Topic* const has no reader anywhere
set -euo pipefail

# ci-gate.sh resolves the repo root from THIS FILE's own location
# (scripts/ci/lib/ → ../../..) and cd's there — robust from any cwd,
# including one entirely outside the git tree (the shape
# TestGates_VerdictIsIndependentOfWorkingDirectory's `t.TempDir()`
# exercises). `git rev-parse --show-toplevel` — the pattern
# check-output-ports.sh uses — is NOT cwd-independent in that sense: it
# only resolves correctly from inside the worktree, and this gate's own
# AC-11 requires the foreign-cwd case to work.
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/lib/ci-gate.sh"

GATE="[broker-topic-consumers]"

ALLOWLIST="scripts/ci/allowlists/i14-unconsumed-broker-topics.txt"
PASSTHROUGH_FILE="core/serve/wsstream.go"

REPORT_MODE=0
if [[ "${1:-}" == "--report" ]]; then
  REPORT_MODE=1
fi

# --- Discover every `<Ident> = "<value>"` const whose identifier
# CONTAINS "Topic" (case-sensitive Topic, or lowercase topic for
# unexported consts like core/serve/wsstream.go's topicLLMStreamChunk),
# anywhere under core/**, excluding tests. One line per const:
# "<file>:<line>:<ident>:<value>".
#
# Matches BOTH Go single-const-declaration shapes: the block form
# (`Ident = "value"` inside a `const ( ... )` group — most of this
# codebase's topics) and the single-line form with the `const` keyword
# on the same line (`const AvailableTopic = "update:available"` — see
# core/update/api.go). Missing the second shape isn't hypothetical: it
# is exactly what let the first cut of this script's own planted-
# violation proof (TopicNobodyReads, written as `const TopicNobodyReads
# = "..."`) sail through undetected.
#
# CONTAINS, not STARTS-WITH — and that distinction is the whole gate.
# The first cut anchored the identifier at "Topic", which made FOUR real
# broker-topic consts invisible to the discovery pass: AvailableTopic
# (core/update/api.go — the very const the paragraph above cites as the
# reason the `const` shape was added; the script claimed to cover a const
# it could not see), ThresholdEventTopic (core/usage/threshold.go),
# ProgressTopic (core/mcp/transport/progress.go) and progressTopic
# (core/rpc/views/sites/impl.go). One of those four, mcp:progress, is
# GENUINELY UNCONSUMED — so the "empty allowlist, 35/35 consumed" claim
# this gate shipped with was false, and false in the gate's own blind
# spot rather than in its matching. A discovery pass keyed on a naming
# convention is only "derived, not declared" for the code that happens to
# follow the convention; anything a topic const cannot be named must not
# be the difference between checked and unchecked.
mapfile -t DEFS < <(
  grep -rnE '^[[:space:]]*(const[[:space:]]+)?[A-Za-z0-9_]*[Tt]opic[A-Za-z0-9_]*[[:space:]]*=[[:space:]]*"[^"]+"' \
    core --include='*.go' 2>/dev/null \
    | grep -v '_test\.go' \
    | sed -E 's/^([^:]+):([0-9]+):[[:space:]]*(const[[:space:]]+)?([A-Za-z0-9_]*[Tt]opic[A-Za-z0-9_]*)[[:space:]]*=[[:space:]]*"([^"]+)".*/\1:\2:\4:\5/'
)

if [[ ${#DEFS[@]} -eq 0 ]]; then
  echo "${GATE} no Topic* consts found — nothing to check (unexpected; verify the glob)." >&2
  exit 2
fi

# --- Precompute pass 1 (frontend): the window (call line + next 3
# lines) around every useEventStream(/EventsOn(/onServedEvent( call in
# PRODUCTION frontend/src (tests excluded — a topic string in a test's
# mock/doc-comment does not prove a real subscriber exists; mirrors
# check-output-ports.sh's YAML_DIRS excluding core/agentgraph/testdata
# as a laundering vector). Window, not same-line: a generic type param
# (`useEventStream<Payload>(`) pushes the topic literal onto its own
# line under this repo's formatting (see lib/useSession.ts's
# TopicStreamTruncated subscription, or this mission's own
# UpdatesPanel.vue).
FRONTEND_WINDOW=""
if [[ -d frontend/src ]]; then
  mapfile -t FRONTEND_FILES < <(
    find frontend/src -type f \( -name '*.ts' -o -name '*.vue' \) \
      ! -path '*__tests__*' ! -name '*.spec.ts' ! -name '*.test.ts'
  )
  if [[ ${#FRONTEND_FILES[@]} -gt 0 ]]; then
    # `(<[^>]*>)?` — useEventStream is generic (`useEventStream<Payload>(`);
    # without allowing an optional type-param between the name and the
    # open paren, EVERY typed call site in this codebase is invisible to
    # this pass. Confirmed the hard way: this exact gap was a false-DEAD
    # on UpdatesPanel.vue's own three subscriptions before it was fixed.
    #
    # Exit-code discipline (here and in the Go pass below): grep exit 1
    # means "no matches" — a legitimate empty window. Anything ≥2 means
    # the scan itself failed (unreadable file, killed mid-walk), and a
    # window built from a PARTIAL scan silently declares every
    # subscriber past the failure point dead. That is exactly the flake
    # TestGates_VerdictIsIndependentOfWorkingDirectory kept catching on
    # main/#294/#295: a random innocent topic — clustered by directory —
    # reported unconsumed on one invocation and consumed on the next.
    # A failed scan must be a loud gate error, never a verdict.
    FRONTEND_WINDOW=$(grep -A3 -E '(useEventStream|EventsOn|onServedEvent)(<[^>]*>)?\(' "${FRONTEND_FILES[@]}") || {
      rc=$?
      if [[ $rc -ge 2 ]]; then
        echo "${GATE} ERROR: frontend subscriber scan failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
        exit 1
      fi
    }
  fi
fi

# --- Precompute pass 2 (Go): same idea, over every non-test *.go file
# in the repo (main.go at the root is the primary Go consumer of this
# mission's three update:download-* topics, so the walk starts at "."
# rather than "core").
mapfile -t GO_FILES < <(
  find . \
    \( -path './.git' -o -path '*/node_modules' -o -path './frontend' -o -path '*/.claude' \) -prune -o \
    -name '*.go' -print 2>/dev/null \
    | grep -v '_test\.go' \
    | sort -u
)
GO_WINDOW=""
if [[ ${#GO_FILES[@]} -gt 0 ]]; then
  # Same exit-code discipline as the frontend pass: 1 = no matches
  # (fine), ≥2 = failed scan = loud error, never a partial window.
  GO_WINDOW=$(grep -A3 -E '(EventsOn\(|\.Subscribe\()' "${GO_FILES[@]}") || {
    rc=$?
    if [[ $rc -ge 2 ]]; then
      echo "${GATE} ERROR: Go subscriber scan failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
      exit 1
    fi
  }
fi

# --- Precompute pass 3 (passthroughTopics, served mode): the body of
# wsstream.go's passthroughTopics slice literal.
PASSTHROUGH_BLOCK=""
if [[ -f "$PASSTHROUGH_FILE" ]]; then
  PASSTHROUGH_BLOCK=$(awk '/var passthroughTopics = \[\]string\{/{flag=1} flag{print} /^\}/{if(flag)exit}' "$PASSTHROUGH_FILE")
fi

# --- Precompute pass 4 (allowlist): comment-stripped DATA lines, read
# once. (Same rationale as the per-topic passes below going in-process.)
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

# --- Per-topic checks are IN-PROCESS bash matches ([[ == *…* ]] /
# [[ =~ ]]) against the precomputed windows, not printf|grep pipelines.
# The pipeline form spawned ~4 subprocesses per topic (~160 per run,
# × 16 gates running in parallel under the cwd-independence meta-test);
# any spawn or pipe failure under CI load reads as "no match" and flips
# a live topic to DEAD. An in-process substring check cannot fail — it
# can only be wrong, which is what the planted-violation proof pins.

fail=0
checked=0

for def in "${DEFS[@]}"; do
  file="${def%%:*}"
  rest="${def#*:}"
  line="${rest%%:*}"
  rest="${rest#*:}"
  ident="${rest%%:*}"
  value="${rest#*:}"
  checked=$((checked + 1))

  # --- allowlist: a dated "<value>" DATA line in the allowlist file.
  # Comment lines are stripped first, deliberately. Matching the raw file
  # would mean any prose mentioning a quoted topic — including the
  # header's own `#   "example:topic"` illustration, or a justification
  # paragraph that quotes a NEIGHBOURING topic while explaining this one
  # — silently allowlists it. That is the same "a mention is not a
  # consumer" laundering the frontend pass already guards against by
  # excluding __tests__; the escape hatch has to be as literal as the
  # thing it excuses.
  allow_hit=0
  if [[ -n "$ALLOWLIST_DATA" && "$ALLOWLIST_DATA" == *"\"${value}\""* ]]; then
    allow_hit=1
  fi

  # --- pass 1: frontend subscriber. The literal (single- or
  # double-quoted) appears within the precomputed subscribe-call window.
  frontend_hit=0
  if [[ "$FRONTEND_WINDOW" == *"\"${value}\""* || "$FRONTEND_WINDOW" == *"'${value}'"* ]]; then
    frontend_hit=1
  fi

  # --- pass 2: Go subscriber. Either the literal string, or the const's
  # own identifier (qualified or bare) followed by a non-identifier
  # character, appears within the precomputed EventsOn(/.Subscribe( call
  # window. ([^A-Za-z0-9_]|$) replaces the old grep \> word boundary —
  # bash =~ uses the platform's POSIX ERE, where \> is a non-portable
  # GNU extension.
  go_hit=0
  if [[ "$GO_WINDOW" == *"\"${value}\""* || "$GO_WINDOW" == *"'${value}'"* \
      || "$GO_WINDOW" =~ ${ident}([^A-Za-z0-9_]|$) ]]; then
    go_hit=1
  fi

  # --- pass 3: passthroughTopics (served mode). The const's identifier
  # (bare or package-qualified) appears inside wsstream.go's
  # passthroughTopics slice literal. Per spec §5, self-update's three
  # topics are DELIBERATELY absent here — this pass exists for the
  # topics that legitimately use it (llm:stream-chunk, permission-pending,
  # etc), not to launder self-update's desktop-only topics through.
  passthrough_hit=0
  if [[ -n "$PASSTHROUGH_BLOCK" ]] && [[ "$PASSTHROUGH_BLOCK" =~ ${ident}([^A-Za-z0-9_]|$) ]]; then
    passthrough_hit=1
  fi

  covered=0
  if [[ $allow_hit -eq 1 || $frontend_hit -eq 1 || $go_hit -eq 1 || $passthrough_hit -eq 1 ]]; then
    covered=1
  fi

  if [[ $REPORT_MODE -eq 1 ]]; then
    printf '  %-32s = %-40s frontend=%d go=%d passthrough=%d allowlist=%d -> %s\n' \
      "$ident" "$value" "$frontend_hit" "$go_hit" "$passthrough_hit" "$allow_hit" \
      "$([[ $covered -eq 1 ]] && echo covered || echo DEAD)"
  fi

  if [[ $covered -eq 0 ]]; then
    fail=1
    echo "" >&2
    echo "${GATE} FAIL: ${ident} = \"${value}\" (${file}:${line}) has no frontend subscriber, no Go subscriber, no passthroughTopics entry, and no dated allowlist line." >&2
    echo "  Fix: wire a real subscriber (frontend useEventStream/EventsOn/onServedEvent, or Go wailsruntime.EventsOn/broker.Subscribe), add it to core/serve/wsstream.go's passthroughTopics if served mode needs it, or add a dated line to ${ALLOWLIST}." >&2
  fi
done

if [[ $REPORT_MODE -eq 1 ]]; then
  echo ""
  echo "${GATE} ${checked} Topic* consts checked."
fi

if [[ $fail -ne 0 ]]; then
  echo "" >&2
  echo "${GATE} FAIL — see offending topics above." >&2
  exit 2
fi

echo "${GATE} clean — every Topic* const is consumed or explicitly allowlisted (${checked} checked)."
exit 0
