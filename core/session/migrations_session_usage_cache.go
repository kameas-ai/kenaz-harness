package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDSessionUsageCache identifies migration 0346 — the
// prompt-cache split of each assistant row's usage, beside the usage
// columns migration 0314 added to session_messages.
//
//   - cached_tokens       prompt tokens the provider served from its cache
//   - cache_write_tokens  prompt tokens the provider wrote to its cache
//
// Nullable like 0314's columns: a row written before 0346, or by a
// provider that reports no cache split, reads as NULL and sums as 0.
//
// Additive only, so check-destructive-migration-coverage.sh has nothing
// to cover. Idempotent: each ALTER is guarded by a pragma_table_info
// probe so the ledger-rewind repair path re-runs it as a no-op.
const migrationIDSessionUsageCache = "sessions/0346-session-usage-cache-tokens"

const sqlSessionUsageCacheSchema = `
        ALTER TABLE session_messages ADD COLUMN cached_tokens INTEGER;
        ALTER TABLE session_messages ADD COLUMN cache_write_tokens INTEGER;
    `

// migration0346 returns the usage cache-tokens migration. Down is a
// no-op, the package convention for additive nullable columns (see
// migration0314).
func migration0346() migrations.Migration {
	cols := []struct{ name, ddl string }{
		{"cached_tokens", "ALTER TABLE session_messages ADD COLUMN cached_tokens INTEGER"},
		{"cache_write_tokens", "ALTER TABLE session_messages ADD COLUMN cache_write_tokens INTEGER"},
	}
	return migrations.Migration{
		ID:            migrationIDSessionUsageCache,
		Version:       346,
		OwningMission: OwningMission,
		UpSource:      sqlSessionUsageCacheSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, c := range cols {
				var n int
				if err := tx.QueryRow(ctx,
					"SELECT COUNT(*) FROM pragma_table_info('session_messages') WHERE name=?", c.name).Scan(&n); err != nil {
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
