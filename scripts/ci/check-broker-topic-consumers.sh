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
# `ci_require_dir`, not `[[ -d … ]]`. The old guard SKIPPED pass 1 when
# frontend/src was unreachable and carried on to a verdict — so a topic whose
# only subscriber is a .vue file was reported dead, confidently and with a
# file:line. That is how run 32279405838 accused menu:cmd-palette:open, whose
# subscriber sits at frontend/src/App.vue:74. The underlying cause there was a
# harness race (fixed in gates_can_fail_test.go), but the silent skip is what
# converted it into a false finding instead of a legible error. A gate that
# cannot see its inputs must say so, per lib/ci-gate.sh's own doctrine: an
# absent scan root is never "clean".
ci_require_dir "frontend/src" "$GATE" \
  "Pass 1 (frontend subscribers) cannot run without it, and a frontend-only subscriber would be reported dead."

mapfile -t FRONTEND_FILES < <(
  find frontend/src -type f \( -name '*.ts' -o -name '*.vue' \) \
    ! -path '*__tests__*' ! -name '*.spec.ts' ! -name '*.test.ts'
)
if [[ ${#FRONTEND_FILES[@]} -eq 0 ]]; then
  echo "$GATE FAIL: frontend/src has no production .ts/.vue files to scan." >&2
  echo "$GATE Pass 1 would find no subscriber for ANY topic, so every" >&2
  echo "$GATE frontend-only-consumed topic would be reported dead." >&2
  exit 1
fi

# `(<[^>]*>)?` — useEventStream is generic (`useEventStream<Payload>(`);
# without allowing an optional type-param between the name and the
# open paren, EVERY typed call site in this codebase is invisible to
# this pass. Confirmed the hard way: this exact gap was a false-DEAD
# on UpdatesPanel.vue's own three subscriptions before it was fixed.
#
# Exit-code discipline (here and in the Go pass below): grep exit 1
# means "no matches" — a legitimate empty window. Anything >=2 means
# the scan itself failed (unreadable file, killed mid-walk), and a
# window built from a PARTIAL scan silently declares every subscriber
# past the failure point dead. That is exactly the flake
# TestGates_VerdictIsIndependentOfWorkingDirectory kept catching on
# main/#294/#295: a random innocent topic — clustered by directory —
# reported unconsumed on one invocation and consumed on the next.
# A failed scan must be a loud gate error, never a verdict.
#
# MERGE NOTE (2026-08-20): two independent hardenings of this same gate
# landed in parallel and are BOTH kept here. The release branch replaced
# the silent `[[ -d frontend/src ]]` skip with ci_require_dir (absent
# scan root); #296 added the grep exit-code discipline (partial scan).
# They cover different failure modes and neither subsumes the other.
# The pre-merge HEAD form of this line ended `2>/dev/null || true`,
# which swallowed precisely the rc>=2 case the discipline exists to
# catch — that swallow is deliberately NOT carried forward.
FRONTEND_WINDOW=$(grep -A3 -E '(useEventStream|EventsOn|onServedEvent)(<[^>]*>)?\(' "${FRONTEND_FILES[@]}") || {
  rc=$?
  if [[ $rc -ge 2 ]]; then
    echo "${GATE} ERROR: frontend subscriber scan failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
    exit 1
  fi
}
FRONTEND_WINDOW=$(grep -A3 -E '(useEventStream|EventsOn|onServedEvent)(<[^>]*>)?\(' "${FRONTEND_FILES[@]}" 2>/dev/null || true)

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

# --- Precompute pass 2b (Go PUBLISHER): a call matching
# `.Publish<AnySuffix>(` anywhere in non-test Go — e.g. `.Publish(`,
# `.PublishHealthChange(`. This is what "or when a publisher exists"
# (spec.md §1.12 R-6, this file's header below) actually checks: a
# genuinely different call SHAPE from `.Subscribe(`/`EventsOn(`, so it
# carries no self-registration ambiguity — a topic's own declaring
# file publishing it for real is real coverage, unlike that same file
# merely subscribing to its own topic (see the self-exclusion
# G-0 note below pass 2's per-topic check).
GO_PUBLISH_WINDOW=""
if [[ ${#GO_FILES[@]} -gt 0 ]]; then
  GO_PUBLISH_WINDOW=$(grep -A3 -E '\.Publish[A-Za-z]*\(' "${GO_FILES[@]}") || {
    rc=$?
    if [[ $rc -ge 2 ]]; then
      echo "${GATE} ERROR: Go publisher scan failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
      exit 1
    fi
  }
fi

# --- G-0 (spec.md §1.12 R-6, connector-lifecycle-truth-01PMZ303
# UNIT-8): "declaring the topic constant enables
# check-broker-topic-consumers.sh to cover it" was the register's
# stated gate-extension obligation for shipping live MCP health
# (ruling A-2) — and it does not work. Pass 2's `-A3` window is
# satisfied by ANY `.Subscribe(`/`EventsOn(` call that mentions the
# topic, including the exact call that REGISTERS the topic in the
# first place (`core/rpc/views/mcp/impl.go`'s own
# `a.broker.Subscribe(ctx, "mcp", TopicMCPHealthChanged, ch)`) — a
# subscribe-site registration is not a downstream consumer, but
# nothing distinguished it from one. UNIT-0's independent
# reproduction (a scratch `Topic*` const + one `.Subscribe` + no
# publisher) confirmed the gate exits 0 on exactly that shape.
#
# The correction: for each topic, Go-pass (pass 2) evidence from the
# SAME FILE the topic is declared in does not count — only a
# `.Subscribe(`/`EventsOn(` site in a DIFFERENT file, or ANY
# `.Publish*(` site (pass 2b, any file, self or not — see that pass's
# comment for why publishing carries no self-registration ambiguity),
# counts as real coverage. This is coarser than excluding just the
# one offending line (a same-file, genuinely-unrelated second
# `.Subscribe` would also be excluded) — a deliberate, documented
# trade-off for tractability in bash; the concrete violation shape
# this fixes (one file: const + Subscribe, nothing else) is what the
# planted-violation proof below and gates_can_fail_test.go's Go
# counterpart both pin.
#
# GO_WINDOW_EXCLUDING_FILE[f] is precomputed ONCE per DISTINCT
# declaring file among DEFS (typically far fewer than the topic count
# or the total Go file count — many topics share a declaring file),
# not once per topic or once per file in the repo. Each entry costs
# one grep over "every Go file except f", so this pass costs
# O(distinct declaring files) full-ish scans instead of O(1) — still
# nowhere near the O(topics × files) shape this script's header
# documents rejecting.
declare -A GO_WINDOW_EXCLUDING_FILE
if [[ ${#GO_FILES[@]} -gt 0 ]]; then
  for def in "${DEFS[@]}"; do
    declfile="./${def%%:*}"
    if [[ -n "${GO_WINDOW_EXCLUDING_FILE[$declfile]+set}" ]]; then
      continue
    fi
    OTHER_GO_FILES=()
    for gf in "${GO_FILES[@]}"; do
      if [[ "$gf" != "$declfile" ]]; then
        OTHER_GO_FILES+=("$gf")
      fi
    done
    if [[ ${#OTHER_GO_FILES[@]} -gt 0 ]]; then
      w=$(grep -A3 -E '(EventsOn\(|\.Subscribe\()' "${OTHER_GO_FILES[@]}") || {
        rc=$?
        if [[ $rc -ge 2 ]]; then
          echo "${GATE} ERROR: Go subscriber scan (excluding ${declfile}) failed (grep exit ${rc}) — refusing to judge topics against a partial window." >&2
          exit 1
        fi
      }
      GO_WINDOW_EXCLUDING_FILE["$declfile"]="$w"
    else
      GO_WINDOW_EXCLUDING_FILE["$declfile"]=""
    fi
  done
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

  # --- pass 2: Go subscriber, EXCLUDING the topic's own declaring file
  # (G-0, spec.md §1.12 R-6 — see the precompute block above for why).
  # Either the literal string, or the const's own identifier (qualified
  # or bare) followed by a non-identifier character, appears within a
  # `.Subscribe(`/`EventsOn(` window in some OTHER Go file.
  # ([^A-Za-z0-9_]|$) replaces the old grep \> word boundary — bash =~
  # uses the platform's POSIX ERE, where \> is a non-portable GNU
  # extension.
  declfile="./${file}"
  go_window_other="${GO_WINDOW_EXCLUDING_FILE[$declfile]:-$GO_WINDOW}"
  go_hit=0
  if [[ "$go_window_other" == *"\"${value}\""* || "$go_window_other" == *"'${value}'"* \
      || "$go_window_other" =~ ${ident}([^A-Za-z0-9_]|$) ]]; then
    go_hit=1
  fi

  # --- pass 2b: Go PUBLISHER (G-0's "or when a publisher exists" half).
  # A `.Publish*(` call site — ANY file, including the topic's own
  # declaring file, since publishing carries no self-registration
  # ambiguity (see the precompute block's comment) — mentions the
  # topic.
  go_publish_hit=0
  if [[ "$GO_PUBLISH_WINDOW" == *"\"${value}\""* || "$GO_PUBLISH_WINDOW" == *"'${value}'"* \
      || "$GO_PUBLISH_WINDOW" =~ ${ident}([^A-Za-z0-9_]|$) ]]; then
    go_publish_hit=1
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
  if [[ $allow_hit -eq 1 || $frontend_hit -eq 1 || $go_hit -eq 1 || $go_publish_hit -eq 1 || $passthrough_hit -eq 1 ]]; then
    covered=1
  fi

  if [[ $REPORT_MODE -eq 1 ]]; then
    printf '  %-32s = %-40s frontend=%d go=%d go_publish=%d passthrough=%d allowlist=%d -> %s\n' \
      "$ident" "$value" "$frontend_hit" "$go_hit" "$go_publish_hit" "$passthrough_hit" "$allow_hit" \
      "$([[ $covered -eq 1 ]] && echo covered || echo DEAD)"
  fi

  if [[ $covered -eq 0 ]]; then
    fail=1
    echo "" >&2
    echo "${GATE} FAIL: ${ident} = \"${value}\" (${file}:${line}) has no frontend subscriber, no Go subscriber outside its own declaring file, no Go publisher, no passthroughTopics entry, and no dated allowlist line." >&2
    echo "  Fix: wire a real subscriber (frontend useEventStream/EventsOn/onServedEvent, or a Go wailsruntime.EventsOn/broker.Subscribe call in a DIFFERENT file from the const's declaration — G-0, spec.md §1.12 R-6: the declaring file's own Subscribe call is not a consumer), wire a real Go publisher (.Publish*( call anywhere), add it to core/serve/wsstream.go's passthroughTopics if served mode needs it, or add a dated line to ${ALLOWLIST}." >&2
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
