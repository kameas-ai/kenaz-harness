---
work_package_id: "WP07"
title: "Reader query API by session, kind, time, emitter, content"
dependencies:
  - "WP01"
  - "WP03"
  - "WP06"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
phase: "Phase 6 - Reader API"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP07 – Reader query API by session, kind, time, emitter, content

## Goal

Implement the read surface every consumer (and the replayer, verifier,
brancher, retention engine, RPC layer) depends on. Indexed lookups by
session, kind, emitter, time range, and FTS5-backed content search;
forward-only stable cursors; primary-key point-read by event id;
archived-session indication so callers do not get silent empty results
(spec edge case).

## Spec references

- FR-008 — Query API by session, kind, time range, emitter, content.
- NFR-007 — 50 ms p95 against a 10M-event log on a typical
  session-scoped query.
- C-001 — Reader is the only read seam exposed outside `core/event/`.
- C-003 — Local-first; reads against local DB.

## Plan references

- §3 Public API — `Reader` interface.
- §4.2 Read path — composite indexes, forward-only stable cursors,
  point-read.
- §5.1 Indexes — `(session_id, event_id)`, `(kind, event_id)`,
  `(emitter_id, event_id)`, `(emitted_at)`, FTS5.
- §6.4 Frontend / RPC — `Reader` is exposed via RPC; `Emitter` is not.
- Risk R3 — query performance mitigation; benchmark gate on 10M corpus.

## Subtasks

- T001 — Implement `Reader` constructor wired to the unexported
  `log.Store`; expose `BySession`, `ByKind`, `ByEmitter`,
  `ByTimeRange`, `Search`, `Get` per plan §3.
- T002 — Implement forward-only `Cursor`: opens at a starting event id,
  always replays from that point even if later events arrive
  (FR-009 byte-stable foundation). Cursor is single-use, closeable, and
  honors `context.Cancel`.
- T003 — Implement `Search(ContentQuery)` over the FTS5 virtual table
  (redacted payload only); document the query grammar; surface FTS5
  errors as typed `ErrInvalidQuery`.
- T004 — Implement archived-session signal: when a query targets a
  session whose events have been archived (via WP10), the reader
  returns `ErrAlreadyArchived` with the archive reference, *not* an
  empty cursor (spec edge case).
- T005 — Black-box integration + benchmark: synthetic 10M-event corpus;
  assert (a) `BySession` returns events in monotonic ULID order,
  (b) point-read p95, (c) session-scoped range scan p95 < 50 ms
  (NFR-007 measured baseline; gate documented but not blocking
  pre-1.0).

## Acceptance criteria

- All five query patterns return monotonic, stable orderings keyed by
  `event_id`.
- Cursor stability: a cursor opened mid-stream and a fresh cursor at
  the same id yield byte-identical sequences.
- Archived-session signal returned with archive reference; never empty
  cursor.
- Benchmark numbers recorded for NFR-007; regression baseline checked
  in.
- `go test ./core/event/...` green; `go test -race` green for cursor
  cancellation tests.

## Files to create / modify

- `core/event/reader.go`
- `core/event/reader_test.go`
- `core/event/cursor.go`
- `core/event/reader_integration_test.go`
- `core/event/reader_bench_test.go`

## Definition of done

- All subtasks complete; integration tests green; benchmarks recorded.
- `go vet`, `golangci-lint run` clean.
- No raw SQL or `database/sql` types leak above `core/event/log/`.
