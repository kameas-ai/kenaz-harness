---
work_package_id: "WP12"
title: "Single-writer lockfile enforcement and cross-cutting stress tests"
dependencies:
  - "WP01"
  - "WP03"
  - "WP05"
  - "WP06"
  - "WP07"
  - "WP10"
  - "WP11"
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: implement OS-portable flock/LockFileEx in internal/lockfile"
  - "T002: O_EXCL creation race-handling for first-run init"
  - "T003: lockfile carries PID + start_time; ErrDBLocked surfaces holder identity"
  - "T004: one-hour concurrency stress test (NFR-004)"
  - "T005: end-to-end mission acceptance suite mapping each FR/NFR/SC to a test"
  - "T006: charter no-network-egress assertion (SC-006)"
  - "T007: coverage gate >=80% on core/storage"
phase: "Phase 12 - Single-writer + cross-cutting tests"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP12 – Single-writer lockfile and cross-cutting stress tests

## Goal

Land the single-writer lockfile enforcement (one harness process per DB file) and the cross-cutting test suite that verifies the entire mission against its FRs, NFRs, and Success Criteria. Includes the one-hour concurrency stress test (NFR-004), the charter no-network-egress assertion (SC-006), and a coverage gate of >=80% on `core/storage`.

## Spec references

- FR-011 (single-writer enforcement)
- FR-012 (connection management)
- NFR-004 (concurrency safety — sustained one-hour zero deadlock zero lost write)
- SC-002 (100% concurrent-load runs zero deadlock zero lost write)
- SC-006 (zero outbound network traffic for steady-state read/write)
- C-002 (local-first invariant)
- US 6 acceptance scenarios 1-2 (multi-writer; WAL reader during writes)
- Edge case: two harness processes on same DB

## Plan references

- §4.1 Connection Pool & Single-Writer Enforcement (lockfile + flock/LockFileEx)
- §8 R8 (race between two harness processes on first run)
- §11 Test Plan (entire section)
- §10 Charter Check rows

## Subtasks

1. Implement `core/storage/internal/lockfile/lock.go` with build-tag-split implementations: POSIX `flock` (Linux/macOS) and `LockFileEx` (Windows). Lockfile path: `<DataDir>/data.db.harness-lock`. Write `PID|start_time_ULID` payload on acquire; clear on release.
2. Acquire lock first thing in `core/storage.Open` (before mount detect, before pragma). On failure, parse the existing payload and return `ErrDBLocked` with the holding PID + start time. `O_EXCL` creation handles the first-run race per plan §8 R8.
3. Release on `Close`; also register a `defer` cleanup path for crash scenarios (lockfile becomes stale; on next Open, if the recorded PID is not running, log a warning and re-acquire).
4. Implement the one-hour stress test (`TestConcurrencyStress` under build tag `stress`, gated to nightly CI): N concurrent writers (event-log-shape inserts + scheduler-shape upserts + session-shape updates) plus M readers (point queries + range scans) running for 1 hour against a single DB; assert zero deadlocks (no `ErrBusy` after retries), zero lost writes (commit count == observed count), no panics, `PRAGMA integrity_check` clean at end.
5. Implement `TestNoNetworkEgress` (SC-006): wrap an end-to-end harness run (Open + writes + reads + vector ops + Backup) inside a `net.Dial` interceptor that fails on any outbound socket; assert no dial attempts happen.
6. Implement the FR/NFR/SC traceability matrix as `core/storage/acceptance_test.go`: one table-driven test per spec ID with a comment line referencing the FR/NFR/SC and pointing at the WP that fulfills it. Helps reviewers verify coverage.
7. Wire coverage gate: `go test -coverprofile=cover.out ./core/storage/...` and assert per-package >=80% line coverage in CI (matches plan §11).
8. Edge-case tests: data dir on tmpfs is allowed; data dir on a freshly-mounted fuse.sshfs is refused (synthesized via test helper); two `Open` calls in sequence on the same path with the first not yet `Close`d -> second fails with `ErrDBLocked` carrying the first's PID.

## Acceptance criteria

- Two `Open` calls on the same DB file from the same process (or a child process spawned in tests) produce `ErrDBLocked` for the second with PID/start-time payload.
- One-hour stress test (gated, nightly) passes: zero deadlocks, zero lost writes, integrity_check clean.
- Network-egress test passes: zero outbound dials during a full storage lifecycle.
- Acceptance traceability matrix maps each FR, NFR, and SC to a passing test.
- Coverage gate: `core/storage` >= 80% line coverage.
- All previously-passing WP tests still green.

## Files to create/modify

- Create: `core/storage/internal/lockfile/lock.go`, `lock_unix.go`, `lock_windows.go`, `lock_test.go`
- Modify: `core/storage/db/conn.go` (acquire lock as the first step in Open; release on Close)
- Modify: `core/storage/storage.go` (`ErrDBLocked` populated with holder identity)
- Create: `core/storage/stress_test.go` (build tag `stress`)
- Create: `core/storage/no_network_test.go`
- Create: `core/storage/acceptance_test.go` (FR/NFR/SC traceability)
- Modify: CI configuration to add coverage gate and nightly stress job

## Definition of done

- Single-writer lockfile enforced cross-platform with crash-recovery fallback.
- Mission-level acceptance suite green; FR/NFR/SC traceability documented in test source.
- Concurrency and no-network gates green.
- Coverage >=80% on `core/storage`.
- Mission ready for review and merge to `main` via squash.
