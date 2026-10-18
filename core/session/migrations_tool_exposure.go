package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDToolExposure identifies migration 0347 — the project and
// session layers of tool exposure (toolexposure.Exposure, spec
// tool-context-budget-01TCBUD01 §2.1) and a session's activated set.
//
// Columns (additive, nullable — NULL is "this layer has no opinion" /
// "nothing activated", which is what every pre-0347 row means):
//
//   - projects.tool_exposure     JSON toolexposure.Exposure
//   - sessions.tool_exposure     JSON toolexposure.Exposure
//   - sessions.tool_activations  JSON []toolexposure.Activation
//
// 0346 is reserved for the same mission's usage-columns migration
// (WP01); once registered it sits before this one in Migrations().
//
// Additive only, so check-destructive-migration-coverage.sh has nothing
// to cover. Each ALTER is guarded by a pragma_table_info probe so the
// ledger-rewind repair path re-runs it as a no-op.
const migrationIDToolExposure = "sessions/0347-tool-exposure"

const sqlToolExposureSchema = `
        ALTER TABLE projects ADD COLUMN tool_exposure TEXT;
        ALTER TABLE sessions ADD COLUMN tool_exposure TEXT;
        ALTER TABLE sessions ADD COLUMN tool_activations TEXT;
    `

// migration0347 returns the tool-exposure columns migration. Down is a
// no-op, the package convention for additive nullable columns.
func migration0347() migrations.Migration {
	cols := []struct{ table, name, ddl string }{
		{"projects", "tool_exposure", "ALTER TABLE projects ADD COLUMN tool_exposure TEXT"},
		{"sessions", "tool_exposure", "ALTER TABLE sessions ADD COLUMN tool_exposure TEXT"},
		{"sessions", "tool_activations", "ALTER TABLE sessions ADD COLUMN tool_activations TEXT"},
	}
	return migrations.Migration{
		ID:            migrationIDToolExposure,
		Version:       347,
		OwningMission: OwningMission,
		UpSource:      sqlToolExposureSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				var n int
				if err := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info('"+c.table+"') WHERE name=?", c.name).Scan(&n); err != nil {
					return err
				}
				if n > 0 {
					continue
				}
				if _, err := tx.Exec(ctx, c.ddl); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(context.Context, migrations.WriteTx) error { return nil },
	}
}
