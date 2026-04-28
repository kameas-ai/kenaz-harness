# Implementation Plan — Storage Foundations

**Mission**: `storage-foundations-01KQ1A3K`
**Branch**: `feat/storage-foundations-01KQ1A3K` (target: `main`)
**Status**: Draft
**Spec**: `kitty-specs/storage-foundations-01KQ1A3K/spec.md`
**Research**: `kitty-specs/storage-foundations-01KQ1A3K/research.md`
**Data Model**: `kitty-specs/storage-foundations-01KQ1A3K/data-model.md`

> **Branch Contract**
> - Planning base branch: `feat/storage-foundations-01KQ1A3K` off `main`.
> - Final merge target: `main` (squash-merge; charter Branch Strategy).
> - This planning document and downstream WPs land on the same `feat/`
>   branch via PR(s) referencing this mission.

---

## 1. Overview

This mission ships the **persistent substrate** every other harness mission
will sit on top of. Concretely:

- One **embedded libSQL database file** per harness install, holding all
  relational state (sessions, tasks, event-log entries, scheduler jobs, MCP
  registry, lockfile resolution snapshots, signed-cards trust state, A2A
  task records, context-pack cache, etc.). Single file. Single process.
  WAL on. Foreign keys on.
- One **vector store** keyed off the same file by default (`sqlite-vec`
  extension), with an interface so memory/RAG and any future consumer can
  swap to LanceDB (>500k vectors) or `chromem-go` (pure-Go fallback) without
  touching consumers.
- A **migration framework** every consuming mission registers its DDL with;
  forward-only by default, rollback explicit, ledger append-only, hash-checked
  against declared source.
- **Encryption-at-rest** via libSQL's page-level encryption, key referenced
  from the `secrets-keychain` mission, opt-in but recommended-on for new
  installs.
- **Online backup / restore** via libSQL's online backup primitive.
- **Single-writer enforcement** (one harness process per DB file) and
  **mount-kind detection** (refuse NFS/SMB/CIFS/cloud-sync paths by default).
- An **integrity-check** operation and a **status surface** that reports
  schema version, applied migrations, file size, page count, and encryption
  state.

**Out of scope (explicitly):**
- Feature schemas — every consuming mission ships its own migration + table
  set against the framework defined here.
- Cloud-sync (libSQL embedded replicas) — additive future mission.
- Retention policies for backups, event-log archival — owned by the scheduler
  mission and event-log mission respectively.
- Multi-tenant data dirs / per-tenant DBs — flagged for v2.

Success means: the next mission to land (event-log) can register its
migrations, append events through the public DB API, and ride the same
backup/encryption/integrity guarantees without writing any new storage code.

---

## 2. Architectural Placement

DIRECTIVE_001 demands clear, replaceable boundaries. All storage code lives
under `core/storage/` and is the **only** code in `core/` allowed to import
SQLite, libSQL, sqlite-vec, LanceDB, or chromem SDKs. Other `core/` packages
consume the public Go interfaces in `core/storage` only.

```
core/storage/
├── storage.go              # public types & errors; entry point for openers
├── db/                     # libSQL connection management
│   ├── conn.go             # Open, Close, single-writer enforcement
│   ├── wal.go              # WAL configuration, foreign-key pragma
│   ├── encryption.go       # libSQL page-level encryption hookup
│   └── mount.go            # OS-specific local-mount detection (build-tag split)
├── migrations/             # versioned migration framework + ledger
│   ├── registry.go         # Registry, Migration, MigrationLedger surfaces
│   ├── runner.go           # apply/rollback engine, transactional safety
│   └── ledger.go           # MigrationLedger table DDL + read/write helpers
├── vector/                 # vector-store abstraction
│   ├── vector.go           # VectorStore interface, Collection types
│   ├── sqlitevec/          # default backend (extension into AppDatabase)
│   ├── lancedb/            # opt-in (build tag: harness_lancedb)
│   └── chromem/            # opt-in pure-Go (build tag: harness_chromem)
├── backup/                 # online backup + restore
│   ├── backup.go           # BackupTaker
│   └── restore.go          # RestoreApplier
├── diagnostics/            # integrity check, status surface
│   ├── verify.go           # PRAGMA integrity_check + libSQL health
│   └── status.go           # surfaceable status struct
└── internal/               # implementation-only helpers (not exported)
    ├── lockfile/           # PID/instance lock for single-writer enforcement
    └── events/             # adapter that emits to event-log without circular dep
```

**Rationale:**
- Subpackages map to discrete responsibilities (DDD bounded contexts inside
  the storage context).
- Vector-backend subpackages can be added/removed without touching anyone else
  (SC-005, C-006).
- `internal/` keeps cross-cutting helpers private; nothing outside `core/storage`
  can import them.

---

## 3. Public API

The public surface other `core/` packages consume. Illustrative Go only —
exact signatures finalized during WP implementation.

```go
// core/storage/storage.go

// DB is the harness's app-database handle: a libSQL connection plus
// migration / vector / backup / diagnostics surfaces. One instance per process.
type DB interface {
    // Read-only point queries and reports.
    Reader() Reader
    // Write transactions — serialized; FRs require single-writer semantics.
    WriteTx(ctx context.Context, fn func(tx WriteTx) error) error

    // Migrations — registered by consuming missions at boot.
    Migrations() MigrationRegistry

    // Vector-store handle (default backend: sqlite-vec on this DB file).
    VectorStore() VectorStore

    // Operational primitives.
    Backup() BackupTaker
    Diagnostics() Diagnostics

    // Lifecycle.
    Close(ctx context.Context) error
}

// Open is the single entry point. Returns DB or a typed error.
// cfg carries: data dir, encryption-key reference, mount-override flag,
// vector-backend selection, foreign-key + WAL toggles (defaulted on).
func Open(ctx context.Context, cfg Config) (DB, error)
```

```go
// core/storage/migrations/registry.go

// MigrationRegistry collects migrations from every consuming mission.
// Missions register at process boot, before Open completes the schema check.
type MigrationRegistry interface {
    Register(m Migration) error           // add a migration declaration
    Applied() ([]LedgerEntry, error)      // current ledger view
    Pending() ([]Migration, error)        // not-yet-applied migrations in order
    Apply(ctx context.Context) error      // apply pending in version order
    Rollback(ctx context.Context, toVersion int) error // explicit downgrade
}

type Migration struct {
    ID            string                       // "<mission>/<NNN>-<short-desc>"
    Version       int                          // global ordinal
    OwningMission string                       // "event-log", "scheduler", ...
    Up            func(ctx context.Context, tx WriteTx) error
    Down          func(ctx context.Context, tx WriteTx) error
    ContentHash   string                       // hash of Up source for tamper check
}
```

```go
// core/storage/vector/vector.go

type VectorStore interface {
    OpenCollection(ctx context.Context, spec CollectionSpec) (Collection, error)
    Backends() []BackendKind   // ["sqlite-vec"] by default; +lancedb / chromem if compiled
}

type Collection interface {
    Insert(ctx context.Context, items []Embedding) error
    Search(ctx context.Context, q QueryVector, k int, filter Filter) ([]Match, error)
    Delete(ctx context.Context, ids []string) error
    Reindex(ctx context.Context) error  // rebuild from authoritative embeddings
    Stats(ctx context.Context) (CollectionStats, error)
    Close() error
}

type CollectionSpec struct {
    Name         string
    Dimension    int
    Metric       Metric            // cosine | l2 | dot
    Quantization Quantization      // none | binary_int8 (sqlite-vec)
    Backend      BackendKind       // optional override; default = configured default
}
```

```go
// core/storage/backup/backup.go

type BackupTaker interface {
    // Take a consistent online backup while writes are happening.
    // Backup file inherits the source's encryption posture.
    Take(ctx context.Context, dst string) (Artifact, error)
}

type RestoreApplier interface {
    // Restore must run with the harness stopped (validated via lockfile).
    // Verifies content hash, libSQL release floor, and encryption-key match.
    Apply(ctx context.Context, src string) error
}
```

```go
// core/storage/diagnostics/status.go

type Diagnostics interface {
    Verify(ctx context.Context) (VerifyReport, error)   // PRAGMA integrity_check
    Status(ctx context.Context) (StatusReport, error)   // schema ver, sizes, encryption
}
```

**Error taxonomy (typed sentinels):**
`ErrDBLocked` (single-writer violation), `ErrNonLocalMount`,
`ErrEncryptionKeyMissing`, `ErrEncryptionKeyMismatch`, `ErrSchemaGap`,
`ErrMigrationFailed`, `ErrLedgerHashMismatch`, `ErrIntegrityCheckFailed`,
`ErrBackupInProgress`, `ErrRestoreLockHeld`.

---

## 4. Internal Layering

### 4.1 Connection Pool & Single-Writer Enforcement

- One `*libsql.DB` per process. Read connections are pooled; write
  connections funnel through a single goroutine + queue (SQLite/libSQL is
  single-writer under WAL anyway, but routing all writes through one queue
  prevents lock contention from competing internal goroutines).
- Process-level lock: a sidecar lock file `data.db.harness-lock` carrying
  PID + start time. On `Open`, attempt `flock`/`LockFileEx`; failure →
  `ErrDBLocked` with the holding PID identified (FR-011).
- Connection settings applied at open: `PRAGMA journal_mode=WAL`,
  `PRAGMA foreign_keys=ON`, `PRAGMA synchronous=NORMAL`,
  `PRAGMA busy_timeout=5000` (FR-002, FR-016).

### 4.2 Encryption Hookup

- libSQL exposes a `WithEncryption(key []byte, cipher Cipher)` option on its
  Go SDK. Key bytes are obtained at `Open` time from `core/secrets`'s
  `Backend.Resolve(ref)` against a `CredentialReference` carried in the
  storage config — never inline.
- After resolution, the byte slice is passed to libSQL and **zeroed via
  `subtle.ConstantTimeCompare` + explicit overwrite** as soon as the
  connection is open (matches `secrets-keychain` FR-013).
- Re-key (`harness db encryption rotate`) drains all connections, runs
  libSQL's re-key, emits an `encryption_rotated` storage event.
- Opt-out (`encryption_status = disabled_with_disk_encryption`) is recorded
  in DB metadata and emits a `db_opened` event with the explicit choice
  payload, so the operator's decision is auditable (FR-008, US 4).

### 4.3 Mount Detection (refuse non-local mounts)

OS-specific source files behind build tags:

| OS | Detection |
|---|---|
| `mount_darwin.go` | `getmntinfo(3)` → `MNT_LOCAL` flag; reject NFS/SMB/AFP. Detect iCloud (`/Users/.../Library/Mobile Documents/...`), Dropbox (`~/Dropbox`, `~/Library/CloudStorage/Dropbox-*`), OneDrive, Google Drive via path heuristics. |
| `mount_linux.go` | parse `/proc/self/mountinfo` for the path's filesystem; reject `nfs*`, `cifs`, `smb*`, `fuse.sshfs`. Detect Dropbox/OneDrive/Google Drive via path heuristics. |
| `mount_windows.go` | `GetDriveType` for the volume; reject `DRIVE_REMOTE`. Detect OneDrive/Dropbox/iCloudDrive sync roots via path heuristics. |

Override flag: `cfg.AllowNonLocalMount = true` (defaults false) emits a
`non_local_mount_overridden` event so operator override is in the audit
trail. Refusal emits `non_local_mount_refused`.

### 4.4 Migration Engine

- Sorted apply: pending migrations applied in `Version` ascending order
  inside a transaction per migration. Failure → rollback that one
  transaction, ledger entry not written, harness exits with structured error
  (NFR-006, FR-003, US 3.2).
- Hash check: at startup, every previously-applied migration's `ContentHash`
  is compared to the registered source. Mismatch → `ErrLedgerHashMismatch`,
  refusal to start (data-model.md "Lifecycle Notes").
- Gaps: missing version between `min(applied)+1` and `max(applied)` →
  `ErrSchemaGap`, refusal to start. Prevents accidental downgrade or
  half-merged feature branches.
- Rollback: explicit operator action (`harness db migrate rollback
  --to <version>`); calls `Down` for each migration above target version, in
  reverse order, each in its own transaction; emits a `migration_rolled_back`
  event per migration. Ledger appends a rollback record (does not delete the
  prior apply record — append-only).

### 4.5 Backup Pipeline

- libSQL exposes the SQLite online backup C API via Go bindings. `BackupTaker`
  wraps it with progress reporting, content-hash computation (SHA-256
  streamed during copy), and metadata sidecar (`<dst>.meta.json` carrying
  source schema version, libSQL version, taken_at, encryption status).
- Restore validates that:
  1. No harness lock is held on the destination data dir (`ErrRestoreLockHeld`).
  2. The content hash of the source matches the sidecar.
  3. The libSQL release on the restoring host can open the source (libSQL
     version compatibility check).
  4. Encryption-key reference resolves on this host.
  Then atomically renames into place. Emits `backup_restored`.

### 4.6 Storage-Event Emission (event-log integration)

Storage emits typed events. To avoid a hard cycle (`core/event` will depend
on `core/storage` to persist its tables), the integration goes through an
**outbound event sink interface** the storage package owns:

```go
// core/storage/storage.go (abbrev.)
type EventSink interface {
    Emit(ctx context.Context, kind string, payload map[string]any) error
}
type Config struct {
    // ...
    EventSink EventSink   // optional; nil during bootstrap before event-log is up
}
```

Bootstrap order:
1. `Open` runs with a buffered `BootstrapEventSink` (in-memory ring buffer).
2. Migrations run, including `event-log`'s own migrations.
3. Caller wires the real `core/event` sink and calls `db.SetEventSink(...)`.
4. Buffered events are flushed to the real sink in original order.

This keeps the storage package free of `core/event` imports (DIRECTIVE_001).

---

## 5. Data Model Recap

(Full detail in `data-model.md`. This section pins the table-naming convention
and the storage-event vocabulary so consuming missions plan against a stable
contract.)

### 5.1 Tables Owned By This Mission

All in the same libSQL file. Prefix `harness_` for storage-foundation tables,
to disambiguate from feature-mission tables.

- `harness_meta` — single-row install metadata: `install_id`, `created_at`,
  `libsql_version`, `sqlite_version`, `schema_version`, `encryption_status`,
  `wal_mode`, `foreign_keys_on`.
- `harness_migrations` — the ledger. `(version PK, id, applied_at,
  content_hash, owning_mission, action ENUM(applied|rolled_back),
  rolled_back_from_version NULLABLE)`.
- `harness_backup_artifacts` — sidecar audit row per `Take`: path, taken_at,
  source_schema_version, content_hash, encryption_status. (Backup files
  themselves are *outside* the DB, naturally.)

### 5.2 Table-Prefix Convention For Consuming Missions

| Mission | Prefix |
|---|---|
| event-log | `event_` |
| scheduler | `sched_` |
| sessions | `session_` |
| MCP registry | `mcp_` |
| A2A tasks | `a2a_` |
| signed cards trust | `trust_` |
| shared context distribution | `ctx_` |
| bundle resolver cache | `bundle_` |
| memory/RAG | `memory_` (relational); `vec_memory_*` (sqlite-vec virtual tables) |

This is **declarative guidance** in the migration registry's documentation,
not enforced by code — the registry doesn't care, but consistent prefixes
keep `harness db status` readable.

### 5.3 Storage Event Kinds (consumed by event-log)

`db_opened`, `migration_applied`, `migration_failed`, `migration_rolled_back`,
`backup_taken`, `backup_restored`, `integrity_check_run`,
`encryption_rotated`, `non_local_mount_refused`, `non_local_mount_overridden`,
`vector_collection_opened`, `vector_reindex_completed`.

Each carries a small redacted payload — e.g., `migration_applied` carries
`{version, id, owning_mission, content_hash, duration_ms}`. Never the encryption
key, never resolved file contents.

---

## 6. Integration Points

### 6.1 secrets-keychain (encryption-key resolution)

- Storage's `Config.EncryptionKey` is a `secrets.CredentialReference` (the
  shape defined in `secrets-keychain` FR-001), e.g. `{ keychain:
  "harness-db-key" }`. The reference is what lives in operator config; the
  bytes never appear there.
- `Open` resolves via `secretsBackend.Resolve(ref)` once, passes to libSQL,
  zeros the slice. Re-key drains, re-resolves, applies the new key, then
  zeros.
- A `secrets-keychain` pre-flight pass (the one `secrets-keychain` FR-009
  defines) validates the reference exists *before* `storage.Open` is called.
  Ordering at boot: secrets pre-flight → storage open → migrations →
  event-log open → other consumers.

### 6.2 event-log (storage-event sink)

Per §4.6: bootstrap uses an in-memory buffer; once `event-log` finishes its
own migrations, the runtime swaps in the real sink. Every storage-foundation
operation emits one event (see §5.3). The event-log mission can implement
its replay/branch primitives over these events without storage knowing.

### 6.3 Consuming missions register migrations

Each mission ships a package-level `func RegisterMigrations(reg
storage.MigrationRegistry) error`. The runtime composition root calls these
in a deterministic order at boot (alphabetical by `OwningMission` then by
`Version`). The mission owns the source of truth for its DDL; storage owns
applying it.

Example skeleton (illustrative):

```go
// core/event/migrations.go
package event

import "github.com/sigil-tech/kaneaz-harness/core/storage"

func RegisterMigrations(reg storage.MigrationRegistry) error {
    return reg.Register(storage.Migration{
        ID:            "event-log/0001-init",
        Version:       100, // event-log reserves the 100-block
        OwningMission: "event-log",
        Up:            func(ctx context.Context, tx storage.WriteTx) error { /* DDL */ },
        Down:          func(ctx context.Context, tx storage.WriteTx) error { /* drop */ },
        ContentHash:   "sha256:...", // computed at registration time
    })
}
```

**Version-block reservation** (so missions don't collide):

| Range | Mission |
|---|---|
| 1-99 | `storage` (this mission) |
| 100-199 | `event-log` |
| 200-299 | `secrets-keychain` (audit tables) |
| 300-399 | `session` |
| 400-499 | `scheduler` |
| 500-599 | `mcp` |
| 600-699 | `a2a` + `signed-cards-trust` |
| 700-799 | `bundle` + `shared-context-distribution` |
| 800-899 | `memory-rag` |
| 900+ | reserved for future / app-layer |

This is an operator-facing invariant — written into the planning artifact
each mission inherits.

### 6.4 RPC surface (Wails app)

Operations exposed to the frontend / CLI behind the existing RPC layer:
- `db.status` → `Diagnostics.Status`
- `db.verify` → `Diagnostics.Verify`
- `db.migrate.apply` / `db.migrate.rollback`
- `db.backup.take` / `db.backup.restore`
- `db.encryption.rotate`
- `db.encryption.decrypt` (forensic / migration use; opens an encrypted
  DB and writes a plaintext copy under operator confirmation)

All RPCs route through the public `core/storage` API. The Wails layer never
imports libSQL directly (charter Deployment Constraints; DIRECTIVE_001).

---

## 7. Phasing

### v1.0 — ships with this mission
- libSQL connection management, WAL, foreign keys.
- Single-writer lockfile enforcement.
- Mount-kind detection on macOS / Linux / Windows.
- Migration framework with ledger, hash check, gap check, transactional
  apply, explicit rollback.
- sqlite-vec backend (default; loaded as extension into the libSQL connection).
- Encryption-at-rest via libSQL page-level cipher; keychain reference
  resolution; opt-out recorded.
- Online backup + restore, content-hash audit row, sidecar metadata.
- Integrity check + status RPC surface.
- Storage events emitted to a buffered sink; flushed once event-log opens.
- `harness db decrypt` operation for forensics (research §"Risks/Concerns").

### v1.x — follow-up missions
- LanceDB backend opt-in (build tag `harness_lancedb`); add when memory/RAG
  exceeds ~500k vectors.
- chromem-go backend opt-in (build tag `harness_chromem`) for CGo-free /
  small-corpus / test environments.
- Backup retention policy, scheduled by the scheduler mission.
- Vector-collection reindex from authoritative source; event-log replay
  drives rebuild.

### v2 — additive futures
- libSQL embedded replicas → cloud-sync (charter local-first remains the
  default; sync is opt-in).
- Multi-tenant data dirs (`~/.harness/<install-id>/`) and tenant-scoped
  encryption keys.
- Tiered storage (hot relational on libSQL, cold archive on object storage)
  via the event-log retention mission.

---

## 8. Risk Register

| ID | Risk | Likelihood | Impact | Mitigation |
|---|---|---|---|---|
| R1 | libSQL release drift — pinned version loses SQLite ≥ 3.51 floor or breaks Go SDK API | Medium | High | Pin a specific libSQL release in `go.mod`; build-time assertion against `sqlite_version()`; CI smoke test on each Go SDK bump. |
| R2 | sqlite-vec is pre-v1 (v0.1.9); on-disk format may break | Medium | Medium | Pin extension version via vendored binary; expose `Reindex` from day one so a format change becomes a cost-of-rebuild, not a data-loss; document in operator guide. |
| R3 | Encrypted libSQL files unreadable by stock SQLite tools | Certain | Medium | Ship `harness db decrypt` operation; document in restore guide; never the default workflow. |
| R4 | Windows NTFS WAL `close()` lock bug pre-3.51 | Resolved if floor enforced | High | Hard-fail at `Open` if `sqlite_version() < 3.51`; CI matrix includes Windows. |
| R5 | NFS/SMB detection differs across Linux distros (mountinfo formats); false negatives leave WAL on a network mount | Medium | High | Defense in depth: filesystem-type list + path-prefix heuristic for cloud-sync; PRAGMA `locking_mode=NORMAL` smoke test that intentionally provokes lock-take to verify. |
| R6 | Migration hash check fails after a refactor that touches DDL formatting | Medium | Low–Medium | Hash is computed over canonicalized SQL (whitespace-collapsed, comment-stripped); if a real change is needed, a *new* migration version supersedes — no in-place edits. Documented invariant. |
| R7 | LanceDB Go bindings (v0.1.2) regress or stagnate | Medium | Low | Opt-in only behind build tag; sqlite-vec covers v1; reassess at v1.x. |
| R8 | Race between two harness processes creating the same data dir on first run (`harness init` + concurrent `harness run`) | Low | Medium | Lockfile is taken *before* schema check; `O_EXCL` on creation; second process gets `ErrDBLocked` cleanly. |
| R9 | Encryption-key rotation interrupted partway leaves DB in mixed state | Low | High | libSQL re-key is itself transactional at the page-cache level; storage adds a "rotation in progress" sentinel row; on next open, sentinel + version mismatch triggers automatic recovery to the prior key. |
| R10 | Backup taken while a long-running write transaction is open produces a backup that fails restore validation | Low | Medium | Online backup uses SQLite's snapshot-isolation copy; restore re-runs `PRAGMA integrity_check` on the destination as part of `Apply`; failure → operator must re-take the backup. |

Pre-mortem coverage: each risk above maps to at least one acceptance scenario
in spec.md or one storage-event kind in §5.3. Operator-visible failures are
typed errors, not generic.

---

## 9. Open Questions (planning-phase decisions)

The spec carries three open questions. Resolutions for each:

1. **Encryption library — RESOLVED in research phase.**
   - Decision: **libSQL (`tursodatabase/go-libsql`)** for both OSS and
     enterprise builds. Apache-2.0/MIT, no SQLCipher attribution screen,
     page-level encryption with SQLCipher-compatible cipher options. The
     spec's "two builds, two backends" complication is unnecessary.
   - Spec Open Question 1 should be marked resolved in the next spec
     revision; this plan freezes the decision.

2. **Default data-directory layout — DECIDED tree, single install in v1.**
   - Layout: `~/.harness/<install-id>/data.db` plus `data.db.harness-lock`,
     `data.db.harness-meta.json`, `backups/`, `archive/`.
   - `<install-id>` is a ULID created on first run, persisted in
     `harness_meta.install_id`. v1 supports one install-id per machine in
     practice; v2 multi-tenant uses the same layout with several install-ids
     side by side under `~/.harness/`.
   - Operator override: `cfg.DataDir` from harness config; defaulted from
     XDG (`$XDG_DATA_HOME/harness`) on Linux, `~/Library/Application
     Support/harness` on macOS, `%LOCALAPPDATA%\harness\` on Windows.

3. **Backup retention surface — DECIDED take-backup primitive only in v1.**
   - v1 ships `BackupTaker.Take` and an audit row per backup. Retention
     (rotate-N, keep-daily-for-X, weekly-for-Y) lives in a follow-up mission
     orchestrated by the scheduler. This keeps storage's responsibility to
     "produce a consistent artifact"; lifecycle policy belongs at a layer
     that already understands time-based scheduling.

**Remaining unknowns to surface at WP planning:**

- Q-A: Which libSQL release tag pins us to SQLite ≥ 3.51 cleanly across all
  three target OSes? (Research §"Next Actions" item 3.) Resolve in the first
  WP that touches `go.mod`.
- Q-B: Mount-detection heuristic for cloud-sync paths — explicit allow-list
  vs deny-list. Default plan: deny-list (refuse known sync roots, allow
  everything else); revisit if false positives surface in early dogfooding.
- Q-C: Should the storage layer expose a synchronous `Open` only, or also a
  streaming variant for the Wails first-window startup-budget (charter:
  cold start <2s)? Default plan: synchronous; profile during the first
  consumer integration and revisit if the 2s budget is at risk.

---

## 10. Charter Check

| Charter constraint | This plan satisfies it by |
|---|---|
| Local-first invariant | All steady-state I/O is on the local DB file; libSQL embedded replicas are deferred to v2 and remain opt-in. |
| Security-first / SOC 2 | Encryption-at-rest default-on; key indirection through `secrets-keychain`; storage events into append-only event log; backup audit row; integrity check exposed. |
| DIRECTIVE_001 (boundaries) | All SQLite/libSQL/vector imports confined to `core/storage/...`; consumers see Go interfaces only. |
| DIRECTIVE_003 (decisions captured) | Research log + this plan record D1–D8 with rationale; Open Questions tracked through to resolution. |
| DIRECTIVE_010 (faithfulness to spec) | §3 maps each FR-### to a concrete API surface; §7 phasing preserves spec priorities. |
| DIRECTIVE_024 (small blast radius) | Storage is a leaf substrate; the public API is small; backends and feature schemas are additive. |
| DIRECTIVE_028 (efficient local tooling) | sqlite-vec keeps everything in one file; `harness db status` / `verify` give operators direct insight without external tools. |
| DIRECTIVE_036 (black-box testing) | All FRs map to behavior observable at the public API; integration tests drive `core/storage` only, never `db/` internals. |
| Performance targets | NFR-001 (5 ms event-log append), NFR-002 (2 ms session row read), NFR-003 (100 ms k=10 over 100k vectors) are all within the budget the chosen stack has demonstrated in the research evidence log. |

No conflicts identified. No charter exceptions requested.

---

## 11. Test Plan (sketch — finalized at WP stage)

Black-box suite against the public `core/storage` API (DIRECTIVE_036):

- **Open / Close lifecycle**: WAL on, foreign keys on, lockfile present,
  duplicate-open refused, encryption present when key supplied, refused
  cleanly when missing key.
- **Mount detection**: synthetic NFS / SMB / SSHFS mount in CI under Linux;
  Windows mapped drive; macOS network share; iCloud / Dropbox path
  heuristics.
- **Migration framework**: idempotent re-apply, rollback round-trip, gap
  refusal, hash mismatch refusal, partial-failure rollback to pre-migration
  state.
- **Vector store**: insert + k-NN against a known corpus with deterministic
  ordering; reindex; cross-backend contract test (sqlite-vec; lancedb +
  chromem under build tags).
- **Backup / restore**: round-trip on running harness; sidecar validation;
  encryption posture preserved; restore on a fresh data dir.
- **Integrity check**: clean DB passes; tampered page detected.
- **Storage events**: every operation in §5.3 produces exactly one event of
  the expected kind with the expected payload shape.
- **Concurrency stress** (NFR-004, US 6): one-hour run with multiple writers
  + readers, no deadlocks, no lost writes.

`go test ./core/storage/... -race` is the gate; ≥80% line coverage on
`core/storage`; integration tests use a real on-disk DB under `t.TempDir()`
(charter: "no mocking of storage in tests that assert audit/replay behavior").

---

## 12. Next Steps

1. Operator review of this plan and the three Open-Questions resolutions in §9.
2. `/spec-kitty.tasks --mission storage-foundations-01KQ1A3K` decomposes
   this plan into work packages. Suggested WP boundaries:
   - WP-01: `core/storage` skeleton, `Open`/`Close`, libSQL wiring,
     WAL/FK pragmas, lockfile, mount detection.
   - WP-02: Migration framework + ledger + hash/gap checks.
   - WP-03: Encryption hookup (depends on a stub `secrets` interface; full
     wiring lands when secrets-keychain merges).
   - WP-04: sqlite-vec backend + `VectorStore` interface.
   - WP-05: Backup + restore + decrypt operation.
   - WP-06: Diagnostics (verify + status) + RPC surface.
   - WP-07: Storage-event emission (buffered sink + flush handshake).
3. Each WP carries acceptance tests against the FRs/NFRs it implements.

---

> **Branch Contract (repeat)**
> - Planning base branch: `feat/storage-foundations-01KQ1A3K` off `main`.
> - Final merge target: `main` (squash-merge per charter Branch Strategy).
> - Next command (operator-driven, not auto-run): `/spec-kitty.tasks
>   --mission storage-foundations-01KQ1A3K`.
