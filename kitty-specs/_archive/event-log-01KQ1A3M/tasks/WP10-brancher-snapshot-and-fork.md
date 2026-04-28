---
work_package_id: "WP10"
title: "Brancher: snapshot computation and child-session fork"
dependencies:
  - "WP01"
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
phase: "Phase 9 - Brancher"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Brancher: snapshot computation and child-session fork

## Goal

Implement `core/event/branch/` — given a parent session and an event id
E, compute the Replay Snapshot up to E (the accumulated state the
parent had at that moment), create a new child session id, and seed the
child's chain with a single `event-log.session.branched` event
referencing `(parent_session_id, parent_event_id)`. Subsequent writes
on the child proceed as a normal session. Original is unaffected.

## Spec references

- FR-010 — Branch primitive.
- SC-002 — Operator can branch at any event id; resulting branch has
  the same accumulated context as the parent at that point.
- User Story 5 — Branch is a first-class feature.

## Plan references

- §3 Public API — `Brancher.Branch`.
- §4.5 Branch path — five-step procedure (resolve parent, snapshot,
  create child id, append seed event, original-read-only invariant).
- §2 (`branch/`) — `snapshot.go`, `fork.go`.
- Risk R5 — branch state reconstruction cost; cache snapshot per branch.
- Risk R8 — branch ancestor protection in retention pre-flight.

## Subtasks

- T001 — Implement `core/event/branch/snapshot.go`: compute Replay
  Snapshot up to E by replaying the parent stream via WP09's iterator;
  result is a byte-stable representation of accumulated state at E.
- T002 — Implement `core/event/branch/fork.go`: assigns new ULID for
  the child session; appends a single `event-log.session.branched`
  event via the WP06 emitter referencing
  `(parent_session_id, parent_event_id)` plus the snapshot reference
  hash; returns the child session id.
- T003 — Implement `Brancher` constructor and `Branch(parent, atEvent)`
  per plan §3. Surfaces `ErrSessionNotFound` for unknown parent;
  `ErrInvalidArgument` for `atEvent` outside parent's stream.
- T004 — Snapshot caching: cache the computed snapshot keyed by
  `(parent, atEvent)` for reuse across multiple branches at the same
  point (Risk R5). Cache invalidation: snapshots are immutable since
  parent is append-only; entries can be evicted by LRU.
- T005 — Black-box integration tests: branch from a recorded session
  midstream; assert (a) child session has the same accumulated context
  as parent at E, (b) writes to the child do not appear in the parent's
  stream, (c) original parent replay is unaffected, (d) verifying the
  child chain succeeds end-to-end.

## Acceptance criteria

- Integration tests cover all four assertions in T005.
- Branching from event id E twice yields two independent child
  sessions; both have identical initial state.
- Branch on a non-existent session returns `ErrSessionNotFound`; branch
  on an out-of-range event id returns the typed argument error.
- Snapshot cache hits avoid recomputation under realistic load.
- `go test ./core/event/...` and `-race` green.

## Files to create / modify

- `core/event/branch/snapshot.go`
- `core/event/branch/snapshot_test.go`
- `core/event/branch/fork.go`
- `core/event/brancher.go`
- `core/event/brancher_integration_test.go`

## Definition of done

- All subtasks complete; integration tests green.
- `go vet`, `golangci-lint run` clean.
- ADR optional — if snapshot caching strategy diverges from a vanilla
  recompute-each-time baseline, document via ADR per DIRECTIVE_003.
