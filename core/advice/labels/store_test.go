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
		KindID:        "branch_now",
		PromptVersion: "v1",
		FeaturesHash:  "hash-1",
		FeaturesJSON:  `{"turns_since_session_start":3}`,
		ModelID:       "heuristic/branch-regex-v1",
		Rung:          "heuristic",
		Decision:      true,
		Confidence:    82,
		Shown:         true,
		UserAction:    ActionIgnored,
		LatencyMS:     5,
		SessionID:     "sess-1",
		CreatedAt:     time.Unix(1700000000, 0).UTC(),
	}
	if err := store.Insert(ctx, row); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	var (
		gotKind    string
		gotDecide  int
		gotShown   int
		gotAction  string
		gotSession string
	)
	err := db.QueryRowContext(ctx,
		`SELECT kind, decision, shown, user_action, session_id FROM advice_labels WHERE features_hash = ?`,
		"hash-1").Scan(&gotKind, &gotDecide, &gotShown, &gotAction, &gotSession)
	if err != nil {
		t.Fatalf("query inserted row: %v", err)
	}
	if gotKind != "branch_now" || gotDecide != 1 || gotShown != 1 || gotAction != "ignored" || gotSession != "sess-1" {
		t.Errorf("row mismatch: kind=%s decision=%d shown=%d action=%s session=%s",
			gotKind, gotDecide, gotShown, gotAction, gotSession)
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
