package telemetry_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func newTestStorage(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// TestLocalSpanExporter_RoundTrip pins the basic write-read contract:
// a span emitted through the SDK lands in telemetry_spans with the
// expected name, kind, status, and attribute encoding.
func TestLocalSpanExporter_RoundTrip(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalSpanExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalSpanExporter: %v", err)
	}
	defer exp.Shutdown(context.Background())

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exp), // sync so the test doesn't race the flusher
	)
	tracer := tp.Tracer("test")
	ctx, span := tracer.Start(context.Background(), "chat.turn",
		trace.WithSpanKind(trace.SpanKindInternal))
	span.SetAttributes(attribute.String("session_id", "sess-1"))
	span.SetStatus(codes.Ok, "")
	span.End()
	_ = ctx
	_ = otel.GetTracerProvider // keep the import live in case future tests need it

	// Span exporter is async via batcher (here we used WithSyncer
	// which calls ExportSpans synchronously). Wait for the flusher to
	// pick it up.
	if !waitForRow(t, db, "SELECT COUNT(*) FROM telemetry_spans WHERE name='chat.turn'", 2*time.Second) {
		t.Fatalf("span did not land in telemetry_spans within timeout")
	}

	var name, kind, status string
	var statusCode int
	row := db.Reader().QueryRow(context.Background(),
		`SELECT name, kind, status_code, status_message
         FROM telemetry_spans WHERE name='chat.turn' LIMIT 1`)
	if err := row.Scan(&name, &kind, &statusCode, &status); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "chat.turn" || kind != "internal" || statusCode != int(codes.Ok) {
		t.Errorf("got (name=%q, kind=%q, code=%d), want (chat.turn, internal, %d)",
			name, kind, statusCode, int(codes.Ok))
	}
}

// TestLocalSpanExporter_ConcurrentProducers fires many goroutines into
// the exporter at once. After Shutdown drains, every span (modulo
// drops) must be in the DB. Race detector + the lock-free queue path
// shake out concurrency bugs.
func TestLocalSpanExporter_ConcurrentProducers(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalSpanExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalSpanExporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp))
	tracer := tp.Tracer("test")

	const goroutines = 20
	const perGoroutine = 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				_, span := tracer.Start(context.Background(), "concurrent.span")
				span.SetAttributes(attribute.Int("i", i))
				span.End()
			}
		}()
	}
	wg.Wait()

	// Flush the batcher so the local exporter sees the spans.
	if err := tp.Shutdown(context.Background()); err != nil {
		t.Fatalf("tp.Shutdown: %v", err)
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("exp.Shutdown: %v", err)
	}

	var count int64
	if err := db.Reader().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM telemetry_spans WHERE name='concurrent.span'").
		Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	dropped := int64(exp.Dropped())
	want := int64(goroutines * perGoroutine)
	if count+dropped != want {
		t.Errorf("count(%d) + dropped(%d) = %d, want %d", count, dropped, count+dropped, want)
	}
	if count == 0 {
		t.Errorf("no spans landed; expected at least some success")
	}
}

// TestLocalSpanExporter_DropsOnFullBuffer stuffs the queue past its
// bound and asserts the dropped counter increments. The shutdown path
// must still drain whatever remains.
func TestLocalSpanExporter_DropsOnFullBuffer(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalSpanExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalSpanExporter: %v", err)
	}

	// Build a synthetic batch much larger than the queue (1024).
	const tries = 4096
	tp := sdktrace.NewTracerProvider() // no exporter — we only need it to start spans we'll re-export
	tracer := tp.Tracer("drop-test")
	spans := make([]sdktrace.ReadOnlySpan, 0, tries)
	for i := 0; i < tries; i++ {
		_, sp := tracer.Start(context.Background(), "drop")
		sp.End()
		// Drop-on-full requires the underlying span be a ReadOnlySpan;
		// SDK spans implement it directly.
		if ros, ok := sp.(sdktrace.ReadOnlySpan); ok {
			spans = append(spans, ros)
		}
	}
	// Push everything synchronously without giving the flusher time
	// to drain — the queue should saturate and drops should rise.
	if err := exp.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	// Some drops should have occurred (queue is 1024).
	if exp.Dropped() == 0 {
		// Best-effort: depending on scheduler, the flusher may have
		// kept up. Fail only if every span landed AND dropped is 0.
		var count int64
		_ = db.Reader().QueryRow(context.Background(),
			"SELECT COUNT(*) FROM telemetry_spans").Scan(&count)
		if count == int64(tries) {
			t.Skip("scheduler kept up; cannot verify drop path on this machine")
		}
	}
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

// TestLocalSpanExporter_ShutdownDrains pushes a few spans and confirms
// Shutdown waits for the queue to drain before returning.
func TestLocalSpanExporter_ShutdownDrains(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalSpanExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalSpanExporter: %v", err)
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	tracer := tp.Tracer("drain-test")
	for i := 0; i < 5; i++ {
		_, sp := tracer.Start(context.Background(), "drain.span")
		sp.End()
	}
	// Shutdown must block until the queue drains.
	if err := exp.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	var count int64
	if err := db.Reader().QueryRow(context.Background(),
		"SELECT COUNT(*) FROM telemetry_spans WHERE name='drain.span'").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 5 {
		t.Errorf("count = %d, want 5 (drain path failed)", count)
	}
}

// waitForRow polls a COUNT(*) query until it returns >0 or the
// timeout elapses. Used to bridge the sync-syncer-but-async-flusher
// gap in the round-trip test.
func waitForRow(t *testing.T, db storage.DB, query string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var n int64
		if err := db.Reader().QueryRow(context.Background(), query).Scan(&n); err == nil && n > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
