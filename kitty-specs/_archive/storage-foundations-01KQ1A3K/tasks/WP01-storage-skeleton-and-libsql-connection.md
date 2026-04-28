---
work_package_id: "WP01"
title: "Storage skeleton and libSQL connection management"
dependencies: []
planning_base_branch: "feat/storage-foundations-01KQ1A3K"
merge_target_branch: "main"
branch_strategy: "Planning artifacts were generated on feat/storage-foundations-01KQ1A3K; completed changes must merge back into main via squash merge per charter Branch Strategy."
subtasks:
  - "T001: scaffold core/storage package tree per plan §2"
  - "T002: declare public types, errors, and Open/Close API surface in storage.go"
  - "T003: integrate tursodatabase/go-libsql in go.mod and pin to a release on SQLite >= 3.51"
  - "T004: implement db.Open with WAL, foreign keys, busy_timeout, synchronous=NORMAL pragmas"
  - "T005: implement read connection pool plus single-writer-goroutine queue"
  - "T006: wire Config struct (DataDir, EncryptionKey ref, Mount override, Vector backend, FK/WAL toggles)"
  - "T007: black-box lifecycle tests for Open/Close, pragma assertions, default config defaults"
phase: "Phase 1 - DB connection management"
assignee: ""
agent: ""
shell_pid: ""
history:
  - timestamp: "2026-04-25T00:00:00Z"
    agent: "system"
    action: "Prompt generated via /spec-kitty.tasks"
---

# Work Package Prompt: WP01 – Storage skeleton and libSQL connection management

## Goal

Stand up the `core/storage` package skeleton and the libSQL connection-management foundation that every other WP in this mission builds on. Establish the public `Open`/`Close` API, configuration surface, default pragmas (WAL on, foreign keys on, busy_timeout, synchronous=NORMAL), and the read-pool plus single-writer-queue pattern. Pin a libSQL release that embeds SQLite >= 3.51.0.

## Spec references

- FR-001 (single-file SQLite app database)
- FR-002 (WAL mode by default)
- FR-012 (connection management — single shared pool with read/write contracts)
- FR-016 (foreign-key enforcement default-on)
- FR-017 (configurable data directory)
- NFR-001, NFR-002 (write/read latency budgets — set by connection settings)
- NFR-007 (data directory portability)
- C-001 (storage logic confined to `core/storage/...`)
- C-002 (local-first; zero network egress steady-state)
- SC-006 (zero outbound network traffic for steady-state read/write)

## Plan references

- §2 Architectural Placement (subpackage tree)
- §3 Public API (DB interface, Config, Open entry point)
- §4.1 Connection Pool & Single-Writer Enforcement (pragmas; read pool + single-writer queue)
- §9 Q-A (libSQL release pinning to SQLite >= 3.51)
- §10 Charter Check rows for DIRECTIVE_001, NFR-001/002

## Subtasks

1. Create the directory layout under `core/storage/` per plan §2: `storage.go`, `db/`, `migrations/`, `vector/`, `backup/`, `diagnostics/`, `internal/lockfile/`, `internal/events/`. Empty stubs are acceptable; later WPs fill them.
2. Add `github.com/tursodatabase/go-libsql` to `go.mod`. Choose a tag whose embedded SQLite is >= 3.51.0; record the chosen tag and the assertion strategy (build-time or runtime `sqlite_version()` check) in package docs.
3. Define the public `Config` struct (DataDir, EncryptionKey CredentialReference, AllowNonLocalMount, VectorBackend, ForeignKeys, WAL toggles), the `DB` interface (Reader, WriteTx, Migrations, VectorStore, Backup, Diagnostics, Close, SetEventSink), and the typed sentinel errors listed in plan §3 (initially returned only where natural; later WPs extend coverage).
4. Implement `core/storage/db/conn.go` `Open(ctx, cfg) (*libsql.DB, error)` that opens the libSQL handle and applies pragmas: `journal_mode=WAL`, `foreign_keys=ON`, `synchronous=NORMAL`, `busy_timeout=5000`. Verify each PRAGMA via a follow-up read.
5. Implement read-pool + single-writer-queue pattern in `db/`: read connections served from libSQL's pool; writes enqueued onto a single goroutine that owns the write connection, exposing `WriteTx(ctx, fn)`.
6. Wire `core/storage.Open` to compose `db.Open` plus the placeholder hooks for migrations/vector/backup/diagnostics WPs (return `nil` or unimplemented for now; later WPs fill them).
7. Black-box tests under `core/storage` (no white-box reach into `db/`): open + close round-trip, pragma assertions, default-config sanity, libSQL version assertion, FR-017 override path. Use `t.TempDir()`.

## Acceptance criteria

- `core/storage.Open` returns a `DB` whose `*libsql.DB` runs in WAL mode with foreign keys ON, busy_timeout 5000, synchronous NORMAL — verified via PRAGMA reads in tests.
- `Close` releases the underlying libSQL handle cleanly; subsequent `Open` on the same path succeeds.
- Build fails or `Open` errors with a descriptive sentinel if the embedded SQLite version is below 3.51.
- No package outside `core/storage/...` imports `tursodatabase/go-libsql` (DIRECTIVE_001; greppable invariant).
- Test suite passes under `go test ./core/storage/... -race`.

## Files to create/modify

- Create: `core/storage/storage.go`
- Create: `core/storage/db/conn.go`
- Create: `core/storage/db/wal.go`
- Create: `core/storage/db/doc.go` (package docs noting libSQL pin and SQLite floor)
- Create: empty stubs `core/storage/migrations/{registry,runner,ledger}.go`, `core/storage/vector/vector.go`, `core/storage/backup/{backup,restore}.go`, `core/storage/diagnostics/{verify,status}.go`, `core/storage/internal/lockfile/lock.go`, `core/storage/internal/events/sink.go`
- Create: `core/storage/storage_test.go`, `core/storage/db/conn_test.go`
- Modify: `go.mod`, `go.sum` (add libSQL dependency)

## Definition of done

- Skeleton exists and compiles. Public API surface declared (even if downstream WPs return `ErrNotImplemented`).
- libSQL is pinned to a release embedding SQLite >= 3.51 and the floor is enforced.
- Pragmas verified by tests on macOS/Linux runners (Windows runner enabled in WP02 once mount detection lands).
- All new code lives under `core/storage/...`; no SQLite/libSQL imports leak out.
- WP01 PR can be reviewed and merged independently of any other WP.
