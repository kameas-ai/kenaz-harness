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
