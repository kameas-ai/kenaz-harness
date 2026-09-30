package labels

// store_test.go drives REAL sqlite (modernc.org/sqlite, no CGo) against
// this package's own migration DDL (sqlAdviceLabelsInit, migrations.go)
// — per CLAUDE.md blind spot #2, a persistence assertion built on a
// hand-rolled in-memory fixture would not prove the SQL round-trips.
// This is a leaf-package test (no core/storage dependency): it opens the
// DDL directly rather than going through the full migration registry,
// which core/storage/sqlite/upgrade_path_test.go already exercises end
// to end (assertAdviceLabelsTableMigrated, against every upgrade
// snapshot including v0.83.0's live `units` rows).

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	for _, stmt := range strings.Split(sqlAdviceLabelsInit, ";") {
		s := strings.TrimSpace(stmt)
		if s == "" {
			continue
		}
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("exec migration DDL: %v\nstmt: %s", err, s)
		}
	}
	return db
}

func TestSQLStore_InsertAndQuery_RealSQLite(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	row := Row{
		KindID:           "branch_now",
		PromptVersion:    "v1",
		FeaturesHash:     "hash-1",
		FeaturesJSON:     `{"turns_since_session_start":3}`,
		FeaturesComplete: true,
		ModelID:          "heuristic/branch-regex-v1",
		Rung:             "heuristic",
		Decision:         true,
		Confidence:       82,
		Shown:            true,
		UserAction:       ActionIgnored,
		LatencyMS:        5,
		SessionID:        "sess-1",
		CreatedAt:        time.Unix(1700000000, 0).UTC(),
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var (
		gotKind     string
		gotDecide   int
		gotShown    int
		gotComplete int
		gotAction   string
		gotSession  string
	)
	err := db.QueryRowContext(ctx,
		`SELECT kind, decision, shown, features_complete, user_action, session_id FROM advice_labels WHERE features_hash = ?`,
		"hash-1").Scan(&gotKind, &gotDecide, &gotShown, &gotComplete, &gotAction, &gotSession)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if gotKind != "branch_now" || gotDecide != 1 || gotShown != 1 || gotComplete != 1 || gotAction != "ignored" || gotSession != "sess-1" {
		t.Errorf("row mismatch: kind=%s decision=%d shown=%d features_complete=%d action=%s session=%s",
			gotKind, gotDecide, gotShown, gotComplete, gotAction, gotSession)
	}
}

// TestSQLStore_FeaturesComplete_RoundTrips is the placeholder-row
// discriminator's persistence proof (review promotion, laya-advisors-
// 01LAYA001 WP07/WP08 review round, 2026-09-29): both polarities of
// features_complete round-trip through real sqlite distinctly, and the
// column DEFAULT (1, matching the safe "complete" default) is what an
// explicit false actually overrides — a schema bug that silently ignored
// the column, or that always wrote 1, would pass a test that only checked
// the true case.
func TestSQLStore_FeaturesComplete_RoundTrips(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	complete := Row{
		KindID: "branch_now", PromptVersion: "v1", FeaturesHash: "hash-complete",
		FeaturesJSON: "{}", FeaturesComplete: true, ModelID: "heuristic/branch-regex-v1",
		Rung: "heuristic", Decision: true, Confidence: 80, SessionID: "sess-fc",
	}
	incomplete := Row{
		KindID: "compact_now", PromptVersion: "v1", FeaturesHash: "hash-incomplete",
		FeaturesJSON: "{}", FeaturesComplete: false, ModelID: "heuristic/compact-fill-threshold-v1",
		Rung: "heuristic", Decision: false, Confidence: 0, SessionID: "sess-fc",
	}
	if err := store.Insert(ctx, complete); err != nil {
		t.Fatalf("insert complete: %v", err)
	}
	if err := store.Insert(ctx, incomplete); err != nil {
		t.Fatalf("insert incomplete: %v", err)
	}

	var got int
	if err := db.QueryRowContext(ctx, `SELECT features_complete FROM advice_labels WHERE features_hash = ?`, "hash-complete").Scan(&got); err != nil {
		t.Fatalf("query complete row: %v", err)
	}
	if got != 1 {
		t.Errorf("features_complete for the complete row = %d, want 1", got)
	}
	if err := db.QueryRowContext(ctx, `SELECT features_complete FROM advice_labels WHERE features_hash = ?`, "hash-incomplete").Scan(&got); err != nil {
		t.Fatalf("query incomplete row: %v", err)
	}
	if got != 0 {
		t.Errorf("features_complete for the incomplete row = %d, want 0", got)
	}
}

func TestSQLStore_Insert_InvalidUserAction_Rejected(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	err := store.Insert(context.Background(), Row{
		KindID:     "branch_now",
		SessionID:  "sess-1",
		UserAction: "not-a-real-action",
	})
	if err == nil {
		t.Fatal("Insert with invalid UserAction: want error, got nil")
	}
}

func TestSQLStore_UpdateAction_UpdatesMostRecentMatchingRow(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	ctx := context.Background()

	base := Row{
		KindID:        "branch_now",
		PromptVersion: "v1",
		FeaturesHash:  "hash-x",
		FeaturesJSON:  "{}",
		ModelID:       "heuristic/branch-regex-v1",
		Rung:          "heuristic",
		Decision:      true,
		Confidence:    90,
		SessionID:     "sess-A",
	}
	// Two rows sharing the same (session, kind, features_hash) key —
	// the "most recent" tie-break must pick the SECOND one.
	if err := store.Insert(ctx, base); err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	if err := store.Insert(ctx, base); err != nil {
		t.Fatalf("insert 2: %v", err)
	}
	if err := store.UpdateAction(ctx, "sess-A", "branch_now", "hash-x", ActionAccepted); err != nil {
		t.Fatalf("UpdateAction: %v", err)
	}

	rows, err := db.QueryContext(ctx, `SELECT id, user_action FROM advice_labels WHERE session_id = ? ORDER BY id ASC`, "sess-A")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var id int64
		var action string
		if err := rows.Scan(&id, &action); err != nil {
			t.Fatalf("scan: %v", err)
		}
		actions = append(actions, action)
	}
	if len(actions) != 2 {
		t.Fatalf("row count = %d, want 2", len(actions))
	}
	if actions[0] != string(ActionIgnored) {
		t.Errorf("first (older) row user_action = %q, want %q (unaffected)", actions[0], ActionIgnored)
	}
	if actions[1] != string(ActionAccepted) {
		t.Errorf("second (most recent) row user_action = %q, want %q", actions[1], ActionAccepted)
	}
}

func TestSQLStore_UpdateAction_NoMatchingRow_IsNoop(t *testing.T) {
	db := newTestDB(t)
	store := NewSQLStore(db)
	if err := store.UpdateAction(context.Background(), "no-such-session", "branch_now", "no-such-hash", ActionDismissed); err != nil {
		t.Fatalf("UpdateAction on no match: want nil error, got %v", err)
	}
}
