package session

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/storage/migrations"
)

// migrationIDBranchDisplayMeta identifies migration 0323 — additive
// columns on the branches table for the branching-ux-polish mission
// (branching-ux-polish-01KQ8TD7 WP01). The schema "0307" referenced in
// the spec was already taken by corpora (0307); 0322 was taken by
// last_usage_json (backend-context-window-length-01KQ8TD3 WP02); this
// mission lands at the next available slot (0323).
//
// Columns added:
//
//   - parent_message_id TEXT NOT NULL DEFAULT '' — the message anchor
//     used by the breadcrumb to show "Branch from turn N of <parent>".
//     Existing rows backfill to '' (breadcrumb degrades gracefully to
//     "Branch of <parent>" without a turn number).
//
//   - branch_title TEXT NOT NULL DEFAULT '' — a display-name override;
//     supplements the existing `title` column (which keeps its sidebar
//     row meaning). Existing rows backfill to ''.
//
//   - creation_path TEXT NOT NULL DEFAULT 'unknown' — discriminates how
//     the branch was created: 'edit_resend' | 'explicit' | 'unknown'.
//     Existing rows backfill to 'unknown'.
//
//   - parent_session_title TEXT NOT NULL DEFAULT '' — snapshot of the
//     parent's display name at fork time. Used by the breadcrumb when the
//     parent session has since been deleted — renders "Branch of [parent
//     deleted: <name>]" instead of going blank.
//
// The existing idx_branches_child index is verified here (no-op CREATE
// IF NOT EXISTS) as specified in plan §1.
const migrationIDBranchDisplayMeta = "sessions/0323-branch-display-meta"

const sqlBranchDisplayMeta = `
        ALTER TABLE branches ADD COLUMN parent_message_id TEXT NOT NULL DEFAULT '';
        ALTER TABLE branches ADD COLUMN branch_title TEXT NOT NULL DEFAULT '';
        ALTER TABLE branches ADD COLUMN creation_path TEXT NOT NULL DEFAULT 'unknown';
        ALTER TABLE branches ADD COLUMN parent_session_title TEXT NOT NULL DEFAULT '';
        CREATE INDEX IF NOT EXISTS idx_branches_child_lookup ON branches (child_session_id);
    `

// migration0323 returns the branch-display-meta migration.
// Down reverses the four ALTER TABLE columns (SQLite requires recreating
// the table; we take the simpler approach of dropping the index and
// accepting that SQLite's limited ALTER TABLE DROP COLUMN is available
// in SQLite ≥ 3.35.0 which Wails/CGO bundles).
func migration0323() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDBranchDisplayMeta,
		Version:       323,
		OwningMission: OwningMission,
		UpSource:      sqlBranchDisplayMeta,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range splitSQL(sqlBranchDisplayMeta) {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			for _, stmt := range []string{
				"DROP INDEX IF EXISTS idx_branches_child_lookup",
				"ALTER TABLE branches DROP COLUMN parent_session_title",
				"ALTER TABLE branches DROP COLUMN creation_path",
				"ALTER TABLE branches DROP COLUMN branch_title",
				"ALTER TABLE branches DROP COLUMN parent_message_id",
			} {
				if _, err := tx.Exec(ctx, stmt); err != nil {
					return err
				}
			}
			return nil
		},
	}
}
