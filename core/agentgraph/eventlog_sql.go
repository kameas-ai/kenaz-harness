package agentgraph

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// SQLDB is the narrow database surface the SQL-backed EventLog needs.
// Mirrors *sql.DB so the kernel can wire to whatever database/sql
// driver the chassis provides without depending on the harness's
// `core/storage` directly. (`core/agentgraph` keeps its import graph
// flat by talking to a stdlib interface.)
type SQLDB interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SQLEventLog is the SQLite-backed EventLog. The schema is created by
// migration 0309 (see migrations.go in this package); SQLEventLog
// itself only does row-level reads/writes.
type SQLEventLog struct {
	db SQLDB
	mu sync.Mutex
}

// NewSQLEventLog wraps a SQLDB. The caller is responsible for ensuring
// migration 0309 has been applied.
func NewSQLEventLog(db SQLDB) *SQLEventLog {
	return &SQLEventLog{db: db}
}

// Append commits a batch atomically. The implementation is naive —
// one INSERT per event in a transaction — sufficient for the kernel's
// expected event volume (~ tens per node fire, hundreds per run).
func (l *SQLEventLog) Append(batch EventBatch) (int64, error) {
	if batch.Len() == 0 {
		return 0, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ctx := context.Background()
	var last int64
	for i := range batch.Events {
		ev := &batch.Events[i]
		if ev.RunID == "" {
			return 0, fmt.Errorf("agentgraph: sql event log: event #%d missing run_id", i)
		}
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		// Get the next seq for this run.
		var nextSeq int64
		row := l.db.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(seq), 0) + 1 FROM agent_graph_events WHERE run_id = ?`,
			ev.RunID)
		if err := row.Scan(&nextSeq); err != nil {
			return 0, fmt.Errorf("agentgraph: sql event log: next seq: %w", err)
		}
		ev.Seq = nextSeq
		payload := []byte(ev.Payload)
		if len(payload) == 0 {
			payload = []byte("null")
		}
		if _, err := l.db.ExecContext(ctx,
			`INSERT INTO agent_graph_events
			    (run_id, session_id, node_id, seq, kind, payload_json, ts_ns)
			 VALUES (?, ?, ?, ?, ?, ?, ?)`,
			ev.RunID, ev.SessionID, ev.NodeID, nextSeq, string(ev.Kind),
			string(payload), ev.Timestamp.UnixNano()); err != nil {
			return 0, fmt.Errorf("agentgraph: sql event log: insert: %w", err)
		}
		last = nextSeq
	}
	return last, nil
}

// Replay walks every event for the run in seq order.
func (l *SQLEventLog) Replay(runID string, fn func(Event) error) error {
	ctx := context.Background()
	rows, err := l.db.QueryContext(ctx,
		`SELECT seq, run_id, session_id, node_id, kind, payload_json, ts_ns
		   FROM agent_graph_events
		  WHERE run_id = ?
		  ORDER BY seq ASC`,
		runID)
	if err != nil {
		return fmt.Errorf("agentgraph: sql event log: replay: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			seq                                          int64
			rRun, rSession, rNode, rKind, rPayload       string
			rTs                                          int64
		)
		if err := rows.Scan(&seq, &rRun, &rSession, &rNode, &rKind, &rPayload, &rTs); err != nil {
			return fmt.Errorf("agentgraph: sql event log: scan: %w", err)
		}
		var payload json.RawMessage
		if rPayload != "" && rPayload != "null" {
			payload = json.RawMessage(rPayload)
		}
		ev := Event{
			Seq:       seq,
			RunID:     rRun,
			SessionID: rSession,
			NodeID:    rNode,
			Kind:      EventKind(rKind),
			Timestamp: time.Unix(0, rTs).UTC(),
			Payload:   payload,
		}
		if err := fn(ev); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Len returns how many events the run has produced.
func (l *SQLEventLog) Len(runID string) int {
	ctx := context.Background()
	row := l.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM agent_graph_events WHERE run_id = ?`,
		runID)
	var n int
	_ = row.Scan(&n)
	return n
}

// Close is a no-op — the caller owns the underlying SQLDB.
func (l *SQLEventLog) Close() error { return nil }
