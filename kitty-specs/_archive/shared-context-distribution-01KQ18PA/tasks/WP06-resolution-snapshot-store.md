---
work_package_id: "WP06"
title: "Resolution snapshot store backed by storage-foundations"
dependencies:
  - "WP03"
  - "storage-foundations"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
phase: "Phase 6 - Snapshot store"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP06 – Resolution snapshot store

## Goal

Persist every `ResolutionSnapshot` content-addressably in a SQLite-backed store (via the `storage-foundations` substrate) so that replay (FR-018) can return the post-merge result cheaply without re-running the merger and without consulting the network. Raw pack cache (WP09) is best-effort secondary; the snapshot store is the canonical replay primitive.

## Spec references

- FR-005 (Lockfile-pinned versions — snapshot keyed by lockfile hash)
- FR-012 (Session-time audit event references the snapshot id)
- FR-018 (Replay against pinned pack versions)
- SC-005 (Replay 30 days later produces byte-identical resolved context)
- NFR-001 (≤100 ms p95 warm-cache resolution — snapshot lookup must be O(1) on id)

## Plan references

- §4.6 (Resolution snapshot store layout)
- §5.3 (SQLite schema: `context_resolution_snapshot`, `context_resolution_pack`)
- §6 (Storage-foundations integration — new tables under context schema namespace)
- Risk R3 (cache staleness vs replay determinism — snapshot store insulates replay)

## Subtasks

- T001 Define `core/context/cache/snapshot_store.go` against the `storage-foundations` SQLite substrate; create migrations for the two tables in plan §5.3.
- T002 Implement `Put(snapshot)` — content-hash the canonical-JSON body, idempotent insert; record contributing pack rows in `context_resolution_pack`.
- T003 Implement `Get(id) → ResolutionSnapshot` and `GetByLockfileHash(lockfile_hash) → []ResolutionSnapshot` for replay and recent-snapshot lookup.
- T004 Add a conservative GC policy: snapshot rows are retained until lockfile hash is no longer referenced by any session event log entry. Unit + integration tests against a temp on-disk SQLite.

## Acceptance criteria

- Round-trip: Put then Get returns a byte-identical snapshot (validates SC-005 mechanism).
- Lookup by lockfile hash returns all snapshots derived from that lockfile, ordered by `generated_at`.
- Migrations apply cleanly against a fresh storage-foundations DB.
- Tests exercise the real on-disk SQLite (no mocks per charter testing standards) and meet ≥80 % coverage.

## Files to create/modify

- `core/context/cache/snapshot_store.go`
- `core/context/cache/migrations/001_context_resolution.sql`
- `core/context/cache/snapshot_store_test.go`
- `core/context/cache/types.go`

## Definition of done

- Black-box integration test stores and retrieves a snapshot through the real storage-foundations DB.
- No mocking of storage-foundations primitive in test paths.
- WP merged to main via squash-merge PR.
