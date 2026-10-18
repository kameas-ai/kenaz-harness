package labels

// push_source_test.go — real-sqlite proofs for the WP14 revision column
// and push cursor (migration laya-advisors/1601-advice-labels-revision):
// the revision is a table-global monotonic change counter, an action
// change re-versions the row, and the cursor round-trips durably.

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/advice"
)

func pushTestRow(hash, session string, ts int64) Row {
	return Row{
		KindID: "branch_now", PromptVersion: "v1", FeaturesHash: hash,
		FeaturesJSON: `{"a":1}`, FeaturesComplete: true, ModelID: "m", Rung: "heuristic",
		Decision: true, Confidence: 80, Shown: true, UserAction: ActionIgnored,
		SessionID: session, CreatedAt: time.UnixMilli(ts).UTC(),
	}
}

func TestRevision_InsertsAreMonotonicAndActionChangeReversions(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	for i, h := range []string{"h1", "h2", "h3"} {
		if err := store.Insert(ctx, pushTestRow(h, "s", int64(1000+i))); err != nil {
			t.Fatalf("insert %s: %v", h, err)
		}
	}
	rows, err := store.PendingSince(ctx, "branch_now", 0, 10)
	if err != nil || len(rows) != 3 {
		t.Fatalf("PendingSince = %d rows, err %v; want 3", len(rows), err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Revision <= rows[i-1].Revision {
			t.Fatalf("revisions not strictly ascending: %+v", rows)
		}
	}
	firstRev := rows[0].Revision
	maxRev := rows[2].Revision

	// An action change on the OLDEST row must take a revision above every
	// existing one — that is what lets one cursor find it again.
	if err := store.UpdateAction(ctx, "s", "branch_now", "h1", ActionDismissed); err != nil {
		t.Fatalf("UpdateAction: %v", err)
	}
	after, err := store.PendingSince(ctx, "branch_now", maxRev, 10)
	if err != nil || len(after) != 1 {
		t.Fatalf("rows past the old max revision = %d, err %v; want exactly the updated row", len(after), err)
	}
	if after[0].FeaturesHash != "h1" || after[0].UserAction != ActionDismissed || after[0].Revision <= firstRev {
		t.Fatalf("updated row = %+v (old revision %d)", after[0], firstRev)
	}
	if after[0].TS != 1000 {
		t.Errorf("updated row ts = %d, want the ORIGINAL created_at 1000 (upsert key is (client, kind, features_hash, ts))", after[0].TS)
	}

	// Re-recording the action the row already carries is not a change.
	if err := store.UpdateAction(ctx, "s", "branch_now", "h1", ActionDismissed); err != nil {
		t.Fatalf("UpdateAction (same action): %v", err)
	}
	again, _ := store.PendingSince(ctx, "branch_now", after[0].Revision, 10)
	if len(again) != 0 {
		t.Errorf("re-recording the same action bumped the revision (%d rows pending)", len(again))
	}

	// A second, different action bumps again.
	if err := store.UpdateAction(ctx, "s", "branch_now", "h1", ActionAccepted); err != nil {
		t.Fatalf("UpdateAction (changed): %v", err)
	}
	third, _ := store.PendingSince(ctx, "branch_now", after[0].Revision, 10)
	if len(third) != 1 || third[0].Revision <= after[0].Revision {
		t.Errorf("second action change did not re-version the row: %+v", third)
	}
}

func TestPushCursor_RoundTripAndReset(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	c, err := store.LoadCursor(ctx, "sidecar", "branch_now")
	if err != nil || (c != PushCursor{}) {
		t.Fatalf("fresh cursor = %+v, err %v; want zero", c, err)
	}
	if err := store.SaveCursor(ctx, "sidecar", "branch_now", PushCursor{TS: 50, Revision: 7}); err != nil {
		t.Fatalf("SaveCursor: %v", err)
	}
	if err := store.SaveCursor(ctx, "sidecar", "branch_now", PushCursor{TS: 60, Revision: 9}); err != nil {
		t.Fatalf("SaveCursor (upsert): %v", err)
	}
	if err := store.SaveCursor(ctx, "other", "branch_now", PushCursor{TS: 1, Revision: 1}); err != nil {
		t.Fatalf("SaveCursor other sink: %v", err)
	}
	c, _ = store.LoadCursor(ctx, "sidecar", "branch_now")
	if c != (PushCursor{TS: 60, Revision: 9}) {
		t.Fatalf("cursor = %+v, want {60 9}", c)
	}
	if err := store.ResetCursors(ctx, "sidecar"); err != nil {
		t.Fatalf("ResetCursors: %v", err)
	}
	c, _ = store.LoadCursor(ctx, "sidecar", "branch_now")
	if (c != PushCursor{}) {
		t.Errorf("cursor after reset = %+v, want zero (full re-push from zero)", c)
	}
	o, _ := store.LoadCursor(ctx, "other", "branch_now")
	if o.Revision != 1 {
		t.Errorf("reset of sink 'sidecar' clobbered sink 'other': %+v", o)
	}
}

// TestMigration1601Backfill_PopulatedTable applies 1601's DDL to a table
// that already holds 1600-era rows (no revision column) and asserts the
// backfill: revision = id, so the counter stays monotonic and the next
// insert lands above every backfilled row.
func TestMigration1601Backfill_PopulatedTable(t *testing.T) {
	name := strings.NewReplacer("/", "_").Replace(t.Name())
	db, err := sql.Open("sqlite", "file:labels_"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range splitAdviceLabelsSQL(sqlAdviceLabelsInit) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("1600 DDL: %v", err)
		}
	}
	for i := 0; i < 3; i++ {
		if _, err := db.Exec(`INSERT INTO advice_labels
		  (kind, prompt_version, features_hash, model_id, rung, decision, confidence, shown, session_id, created_at)
		  VALUES ('branch_now','v1',?, 'm','heuristic',1,80,1,'s',?)`, "h"+string(rune('a'+i)), 1000+i); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, stmt := range splitAdviceLabelsSQL(sqlAdviceLabelsRevision) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("1601 DDL: %v\n%s", err, stmt)
		}
	}
	var mismatched int
	if err := db.QueryRow(`SELECT COUNT(*) FROM advice_labels WHERE revision <> id OR revision = 0`).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 {
		t.Errorf("%d backfilled rows have revision != id", mismatched)
	}
	if err := NewSQLStore(db).Insert(context.Background(), pushTestRow("hz", "s", 2000)); err != nil {
		t.Fatal(err)
	}
	var newRev int64
	if err := db.QueryRow(`SELECT revision FROM advice_labels WHERE features_hash = 'hz'`).Scan(&newRev); err != nil {
		t.Fatal(err)
	}
	if newRev != 4 {
		t.Errorf("post-backfill insert revision = %d, want 4 (above every backfilled row)", newRev)
	}
}

func TestCaptureAdvisor_AfterWrite_FiresOnWritesOnlyWhenCaptureOn(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	on := true
	fired := 0
	inner := advice.NewFakeAdvisor()
	c := NewCaptureAdvisor(inner, store, func() bool { return on }, WithAfterWrite(func() { fired++ }))
	kind := advice.AdviceKind{ID: "branch_now", PromptVersion: "v1"}
	inner.ScriptDefault(advice.Recommendation{Decision: true, Confidence: 90, KindID: "branch_now", PromptVersion: "v1", Model: "m", Rung: advice.RungHeuristic})

	ctx := context.Background()
	sess := advice.SessionContext{SessionID: "s"}
	if _, err := c.Recommend(ctx, kind, "f1", sess); err != nil {
		t.Fatalf("Recommend: %v", err)
	}
	if fired != 1 {
		t.Fatalf("afterWrite fired %d times after a captured Recommend, want 1", fired)
	}
	c.RecordAction(ctx, sess, kind, "f1", ActionAccepted)
	if fired != 2 {
		t.Fatalf("afterWrite fired %d times after RecordAction, want 2", fired)
	}
	on = false
	if _, err := c.Recommend(ctx, kind, "f2", sess); err != nil {
		t.Fatalf("Recommend (capture off): %v", err)
	}
	c.RecordAction(ctx, sess, kind, "f2", ActionAccepted)
	if fired != 2 {
		t.Errorf("afterWrite fired with capture OFF (%d total, want still 2)", fired)
	}
}
