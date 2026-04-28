---
work_package_id: "WP09"
title: "Replayer: deterministic byte-stable lazy iterator"
dependencies:
  - "WP01"
  - "WP07"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 8 - Replayer"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP09 – Replayer: deterministic byte-stable lazy iterator

## Goal

Implement `core/event/replay/` — a deterministic, byte-stable, lazy
iterator over a session's events. Replay is one of the harness's three
named first-class features. Default replay applies current visibility
(redaction-supersession-aware once WP11 lands); a `--raw` flag exposes
original visibility for incident response only and is itself logged
(spec OQ-3 default).

## Spec references

- FR-009 — Deterministic replay primitive; byte-identical reproduction.
- NFR-004 — Replay determinism across the test matrix.
- SC-001 — Operator can run a session, replay it, and reproduce the
  original sequence byte-identically.

## Plan references

- §3 Public API — `Replayer.Open`.
- §4.4 Replay path — lazy iterator over `Reader.BySession` with
  replay-mode filtering; ordering by `event_id`; payload yielded as
  the canonicalized form recorded at append time.
- §9 OQ-3 — current visibility default; `--raw` opt-in, gated on
  operator authorization, logged.

## Subtasks

- T001 — Implement `core/event/replay/iterator.go`: lazy session-stream
  iterator backed by `Reader.BySession`; yields events ordered by
  `event_id`; emits the recorded canonical payload bytes verbatim.
- T002 — Implement `ReplayOpts.Mode`: `CurrentVisibility` (default) and
  `Raw`. `Raw` requires an operator-authorization token in the
  context; opening a `Raw` cursor logs an `event-log.replay.raw-opened`
  self-event before yielding the first event.
- T003 — Implement `Replayer` constructor and `Open(sid, opts)` per
  plan §3.
- T004 — Bundle / context / pack version pinning: when replaying,
  surface the recorded version refs from each event verbatim (not the
  current head), per spec User Story 4 acceptance scenario 2. This is
  a passthrough invariant; nothing in the replayer rewrites payload
  fields.
- T005 — Determinism tests: record-replay-rerecord cycle across the
  test matrix; assert byte-equality of the replayed sequence vs the
  original (NFR-004). Concurrent replay test: two replayers on the
  same session yield byte-identical sequences.

## Acceptance criteria

- Record-replay-rerecord cycle produces byte-identical sequences for
  100 % of test sessions in the matrix (NFR-004 / SC-001).
- `Raw` mode requires operator authorization; unauthorized open
  returns `ErrPolicyViolation`; authorized open emits a
  `event-log.replay.raw-opened` self-event (auditable).
- Default `CurrentVisibility` mode is the cursor's behavior absent
  an explicit opt-in.
- `go test ./core/event/...` and `-race` green.

## Files to create / modify

- `core/event/replay/iterator.go`
- `core/event/replay/iterator_test.go`
- `core/event/replayer.go`
- `core/event/replayer_integration_test.go`

## Definition of done

- All subtasks complete; determinism tests + raw-mode auth gate met.
- ADR drafted for "default replay uses current visibility" (plan §11;
  DIRECTIVE_003).
- `go vet`, `golangci-lint run` clean.
