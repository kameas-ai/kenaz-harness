package storage

import (
	"errors"
	"fmt"
)

// Storage event kinds (plan §5.3). Each kind has a documented payload
// schema validated by ValidateEventPayload.
const (
	EventKindDBOpened                 = "db_opened"
	EventKindMigrationApplied         = "migration_applied"
	EventKindMigrationFailed          = "migration_failed"
	EventKindMigrationRolledBack      = "migration_rolled_back"
	EventKindBackupTaken              = "backup_taken"
	EventKindBackupRestored           = "backup_restored"
	EventKindIntegrityCheckRun        = "integrity_check_run"
	EventKindEncryptionRotated        = "encryption_rotated"
	EventKindNonLocalMountRefused     = "non_local_mount_refused"
	EventKindNonLocalMountOverridden  = "non_local_mount_overridden"
	EventKindVectorCollectionOpened   = "vector_collection_opened"
	EventKindVectorReindexCompleted   = "vector_reindex_completed"
	// EventKindEventsDropped is a synthetic kind emitted by the
	// BootstrapEventSink when its ring buffer overflows. See
	// internal/events for the Drain semantics.
	EventKindEventsDropped = "events_dropped"
)

// AllEventKinds is the canonical set; useful for tests and validators.
var AllEventKinds = []string{
	EventKindDBOpened,
	EventKindMigrationApplied,
	EventKindMigrationFailed,
	EventKindMigrationRolledBack,
	EventKindBackupTaken,
	EventKindBackupRestored,
	EventKindIntegrityCheckRun,
	EventKindEncryptionRotated,
	EventKindNonLocalMountRefused,
	EventKindNonLocalMountOverridden,
	EventKindVectorCollectionOpened,
	EventKindVectorReindexCompleted,
	EventKindEventsDropped,
}

// eventPayloadSchemas declares the required keys for each event kind.
// Optional keys are not enforced by the validator but are documented in
// the comment above each entry.
//
// Required keys:
//
//	db_opened                     : install_id, libsql_version, sqlite_version, encryption_status, wal_mode, foreign_keys_on
//	migration_applied             : version, id, owning_mission, content_hash, duration_ms
//	migration_failed              : version, id, owning_mission, error
//	migration_rolled_back         : version, id, owning_mission, content_hash, duration_ms
//	backup_taken                  : path, taken_at, source_schema_version, content_hash, encryption_status
//	backup_restored               : src, taken_at, source_schema_version, content_hash, encryption_status
//	integrity_check_run           : ok, message_count
//	encryption_rotated            : install_id
//	non_local_mount_refused       : path, kind, detail
//	non_local_mount_overridden    : path, kind, detail
//	vector_collection_opened      : name, dimension, metric, backend
//	vector_reindex_completed      : name, count, duration_ms
//	events_dropped                : count
var eventPayloadSchemas = map[string][]string{
	EventKindDBOpened: {
		"install_id", "libsql_version", "sqlite_version",
		"encryption_status", "wal_mode", "foreign_keys_on",
	},
	EventKindMigrationApplied: {
		"version", "id", "owning_mission", "content_hash", "duration_ms",
	},
	EventKindMigrationFailed: {
		"version", "id", "owning_mission", "error",
	},
	EventKindMigrationRolledBack: {
		"version", "id", "owning_mission", "content_hash", "duration_ms",
	},
	EventKindBackupTaken: {
		"path", "taken_at", "source_schema_version", "content_hash", "encryption_status",
	},
	EventKindBackupRestored: {
		"src", "taken_at", "source_schema_version", "content_hash", "encryption_status",
	},
	EventKindIntegrityCheckRun: {
		"ok", "message_count",
	},
	EventKindEncryptionRotated: {
		"install_id",
	},
	EventKindNonLocalMountRefused: {
		"path", "kind", "detail",
	},
	EventKindNonLocalMountOverridden: {
		"path", "kind", "detail",
	},
	EventKindVectorCollectionOpened: {
		"name", "dimension", "metric", "backend",
	},
	EventKindVectorReindexCompleted: {
		"name", "count", "duration_ms",
	},
	EventKindEventsDropped: {
		"count",
	},
}

// ErrUnknownEventKind is returned by ValidateEventPayload when kind is
// not in eventPayloadSchemas.
var ErrUnknownEventKind = errors.New("storage: unknown event kind")

// ValidateEventPayload returns nil when payload contains every required
// key for the given kind. Type-checking is intentionally minimal —
// payloads are passed to the EventSink as map[string]any and serialized
// downstream.
//
// Tests in eventkinds_test.go assert that every kind in AllEventKinds
// has a schema entry, so consuming code can rely on the validator
// covering the full taxonomy.
func ValidateEventPayload(kind string, payload map[string]any) error {
	required, ok := eventPayloadSchemas[kind]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEventKind, kind)
	}
	for _, key := range required {
		if _, present := payload[key]; !present {
			return fmt.Errorf("storage: event %q missing required key %q", kind, key)
		}
	}
	return nil
}

// EventPayloadKeys returns the required keys for a given kind. Returns
// (nil, false) when the kind is unknown.
func EventPayloadKeys(kind string) ([]string, bool) {
	keys, ok := eventPayloadSchemas[kind]
	if !ok {
		return nil, false
	}
	out := make([]string, len(keys))
	copy(out, keys)
	return out, true
}
