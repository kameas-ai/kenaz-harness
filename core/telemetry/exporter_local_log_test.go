package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/telemetry"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// TestLocalLogExporter_RoundTrip emits a log record through the SDK
// LoggerProvider and asserts it lands in telemetry_logs.
func TestLocalLogExporter_RoundTrip(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalLogExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalLogExporter: %v", err)
	}

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp,
			sdklog.WithExportInterval(50*time.Millisecond))),
	)
	defer lp.Shutdown(context.Background())

	logger := lp.Logger("test")
	var rec log.Record
	rec.SetTimestamp(time.Now())
	rec.SetSeverity(log.SeverityInfo)
	rec.SetBody(log.StringValue("hello-world"))
	rec.AddAttributes(log.String("session_id", "sess-1"))
	logger.Emit(context.Background(), rec)

	if err := lp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}
	// Drain the local exporter's queue.
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	var severity int
	var body string
	row := db.Reader().QueryRow(context.Background(),
		"SELECT severity, body FROM telemetry_logs ORDER BY ts_ns DESC LIMIT 1")
	if err := row.Scan(&severity, &body); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if body != "hello-world" {
		t.Errorf("body = %q, want hello-world", body)
	}
	if severity != int(log.SeverityInfo) {
		t.Errorf("severity = %d, want %d", severity, int(log.SeverityInfo))
	}
}

// TestLocalLogExporter_NilDB rejects construction without a DB.
func TestLocalLogExporter_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewLocalLogExporter(nil, nil); err == nil {
		t.Errorf("expected error for nil db")
	}
}

// TestLocalLogExporter_ShutdownIdempotent confirms repeated Shutdown
// calls are safe.
func TestLocalLogExporter_ShutdownIdempotent(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalLogExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalLogExporter: %v", err)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown #1: %v", err)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown #2: %v", err)
	}
}
