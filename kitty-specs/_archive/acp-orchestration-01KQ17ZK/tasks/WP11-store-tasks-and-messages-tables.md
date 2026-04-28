---
work_package_id: "WP11"
title: "Persistence: acp_tasks and acp_messages tables and store adapter"
dependencies:
  - "WP01"
  - "storage-foundations:WP-migration-framework"
planning_base_branch: "main"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on main; completed changes must merge back into main."
subtasks:
  - "T001"
  - "T002"
  - "T003"
  - "T004"
  - "T005"
  - "T006"
phase: "Phase 11 - Persistence"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Persistence: acp_tasks and acp_messages tables and store adapter

## Goal

Implement `core/acp/store/` — the persistence adapter for `acp_tasks`
and `acp_messages` against the harness app database — and ship a
migration that creates both tables, their indexes, and the
foreign-key relationship. Large message bodies indirect through
`core/blob` to keep the relational tables lean; only blob references
land in `acp_messages.payload_blob_ref`.

State transitions on `acp_tasks` MUST never move backwards
(`completed` / `failed` / `cancelled` are terminal). `acp_messages`
is strictly append-only; corrections are new rows referencing the
prior message id.

## Spec references

- FR-009 — Task lifecycle management; state never moves backwards.
- FR-019 — Replay-friendly Task records (snapshot id pin).
- C-002 — Append-only event log immutability (mirrored to message
  rows here).
- US4 Acceptance Scenario 1, 3 — replay reconstruction; partial
  stream persistence.
- NFR-008 — Audit completeness (every task has full history).
- NFR-009 — Concurrency target ≥ 32 in-flight tasks.

## Plan references

- §5.2 Persistence — table schema, indexes, blob-ref strategy.
- §6.4 storage-foundations integration — migration framework
  registration; transactional surface for writes.
- §4 Internal Layering, "store/" — append-only on messages; state
  transitions on tasks emit paired event with before/after state.

## Subtasks

- T001 — Author the migration file under
  `core/acp/store/migrations/0001_create_acp_tables.sql` (or the
  format the storage-foundations mission accepts). Schema:
  - `acp_tasks(task_id PK, a2a_task_id, local_agent_id,
    remote_peer_id, skill_id, role, state, parent_session_id,
    snapshot_id, created_at, updated_at)`.
  - `acp_messages(message_id PK, task_id FK, direction, kind,
    sequence, payload_blob_ref, emitted_at)`.
  - Indexes: `(parent_session_id, created_at)` on `acp_tasks` for
    replay; `(state)` on `acp_tasks` for in-flight cancellation
    sweeps; `UNIQUE (task_id, sequence)` on `acp_messages`.
- T002 — Register the migration through the storage-foundations
  migration framework so `go run ./cmd/kaneaz migrate up` (or the
  equivalent harness API) applies it.
- T003 — Implement `store.Store` adapter struct with methods:
  `CreateTask(ctx, task)`, `UpdateTaskState(ctx, taskID, from, to)`
  (rejects backwards transitions and terminal-state edits with
  `ErrIllegalStateTransition`), `AppendMessage(ctx, msg)`,
  `GetTask(ctx, taskID)`, `ListMessages(ctx, taskID)`,
  `ListInFlight(ctx)` (used by cancellation sweep + restart
  reconciliation).
- T004 — Wire `payload_blob_ref` through `core/blob`: the store
  writes the (already-redacted by WP12 / event-log pipeline) message
  body to the blob store and persists only the returned reference.
- T005 — Restart-reconciliation helper: `ReloadInFlight(ctx)` is
  called by the harness on startup to surface tasks left in
  `running` / `awaiting_input` after an unclean shutdown
  (spec edge case: laptop closed mid-task).
- T006 — Tests using a real on-disk SQLite instance under
  `t.TempDir()` (no mocks; charter testing standard):
  - Round-trip a task through every legal state transition.
  - Reject every illegal transition (cover the full state-machine
    matrix).
  - 32-concurrent task append test: no lifecycle ordering errors,
    no missing rows, monotonic `sequence` per task (NFR-009).
  - Replay test: `GetTask + ListMessages` reconstructs a complete
    request/response history.

## Acceptance criteria

- `go test ./core/acp/store/...` passes; coverage ≥ 80%.
- The migration applies cleanly on a fresh DB and is idempotent on
  re-run.
- Backwards / illegal state transitions are rejected with a typed
  error in 100% of the matrix tests.
- 32 concurrent task lifecycles complete with no lifecycle ordering
  errors and no missing message sequences (NFR-009).
- Restart reconciliation surfaces a fixture in-flight task left in
  `running` after a simulated crash.

## Files to create / modify

- `core/acp/store/store.go`
- `core/acp/store/state_machine.go` — transition validator.
- `core/acp/store/store_test.go`
- `core/acp/store/migrations/0001_create_acp_tables.sql`
- `core/acp/store/migrations/register.go` — registration with the
  storage-foundations migration framework.

## Definition of done

- All subtasks complete; tests green; lint clean.
- Cross-mission dependency on `storage-foundations` migration
  framework documented; migration registration verified via the
  framework's test harness.
- Cross-mission dependency on `core/blob` documented.
- PR merged.
