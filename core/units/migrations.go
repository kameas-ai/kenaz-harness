package units

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// OwningMission is the block name registered in CanonicalBlocks for
// the units schema. Block range: 1100-1199.
const OwningMission = "units"

const (
	// migrationIDUnitsInit identifies migration 1100 — the initial
	// schema landing for the unified-context-artifacts units store.
	migrationIDUnitsInit = "units/1100-init"

	// migrationIDUnitsSyncState identifies migration 1101 — the Phase-2
	// fleet sync sidecar (unit_sync_state).
	migrationIDUnitsSyncState = "units/1101-sync-state"

	// migrationIDUnitsSyncStateBaselines identifies migration 1102 — the
	// 3-way sync baseline columns added to unit_sync_state so the pull path
	// can distinguish server-counter space from local-counter space and
	// avoid silently overwriting un-synced local edits.
	migrationIDUnitsSyncStateBaselines = "units/1102-sync-state-baselines"
)

// sqlUnitsSchema is the DDL for migration 1100. Three tables:
//
//   - units: the primary entity table. scope + kind + classification +
//     load_policy are validated at the SQL CHECK boundary so no rogue
//     writer can slip in unknown enum values. scope_id is nullable to
//     accommodate global-scope units that have no parent scope object.
//
//   - unit_versions: append-only revision history. (unit_id, version)
//     is UNIQUE so concurrent bumps produce a detectable UNIQUE
//     constraint failure (mapped to ErrVersionConflict in the Go layer).
//
//   - unit_edges: directed typed edges between units. (from_id, to_id,
//     kind) has no uniqueness constraint — callers may model multi-edges
//     if needed (e.g. multiple promoted_from hops).
//
// Indexes cover the anticipated query patterns (FR-002 list-by-scope,
// FR-003 global-scope list, edge traversal).
const sqlUnitsSchema = `
    CREATE TABLE IF NOT EXISTS units (
        id              TEXT PRIMARY KEY,
        kind            TEXT NOT NULL CHECK (kind IN ('root','doc','artifact','snippet','tool_output')),
        scope           TEXT NOT NULL CHECK (scope IN ('global','project','session')),
        scope_id        TEXT NOT NULL DEFAULT '',
        classification  TEXT NOT NULL CHECK (classification IN ('personal','team','org')),
        version         INTEGER NOT NULL DEFAULT 0,
        load_policy     TEXT NOT NULL CHECK (load_policy IN ('always','on_demand')),
        title           TEXT NOT NULL DEFAULT '',
        body            TEXT NOT NULL DEFAULT '',
        metadata        TEXT NOT NULL DEFAULT '{}',
        created_at      INTEGER NOT NULL,
        updated_at      INTEGER NOT NULL
    );

    CREATE INDEX IF NOT EXISTS idx_units_scope
        ON units (scope, scope_id, created_at DESC);

    CREATE INDEX IF NOT EXISTS idx_units_kind
        ON units (kind, created_at DESC);

    CREATE TABLE IF NOT EXISTS unit_versions (
        id          INTEGER PRIMARY KEY AUTOINCREMENT,
        unit_id     TEXT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
        version     INTEGER NOT NULL,
        body        TEXT NOT NULL DEFAULT '',
        metadata    TEXT NOT NULL DEFAULT '{}',
        created_at  INTEGER NOT NULL,
        UNIQUE (unit_id, version)
    );

    CREATE INDEX IF NOT EXISTS idx_unit_versions_unit
        ON unit_versions (unit_id, version ASC);

    CREATE TABLE IF NOT EXISTS unit_edges (
        id          TEXT PRIMARY KEY,
        from_id     TEXT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
        to_id       TEXT NOT NULL REFERENCES units(id) ON DELETE CASCADE,
        kind        TEXT NOT NULL CHECK (kind IN ('references','derived_from','promoted_from','supersedes')),
        version     INTEGER NOT NULL DEFAULT 1,
        created_at  INTEGER NOT NULL
    );

    CREATE INDEX IF NOT EXISTS idx_unit_edges_from
        ON unit_edges (from_id, created_at ASC);

    CREATE INDEX IF NOT EXISTS idx_unit_edges_to
        ON unit_edges (to_id, created_at ASC);
`

// sqlUnitsSyncStateSchema is the DDL for migration 1101 — the Phase-2
// fleet sync sidecar (WP13/WP14 of unified-context-artifacts-01NCTXU01).
//
// unit_sync_state holds one row per *synced* unit: the fleet node id it
// maps to, the unit version last confirmed in sync, the opaque fleet
// classification recorded at last sync, and the last-sync wall clock.
// Personal units never get a row (they never leave the machine, NFR-005).
//
//   - unit_id is the primary key and FK to units(id) (ON DELETE CASCADE so
//     deleting a unit drops its sidecar).
//   - node_id has a UNIQUE index so the pull path can resolve a server node
//     back to its local unit in one lookup (no re-discovery).
//   - classification is stored opaquely as a string so core/units never
//     learns the fleet vocabulary (team_shared / org_shared) — the mapper
//     in core/fleet owns the translation.
const sqlUnitsSyncStateSchema = `
    CREATE TABLE IF NOT EXISTS unit_sync_state (
        unit_id         TEXT PRIMARY KEY REFERENCES units(id) ON DELETE CASCADE,
        node_id         TEXT NOT NULL DEFAULT '',
        synced_version  INTEGER NOT NULL DEFAULT 0,
        classification  TEXT NOT NULL DEFAULT '',
        last_synced     INTEGER NOT NULL
    );

    CREATE UNIQUE INDEX IF NOT EXISTS idx_unit_sync_state_node
        ON unit_sync_state (node_id);
`

// sqlUnitsSyncStateBaselines is the DDL for migration 1102 — adds the two
// separate version-space columns that implement the correct 3-way sync
// baseline, fixing the read-down overwrite bug where a post-pull local edit
// could be silently overwritten because the single synced_version column
// conflated the server counter space with the local counter space.
//
//   - synced_server_version: the server's node Version at last sync. Used as
//     the server-side baseline: server.Version > synced_server_version means
//     the server moved on (serverChanged).
//   - synced_local_version: the local unit's Version at last sync. Used as
//     the local-side baseline: local.Version > synced_local_version means the
//     user made un-synced local edits (localChanged).
//
// Both columns default to 0 (no edits assumed), which is the conservative
// safe default: if an existing row has no value for these columns the first
// pull after migration will treat any local version > 0 as a potential
// conflict, surfacing it for review rather than silently overwriting.
const sqlUnitsSyncStateBaselines = `
    ALTER TABLE unit_sync_state
        ADD COLUMN synced_server_version INTEGER NOT NULL DEFAULT 0;

    ALTER TABLE unit_sync_state
        ADD COLUMN synced_local_version INTEGER NOT NULL DEFAULT 0;
`

// Migrations returns the migration set that owns the units schema.
func Migrations() []migrations.Migration {
	return []migrations.Migration{
		{
			ID:            migrationIDUnitsInit,
			Version:       1100,
			OwningMission: OwningMission,
			UpSource:      sqlUnitsSchema,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitUnitSQL(sqlUnitsSchema) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			// Down is a best-effort no-op; operators wanting to downgrade
			// past 1100 must restore from a pre-1100 backup.
			Down: func(_ context.Context, _ migrations.WriteTx) error {
				return nil
			},
		},
		{
			ID:            migrationIDUnitsSyncState,
			Version:       1101,
			OwningMission: OwningMission,
			UpSource:      sqlUnitsSyncStateSchema,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitUnitSQL(sqlUnitsSyncStateSchema) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS unit_sync_state")
				return err
			},
		},
		{
			ID:            migrationIDUnitsSyncStateBaselines,
			Version:       1102,
			OwningMission: OwningMission,
			UpSource:      sqlUnitsSyncStateBaselines,
			Up: func(ctx context.Context, tx migrations.WriteTx) error {
				for _, stmt := range splitUnitSQL(sqlUnitsSyncStateBaselines) {
					if _, err := tx.Exec(ctx, stmt); err != nil {
						return err
					}
				}
				return nil
			},
			Down: func(ctx context.Context, tx migrations.WriteTx) error {
				// SQLite does not support DROP COLUMN in older versions; a
				// down-migration that must run on SQLite recreates the table
				// without the new columns. In practice operators downgrading
				// past 1102 should restore from backup.
				_, err := tx.Exec(ctx, `
					CREATE TABLE IF NOT EXISTS unit_sync_state_old AS
						SELECT unit_id, node_id, synced_version, classification, last_synced
						FROM unit_sync_state
				`)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "DROP TABLE unit_sync_state"); err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `
					ALTER TABLE unit_sync_state_old RENAME TO unit_sync_state
				`)
				return err
			},
		},
	}
}

// RegisterMigrations registers every migration returned by Migrations()
// into the given registry. Callers MUST register before storage.Open.
func RegisterMigrations(reg *migrations.Registry) error {
	for _, m := range Migrations() {
		if err := reg.Register(m); err != nil {
			return err
		}
	}
	return nil
}

// splitUnitSQL is a tiny semicolon splitter — the DDL above contains
// no quoted semicolons, so a literal split is sufficient.
func splitUnitSQL(src string) []string {
	out := make([]string, 0, 16)
	cur := make([]byte, 0, len(src))
	for i := 0; i < len(src); i++ {
		c := src[i]
		if c == ';' {
			s := trimUnitSpace(string(cur))
			if s != "" {
				out = append(out, s)
			}
			cur = cur[:0]
			continue
		}
		cur = append(cur, c)
	}
	if s := trimUnitSpace(string(cur)); s != "" {
		out = append(out, s)
	}
	return out
}

func trimUnitSpace(s string) string {
	start := 0
	for start < len(s) {
		c := s[start]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		start++
	}
	end := len(s)
	for end > start {
		c := s[end-1]
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			break
		}
		end--
	}
	return s[start:end]
}
