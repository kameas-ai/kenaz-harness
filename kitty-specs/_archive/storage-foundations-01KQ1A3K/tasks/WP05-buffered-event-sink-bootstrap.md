---
work_package_id: "WP05"
title: "Buffered EventSink and bootstrap-cycle resolution"
dependencies:
  - "WP03"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: define EventSink interface and StorageEvent payload types"
  - "T002: implement BootstrapEventSink ring buffer with overflow policy"
  - "T003: implement SetEventSink swap and ordered drain"
  - "T004: thread queued events through Open path (mount, version-floor, migrations)"
  - "T005: define storage event kinds and payload schemas (plan §5.3)"
  - "T006: tests covering bootstrap-cycle, overflow, ordered drain"
phase: "Phase 5 - Buffered EventSink"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP05 – Buffered EventSink and bootstrap-cycle resolution

## Goal

Resolve the bootstrap cycle between `core/storage` and `core/event` (event-log mission). Storage emits lifecycle events but event-log's own migrations may not have applied yet. Solution per plan §4.6: storage owns an `EventSink` interface, defaults to a `BootstrapEventSink` (in-memory ring buffer), runs migrations (including event-log's once it registers), then exposes `SetEventSink` so the runtime can swap in the real sink and drain buffered events in original order.

## Spec references

- FR-004 (migration audit log via event log)
- C-004 (append-only event log immutability)
- C-005 (SOC 2 readiness — audit evidence preserved across boot)
- StorageEvent entity (data-model §StorageEvent)

## Plan references

- §4.6 Storage-Event Emission — bootstrap order steps 1-4
- §5.3 Storage Event Kinds (db_opened, migration_applied, migration_failed, migration_rolled_back, backup_taken, backup_restored, integrity_check_run, encryption_rotated, non_local_mount_refused, non_local_mount_overridden, vector_collection_opened, vector_reindex_completed)
- §6.2 event-log integration

## Subtasks

1. In `core/storage/storage.go` (or a sub-file), define `EventSink interface { Emit(ctx, kind string, payload map[string]any) error }` and the canonical event-kind constants from plan §5.3.
2. In `core/storage/internal/events/sink.go`, implement `BootstrapEventSink`: bounded ring buffer (default 1024 entries; configurable via Config), records `(timestamp, kind, payload)` tuples, thread-safe, with `Drain() []bufferedEvent` and `Len()`. Document overflow policy (oldest dropped, with a synthetic `events_dropped` entry appended when drain happens).
3. Add `DB.SetEventSink(ctx, sink) error`: under a mutex, swap the active sink to the provided one, drain the bootstrap buffer in insertion order through `sink.Emit`. After successful drain, the bootstrap buffer is released and subsequent emits go straight to the new sink.
4. Thread queued event emissions through the existing Open path: mount refusal/override, SQLite-version refusal, migration_applied/failed/rolled_back. Each call site goes through a single internal `emit(ctx, kind, payload)` that locates the active sink and either buffers or forwards.
5. Define payload-shape contracts per event kind (plan §5.3 partial example for migration_applied: `{version, id, owning_mission, content_hash, duration_ms}`). Stored as a `core/storage/eventkinds.go` file describing each kind plus a small `Validate(kind, payload)` helper used in tests.
6. Tests: emits during pre-event-log boot land in the buffer; SetEventSink replays them in order; payload validators trip on missing required fields; ring-buffer overflow produces an `events_dropped` synthetic entry; concurrent Emit + SetEventSink does not lose events (race-tested).

## Acceptance criteria

- `Open` succeeds before any external event sink is supplied; a subsequent `SetEventSink` flushes all storage events in original order.
- All event kinds in plan §5.3 are defined as exported string constants and have payload contracts.
- Buffer overflow is observable (synthetic `events_dropped` event with count) — not silent loss.
- Tests pass under `-race`.

## Files to create/modify

- Create: `core/storage/eventkinds.go`
- Create: `core/storage/internal/events/sink.go`
- Modify: `core/storage/storage.go` (`EventSink`, `SetEventSink` on the DB interface and its impl)
- Modify: `core/storage/db/conn.go`, `core/storage/db/mount.go`, `core/storage/migrations/runner.go` (route emissions through the internal `emit` helper)
- Create: `core/storage/internal/events/sink_test.go`, `core/storage/eventkinds_test.go`

## Definition of done

- Bootstrap cycle resolved without `core/storage` importing `core/event`.
- Event-kind taxonomy locked; payload schemas tested.
- Drain-on-swap is ordered, lossless under nominal load, and observable when buffer overflows.
- All previous tests still green.
