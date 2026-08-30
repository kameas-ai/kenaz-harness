package session

import (
	"context"
	"database/sql"
	"sort"
	"strings"

	"github.com/kameas-ai/kenaz-harness/core/storage/migrations"
)

// migrationIDCheckpointRepair identifies migration 0337 — the repair
// migration for session_messages rows polluted by the periodic-flush
// checkpoint mechanism BEFORE migration 0336 relocated that durability
// seam into the dedicated stream_checkpoints table
// (chat-turn-integrity-01PMZ606 WP02/WP03, migrations_stream_checkpoints.go).
// Every install that ran a long streaming turn on a build between
// v0.59.0 (when the periodic flush started writing checkpoints into the
// transcript) and v0.65.0 (when 0336 stopped it) may carry these junk
// rows: up to six copies of the same answer, at ever-increasing length,
// interleaved into session_messages as if they were real turns.
//
// THIS MIGRATION IS DESTRUCTIVE (DELETE) AGAINST REAL USER ROWS. Owner
// ruling on escalation E-002 (spec.md §12, 2026-08-30): REPAIR. The
// naive predicate — "delete every row with
// streaming_failure_kind='transient'" — was REJECTED as C-3 records
// (migrations.go's sibling doc, spec.md §1.7): classifyPartialFailureKind
// (core/rpc/views/agentgraph/chat/chat_runner.go) returns "transient"
// for legitimate network/stream errors too, and those partials are
// EXACTLY what the Resume affordance exists to recover. Deleting them
// would destroy real user-visible history, not just checkpoint noise.
//
// THE DISCRIMINATOR (spec.md §5.3). Within one session, ordered by
// sequence, a row R is a periodic-flush checkpoint — and therefore
// eligible for deletion — iff ALL of:
//
//  1. R.role = 'assistant', R.streaming_failed_at IS NOT NULL,
//     R.streaming_recoverable = 1, R.streaming_failure_kind = 'transient'.
//  2. R.continuation_of IS NULL, AND no row in the session has
//     continuation_of = R.id — a partial the user actually resumed is
//     user-visible history (the Resume RPC's continuation row points
//     back at it) and is never touched, regardless of condition 1.
//  3. There exists a LATER row S in the same session
//     (S.sequence > R.sequence, S.role = 'assistant') whose content has
//     R.content as a STRICT prefix (len(S.content) > len(R.content) and
//     S.content starts with R.content byte-for-byte).
//
// CONDITION 3 IS THE SAFETY ARGUMENT, NOT AN OPTIMISATION. A genuine
// error-path partial is the LAST thing its turn produced — by
// construction nothing later supersedes it by prefix, so condition 3
// can never spuriously hold for it. A checkpoint, by contrast, is
// always a strict prefix of the next checkpoint or of the final healthy
// answer that superseded it. Weakening this migration to conditions 1+2
// only (or to condition 1 alone) deletes real user data — falsified by
// hand against
// TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase
// (core/storage/sqlite/migration_0337_test.go): with conditions 2 and 3
// removed from the loop, that test goes RED because BOTH the genuine
// error-path partial and the resumed partial are deleted. See that
// test's doc comment for the pasted failure.
//
// THE BOUND (spec.md §5.3: "the WP states the bound"). SQLite has no
// cheap prefix-join (no index makes "does any later row start with
// this row's text" a set operation), so this migration is NOT one
// set-based DELETE. It runs as a bounded PER-SESSION scan in Go:
//
//   - Step 1 finds the (typically small) set of session_ids that
//     contain at least one condition-1 candidate — one indexed-ish
//     scan of session_messages, no per-row Go work.
//   - Step 2 processes those sessions ONE AT A TIME. For each session
//     it loads only that session's assistant rows (id, sequence,
//     content, continuation_of, streaming_* columns) into memory —
//     peak additional memory is O(A) content strings, where A is the
//     assistant-row count of the SINGLE largest session being
//     processed, never O(whole table). No other session's rows are
//     held at the same time.
//   - Within a session, checking condition 3 for each candidate is a
//     forward scan over later assistant rows, so the worst-case time
//     per session is O(A²) string-prefix comparisons. This is the
//     bound: sessions in this product are user chat transcripts, not
//     bulk data — A is bounded in practice by ordinary conversation
//     length, and the migration runs exactly once, at upgrade time.
//
// Down is a best-effort no-op: a DELETE has no inverse without a
// pre-migration backup, and per the package-wide Down convention
// (migrations.Registry.Rollback has no production caller — see
// scripts/ci/check-destructive-migration-coverage.sh's doc comment)
// this migration follows 0327/0332's precedent of not implementing one.
//
// Numbering: 0337, reserved for this exact repair by
// docs/v0.65.0-merge-order.md §4 and by the doc comment on migration
// 0336 (migrations_stream_checkpoints.go) — do not renumber.
const migrationIDCheckpointRepair = "sessions/0337-repair-checkpoint-rows"

// sqlCheckpointRepairUpSource is the migration's content-hash source
// (migrations.Register / HashSQL). This migration's Up is procedural Go,
// not a literal SQL script, but UpSource still needs stable text to hash
// — this comment IS that text, spelling out the discriminator so the
// hash changes if and only if the discriminator's meaning changes.
// DO NOT reword casually once this migration ships: every install that
// applies 0337 records HashSQL(this string) in its ledger, and changing
// the text after release trips ErrLedgerHashMismatch on every one of
// them (spec.md §4).
const sqlCheckpointRepairUpSource = `
-- sessions/0337-repair-checkpoint-rows
-- DELETE FROM session_messages a row R iff ALL of:
--   1. R.role='assistant' AND R.streaming_failed_at IS NOT NULL AND
--      R.streaming_recoverable=1 AND R.streaming_failure_kind='transient'
--   2. R.continuation_of IS NULL AND no row has continuation_of=R.id
--   3. a later same-session assistant row S exists (S.sequence >
--      R.sequence) whose content has R.content as a strict prefix.
-- Executed procedurally (bounded per-session scan) because SQLite has
-- no cheap prefix-join; see migrations_checkpoint_repair.go.
`

func migration0337() migrations.Migration {
	return migrations.Migration{
		ID:            migrationIDCheckpointRepair,
		Version:       337,
		OwningMission: OwningMission,
		UpSource:      sqlCheckpointRepairUpSource,
		Up:            repairCheckpointRows,
		Down: func(ctx context.Context, tx migrations.WriteTx) error {
			// Best-effort no-op — see doc comment above.
			return nil
		},
	}
}

// checkpointCandidateRow is one assistant row loaded for a single
// session's bounded scan.
type checkpointCandidateRow struct {
	id                    string
	sequence              int64
	content               string
	continuationOf        sql.NullString
	streamingFailedAt     sql.NullInt64
	streamingRecoverable  sql.NullInt64
	streamingFailureKind  sql.NullString
}

// repairCheckpointRows is migration 0337's Up. See the package doc
// comment above (migrationIDCheckpointRepair) for the discriminator and
// the bound.
func repairCheckpointRows(ctx context.Context, tx migrations.WriteTx) error {
	sessionIDs, err := checkpointCandidateSessionIDs(ctx, tx)
	if err != nil {
		return err
	}
	for _, sessionID := range sessionIDs {
		toDelete, err := checkpointRowsToDeleteForSession(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		for _, id := range toDelete {
			if _, err := tx.Exec(ctx, "DELETE FROM session_messages WHERE id = ?", id); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkpointCandidateSessionIDs returns every session_id that contains
// at least one row matching the discriminator's condition 1. This is
// the one full-table scan the migration performs; everything after it
// is scoped to a single session at a time.
func checkpointCandidateSessionIDs(ctx context.Context, tx migrations.WriteTx) ([]string, error) {
	rows, err := tx.Query(ctx, `
        SELECT DISTINCT session_id FROM session_messages
         WHERE role = 'assistant'
           AND streaming_failed_at IS NOT NULL
           AND streaming_recoverable = 1
           AND streaming_failure_kind = 'transient'
         ORDER BY session_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, err
		}
		out = append(out, sessionID)
	}
	return out, rows.Err()
}

// checkpointRowsToDeleteForSession applies the full three-condition
// discriminator to a single session and returns the ids of rows that
// qualify for deletion. This is the "bounded per-session scan": it
// loads only this session's assistant rows and this session's set of
// continuation_of targets, does its work, and returns — nothing from
// this call is retained for the next session.
func checkpointRowsToDeleteForSession(ctx context.Context, tx migrations.WriteTx, sessionID string) ([]string, error) {
	referenced, err := checkpointReferencedContinuationTargets(ctx, tx, sessionID)
	if err != nil {
		return nil, err
	}

	rows, err := tx.Query(ctx, `
        SELECT id, sequence, content, continuation_of,
               streaming_failed_at, streaming_recoverable, streaming_failure_kind
          FROM session_messages
         WHERE session_id = ? AND role = 'assistant'
         ORDER BY sequence, id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var all []checkpointCandidateRow
	for rows.Next() {
		var r checkpointCandidateRow
		if err := rows.Scan(&r.id, &r.sequence, &r.content, &r.continuationOf,
			&r.streamingFailedAt, &r.streamingRecoverable, &r.streamingFailureKind); err != nil {
			return nil, err
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Defensive: the query already ORDERs BY sequence, but guarantee it
	// here too so the forward scan below (which relies on ascending
	// sequence to treat "later index" as "later sequence") holds even if
	// a future caller reorders the query.
	sort.SliceStable(all, func(i, j int) bool { return all[i].sequence < all[j].sequence })

	var toDelete []string
	for i, r := range all {
		if !isCheckpointCondition1(r) {
			continue
		}
		if !isCheckpointCondition2(r, referenced) {
			continue
		}
		if hasSupersedingRow(all, i) {
			toDelete = append(toDelete, r.id)
		}
	}
	return toDelete, nil
}

// checkpointReferencedContinuationTargets returns the set of message
// ids that some row in this session points at via continuation_of — the
// second half of condition 2. Checked across every row in the session
// (any role), not just assistant rows, since continuation_of is a
// pointer column that could in principle appear anywhere; in practice
// production only ever sets it on assistant rows (core/session/store.go
// AppendContinuation), but the discriminator's text ("no row has
// continuation_of = R.id") does not restrict by role and neither does
// this query.
func checkpointReferencedContinuationTargets(ctx context.Context, tx migrations.WriteTx, sessionID string) (map[string]bool, error) {
	rows, err := tx.Query(ctx, `
        SELECT continuation_of FROM session_messages
         WHERE session_id = ? AND continuation_of IS NOT NULL`, sessionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var target string
		if err := rows.Scan(&target); err != nil {
			return nil, err
		}
		out[target] = true
	}
	return out, rows.Err()
}

// isCheckpointCondition1 is discriminator condition 1: the row is
// flagged exactly as a resumable, transient streaming failure.
func isCheckpointCondition1(r checkpointCandidateRow) bool {
	return r.streamingFailedAt.Valid &&
		r.streamingRecoverable.Valid && r.streamingRecoverable.Int64 == 1 &&
		r.streamingFailureKind.Valid && r.streamingFailureKind.String == "transient"
}

// isCheckpointCondition2 is discriminator condition 2: the row is not
// itself a continuation, and nothing continues it. Either half failing
// means this row is user-visible resume history and must never be
// touched — this is checked independently of, and before, condition 3.
func isCheckpointCondition2(r checkpointCandidateRow, referenced map[string]bool) bool {
	if r.continuationOf.Valid {
		return false
	}
	return !referenced[r.id]
}

// hasSupersedingRow is discriminator condition 3: does a later
// same-session assistant row exist whose content has all[i]'s content
// as a strict prefix. all is ordered ascending by sequence, so indices
// after i are exactly the later rows. This is the O(A) forward scan
// that makes the whole per-session pass O(A^2) in the worst case — see
// the bound documented on migrationIDCheckpointRepair.
func hasSupersedingRow(all []checkpointCandidateRow, i int) bool {
	r := all[i]
	for j := i + 1; j < len(all); j++ {
		s := all[j]
		if s.sequence <= r.sequence {
			continue
		}
		if len(s.content) > len(r.content) && strings.HasPrefix(s.content, r.content) {
			return true
		}
	}
	return false
}
