package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDSubagent is the identifier for migration 0311.
//
// This migration extends the branches table with three columns that
// track the subagent-branch-recommendation context
// (branch-as-subagent-recommendation WP04/WP08):
//
//   - subagent_branch   : 0/1 bool — TRUE when the branch was spawned
//                         via the branch-advisor flow (as opposed to a
//                         manual fork from the BranchSidebar).
//   - recommendation_id : the ULID from BranchSuggestion.ID — correlates
//                         the branch row with the KindBranchAdvisorAccepted
//                         audit event.
//   - advisor_signals   : JSON array of positive-signal labels that fired
//                         (e.g. ["can_you_also","also_figure_out"]).
//
// A corresponding sessions column `branch_advisor_dismissed` carries
// the per-session dismiss flag so the advisor can skip processing for
// sessions where the user has opted out.
//
// Numbering: 0311 — next free version in the sessions block (300-399)
// after 0310 (compaction-strategy-ui WP01).
const migrationIDSubagent = "sessions/0311-subagent-metadata"

const sqlSubagentSchema = `
        ALTER TABLE branches ADD COLUMN subagent_branch INTEGER NOT NULL DEFAULT 0;
        ALTER TABLE branches ADD COLUMN recommendation_id TEXT NOT NULL DEFAULT '';
        ALTER TABLE branches ADD COLUMN advisor_signals TEXT NOT NULL DEFAULT '[]';
        ALTER TABLE sessions ADD COLUMN branch_advisor_dismissed INTEGER NOT NULL DEFAULT 0;
    `

// migration0311 returns the subagent-metadata migration. Down is a
// no-op: SQLite does not support DROP COLUMN in older sqlite3 versions
// shipped on macOS; instead we leave the columns behind and trust that
// future schema evolutions handle them. This is consistent with the
// pattern used in migrations_compaction.go for additive column
// migrations where rollback is low-risk (extra nullable columns with
// safe defaults).
func migration0311() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDSubagent,
		Version:       311,
		OwningMission: OwningMission,
		UpSource:      sqlSubagentSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"ALTER TABLE branches ADD COLUMN subagent_branch INTEGER NOT NULL DEFAULT 0",
				"ALTER TABLE branches ADD COLUMN recommendation_id TEXT NOT NULL DEFAULT ''",
				"ALTER TABLE branches ADD COLUMN advisor_signals TEXT NOT NULL DEFAULT '[]'",
				"ALTER TABLE sessions ADD COLUMN branch_advisor_dismissed INTEGER NOT NULL DEFAULT 0",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// SQLite ALTER TABLE DROP COLUMN requires ≥3.35.0 (2021-03-12).
			// We accept the no-op to stay compatible with the bundled sqlite
			// version on older macOS releases. The columns have safe defaults
			// and cause no functional regression when present.
			return nil
		},
	}
}
