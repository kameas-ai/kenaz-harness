package agentgraph

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB returns a fresh in-memory SQLite DB with the
// agent_graph_events schema applied. The schema mirrors migration 0309
// in core/session/migrations_agent_graph_events.go — keep in sync.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	const schema = `
CREATE TABLE agent_graph_events (
    run_id        TEXT NOT NULL,
    session_id    TEXT NOT NULL DEFAULT '',
    node_id       TEXT NOT NULL DEFAULT '',
    seq           INTEGER NOT NULL,
    kind          TEXT NOT NULL,
    payload_json  TEXT NOT NULL DEFAULT 'null',
    ts_ns         INTEGER NOT NULL,
    PRIMARY KEY (run_id, seq)
);`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	return db
}

func TestSQLEventLog_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	log := NewSQLEventLog(db)
	var b EventBatch
	for i := 0; i < 4; i++ {
		_ = b.AppendKind("rsql", "n", EventLLMCall, map[string]any{"i": i})
	}
	last, err := log.Append(b)
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if last != 4 {
		t.Errorf("last = %d, want 4", last)
	}
	if log.Len("rsql") != 4 {
		t.Errorf("Len = %d", log.Len("rsql"))
	}
	var got []EventKind
	if err := log.Replay("rsql", func(e Event) error {
		got = append(got, e.Kind)
		return nil
	}); err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("replay returned %d events, want 4", len(got))
	}
}

func TestSQLEventLog_PerRunSeqIsolation(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	log := NewSQLEventLog(db)
	var b EventBatch
	_ = b.AppendKind("a", "x", EventLLMCall, nil)
	_ = b.AppendKind("b", "y", EventLLMCall, nil)
	_ = b.AppendKind("a", "z", EventLLMCall, nil)
	if _, err := log.Append(b); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if log.Len("a") != 2 {
		t.Errorf("Len(a) = %d", log.Len("a"))
	}
	if log.Len("b") != 1 {
		t.Errorf("Len(b) = %d", log.Len("b"))
	}
}

func TestSQLEventLog_RejectsMissingRunID(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	log := NewSQLEventLog(db)
	var b EventBatch
	b.Append(Event{Kind: EventNodeStart})
	if _, err := log.Append(b); err == nil {
		t.Fatal("expected error")
	}
}
