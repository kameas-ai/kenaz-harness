# Feature Specification: Storage Foundations — SQLite App Database and Vector Store

**Feature Branch**: `feat/storage-foundations-01KQ1A3K`
**Created**: 2026-04-25
**Status**: Draft
**Input**: Foundation mission. Defines the harness's persistent storage layer: a single-file SQLite application database for relational state, a vector store for embeddings (sqlite-vec default, pluggable for LanceDB / Qdrant / etc.), a migration framework, encryption-at-rest posture, and a backup story. Every other mission that persists state depends on this one.

## Why this mission exists

Multiple drafted specs reference persistent storage as an assumed shared surface — sessions, tasks, event log entries, lockfile state, MCP server registry, scheduler jobs, vector embeddings for memory/RAG, context-pack cache. A coherent storage layer must be specified once, then reused, rather than each mission inventing its own table and connection.

## Dependencies and relationships

- **Blocks**: any mission that persists state — `event-log`, `scheduler`, `memory-rag`, `acp-orchestration` (Tasks/Messages persistence), `shared-context-distribution` (cache state), `bundle-format-resolver` (cache state), session manager.
- **Reuses**: charter local-first invariant. SQLite is single-file, single-process, embedded — fits perfectly.
- **Adjacent**: `secrets-keychain` (encryption keys for the database itself live in the OS keychain).
- **Does not cover**: feature schemas (each consuming mission owns its own tables); cloud-sync (out of scope; future mission).

## User Scenarios & Testing *(mandatory)*

### User Story 1 — All harness state persists in a single embedded database (Priority: P1)

The harness uses a single SQLite database file under the project data directory for all relational state — sessions, tasks, event-log entries, lockfile resolution snapshots, scheduler jobs, MCP server registry, etc. No external database process is required. The file is portable: copy it to another machine and the harness can pick up where it left off.

**Why this priority**: Local-first by default. SQLite gives ACID guarantees, single-file portability, zero administration, and a battle-tested concurrency model — all charter-aligned.

**Independent Test**: Two separate harness installations point at the same database file (sequentially, not concurrently). The second installation reads the first's state correctly.

**Acceptance Scenarios**:

1. **Given** the harness writes session, task, and event data, **When** restarted, **Then** all state is intact and queryable.
2. **Given** an operator copies the database file to a new machine, **When** the harness opens it there, **Then** it reads cleanly without migration if the schema matches; with migration if newer.

---

### User Story 2 — Vector embeddings live alongside relational state (Priority: P1)

A vector store is available for embedding storage and similarity search, used by the memory/RAG layer and any other feature needing semantic retrieval. The default backend is sqlite-vec (an extension to the same SQLite database), so embeddings live in the same file as relational state, the same lockfile, the same backup target. A pluggable backend contract allows swapping to LanceDB, Qdrant, or others without changes elsewhere.

**Why this priority**: RAG and long-term memory are explicit user-facing features. Without a vector store, those missions cannot be planned. sqlite-vec keeps the local-first promise (single file, no extra process).

**Independent Test**: Embeddings are inserted, similarity search returns expected nearest neighbors against a known corpus, and the database remains a single file.

**Acceptance Scenarios**:

1. **Given** sqlite-vec is the active backend, **When** the harness inserts embeddings and runs a k-NN query, **Then** results match expected nearest neighbors with deterministic order on identical input.
2. **Given** an alternative backend (LanceDB, Qdrant) is configured, **When** the harness inserts embeddings and runs a similarity query, **Then** the same API contract holds and the consumer code is unchanged.

---

### User Story 3 — Schema evolves safely with versioned migrations (Priority: P1)

Every consuming mission contributes its tables and indexes through a migration system. Migrations are versioned, ordered, and applied at startup. Forward-only by default; rollbacks are explicit and require operator confirmation. A failed migration leaves the database in its pre-migration state. Migration history is auditable.

**Why this priority**: Without migrations, schema drift is inevitable, and a v1.0 → v1.1 upgrade becomes a data-loss risk. Every long-lived application needs this from day one.

**Independent Test**: A migration is added that creates a new table; a harness with the older schema starts up, applies the migration cleanly, and the new table is usable.

**Acceptance Scenarios**:

1. **Given** the harness starts on a database older than the current schema, **When** startup runs, **Then** outstanding migrations apply in order and the harness comes up healthy.
2. **Given** a migration fails partway, **When** the harness detects the failure, **Then** it rolls back to the prior consistent state and reports a structured migration error.
3. **Given** an operator explicitly downgrades, **When** they invoke a rollback migration, **Then** the rollback runs and the schema returns to the prior version.

---

### User Story 4 — Sensitive data is encrypted at rest (Priority: P2)

The harness writes sensitive content (resolved context, message history, tool-call payloads, partial reasoning, model output) to disk. The database is encrypted at rest using a key drawn from the OS keychain via the `secrets-keychain` mission. An attacker with file-system access but no keychain access cannot read the database.

**Why this priority**: Enterprise / SOC 2 / personal-data posture requires encryption at rest. Operator opt-out exists for environments that already encrypt at the disk layer (e.g., FileVault, BitLocker) and want to avoid double-encryption.

**Independent Test**: A database file is copied to another machine without the keychain entry. The harness on the destination machine cannot open the database without supplying the correct key.

**Acceptance Scenarios**:

1. **Given** encryption is enabled, **When** the database is opened on the original machine, **Then** the harness reads it using the keychain-stored key.
2. **Given** the database file is moved without the keychain entry, **When** opened, **Then** the harness fails to open it with an actionable "encryption key not found" error.
3. **Given** an operator opts out of database encryption (relying on disk encryption), **When** configured, **Then** the database is unencrypted and the operator's choice is recorded in the configuration audit log.

---

### User Story 5 — Database backup and restore are simple and well-defined (Priority: P2)

Operators can take consistent backups of the database without stopping the harness, and restore from a backup with a clear, documented procedure. SQLite's online backup API is the underlying mechanism.

**Why this priority**: Without backup, every harness installation is one disk failure away from total loss of session history and audit trail — unacceptable for SOC 2 readiness.

**Independent Test**: A backup is taken while the harness is running. The backup is restored on a fresh machine, and the harness reads it correctly with no data loss.

**Acceptance Scenarios**:

1. **Given** the harness is actively writing, **When** an online backup is taken, **Then** the backup is internally consistent.
2. **Given** a valid backup, **When** restored on a fresh machine with the same encryption key, **Then** the harness opens it cleanly.

---

### User Story 6 — Concurrency is safe and predictable (Priority: P2)

Multiple harness components (LLM connector emitting events, scheduler running jobs, A2A handler accepting tasks) write to the database concurrently. Writes are serialized through a single connection or write-ahead-log-friendly pattern; readers do not block writers and writers do not corrupt readers. The harness does not deadlock under expected load.

**Why this priority**: The harness is a multi-component process. Deadlocks or "database is locked" errors at startup or under load make the app feel broken.

**Independent Test**: A stress test runs all known writers (event log, scheduler, sessions) concurrently for a sustained period; no deadlocks, no lost writes, no corruption.

**Acceptance Scenarios**:

1. **Given** multiple writers and readers, **When** all run concurrently for an hour, **Then** no deadlocks occur and all writes land.
2. **Given** the database is configured with WAL mode, **When** a long-running read holds open while writes happen, **Then** writes proceed and the reader sees a consistent snapshot.

---

### Edge Cases

- The data directory is on a network filesystem (NFS, SMB) that does not honor SQLite's locking guarantees: surface a startup warning; recommend a local data directory.
- Two harness processes accidentally point at the same database file simultaneously: the second instance fails to acquire the lock and exits with a clear "another harness is already using this database" message.
- The disk fills mid-write: SQLite's transaction guarantees apply; the in-progress transaction rolls back.
- A migration adds a column whose default value depends on existing data: the migration runs the necessary transform and emits progress events for large tables.
- The encryption key in keychain is rotated: a re-key operation is supported; in-place re-encryption is documented but slow.
- The vector index becomes corrupted: the harness can rebuild it from the source embeddings without replaying the entire event log.

## Requirements *(mandatory)*

### Functional Requirements

| ID | Title | User Story | Priority | Status |
|----|-------|------------|----------|--------|
| FR-001 | Single-file SQLite app database | As an operator, I want all relational state in a single SQLite database file under the project data directory. | High | Open |
| FR-002 | WAL mode by default | As an operator, I want the database to use Write-Ahead Logging mode by default so readers do not block writers. | High | Open |
| FR-003 | Migration framework | As a contributor, I want to declare versioned migrations per consuming mission, applied in order at startup, with up and down operations. | High | Open |
| FR-004 | Migration audit log | As an operator, I want every migration apply and rollback recorded in the harness append-only event log. | High | Open |
| FR-005 | Vector store abstraction | As a contributor, I want a stable vector-store contract (insert, batch-insert, k-NN search, delete, reindex) so the backend is swappable. | High | Open |
| FR-006 | sqlite-vec default backend | As an operator, I want sqlite-vec as the default vector backend so embeddings live in the same database file. | High | Open |
| FR-007 | Pluggable vector backends | As an enterprise operator, I want LanceDB and Qdrant adapters available as opt-in backends without modifying core. | Medium | Open |
| FR-008 | Encryption at rest | As an operator, I want the database optionally encrypted at rest using a key from the OS keychain. | High | Open |
| FR-009 | Online backup | As an operator, I want to take a consistent backup while the harness is running, using SQLite's online backup API. | High | Open |
| FR-010 | Restore procedure | As an operator, I want a documented restore procedure that produces a working harness on a fresh machine. | High | Open |
| FR-011 | Single-writer enforcement | As an operator, I want the harness to detect and refuse to start if another instance is already using the database file. | High | Open |
| FR-012 | Connection management | As a contributor, I want a single, shared connection pool with clear read/write contracts so consumers do not fight for locks. | High | Open |
| FR-013 | Vector reindex | As an operator, I want a reindex operation that rebuilds the vector index from authoritative embeddings without replaying upstream sources. | Medium | Open |
| FR-014 | Database integrity check | As an operator, I want a `harness db verify` operation that runs SQLite's integrity check and reports results. | Medium | Open |
| FR-015 | Schema introspection | As an operator, I want a `harness db status` operation showing schema version, applied migrations, file size, page count, and integrity status. | Medium | Open |
| FR-016 | Foreign-key enforcement | As a contributor, I want SQLite foreign-key enforcement on by default so reference integrity is maintained. | High | Open |
| FR-017 | Configurable data directory | As an operator, I want to override the default data directory for testing and multi-environment workflows. | Medium | Open |

### Non-Functional Requirements

| ID | Title | Requirement | Category | Priority | Status |
|----|-------|-------------|----------|----------|--------|
| NFR-001 | Write latency | Single-row insert into the event-log table completes in under 5 ms p99 on a developer laptop. | Performance | High | Open |
| NFR-002 | Read latency | Indexed point-query on a session row completes in under 2 ms p99. | Performance | High | Open |
| NFR-003 | Vector k-NN latency | k-NN search over 100k embeddings (sqlite-vec backend) completes in under 100 ms p95 for k=10. | Performance | High | Open |
| NFR-004 | Concurrency safety | Sustained concurrent load of all known writers for one hour produces zero deadlocks and zero lost writes. | Reliability | High | Open |
| NFR-005 | Encryption performance overhead | Encryption-at-rest adds under 10 % p95 latency overhead to write operations. | Performance | Medium | Open |
| NFR-006 | Migration safety | 100 % of failed migrations leave the database in its pre-migration state across the test matrix. | Reliability | High | Open |
| NFR-007 | Data directory portability | Database file copied between machines of the same architecture and OS family is byte-portable and openable. | Portability | High | Open |

### Constraints

| ID | Title | Constraint | Category | Priority | Status |
|----|-------|------------|----------|----------|--------|
| C-001 | Architectural integrity | Storage logic lives in `core/storage/` (or equivalent). Other `core/` packages consume only the public API; no other package imports SQLite or vector-backend SDKs directly. | Technical | High | Open |
| C-002 | Local-first | The harness operates against the local database file with zero network egress for steady-state read/write. | Technical | High | Open |
| C-003 | No plaintext encryption keys in config | The encryption key is fetched from the OS keychain via `secrets-keychain`. It never appears inline in any config file. | Security | High | Open |
| C-004 | Append-only event log immutability | Migration events and database lifecycle events emit append-only entries. | Security | High | Open |
| C-005 | SOC 2 readiness | Migrations, restores, encryption changes, and backup operations produce audit evidence sufficient for SOC 2 audit per the charter. | Regulatory | High | Open |
| C-006 | Vector backend extensibility | Adding a new vector backend requires no changes to packages outside its own directory. | Technical | Medium | Open |

### Key Entities

- **App Database**: a single SQLite file under the project data directory. ACID, WAL-mode, foreign-keys-on. Holds every consuming mission's tables.
- **Vector Store**: a logical store for embeddings with a stable contract. Default backend is sqlite-vec (lives in the same file as the App Database); alternative backends (LanceDB, Qdrant) are pluggable.
- **Migration**: a versioned, ordered, idempotent unit of schema change. Carries a numeric version, an `up` and `down` script, an owning mission, and an applied-at timestamp once executed.
- **Migration Ledger**: a system table recording every applied migration; the source of truth for current schema version.
- **Encryption Key Reference**: a credential reference (per `secrets-keychain`) pointing to the database encryption key; never inline.
- **Storage Event**: an append-only event log entry emitted on database lifecycle operations (open, migration, backup taken, restored, integrity check, encryption rotation).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: An operator can run the harness, write session and event data, and back up + restore that data on a fresh machine in under 30 minutes from a clean clone.
- **SC-002**: 100 % of test-matrix concurrent-load runs complete with zero deadlocks and zero lost writes.
- **SC-003**: 100 % of failed migrations leave the database in its pre-migration state.
- **SC-004**: Encryption at rest adds under 10 % p95 write-latency overhead.
- **SC-005**: A new vector backend is added end-to-end without modifying any `core/` package outside the new backend's directory.
- **SC-006**: With a populated database, the harness reads and writes successfully with zero outbound network traffic.

## Assumptions

- SQLite 3.x is acceptable as the embedded store. (Modern Go SQLite drivers ship a compatible build embedded; no system dependency.)
- sqlite-vec is the right v1 default vector backend. (See ACP research mission for context.)
- Operators who run the harness under FileVault / BitLocker / dm-crypt may opt out of database-level encryption and rely on disk encryption.
- The encryption key lives in OS keychain (Keychain on macOS, Credential Manager on Windows, Secret Service / kernel keyring on Linux) per `secrets-keychain`.
- Database file format is portable across machines of the same SQLite version family; no custom binary format on top.

## Open Questions

1. **[NEEDS CLARIFICATION]** Encryption library — SQLCipher (proven, dual-licensed; commercial license required for redistributing the SQLite branch) or wxSQLite3 / `crypto`-on-top patterns (more permissive, less battle-tested)? Default if unresolved: SQLCipher in the enterprise build (license obtained), `crypto`-on-top approach in OSS — same encryption-at-rest contract, different backend. Decided in planning.
2. **[NEEDS CLARIFICATION]** Default data directory layout — `~/.harness/data.db` flat, or a tree under `~/.harness/<install-id>/data.db` to allow multiple coexisting installations? Default: tree, to support the "multiple harness installations on one machine" path operators may take for staging vs prod or per-tenant testing.
3. **[NEEDS CLARIFICATION]** Backup retention surface — does the harness manage retention policy (rotate N backups, keep daily for X days, weekly for Y) in v1, or expose only the take-backup primitive and leave retention to the operator's tooling? Default: take-backup primitive in v1; retention scheduler in a follow-up mission via the scheduler.
