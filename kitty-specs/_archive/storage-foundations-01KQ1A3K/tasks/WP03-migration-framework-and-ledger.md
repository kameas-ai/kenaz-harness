---
work_package_id: "WP03"
title: "Migration framework and ledger"
dependencies:
  - "WP01"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: define MigrationRegistry, Migration, LedgerEntry surfaces"
  - "T002: implement transactional apply runner with per-migration tx and rollback-on-failure"
  - "T003: implement explicit Rollback(toVersion) with reverse-order Down execution"
  - "T004: implement ledger DDL bootstrap (harness_meta and harness_migrations tables) as migration v1"
  - "T005: implement startup hash-check and gap-check against ledger"
  - "T006: implement canonicalized SQL hashing (whitespace-collapsed, comment-stripped)"
  - "T007: black-box tests: idempotent re-apply, partial-failure rollback, gap refusal, hash-mismatch refusal"
phase: "Phase 3 - Migration framework"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP03 – Migration framework and ledger

## Goal

Implement the versioned migration framework that every consuming mission registers against. Apply pending migrations in version order inside per-migration transactions; record each apply or rollback in an append-only `harness_migrations` ledger; enforce hash-check and gap-check at startup. Bootstrap `harness_meta` and `harness_migrations` themselves as the storage mission's reserved version-block 1-99.

## Spec references

- FR-003 (migration framework)
- FR-004 (migration audit log)
- NFR-006 (migration safety — failed migrations leave pre-migration state)
- C-004 (append-only event log immutability — applies to ledger conceptually)
- C-005 (SOC 2 readiness — migration audit evidence)
- US 3 acceptance scenarios 1-3 (auto-apply, rollback-on-failure, explicit downgrade)
- SC-003 (100% of failed migrations leave pre-migration state)

## Plan references

- §3 Public API (MigrationRegistry, Migration, LedgerEntry)
- §4.4 Migration Engine (sorted apply, hash check, gap detection, rollback semantics)
- §5.1 Tables Owned (harness_meta, harness_migrations)
- §6.3 Consuming missions register migrations
- §11 Test Plan — Migration framework

## Subtasks

1. In `core/storage/migrations/registry.go`, define `MigrationRegistry` (Register, Applied, Pending, Apply, Rollback), `Migration` struct (ID, Version, OwningMission, Up, Down, ContentHash), `LedgerEntry` struct, and `Action` enum (`applied`, `rolled_back`).
2. In `core/storage/migrations/ledger.go`, declare the storage-owned bootstrap migrations (versions 1-2): create `harness_meta` (single-row install metadata) and `harness_migrations` (ledger). Provide read/write helpers that refuse to skip gaps or overwrite prior entries (append-only).
3. In `core/storage/migrations/runner.go`, implement `Apply(ctx)`: within a write tx, execute the migration's `Up`; on success, write the ledger row and commit; on failure, roll back the tx and surface `ErrMigrationFailed` carrying the migration ID and underlying error.
4. Implement `Rollback(ctx, toVersion)`: load applied migrations whose version > toVersion, run their `Down` in reverse order, each in its own tx, append a `rolled_back` ledger entry per migration. Emit a queued `migration_rolled_back` event per step.
5. Implement startup verification: load ledger; for each applied entry, recompute the canonical content hash of the registered migration source; mismatch -> `ErrLedgerHashMismatch`. Detect gaps in `[min(applied)+1, max(applied)]` -> `ErrSchemaGap`. Detect ledger entries with no matching registered migration -> `ErrSchemaGap` variant.
6. Implement canonicalization for `ContentHash`: strip line/block comments, collapse whitespace, normalize line endings, then SHA-256. Document the rule so consuming missions can compute hashes themselves at registration time.
7. Hook into `core/storage.Open`: register storage-owned migrations during bootstrap, run pending applies, then return the open `DB` once schema is current. Provide `db.Migrations()` accessor for consuming missions to register their own migrations *before* `Open` completes its schema check (ordering documented).
8. Tests: idempotent re-apply (no-op on second `Apply`); partial-failure rollback (mid-Up failure leaves DB unchanged + no ledger row); explicit `Rollback(toVersion)` round-trip; ledger-hash-mismatch refusal; gap refusal; ordering across multiple owning missions.

## Acceptance criteria

- Registering `Migration{Version: N, ...}` and calling `Apply` runs `Up` inside one write tx and produces exactly one ledger row.
- A migration whose `Up` fails leaves the DB byte-identical to its pre-Apply state (verified via SHA-256 of the file with WAL checkpointed).
- `Rollback(toVersion)` runs `Down` for each migration > toVersion in reverse order; each step appends a `rolled_back` ledger entry; the prior `applied` entry is preserved.
- A startup whose ledger references a hash that no longer matches a registered migration's canonical source returns `ErrLedgerHashMismatch` and refuses to open.
- A startup whose ledger has gaps returns `ErrSchemaGap`.
- `harness_meta` is populated on first run with `install_id` (ULID), libSQL/SQLite versions, schema_version, encryption_status (set in WP06), wal_mode, foreign_keys_on.

## Files to create/modify

- Create: `core/storage/migrations/registry.go`, `runner.go`, `ledger.go`, `hash.go`
- Create: `core/storage/migrations/bootstrap.go` (storage-owned migrations 1-2)
- Modify: `core/storage/storage.go` (`Migrations()` accessor; sentinel errors)
- Modify: `core/storage/db/conn.go` (after pragmas, run startup verification + bootstrap)
- Create: `core/storage/migrations/registry_test.go`, `runner_test.go`, `ledger_test.go`

## Definition of done

- Migration framework usable by consuming missions: register, apply, rollback.
- Storage-owned tables `harness_meta` and `harness_migrations` exist after first open.
- Hash + gap checks enforced at startup; both have explicit failing tests.
- Black-box tests pass under `-race`. Pre-migration-state preservation proven by file-level checksum.
