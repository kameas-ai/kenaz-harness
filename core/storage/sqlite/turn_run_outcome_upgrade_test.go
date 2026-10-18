package sqlite_test

// turn_run_outcome_upgrade_test.go — undelivered-message-retry
// (dogfood 2026-10-07), migration sessions/0344-turn-run-outcome.
//
// 0344 adds outcome / delivered / failure_* / finished_at to
// session_turn_runs. Per CLAUDE.md blind spot #3 a migration that has
// never run against populated tables has never been tested, so this
// starts from the v0.92.0 snapshot (the newest committed release state,
// which predates 0344), plants a pre-0344 turn-run row exactly as v0.92.0
// wrote it, and only then opens under HEAD.
//
// Asserts:
//   - 0344 applies on the populated database (nothing pending after Open);
//   - the pre-existing row survives and reads as outcome "" / not
//     delivered / no failure — the "unknown" state the chat surface
//     renders as nothing, NEVER as NOT DELIVERED (an old turn must not
//     suddenly claim it failed to send);
//   - a new outcome round-trips through SQL on the upgraded table, and
//     RecordTurnRunOutcome on a run id that was never recorded is a no-op.
//
// Falsifiable: drop migration0344 from Migrations() and ListTurnRuns
// fails (no such column); make the old row default to delivered=0 with
// outcome "failed" and the "unknown" assertion fails.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage/sqlite/upgradesnap"
)

func TestTurnRunOutcome_UpgradesPopulatedV0920Database(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()

	dumpText, err := os.ReadFile(filepath.Join("testdata", "upgrade", "v0.92.0", "dump.sql"))
	if err != nil {
		t.Fatalf("read v0.92.0 fixture: %v", err)
	}
	raw := openRawSQLiteAt(t, filepath.Join(dir, "data.db"))
	if err := upgradesnap.Materialize(ctx, raw, string(dumpText)); err != nil {
		t.Fatalf("materialise v0.92.0 snapshot: %v", err)
	}
	if columnExists(t, raw, "session_turn_runs", "outcome") {
		t.Fatal("v0.92.0 snapshot already has session_turn_runs.outcome — the fixture no longer predates 0344")
	}
	var sid string
	if err := raw.QueryRowContext(ctx, "SELECT id FROM sessions ORDER BY id LIMIT 1").Scan(&sid); err != nil {
		t.Fatalf("pick a seeded session: %v", err)
	}
	// A run exactly as v0.92.0 recorded it: the 0342 columns only.
	if _, err := raw.ExecContext(ctx, `INSERT INTO session_turn_runs
		(run_id, session_id, turn_span_id, graph_id, spec_digest, created_at)
		VALUES ('chat-old', ?, 'msg-old', 'chat_default', 'sha256:old', 1)`, sid); err != nil {
		t.Fatalf("plant pre-0344 turn run: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close raw: %v", err)
	}

	db := mustOpen(t, dir)
	t.Cleanup(func() { _ = db.Close(ctx) })
	if pending, err := db.Migrations().Pending(); err != nil || len(pending) != 0 {
		t.Fatalf("Pending after Open = %v (err %v), want none — sessions/0344 must have applied", pending, err)
	}

	mgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	runs, err := mgr.ListTurnRuns(ctx, sid)
	if err != nil {
		t.Fatalf("ListTurnRuns on the upgraded table: %v", err)
	}
	if len(runs) != 1 || runs[0].RunID != "chat-old" {
		t.Fatalf("pre-0344 row did not survive: %+v", runs)
	}
	if got := runs[0].Outcome; got != (session.TurnRunOutcome{}) {
		t.Errorf("pre-0344 run reads outcome %+v, want the zero (unknown) outcome", got)
	}

	// A new run on the upgraded table: record, fail undelivered, read back.
	if err := mgr.RecordTurnRun(ctx, sid, "msg-new", "chat-new", "chat_default", "sha256:new"); err != nil {
		t.Fatalf("RecordTurnRun: %v", err)
	}
	want := session.TurnRunOutcome{
		Outcome:         session.TurnOutcomeFailed,
		Delivered:       false,
		FailureClass:    "user_actionable",
		FailureCode:     "payment_required",
		FailureStatus:   402,
		FailureProvider: "openrouter",
		FailureSummary:  "Out of credits with OpenRouter",
		FailureMessage:  "This request requires more credits.",
	}
	if err := mgr.RecordTurnOutcome(ctx, sid, "chat-new", want); err != nil {
		t.Fatalf("RecordTurnOutcome: %v", err)
	}
	if err := mgr.RecordTurnOutcome(ctx, sid, "chat-never-recorded", want); err != nil {
		t.Fatalf("RecordTurnOutcome on an unrecorded run must be a no-op, got %v", err)
	}
	runs, err = mgr.ListTurnRuns(ctx, sid)
	if err != nil || len(runs) != 2 {
		t.Fatalf("ListTurnRuns after record = %+v (err %v), want 2 rows (no row invented for the unrecorded run)", runs, err)
	}
	var got session.TurnRun
	for _, r := range runs {
		if r.RunID == "chat-new" {
			got = r
		}
	}
	if got.Outcome.FinishedAt.IsZero() {
		t.Error("finished_at not stamped")
	}
	got.Outcome.FinishedAt = want.FinishedAt
	if got.Outcome != want {
		t.Errorf("outcome round-trip = %+v, want %+v", got.Outcome, want)
	}

	// A later delivered completion of the same turn (the Retry).
	if err := mgr.RecordTurnRun(ctx, sid, "msg-new", "chat-retry", "chat_default", "sha256:new"); err != nil {
		t.Fatalf("RecordTurnRun (retry): %v", err)
	}
	if err := mgr.RecordTurnOutcome(ctx, sid, "chat-retry", session.TurnRunOutcome{Outcome: session.TurnOutcomeCompleted, Delivered: true}); err != nil {
		t.Fatalf("RecordTurnOutcome (retry): %v", err)
	}
	runs, _ = mgr.ListTurnRuns(ctx, sid)
	for _, r := range runs {
		if r.RunID == "chat-retry" && (!r.Outcome.Delivered || r.Outcome.Outcome != session.TurnOutcomeCompleted) {
			t.Errorf("retry outcome = %+v", r.Outcome)
		}
	}
}
