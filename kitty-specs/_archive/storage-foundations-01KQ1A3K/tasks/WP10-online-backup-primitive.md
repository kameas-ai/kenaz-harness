---
work_package_id: "WP10"
title: "Online backup primitive with sidecar metadata"
dependencies:
  - "WP01"
  - "WP03"
  - "WP05"
  - "WP06"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: implement BackupTaker.Take using libSQL online backup"
  - "T002: stream SHA-256 during copy"
  - "T003: emit sidecar <dst>.meta.json with version + encryption + hash + taken_at"
  - "T004: append harness_backup_artifacts audit row"
  - "T005: integrate with single-writer queue (snapshot-isolation copy)"
  - "T006: tests covering encrypted backup, integrity_check post-take, concurrent writes during take"
phase: "Phase 10 - Online backup"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP10 – Online backup primitive

## Goal

Implement `BackupTaker.Take(ctx, dst)` over libSQL's online backup API. Produce an internally consistent backup while writes happen, accompanied by a sidecar metadata JSON capturing schema version, libSQL version, taken_at, encryption status, and content hash. Append a row to `harness_backup_artifacts` for audit. Encryption posture of the source is preserved.

## Spec references

- FR-009 (online backup)
- C-005 (SOC 2 readiness — backup audit evidence)
- SC-001 (operator backup + restore in under 30 minutes)
- US 5 acceptance scenarios 1-2 (consistent during writes; restorable on fresh machine)

## Plan references

- §3 Public API BackupTaker
- §4.5 Backup Pipeline (online backup, SHA-256, sidecar metadata)
- §5.1 harness_backup_artifacts table
- §8 R10 (online backup snapshot-isolation correctness)

## Subtasks

1. Add `harness_backup_artifacts` table as a storage-owned migration (version 3 in the 1-99 block): `(id PK, path, taken_at, source_schema_version, source_libsql_version, content_hash, encryption_status)`.
2. In `core/storage/backup/backup.go`, implement `BackupTaker` over libSQL's exposed online-backup API. Stream the destination file through a SHA-256 writer; on completion emit `<dst>.meta.json` with the sidecar fields per data-model §BackupArtifact.
3. Append a `harness_backup_artifacts` row in the same write tx that finalizes metadata. Emit `backup_taken` event with payload `{path, taken_at, schema_version, content_hash, encryption_status, duration_ms, bytes_copied}`.
4. Coordinate with the single-writer queue: backup runs as a read-side operation against libSQL's snapshot; writes continue. Document the libSQL primitive used and any progress callback we expose.
5. Tests:
   - Encrypted source -> encrypted backup file (cannot be opened by stock SQLite tools without key).
   - Concurrent writers running while `Take` runs; backup file passes `PRAGMA integrity_check` after restore.
   - Sidecar JSON shape validated.
   - `harness_backup_artifacts` row present and matches sidecar.

## Acceptance criteria

- `Take(ctx, dst)` produces a destination file plus sidecar; both pass content-hash agreement.
- Backup is internally consistent under concurrent writes (verified by integrity check on the destination).
- Encryption posture is preserved (encrypted source -> encrypted destination).
- `harness_backup_artifacts` audit row + `backup_taken` event are emitted.
- No regressions in WP07 vector tests (sqlite-vec virtual tables included in the backup snapshot).

## Files to create/modify

- Create: `core/storage/backup/backup.go`, `backup_test.go`
- Modify: `core/storage/migrations/bootstrap.go` (add `harness_backup_artifacts` migration)
- Modify: `core/storage/storage.go` (`Backup()` accessor on DB)
- Modify: `core/storage/eventkinds.go` (already declared backup_taken in WP05; verify payload contract)

## Definition of done

- Online backup primitive in place, verified against running writers.
- Sidecar + audit row + event are produced for every Take.
- Encryption posture preserved end-to-end.
