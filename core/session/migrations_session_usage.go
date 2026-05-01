package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDSessionUsage is the identifier for migration 0314.
//
// This migration adds three nullable columns to session_messages for
// per-turn token + cost tracking (token-cost-telemetry-01KQ8TD7):
//
//   - prompt_tokens    INT  — provider-reported input token count
//   - completion_tokens INT — provider-reported output token count
//   - cost_usd        REAL  — derived or provider-reported cost in USD;
//                             NULL when no pricing entry exists
//
// These columns are nullable so existing rows are not affected. The
// session_messages table owns per-message data; the aggregation view
// sums these columns for the Sessions.GetUsage RPC.
//
// Numbering: 0314 — next free version in the sessions block (300-399)
// after 0313 (subagent-metadata, branch-as-subagent-recommendation WP04).
const migrationIDSessionUsage = "sessions/0314-session-usage-columns"

const sqlSessionUsageSchema = `
        ALTER TABLE session_messages ADD COLUMN prompt_tokens INTEGER;
        ALTER TABLE session_messages ADD COLUMN completion_tokens INTEGER;
        ALTER TABLE session_messages ADD COLUMN cost_usd REAL;
        ALTER TABLE session_messages ADD COLUMN cost_source TEXT;
    `

// migration0314 returns the session-usage-columns migration. Down is
// a no-op: SQLite's ALTER TABLE DROP COLUMN requires ≥3.35.0 and the
// columns have safe NULL defaults so leaving them behind is safe.
func migration0314() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSessionUsage,
		Version:       314,
		OwningMission: OwningMission,
		UpSource:      sqlSessionUsageSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"ALTER TABLE session_messages ADD COLUMN prompt_tokens INTEGER",
				"ALTER TABLE session_messages ADD COLUMN completion_tokens INTEGER",
				"ALTER TABLE session_messages ADD COLUMN cost_usd REAL",
				"ALTER TABLE session_messages ADD COLUMN cost_source TEXT",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// SQLite ALTER TABLE DROP COLUMN not supported on older macOS.
			// Accept no-op; extra NULL columns cause no regressions.
			return nil
		},
	}
}
