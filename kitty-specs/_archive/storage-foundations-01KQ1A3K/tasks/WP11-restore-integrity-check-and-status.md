---
work_package_id: "WP11"
title: "Restore procedure, integrity check, and status surface"
dependencies:
  - "WP10"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: implement RestoreApplier.Apply with content-hash + libSQL-version + key-resolvability checks"
  - "T002: implement Diagnostics.Verify (PRAGMA integrity_check + libSQL health)"
  - "T003: implement Diagnostics.Status (schema version, applied migrations, file size, page count, encryption state)"
  - "T004: wire RPC surface via existing Wails layer (db.status, db.verify, db.backup.restore)"
  - "T005: tests for missing-key restore, wrong-version restore, tampered-page detection"
phase: "Phase 11 - Restore + integrity + status"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP11 – Restore procedure, integrity check, and status surface

## Goal

Complete the operational surface: restore from a backup with documented validation steps, an integrity-check operation, and a status report exposing schema version, applied migrations, file size, page count, and encryption state. Wire each through the existing Wails RPC layer.

## Spec references

- FR-010 (restore procedure)
- FR-014 (database integrity check)
- FR-015 (schema introspection / status)
- C-005 (SOC 2 readiness — integrity check + restore audit)
- US 5 acceptance scenario 2 (restore on fresh machine)

## Plan references

- §3 Public API Diagnostics interface; RestoreApplier
- §4.5 Backup Pipeline — Restore validates lockfile, content hash, libSQL version, key resolvability
- §6.4 RPC surface (db.status, db.verify, db.backup.restore)
- §8 R10 (restore re-runs integrity_check on destination)

## Subtasks

1. In `core/storage/backup/restore.go`, implement `RestoreApplier.Apply(ctx, src) error`:
   - Verify no harness lock held on destination data dir (`ErrRestoreLockHeld`).
   - Verify SHA-256 of `src` matches sidecar `content_hash`.
   - Verify libSQL release on this host can open the source (version compat).
   - Verify encryption-key reference resolves on this host.
   - Atomically rename into place. Run `PRAGMA integrity_check` on the new file; failure -> revert.
   - Emit `backup_restored` event.
2. In `core/storage/diagnostics/verify.go`, implement `Verify(ctx)` running `PRAGMA integrity_check` + libSQL-specific health probes (foreign-key check, WAL checkpoint), returning `VerifyReport{passed bool, issues []Issue}`. Emit `integrity_check_run`.
3. In `core/storage/diagnostics/status.go`, implement `Status(ctx)` returning `StatusReport`: schema version, applied migrations (from ledger), file size, page count, page size, WAL state, foreign-keys-on, encryption_status, vector backend in use, vector collection list with row counts.
4. Wire RPC layer (Wails app surface): `db.status`, `db.verify`, `db.backup.take`, `db.backup.restore`, `db.encryption.rotate`, `db.encryption.decrypt`, `db.migrate.apply`, `db.migrate.rollback`. Each RPC routes through `core/storage` public API only — Wails layer never imports libSQL.
5. Tests: restore happy-path; missing-key restore -> `ErrEncryptionKeyMissing`; wrong-version restore -> typed compatibility error; tampered-page DB -> Verify reports failure; status report shape validated against a snapshot.

## Acceptance criteria

- Apply restores a backup atomically with all four validation checks; failure leaves destination dir untouched.
- Verify reports a clean DB as healthy and a tampered DB as failed.
- Status returns the documented fields and matches data-model AppDatabase attributes.
- All three RPCs callable from the Wails frontend through the public storage API; no SQLite/libSQL import outside `core/storage`.
- `integrity_check_run`, `backup_restored` events emitted.

## Files to create/modify

- Create: `core/storage/backup/restore.go`, `restore_test.go`
- Create: `core/storage/diagnostics/verify.go`, `verify_test.go`
- Create: `core/storage/diagnostics/status.go`, `status_test.go`
- Modify: `core/storage/storage.go` (Diagnostics accessor)
- Modify: Wails RPC binding files in `core/` or `app.go` equivalent (depending on current scaffold) to expose `db.*` operations
- Modify: `core/storage/eventkinds.go` (verify payload contract)

## Definition of done

- Restore + Verify + Status complete and exposed via RPC.
- Validation chain enforced; failures produce typed errors.
- Audit events emitted for each operation.
