package session

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDStreamCheckpoints is the identifier for migration 0336.
//
// This migration adds the stream_checkpoints table that backs the
// periodic-flush durability seam (chat-turn-integrity-01PMZ606 WP02 +
// WP03). One row per (session_id, sub_id): runPeriodicFlush upserts it
// every ~10s while a stream is live, so write amplification stays
// bounded to one row per subscription instead of one INSERT into
// session_messages per tick — the P0 this mission exists to fix
// (spec.md §1.1).
//
// The row is a durability checkpoint, not a transcript entry:
//   - On a clean close ("completed" / "stop-called"), driveRun deletes
//     the checkpoint — the real answer was already persisted by
//     SessionWriteNode (or nothing needs recovering, for a user Stop).
//   - On an error close ("backend-error"), the existing PartialPersister
//     path lands the one partial session_messages row first, then the
//     checkpoint is deleted — recovery keeps exactly one source of
//     truth.
//   - On a crash / kill, the checkpoint row survives and is durable —
//     promoting it into a resumable partial on the next boot is
//     deliberately out of scope for this mission (E-006).
//
// ON DELETE CASCADE from sessions mirrors session_messages
// (sqlInitSchema above): the existing session-delete path
// (sqlStore.Delete) is a plain `DELETE FROM sessions WHERE id = ?` that
// already relies on FK cascade to clear session_messages, so a
// checkpoint that outlives its session is exactly the same class of
// orphan that pattern already prevents.
//
// Numbering: 0336 — allocated to chat-turn-integrity-01PMZ606 in
// docs/v0.65.0-merge-order.md §4. sessions/0338-0340 are reserved for
// model-scheduled-jobs-01PMSJ01; do not renumber into that range.
const migrationIDStreamCheckpoints = "sessions/0336-stream-checkpoints"

const sqlStreamCheckpointsSchema = `
        CREATE TABLE IF NOT EXISTS stream_checkpoints (
            session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
            sub_id      TEXT NOT NULL,
            text        TEXT NOT NULL,
            has_tool    INTEGER NOT NULL DEFAULT 0,
            updated_at  INTEGER NOT NULL,
            PRIMARY KEY (session_id, sub_id)
        );
    `

// migration0336 returns the stream-checkpoints table migration. Down
// drops the table — the crash-recovery window it closes reopens, but
// no other table's shape depends on it (it is never referenced by a
// foreign key).
func migration0336() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDStreamCheckpoints,
		Version:       336,
		OwningMission: OwningMission,
		UpSource:      sqlStreamCheckpointsSchema,
		Up: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, sqlStreamCheckpointsSchema)
			return err
		},
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			_, err := tx.Exec(ctx, "DROP TABLE IF EXISTS stream_checkpoints")
			return err
		},
	}
}
