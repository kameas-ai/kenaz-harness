---
work_package_id: "WP03"
title: "Storage migrations and unexported log.Store"
dependencies:
  - "WP01"
  - "WP02"
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
phase: "Phase 2 - Storage substrate"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Storage migrations and unexported log.Store

## Goal

Register the four event-log migrations with the storage-foundations
migration framework and implement the unexported `core/event/log.Store`
adapter that owns the `events`, `event_chain_heads`, `redaction_rules`,
and `retention_config` tables and their indexes plus the FTS5 virtual
table. Store is reachable only via `Emitter` and `Reader` constructors —
not from outside the package (C-001 / C-002).

## Spec references

- FR-007 — Persisted via storage-foundations layer.
- FR-008 — Indexed query API by session, kind, time range, emitter,
  content.
- C-001 — Architectural integrity; only `core/event/` writes to the
  event tables.
- C-002 — Append-only at storage boundary; no mutation paths.
- C-003 — Local-first; steady-state reads/writes against local DB.

## Plan references

- §5.1 `events` table schema and indexes.
- §5.2 `event_chain_heads` table.
- §5.3 `redaction_rules` table.
- §5.4 `retention_config` table.
- §6.1 Integration — register migrations with storage-foundations;
  consume the public connection accessor; no direct libSQL imports.

## Cross-mission dependencies

- **storage-foundations-01KQ1A3K**: this WP depends on the
  storage-foundations migration framework + connection accessor being
  available. Consumes the public package only — no libSQL import here.

## Subtasks

- T001 — Add four ordered migrations under
  `core/event/log/migrations/` (filename convention matches
  storage-foundations): (1) `events` table + composite indexes
  (`PK(event_id)`, `(session_id, event_id)`, `(kind, event_id)`,
  `(emitter_id, event_id)`, `(emitted_at)`) + FTS5 virtual table over
  `payload`; (2) `event_chain_heads`; (3) `redaction_rules`;
  (4) `retention_config`.
- T002 — Implement migration registration entry point invoked by the
  harness bootstrap (call into storage-foundations `MigrationRegistry`).
- T003 — Implement unexported `core/event/log/store.go`: opens the
  storage-foundations connection, exposes append/select/get primitives
  for use by `Emitter` and `Reader` only; struct is unexported.
- T004 — Implement read primitives (`SelectBySession`, `SelectByKind`,
  `SelectByEmitter`, `SelectByTimeRange`, `SearchFTS`, `GetByID`) using
  the documented indexes. No raw SQL leaks above the package boundary.
- T005 — Verify `Store` exposes no `Update`, `Delete`, `Patch` method
  and no method that returns the underlying `*sql.DB` (C-002).
- T006 — Black-box integration test under
  `core/event/log/store_integration_test.go`: bring up a temp on-disk
  storage-foundations DB, run all four migrations, insert sample rows
  through the store's append primitive, assert composite-index lookups
  hit (via `EXPLAIN QUERY PLAN` smoke), assert FTS5 returns expected
  hits.

## Acceptance criteria

- Migrations register cleanly with storage-foundations; `harness migrate`
  (or equivalent test bootstrap) applies all four migrations on a fresh
  DB.
- `go test ./core/event/log/...` passes (real on-disk SQLite under temp
  dir per charter testing standards).
- `Store` is unexported; no symbol from outside `core/event/` references
  the underlying tables (DIRECTIVE_001).
- No direct `database/sql` or libSQL import inside `core/event/`; only
  the storage-foundations exported accessor.
- All four required indexes confirmed present via integration smoke
  using `EXPLAIN QUERY PLAN`.

## Files to create / modify

- `core/event/log/migrations/0001_events.sql`
- `core/event/log/migrations/0002_event_chain_heads.sql`
- `core/event/log/migrations/0003_redaction_rules.sql`
- `core/event/log/migrations/0004_retention_config.sql`
- `core/event/log/migrations/register.go`
- `core/event/log/store.go`
- `core/event/log/query.go`
- `core/event/log/store_integration_test.go`

## Definition of done

- All subtasks complete; integration tests green on an on-disk temp DB.
- Storage-foundations migration framework accepts the four migrations
  with no warnings.
- `go vet` and `golangci-lint run` clean.
- ADR drafted for the per-session chain + global ULID ordering schema
  decision (resolves OQ-1 from plan §9; DIRECTIVE_003).
