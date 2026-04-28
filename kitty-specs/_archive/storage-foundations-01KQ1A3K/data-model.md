# Data Model (Discovery Draft) — Storage Foundations

This captures the storage-layer entities the harness will model on top of libSQL + sqlite-vec, plus the operational metadata needed for migrations, encryption, and concurrency. Implementation will refine field names, types, and indexes.

## Entities

### Entity: AppDatabase

- **Description**: a single libSQL database file under the harness data directory holding every consuming mission's tables. Encrypted-at-rest by default via libSQL's page-level encryption, key fetched from `secrets-keychain`. Vector data co-resides in this same file via `sqlite-vec` extension tables.
- **Attributes**:
  - `path` (filesystem path) — under `~/.harness/<install-id>/data.db` by default.
  - `libsql_version` (semver) — recorded for audit and migration logic.
  - `sqlite_version` (semver) — must be ≥ 3.51.0.
  - `schema_version` (int) — pointer to the latest applied Migration.
  - `encryption_status` (enum: `enabled`, `disabled`, `disabled_with_disk_encryption`) — explicitly recorded; opt-out is a recorded operator choice.
  - `wal_mode` (bool) — should be true for v1.
  - `foreign_keys_on` (bool) — should be true for v1.
- **Identifiers**: filesystem path; harness install-id ties it to a specific installation.
- **Lifecycle Notes**: created at first run via `harness init`. Opened with WAL, foreign keys on, encryption per the recorded status. Refused to open if path is on a non-local mount unless explicit operator override.

### Entity: Migration

- **Description**: a versioned, ordered, idempotent unit of schema change. Owned by a consuming mission. Carries up + down operations.
- **Attributes**:
  - `id` (string) — `<owning-mission>/<NNN>-<short-desc>` (e.g., `event-log/0001-init`).
  - `version` (int) — global ordinal across all migrations in the harness.
  - `owning_mission` (string) — `event-log`, `scheduler`, `acp`, etc.
  - `up` (SQL or programmatic step list)
  - `down` (SQL or programmatic step list)
  - `applied_at` (timestamp, nullable until applied)
  - `content_hash` (string) — hash of the up script for integrity.
- **Identifiers**: `id` (stable, human-readable); `version` (monotonic).
- **Lifecycle Notes**: declared in source. Applied at startup in `version` order. Each successful apply records to MigrationLedger. Failure rolls back the in-progress migration; previously-applied migrations remain.

### Entity: MigrationLedger

- **Description**: system table tracking applied migrations. Source of truth for current schema version.
- **Attributes**:
  - `version` (int, PK)
  - `id` (string, unique)
  - `applied_at` (timestamp)
  - `content_hash` (string) — verified against the migration's hash at every startup; mismatch is a refusal-to-start.
- **Lifecycle Notes**: append-only. Rollbacks emit a *new* ledger entry recording the rollback rather than deleting the prior apply.

### Entity: VectorStore (logical)

- **Description**: the harness's logical vector-store interface. Default backend: `sqlite-vec` extension running inside the same AppDatabase. Pluggable: LanceDB (opt-in for >500k vectors), `chromem-go` (pure-Go escape hatch).
- **Attributes**:
  - `backend_kind` (enum: `sqlite-vec`, `lancedb`, `chromem`) — recorded per-collection so consumers know what they're talking to.
  - `dimension` (int) — embedding dimensionality.
  - `metric` (enum: `cosine`, `l2`, `dot`) — similarity metric.
  - `quantization` (enum: `none`, `binary_int8`) — sqlite-vec quantization mode where applicable.
- **Identifiers**: `(collection_name, dimension)` tuple.
- **Lifecycle Notes**: collection schema is owned by the consuming mission (e.g., memory/RAG). Reindex is a supported operation; rebuilds the index from authoritative embeddings without replaying upstream sources.

### Entity: BackupArtifact

- **Description**: a consistent snapshot of the AppDatabase taken via SQLite's online backup API while the harness is running.
- **Attributes**:
  - `path` (filesystem path) — destination file.
  - `taken_at` (timestamp)
  - `source_schema_version` (int)
  - `source_libsql_version` (semver)
  - `encryption_status` (enum) — backup of encrypted DB is itself encrypted (libSQL encrypts WAL + backup pages by design).
  - `content_hash` (string) — SHA-256 of the backup file at completion.
- **Lifecycle Notes**: produced on operator request or scheduled (retention policy is a follow-up mission). Restoration on a fresh machine requires the same encryption key and same-or-higher libSQL release supporting the source schema version.

### Entity: StorageEvent

- **Description**: append-only event log entry emitted on database lifecycle operations. Lands in the shared event log per `event-log-01KQ1A3M`.
- **Attributes**:
  - `event_id` (ULID)
  - `kind` (enum: `db_opened`, `migration_applied`, `migration_failed`, `migration_rolled_back`, `backup_taken`, `backup_restored`, `integrity_check_run`, `encryption_rotated`, `non_local_mount_refused`, `non_local_mount_overridden`)
  - `payload_ref` (blob reference) — redacted operation metadata.
  - `emitted_at` (timestamp)
- **Lifecycle Notes**: append-only.

## Relationships

| Source | Relation | Target | Cardinality | Notes |
|---|---|---|---|---|
| AppDatabase | **applied** | Migration | 1:N | Recorded in MigrationLedger. |
| AppDatabase | **embeds** | VectorStore (sqlite-vec) | 1:1 | Same file, default backend. |
| AppDatabase | **may use** | VectorStore (lancedb \| chromem) | 0:1 | Opt-in alternative backends; if used, embeddings live outside the AppDatabase file. |
| AppDatabase | **produces** | BackupArtifact | 1:N | Each operator-initiated backup. |
| AppDatabase | **emits** | StorageEvent | 1:N | All lifecycle operations. |
| Migration | **declared by** | Owning Mission | N:1 | Each consuming mission owns its own migrations. |
| MigrationLedger entry | **records** | Migration apply or rollback | 1:1 | Append-only. |
| EncryptionKeyReference (secrets-keychain) | **secures** | AppDatabase | 1:1 | When encryption is enabled. |

## Validation & Governance

- **Data quality requirements**:
  - AppDatabase MUST refuse to open on non-local mounts (NFS / SMB / CIFS / cloud-sync) without explicit operator override.
  - AppDatabase MUST run with WAL mode and foreign keys ON.
  - libSQL release MUST embed SQLite ≥ 3.51.0.
  - Migrations MUST be applied in `version` order; gaps are refusals-to-start.
  - MigrationLedger entries are append-only; rollbacks add new entries that reference prior ones.
  - VectorStore collection writes MUST go through the abstraction layer; no consumer reaches into the underlying backend directly.
- **Compliance considerations**:
  - Encryption-at-rest is the recommended default; opt-out is recorded explicitly in AppDatabase metadata, not silently inferred.
  - The encryption key is sourced via `secrets-keychain`; it never appears inline in any config or in the AppDatabase metadata.
  - Backup files inherit encryption — operators copying a backup to another machine must also have the key reference resolvable on that machine.
  - All lifecycle operations emit StorageEvents into the harness event log per charter C-003.
- **Source of truth**:
  - `MigrationLedger` is authoritative for current schema version.
  - The libSQL DB file is authoritative for relational + sqlite-vec data.
  - Alternative VectorStore backends (LanceDB) maintain their own files; they are *secondary* and rebuildable from upstream sources via reindex.

> Treat this as a working model. Revisit when planning the actual table schemas per consuming mission.
