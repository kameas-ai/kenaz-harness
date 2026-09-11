package cedar

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// openTestDecisionDB opens a real, file-backed sqlite database (blind
// spot #2 in CLAUDE.md: fixtures that skip real SQL encode/decode have
// hidden production defects here before) and applies migration
// cedar-policy/1300-policy-decisions directly.
func openTestDecisionDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	for _, stmt := range splitPolicyDecisionsSQL(sqlPolicyDecisionsInit) {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("apply migration 1300 DDL: %v", err)
		}
	}
	return db
}

// TestSQLDecisionStore_SurvivesCloseAndReopen drives real sqlite (not
// session.NewMemoryStore or any in-memory fixture): append a decision,
// Close the store (flushes the background writer), close the
// underlying *sql.DB, reopen a FRESH *sql.DB against the SAME file, and
// confirm a brand-new SQLDecisionStore hydrates the decision from disk.
func TestSQLDecisionStore_SurvivesCloseAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")

	db1 := openTestDecisionDB(t, path)
	store1, err := NewSQLDecisionStore(db1)
	if err != nil {
		t.Fatalf("NewSQLDecisionStore: %v", err)
	}
	want := Decision{
		Outcome:       Allow,
		Action:        ActionUseTool,
		Principal:     "User::\"local\"",
		Resource:      "kenaz__bash",
		MatchedPolicy: "default_tool_policy.cedar#0#0",
		Reason:        "permit policy matched",
		EvaluatedAt:   time.Now().UTC().Truncate(time.Millisecond),
	}
	store1.Append(want)

	// Recent() must reflect it immediately (in-memory ring), before any
	// flush — proving Append does not silently drop the fast path.
	got := store1.Recent(1)
	if len(got) != 1 || got[0].Action != want.Action {
		t.Fatalf("Recent() immediately after Append = %+v; want one matching %+v", got, want)
	}

	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close: %v", err)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("db1.Close: %v", err)
	}

	// Fresh connection, fresh store — nothing in-process is shared with
	// store1/db1 beyond the file on disk.
	db2, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("reopen sqlite: %v", err)
	}
	defer db2.Close()
	db2.SetMaxOpenConns(1)

	store2, err := NewSQLDecisionStore(db2)
	if err != nil {
		t.Fatalf("NewSQLDecisionStore (reopen): %v", err)
	}
	defer store2.Close()

	after := store2.Recent(10)
	if len(after) != 1 {
		t.Fatalf("Recent() after reopen = %d decisions; want exactly 1 (got %+v)", len(after), after)
	}
	got0 := after[0]
	if got0.Outcome != want.Outcome || got0.Action != want.Action || got0.Principal != want.Principal ||
		got0.Resource != want.Resource || got0.MatchedPolicy != want.MatchedPolicy || got0.Reason != want.Reason {
		t.Fatalf("reopened decision = %+v; want %+v", got0, want)
	}
	if !got0.EvaluatedAt.Equal(want.EvaluatedAt) {
		t.Fatalf("reopened EvaluatedAt = %v; want %v", got0.EvaluatedAt, want.EvaluatedAt)
	}

	// Independent verification: a THIRD, throwaway connection querying
	// the table directly, bypassing both stores' in-memory rings.
	db3, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open verification connection: %v", err)
	}
	defer db3.Close()
	var count int
	if err := db3.QueryRow("SELECT COUNT(*) FROM policy_decisions").Scan(&count); err != nil {
		t.Fatalf("count policy_decisions: %v", err)
	}
	if count != 1 {
		t.Fatalf("policy_decisions row count = %d; want 1", count)
	}
}

// TestSQLDecisionStore_RetentionEvicts asserts BEHAVIOUR — that old
// rows are actually deleted from the durable table — not merely that a
// cap field was set. It writes 5 decisions against a store capped at
// 3, closes it (forcing every queued write through the synchronous
// drain in Close), and independently counts rows in policy_decisions.
// Without trimLocked's DELETE, this would read 5.
func TestSQLDecisionStore_RetentionEvicts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")
	db := openTestDecisionDB(t, path)
	defer db.Close()

	store, err := NewSQLDecisionStore(db, WithSQLDecisionRetention(3))
	if err != nil {
		t.Fatalf("NewSQLDecisionStore: %v", err)
	}

	actions := []string{"a1", "a2", "a3", "a4", "a5"}
	for i, action := range actions {
		store.Append(Decision{
			Outcome:     Allow,
			Action:      action,
			Resource:    "r",
			EvaluatedAt: time.Now().UTC().Add(time.Duration(i) * time.Millisecond),
		})
	}
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	// In-memory ring: exactly 3, newest-first (a5, a4, a3).
	recent := store.Recent(10)
	if len(recent) != 3 {
		t.Fatalf("Recent() len = %d; want 3 (cap)", len(recent))
	}
	wantOrder := []string{"a5", "a4", "a3"}
	for i, d := range recent {
		if d.Action != wantOrder[i] {
			t.Fatalf("Recent()[%d].Action = %q; want %q (got order %+v)", i, d.Action, wantOrder[i], recent)
		}
	}

	// Durable table: independently count and identify surviving rows.
	// This is the assertion that actually falsifies a no-op trim — the
	// in-memory ring alone would pass even if trimLocked's DELETE were
	// deleted entirely, because Append's ring truncation runs
	// regardless.
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM policy_decisions").Scan(&count); err != nil {
		t.Fatalf("count policy_decisions: %v", err)
	}
	if count != 3 {
		t.Fatalf("policy_decisions row count after retention = %d; want 3 — retention did not evict from disk", count)
	}
	rows, err := db.Query("SELECT action FROM policy_decisions ORDER BY id ASC")
	if err != nil {
		t.Fatalf("query surviving actions: %v", err)
	}
	defer rows.Close()
	var surviving []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			t.Fatalf("scan: %v", err)
		}
		surviving = append(surviving, a)
	}
	wantSurviving := []string{"a3", "a4", "a5"}
	if len(surviving) != len(wantSurviving) {
		t.Fatalf("surviving actions = %v; want %v", surviving, wantSurviving)
	}
	for i, a := range surviving {
		if a != wantSurviving[i] {
			t.Fatalf("surviving actions = %v; want %v", surviving, wantSurviving)
		}
	}
}

// TestSQLDecisionStore_ConcurrentAppendIsRace-safe drives concurrent
// Append calls from multiple goroutines (the shape Engine.Evaluate is
// actually called under — request-handling goroutines) while Recent()
// reads concurrently, per CLAUDE.md's race-safe test-fakes convention.
// Run under `go test -race`.
func TestSQLDecisionStore_ConcurrentAppendIsRaceSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "decisions.db")
	db := openTestDecisionDB(t, path)
	defer db.Close()

	const goroutines = 16
	const perGoroutine = 25
	store, err := NewSQLDecisionStore(db, WithSQLDecisionRetention(goroutines*perGoroutine))
	if err != nil {
		t.Fatalf("NewSQLDecisionStore: %v", err)
	}
	defer store.Close()
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				store.Append(Decision{
					Outcome:     Allow,
					Action:      "concurrent",
					Resource:    "r",
					EvaluatedAt: time.Now().UTC(),
				})
			}
		}(g)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_ = store.Recent(10)
			}
		}()
	}
	wg.Wait()

	got := store.Recent(1000)
	if len(got) != goroutines*perGoroutine {
		t.Fatalf("Recent() after concurrent append = %d; want %d", len(got), goroutines*perGoroutine)
	}
}

// TestSQLDecisionStore_ImplementsDecisionStore pins the interface
// satisfaction at compile time via a variable assignment (belt and
// braces alongside Engine's own use of it).
func TestSQLDecisionStore_ImplementsDecisionStore(t *testing.T) {
	var _ DecisionStore = (*SQLDecisionStore)(nil)
}
