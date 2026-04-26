package session_test

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestMigration0305_TablesExistAfterOpen verifies the three telemetry
// tables and their indexes are on disk after a fresh Open (i.e.
// migration 0305 fired).
func TestMigration0305_TablesExistAfterOpen(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())

	ctx := context.Background()

	for _, table := range []string{
		"telemetry_spans",
		"telemetry_metrics",
		"telemetry_logs",
	} {
		var name string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", table)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %q missing: %v", table, err)
			continue
		}
		if name != table {
			t.Errorf("table lookup for %q returned %q", table, name)
		}
	}

	for _, idx := range []string{
		"idx_telemetry_spans_trace",
		"idx_telemetry_spans_start",
		"idx_telemetry_metrics_name",
		"idx_telemetry_logs_ts",
		"idx_telemetry_logs_trace",
	} {
		var name string
		row := db.Reader().QueryRow(ctx,
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?", idx)
		if err := row.Scan(&name); err != nil {
			t.Errorf("index %q missing: %v", idx, err)
			continue
		}
		if name != idx {
			t.Errorf("index lookup for %q returned %q", idx, name)
		}
	}
}

// TestMigration0305_RoundTrip writes one row into each new table
// through the unified storage WriteTx and reads it back through the
// Reader. This pins the schema shape the local exporters will rely on.
func TestMigration0305_RoundTrip(t *testing.T) {
	t.Parallel()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())

	ctx := context.Background()
	if err := db.WriteTx(ctx, func(tx storage.WriteTx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO telemetry_spans
            (trace_id, span_id, parent_span_id, name, kind, start_ns, end_ns,
             status_code, status_message, attributes_json, events_json)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"trace-1", "span-1", nil, "chat.turn", "internal",
			int64(1000), int64(2000), 1, "", `{"k":"v"}`, `[]`); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO telemetry_metrics
            (name, kind, attributes_json, value, ts_ns)
            VALUES (?, ?, ?, ?, ?)`,
			"chat.turn.duration", "histogram", `{}`, 0.42, int64(1000)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO telemetry_logs
            (severity, body, attributes_json, trace_id, span_id, ts_ns)
            VALUES (?, ?, ?, ?, ?, ?)`,
			9, "boot", `{}`, "trace-1", "span-1", int64(1000))
		return err
	}); err != nil {
		t.Fatalf("WriteTx: %v", err)
	}

	var spanCount, metricCount, logCount int
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM telemetry_spans").Scan(&spanCount); err != nil {
		t.Fatalf("count spans: %v", err)
	}
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM telemetry_metrics").Scan(&metricCount); err != nil {
		t.Fatalf("count metrics: %v", err)
	}
	if err := db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM telemetry_logs").Scan(&logCount); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	if spanCount != 1 || metricCount != 1 || logCount != 1 {
		t.Errorf("counts = (spans=%d, metrics=%d, logs=%d), want (1,1,1)",
			spanCount, metricCount, logCount)
	}

	// Index queries used by the inspector — the partial index on
	// telemetry_logs(trace_id) requires the WHERE clause to match.
	rows, err := db.Reader().Query(ctx, `
        SELECT span_id FROM telemetry_logs WHERE trace_id IS NOT NULL ORDER BY ts_ns DESC LIMIT 1
    `)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("expected 1 row, got 0")
	}
	var spanID string
	if err := rows.Scan(&spanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if spanID != "span-1" {
		t.Errorf("span_id = %q, want span-1", spanID)
	}
}
