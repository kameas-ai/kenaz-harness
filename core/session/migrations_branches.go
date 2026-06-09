package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDBranches lands the branches + branch_message_refs tables —
// the storage backbone for the agent-kernel-graph mission's branching
// v1 (Bundle B, WP07). The shape stays intentionally simple so the
// "branching v1 simple" scope (spec §4.6) doesn't pull in semantic-merge
// gear we explicitly defer to v2.
//
// Tables:
//
//   - branches: one row per fork. Holds the parent ↔ child session
//     pair, the branch kind (fork / linear-continuation), the lifecycle
//     status (active / merging / merged / abandoned), the recommended
//     model id, and the merge timestamps. Both session ids reference
//     sessions(id) ON DELETE CASCADE so deleting either end purges the
//     branch row.
//
//   - branch_message_refs: copy-on-write reference table. Until a child
//     branch produces its own messages, the child references parent
//     message ids verbatim through this table. Once the child's first
//     novel message lands the table stops growing for that branch.
//
// Numbering: 0306 per the mission spec §4.11 / FR-055. (Was reserved
// during Bundle A; this migration cashes in the reservation.)
const migrationIDBranches = "sessions/0306-branches"

const sqlBranchesSchema = `
        CREATE TABLE IF NOT EXISTS branches (
            id                  TEXT PRIMARY KEY,
            parent_session_id   TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            child_session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            kind                TEXT NOT NULL DEFAULT 'fork',
            status              TEXT NOT NULL DEFAULT 'active',
            model_id            TEXT NOT NULL DEFAULT '',
            provider_id         TEXT NOT NULL DEFAULT '',
            title               TEXT NOT NULL DEFAULT '',
            task_hint           TEXT NOT NULL DEFAULT '',
            created_at          INTEGER NOT NULL,
            updated_at          INTEGER NOT NULL,
            merged_at           INTEGER,
            abandoned_at        INTEGER
        );

        CREATE INDEX IF NOT EXISTS idx_branches_parent
            ON branches (parent_session_id, status);

        CREATE INDEX IF NOT EXISTS idx_branches_child
            ON branches (child_session_id);

        CREATE TABLE IF NOT EXISTS branch_message_refs (
            branch_id       TEXT NOT NULL REFERENCES branches(id) ON DELETE CASCADE,
            seq             INTEGER NOT NULL,
            parent_msg_id   TEXT NOT NULL,
            child_msg_id    TEXT NOT NULL DEFAULT '',
            PRIMARY KEY (branch_id, seq)
        );

        CREATE INDEX IF NOT EXISTS idx_branch_refs_branch
            ON branch_message_refs (branch_id);
    `

// migration0306 returns the branches migration. Down drops both tables
// (operators downgrading lose every branch row; a re-fork from the UI
// will rebuild what the user wants).
func migration0306() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDBranches,
		Version:       306,
		OwningMission: OwningMission,
		UpSource:      sqlBranchesSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlBranchesSchema) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_branch_refs_branch",
				"DROP TABLE IF EXISTS branch_message_refs",
				"DROP INDEX IF EXISTS idx_branches_child",
				"DROP INDEX IF EXISTS idx_branches_parent",
				"DROP TABLE IF EXISTS branches",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
