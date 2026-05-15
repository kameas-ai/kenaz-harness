// Package log — schema versioning registry.
//
// CurrentSchemaVersion is the canonical version number written to
// events.schema_version by the writer path. The reader path uses the
// registry to lift old rows through the migration chain before handing
// them to callers (audit-log-enhancement-01KX5R8F WP01/WP03).
package log

import (
	"encoding/json"
	"fmt"
)

// CurrentSchemaVersion is the schema version written to new event rows.
// Increment this when the canonical payload shape for ANY kind changes
// in a way that old decoders cannot handle. Per-kind migrations are
// registered via RegisterKindSchema.
const CurrentSchemaVersion = 1

// MigrateFunc transforms a raw JSON payload from one schema version to
// the next. It must return a semantically equivalent payload at
// (fromVersion+1). Returning an error halts the migration chain; the
// caller receives the last successfully migrated bytes.
type MigrateFunc func(rawJSON []byte) ([]byte, error)

// KindSchema holds the migration chain for a single event kind.
type KindSchema struct {
	// CurrentVersion is the current canonical payload version for this kind.
	// Rows at this version are returned as-is.
	CurrentVersion int

	// Migrations is an ordered list of migration functions indexed by
	// (fromVersion - 1). migrations[0] takes v1 → v2, migrations[1]
	// takes v2 → v3, etc. Must be nil / empty when CurrentVersion == 1.
	Migrations []MigrateFunc
}

// schemaRegistry holds per-kind schema registrations.
var schemaRegistry = map[string]KindSchema{}

// RegisterKindSchema registers a KindSchema for an event kind string.
// Call from init() in the kind's declaration file. Panics on duplicate
// registration to surface conflicts at startup.
func RegisterKindSchema(kind string, s KindSchema) {
	if _, exists := schemaRegistry[kind]; exists {
		panic(fmt.Sprintf("log: duplicate KindSchema registration for kind %q", kind))
	}
	schemaRegistry[kind] = s
}

// MigratePayload lifts rawJSON from fromVersion to the kind's current
// schema version by chaining the registered migration functions. If the
// kind has no registered schema or fromVersion >= currentVersion the
// payload is returned unchanged. Errors from individual migrations are
// returned immediately with the last successful intermediate payload.
func MigratePayload(kind string, fromVersion int, rawJSON []byte) ([]byte, int, error) {
	s, ok := schemaRegistry[kind]
	if !ok {
		// Unknown kind — no migrations available; return as-is.
		return rawJSON, fromVersion, nil
	}
	if fromVersion >= s.CurrentVersion {
		return rawJSON, fromVersion, nil
	}
	cur := rawJSON
	curVer := fromVersion
	for curVer < s.CurrentVersion {
		idx := curVer - 1 // migrations[0] = v1→v2
		if idx < 0 || idx >= len(s.Migrations) {
			// No migration function for this step; stop.
			break
		}
		next, err := s.Migrations[idx](cur)
		if err != nil {
			return cur, curVer, fmt.Errorf("log: migrating kind %q from v%d to v%d: %w",
				kind, curVer, curVer+1, err)
		}
		cur = next
		curVer++
	}
	return cur, curVer, nil
}

// EnsureValidJSON is a helper used by migration tests to verify
// that migration output is well-formed JSON.
func EnsureValidJSON(b []byte) error {
	var v json.RawMessage
	if err := json.Unmarshal(b, &v); err != nil {
		return fmt.Errorf("migration produced invalid JSON: %w", err)
	}
	return nil
}
