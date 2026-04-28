---
work_package_id: "WP02"
title: "Mount detection and SQLite version floor"
dependencies:
  - "WP01"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: implement mount_darwin.go using getmntinfo and MNT_LOCAL"
  - "T002: implement mount_linux.go parsing /proc/self/mountinfo"
  - "T003: implement mount_windows.go using GetDriveType"
  - "T004: implement cloud-sync path heuristics (iCloud/Dropbox/OneDrive/Google Drive)"
  - "T005: enforce SQLite version floor (>=3.51.0) at Open with ErrSQLiteVersionTooOld"
  - "T006: wire AllowNonLocalMount override path with audit-event hook"
  - "T007: cross-platform tests including synthetic NFS/SMB and cloud-sync-path fixtures"
phase: "Phase 2 - Mount detection and version floor"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP02 – Mount detection and SQLite version floor

## Goal

Refuse to open the database on non-local mounts (NFS, SMB, CIFS, cloud-sync roots) by default, with an explicit operator override that is auditable. Enforce SQLite version floor of 3.51.0 to dodge the Windows NTFS WAL `close()` lock bug. Both checks run inside `Open` before any pragma or migration logic.

## Spec references

- FR-001 (single-file SQLite app database)
- FR-002 (WAL mode by default; correctness depends on local FS)
- NFR-004 (concurrency safety)
- NFR-007 (data directory portability)
- C-002 (local-first invariant)
- C-005 (SOC 2 readiness — refusals/overrides are audit evidence)
- Edge Cases: data dir on NFS/SMB; iCloud / Dropbox / OneDrive / Google Drive sync roots
- Assumptions: SQLite >= 3.51 floor

## Plan references

- §4.3 Mount Detection (refuse non-local mounts) — OS detection table
- §8 Risk Register R4 (Windows NTFS WAL close lock bug pre-3.51), R5 (NFS/SMB detection)
- §9 Q-B (cloud-sync deny-list vs allow-list — default deny-list)
- Research D5, D6, D8

## Subtasks

1. Add `core/storage/db/mount.go` (build-tag-split): `mount_darwin.go` using `getmntinfo(3)` and `MNT_LOCAL`; `mount_linux.go` parsing `/proc/self/mountinfo` rejecting `nfs*`, `cifs`, `smb*`, `fuse.sshfs`; `mount_windows.go` using `GetDriveType` rejecting `DRIVE_REMOTE`.
2. Add cloud-sync path heuristic table for each OS (macOS: `~/Library/Mobile Documents/...`, `~/Library/CloudStorage/Dropbox-*`, `~/Dropbox`, `OneDrive`, `Google Drive`; Linux: matching `~/Dropbox`, `~/OneDrive`, `~/Insync`, `~/Google Drive`; Windows: registry-based OneDrive/Dropbox/iCloudDrive sync roots). Default deny-list with comment-documented entries.
3. Integrate `mount.Check(path)` into `db.Open`: refusal returns `ErrNonLocalMount` with the matched filesystem/path-heuristic identified; override (`cfg.AllowNonLocalMount=true`) bypasses with a structured warning surfaced to the caller.
4. Enforce SQLite version floor at `Open` by querying `sqlite_version()` after the handle opens and before pragma application. Below 3.51.0 -> `ErrSQLiteVersionTooOld` with the detected version.
5. Provide deferred event-emission hooks (no event-log dependency yet — wired through buffered sink in WP05): refusal -> queue `non_local_mount_refused`; override -> queue `non_local_mount_overridden`.
6. Tests: synthetic mount fixtures via test helpers (Linux: bind-mount, fake mountinfo; macOS/Windows: stubbed syscall via injected detector seam). Path-heuristic tests for each cloud-sync root. SQLite-version test by injecting a version-stub.

## Acceptance criteria

- `Open` on a path under `/mnt/nfs` (Linux), `/Volumes/<smb>` (macOS), or a `DRIVE_REMOTE` Windows path returns `ErrNonLocalMount` by default.
- `Open` on a path under `~/Library/Mobile Documents/...`, `~/Dropbox`, `~/OneDrive`, or `~/Google Drive` is refused with the cloud-sync-specific message.
- Setting `cfg.AllowNonLocalMount=true` permits open and queues a `non_local_mount_overridden` event for later flush.
- `Open` against a SQLite version below 3.51 returns `ErrSQLiteVersionTooOld` carrying the detected version.
- Tests pass on macOS, Linux, and Windows in CI.

## Files to create/modify

- Create: `core/storage/db/mount.go` (shared definitions: `MountKind`, detector interface, deny-list for cloud-sync)
- Create: `core/storage/db/mount_darwin.go`, `mount_linux.go`, `mount_windows.go`
- Create: `core/storage/db/version.go` (SQLite version floor check)
- Modify: `core/storage/db/conn.go` (call mount.Check + version.Check before pragmas)
- Modify: `core/storage/storage.go` (`ErrNonLocalMount`, `ErrSQLiteVersionTooOld` sentinels)
- Create: `core/storage/db/mount_test.go`, `core/storage/db/version_test.go`

## Definition of done

- Per-OS detection logic in place behind build tags; CI green on all three.
- Cloud-sync deny-list documented in source and easy to extend.
- Override path emits the expected (queued) audit event.
- SQLite version floor enforced at Open; tests cover both pass and fail paths.
