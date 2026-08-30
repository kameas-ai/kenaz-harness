package sqlite_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"

	_ "modernc.org/sqlite"
)

// TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase pins
// the row-level contract of migration sessions/0337-repair-checkpoint-rows
// (chat-turn-integrity-01PMZ606 WP05, spec.md §5.3, owner ruling on
// escalation E-002, 2026-08-30: REPAIR).
//
// THE HAZARD. 0337 is a DELETE against session_messages on real,
// upgraded installs — CLAUDE.md blind spot #3's corollary applies
// directly: "a migration that has never run against populated tables
// has never been tested." This test drives the migration against
// core/storage/sqlite/testdata/upgrade/v0.64.0/dump.sql — a snapshot
// whose ledger stops at sessions/0335, genuinely predating both 0336
// (which stopped the pollution going forward) and 0337 (this repair) —
// so 0337 runs on THIS install for the first time, exactly the
// "previously-shipped schema" shape AC-PI-1/AC-PI-3 require, not a
// fresh empty database where the defect (and the fix) would be
// structurally invisible.
//
// THE THREE SHAPES (spec.md §5.3's discriminator). Seeded into a new
// session ("wp05-session-1") so the base snapshot's own seed rows
// (seed-session-1) are an independent, simultaneous proof that a
// healthy row with no streaming columns set is never touched:
//
//   - checkpoint-chain junk (all three conditions hold) -> DELETED.
//   - a genuine error-path partial, transient, nothing supersedes it
//     (condition 3 fails: no later row has it as a prefix) -> SURVIVES
//     byte-for-byte.
//   - a resumed partial, something points at it via continuation_of
//     (condition 2 fails) -> SURVIVES byte-for-byte.
//
// FALSIFIABILITY (the mission's proof requirement). This test was run
// against a deliberately weakened Up — checkpointRowsToDeleteForSession's
// loop body reduced to the naive one-condition predicate C-3 rejects
// (isCheckpointCondition1 alone; the condition-2 and condition-3 calls
// removed) — and went RED with BOTH:
//   migration_0337_test.go:232: read wp05-error-partial-1 after Open:
//     sql: no rows in result set (0 rows means it was incorrectly deleted)
//   migration_0337_test.go:238: read wp05-resumed-partial-1 after Open:
//     sql: no rows in result set (0 rows means it was incorrectly deleted)
// — i.e. exactly the two shapes conditions 2 and 3 exist to protect,
// both actually deleted. The mutation was reverted before landing; it
// must never be a code path this file can select at runtime, only a
// manual, temporary edit for the falsification run.
func TestMigration0337_RepairsCheckpointRowsAgainstUpgradedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	dir := t.TempDir()
	rawPath := filepath.Join(dir, "data.db")

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.64.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.64.0 dump.sql: %v", err)
	}

	raw := openRawSQLiteAt(t, rawPath)
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.64.0 snapshot: %v", err)
	}

	// A session and message rows the base snapshot does not carry —
	// materialised directly via SQL, the same way a previous release's
	// production code would have written them (not through the current
	// Store, which no longer writes checkpoint rows into session_messages
	// after migration 0336).
	seedStmts := []string{
		`INSERT INTO sessions
		    (id, name, created_at, updated_at, last_active_at, position, draft,
		     scroll_position, archived_at, system_prompt, context_kind, project_id,
		     auto_titled, branch_advisor_dismissed, autonomy_level, autonomy_overrides,
		     kind, last_usage_json, knobs_default, move_history_mode)
		 VALUES
		    ('wp05-session-1', 'WP05 repair probe session', 1700100000000, 1700100000000,
		     1700100000000, 5, '', 0, NULL, '', 'system', NULL,
		     0, 0, NULL, NULL, 'chat', NULL, NULL, NULL)`,

		// ---- Shape 1: checkpoint-chain junk. Condition 1 holds
		// (transient/recoverable/streaming_failed_at set), condition 2
		// holds (continuation_of NULL, nothing points at it), condition 3
		// holds (a later row's content has this content as a strict
		// prefix) -> DELETED.
		`INSERT INTO session_messages
		    (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
		     compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens,
		     cost_usd, cost_source, streaming_failed_at, streaming_failure_kind,
		     streaming_recoverable, continuation_of, knobs_override, kind, move_index,
		     turn_span_id, model_tool_args)
		 VALUES
		    ('wp05-checkpoint-junk-1', 'wp05-session-1', 10, 'assistant',
		     'The answer begins here and continues for a while',
		     NULL, 1700100001000, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
		     1700100001000, 'transient', 1, NULL, NULL, NULL, NULL, NULL, NULL)`,
		// The healthy row that supersedes the checkpoint above — no
		// streaming columns set, exactly the shape SessionWriteNode
		// writes for a clean close.
		`INSERT INTO session_messages
		    (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
		     compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens,
		     cost_usd, cost_source, streaming_failed_at, streaming_failure_kind,
		     streaming_recoverable, continuation_of, knobs_override, kind, move_index,
		     turn_span_id, model_tool_args)
		 VALUES
		    ('wp05-final-answer-1', 'wp05-session-1', 11, 'assistant',
		     'The answer begins here and continues for a while and now finishes with the rest of the completed answer.',
		     NULL, 1700100002000, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
		     NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL)`,

		// ---- Shape 2: a genuine error-path partial. Condition 1 holds
		// (same flags a checkpoint would carry — this is exactly the C-3
		// hazard), condition 2 holds (nothing points at it), but
		// condition 3 FAILS: no later row in the session has this
		// content as a prefix, because this really was the last thing
		// the turn produced before the connection dropped -> SURVIVES.
		`INSERT INTO session_messages
		    (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
		     compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens,
		     cost_usd, cost_source, streaming_failed_at, streaming_failure_kind,
		     streaming_recoverable, continuation_of, knobs_override, kind, move_index,
		     turn_span_id, model_tool_args)
		 VALUES
		    ('wp05-error-partial-1', 'wp05-session-1', 20, 'assistant',
		     'I was in the middle of answering when the connection dropped',
		     NULL, 1700100003000, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
		     1700100003000, 'transient', 1, NULL, NULL, NULL, NULL, NULL, NULL)`,

		// ---- Shape 3: a resumed partial. Condition 1 holds, but
		// condition 2 FAILS because wp05-resumed-continuation-1 points
		// back at it via continuation_of -> SURVIVES regardless of
		// condition 3.
		`INSERT INTO session_messages
		    (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
		     compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens,
		     cost_usd, cost_source, streaming_failed_at, streaming_failure_kind,
		     streaming_recoverable, continuation_of, knobs_override, kind, move_index,
		     turn_span_id, model_tool_args)
		 VALUES
		    ('wp05-resumed-partial-1', 'wp05-session-1', 30, 'assistant',
		     'Beginning of a resumed answer',
		     NULL, 1700100004000, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
		     1700100004000, 'transient', 1, NULL, NULL, NULL, NULL, NULL, NULL)`,
		`INSERT INTO session_messages
		    (id, session_id, sequence, role, content, tool_calls, created_at, content_json,
		     compacted_into_id, compacted_at, archived_at, prompt_tokens, completion_tokens,
		     cost_usd, cost_source, streaming_failed_at, streaming_failure_kind,
		     streaming_recoverable, continuation_of, knobs_override, kind, move_index,
		     turn_span_id, model_tool_args)
		 VALUES
		    ('wp05-resumed-continuation-1', 'wp05-session-1', 31, 'assistant',
		     'the rest of the resumed answer',
		     NULL, 1700100005000, NULL, NULL, NULL, NULL, NULL, NULL, NULL, NULL,
		     NULL, NULL, NULL, 'wp05-resumed-partial-1', NULL, NULL, NULL, NULL, NULL)`,
	}
	for _, stmt := range seedStmts {
		if _, err := raw.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}

	// Baseline for the "unrelated tables untouched" check (spec's
	// sessions/0327 precedent: a migration can silently empty a
	// CASCADE-linked child table while foreign_key_check/integrity_check
	// both stay clean). artifact_versions is the table that precedent
	// hit; session_messages rows in OTHER sessions (the base snapshot's
	// own seed-session-1) are the same class of check for THIS
	// migration's own target table.
	var artifactVersionsBefore, seedSessionMessagesBefore int
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM artifact_versions").Scan(&artifactVersionsBefore); err != nil {
		t.Fatalf("count artifact_versions before: %v", err)
	}
	if err := raw.QueryRowContext(ctx, "SELECT COUNT(*) FROM session_messages WHERE session_id='seed-session-1'").Scan(&seedSessionMessagesBefore); err != nil {
		t.Fatalf("count seed-session-1 messages before: %v", err)
	}
	if seedSessionMessagesBefore != 3 {
		t.Fatalf("seed-session-1 message count before Open = %d, want 3 (sanity on the base snapshot)", seedSessionMessagesBefore)
	}

	if err := raw.Close(); err != nil {
		t.Fatalf("close raw after seeding: %v", err)
	}

	// ---- Run the production Open path: applies 0336, then 0337 (this
	// migration), then everything else registered up to HEAD. ----
	db, err := storagesqlite.Open(newConfig(dir))
	if err != nil {
		t.Fatalf("Open on the seeded v0.64.0 snapshot failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	r := db.Reader()

	// ---- Ledger: 0337 actually applied. ----
	var ledgerAction string
	if err := r.QueryRow(ctx,
		"SELECT action FROM harness_migrations WHERE id = ? AND owning_mission = 'sessions'",
		"sessions/0337-repair-checkpoint-rows").Scan(&ledgerAction); err != nil {
		t.Fatalf("read ledger row for sessions/0337-repair-checkpoint-rows: %v", err)
	}
	if ledgerAction != "applied" {
		t.Errorf("sessions/0337-repair-checkpoint-rows ledger action = %q, want %q", ledgerAction, "applied")
	}

	// ---- Shape 1: checkpoint-chain junk row is GONE. ----
	var n int
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM session_messages WHERE id = 'wp05-checkpoint-junk-1'").Scan(&n); err != nil {
		t.Fatalf("count wp05-checkpoint-junk-1: %v", err)
	}
	if n != 0 {
		t.Errorf("wp05-checkpoint-junk-1 (checkpoint-chain junk) survived the repair, want deleted")
	}
	// The row that superseded it (a plain healthy row) must be
	// untouched — it was never a candidate (no streaming_* columns set).
	assertMessageSurvivesUnchanged(t, ctx, r, "wp05-final-answer-1",
		"The answer begins here and continues for a while and now finishes with the rest of the completed answer.",
		false, false, false, "")

	// ---- Shape 2: genuine error-path partial survives BYTE-FOR-BYTE.
	// This is the row a naive streaming_failure_kind='transient'
	// predicate (C-3, spec §1.7) would incorrectly delete. ----
	assertMessageSurvivesUnchanged(t, ctx, r, "wp05-error-partial-1",
		"I was in the middle of answering when the connection dropped",
		true, true, true, "transient")

	// ---- Shape 3: resumed partial survives BYTE-FOR-BYTE, and its
	// continuation row is untouched too. ----
	assertMessageSurvivesUnchanged(t, ctx, r, "wp05-resumed-partial-1",
		"Beginning of a resumed answer",
		true, true, true, "transient")
	var continuationOf sql.NullString
	if err := r.QueryRow(ctx, "SELECT continuation_of FROM session_messages WHERE id = 'wp05-resumed-continuation-1'").
		Scan(&continuationOf); err != nil {
		t.Fatalf("read wp05-resumed-continuation-1: %v", err)
	}
	if !continuationOf.Valid || continuationOf.String != "wp05-resumed-partial-1" {
		t.Errorf("wp05-resumed-continuation-1.continuation_of = %v, want wp05-resumed-partial-1", continuationOf)
	}

	// ---- Unrelated tables untouched (the sessions/0327 precedent: a
	// migration can empty a CASCADE-linked child table while both
	// integrity pragmas stay clean). ----
	var artifactVersionsAfter, seedSessionMessagesAfter int
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM artifact_versions").Scan(&artifactVersionsAfter); err != nil {
		t.Fatalf("count artifact_versions after: %v", err)
	}
	if artifactVersionsAfter != artifactVersionsBefore {
		t.Errorf("artifact_versions row count changed: %d -> %d (0337 must not touch unrelated tables)", artifactVersionsBefore, artifactVersionsAfter)
	}
	if err := r.QueryRow(ctx, "SELECT COUNT(*) FROM session_messages WHERE session_id='seed-session-1'").Scan(&seedSessionMessagesAfter); err != nil {
		t.Fatalf("count seed-session-1 messages after: %v", err)
	}
	if seedSessionMessagesAfter != seedSessionMessagesBefore {
		t.Errorf("seed-session-1 message count changed: %d -> %d (healthy rows with no streaming columns must never be touched)",
			seedSessionMessagesBefore, seedSessionMessagesAfter)
	}

	// ---- Secondary integrity check. ----
	rows, err := r.Query(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	var violations int
	for rows.Next() {
		violations++
	}
	_ = rows.Close()
	if violations > 0 {
		t.Errorf("PRAGMA foreign_key_check reported %d violation(s)", violations)
	}
}

// assertMessageSurvivesUnchanged reads a session_messages row by id and
// asserts its content and streaming_* columns match exactly what was
// seeded — "survives byte-for-byte" as a concrete assertion, not just a
// row-count check.
func assertMessageSurvivesUnchanged(t *testing.T, ctx context.Context, r storage.Reader, id, wantContent string, wantFailedAtSet, wantRecoverableSet, wantKindSet bool, wantKind string) {
	t.Helper()
	var content string
	var failedAt sql.NullInt64
	var recoverable sql.NullInt64
	var kind sql.NullString
	if err := r.QueryRow(ctx,
		"SELECT content, streaming_failed_at, streaming_recoverable, streaming_failure_kind FROM session_messages WHERE id = ?", id).
		Scan(&content, &failedAt, &recoverable, &kind); err != nil {
		t.Fatalf("read %s after Open: %v (0 rows means it was incorrectly deleted)", id, err)
	}
	if content != wantContent {
		t.Errorf("%s.content = %q, want %q (byte-for-byte)", id, content, wantContent)
	}
	if failedAt.Valid != wantFailedAtSet {
		t.Errorf("%s.streaming_failed_at set = %v, want %v", id, failedAt.Valid, wantFailedAtSet)
	}
	if recoverable.Valid != wantRecoverableSet || (wantRecoverableSet && recoverable.Int64 != 1) {
		t.Errorf("%s.streaming_recoverable = %+v, want valid=%v value=1", id, recoverable, wantRecoverableSet)
	}
	if kind.Valid != wantKindSet || (wantKindSet && kind.String != wantKind) {
		t.Errorf("%s.streaming_failure_kind = %+v, want valid=%v value=%q", id, kind, wantKindSet, wantKind)
	}
}
