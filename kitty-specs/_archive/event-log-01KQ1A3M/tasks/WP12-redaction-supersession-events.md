---
work_package_id: "WP12"
title: "Redaction-supersession events with current-visibility default"
dependencies:
  - "WP01"
  - "WP04"
  - "WP06"
  - "WP09"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 11 - Redaction supersession"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Redaction-supersession events with current-visibility default

## Goal

Land FR-012: an operator can apply expanded redaction retroactively *not*
by mutating prior entries (immutability is absolute) but by appending
new `event-log.redaction.applied` events that supersede the visibility
of prior payloads. Replay (WP09) by default applies current visibility:
when iterating a session, superseded payload bytes are replaced by the
superseding placeholder. The `--raw` mode (introduced in WP09) bypasses
this filter for incident response only and is itself logged.

## Spec references

- FR-012 — Redaction-supersession events.
- C-002 / Append-only — corrections are new entries that reference the
  prior; existing rows are never mutated.
- User Story 2 acceptance scenario 2 — operator-invoked redaction
  appends a "redaction-applied" event; the original entry remains.
- User Story 3 — sensitive content redacted before persistence (this
  WP extends the model to retroactive correction).

## Plan references

- §7 v1.x — redaction-supersession events.
- §4.4 Replay path — replay-mode filtering for redaction-supersession;
  default `current visibility`; `--raw` flag for incident response.
- §9 OQ-3 — current visibility default.
- §11 ADR commitments — "Default replay uses current visibility".

## Subtasks

- T001 — Define the supersession event shape: kind
  `event-log.redaction.applied`; payload references the
  superseded `event_id` plus the new placeholder bytes derived from
  the WP04 redaction pipeline; multiple supersessions on the same
  target stack — last-applied wins.
- T002 — Implement an operator-invoked `Redact(eventID, rule)` API
  surface (added to `Emitter` or a separate `Operator` surface — per
  plan §3 via `event-log/` emitter namespace) that runs the WP04
  pipeline against the originally-redacted payload (re-redaction
  cannot recover plaintext) and appends a supersession event.
- T003 — Implement replay filter in WP09's iterator: when in
  `CurrentVisibility` mode, the iterator looks up supersession events
  for each yielded event and substitutes the superseding placeholder
  bytes. `Raw` mode bypasses the filter and yields the original
  recorded bytes verbatim.
- T004 — Filter performance: build an index lookup so per-event
  supersession resolution stays O(1) on the hot path; pre-load the
  supersession map for the session at cursor open.
- T005 — Black-box integration tests: (a) record events with one
  pattern not yet in policy; (b) widen policy and apply
  `Operator.Redact` retroactively; (c) replay default mode yields
  the new placeholder; (d) replay `--raw` mode yields the original
  redacted bytes (which still pass NFR-003 — pre-existing redaction
  was applied at append, just less aggressively); (e) chain
  verification still succeeds end-to-end since no prior row mutated.

## Acceptance criteria

- Supersession events stack correctly (last-applied wins).
- Replay default mode applies current visibility; `--raw` mode yields
  original recorded bytes (with operator authorization + auto-logged
  per WP09).
- No prior event row is mutated; chain verification (WP08) continues to
  succeed end-to-end after retroactive redaction.
- Performance: supersession lookup adds < 1 ms p95 to replay iteration
  (informally measured; not a release gate).
- `go test ./core/event/...` green.

## Files to create / modify

- `core/event/operator.go` (operator surface for `Redact`)
- `core/event/operator_test.go`
- `core/event/replay/iterator.go` (extend with supersession filter)
- `core/event/replay/iterator_test.go` (extend)
- `core/event/redaction_supersession_integration_test.go`

## Definition of done

- All subtasks complete; integration tests green.
- ADR drafted for "default replay uses current visibility" (plan §11)
  if not already landed in WP09.
- `go vet`, `golangci-lint run` clean.
