package fleet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ── stubs ─────────────────────────────────────────────────────────────────

type stubConsent struct{ level string }

func (s *stubConsent) ConsentLevel() string { return s.level }

// makeSpan creates a real ReadOnlySpan using the SDK tracer so that the
// internal unexported methods are satisfied.
func makeSpan(t *testing.T, name string) sdktrace.ReadOnlySpan {
	t.Helper()

	// We use a simple synchronous span recorder to capture the span.
	var captured sdktrace.ReadOnlySpan
	recorder := &spanRecorder{onEnd: func(s sdktrace.ReadOnlySpan) { captured = s }}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("test")
	_, sp := tracer.Start(context.Background(), name)
	sp.End()
	if captured == nil {
		t.Fatal("span recorder did not capture span")
	}
	return captured
}

// makeSpanWithAttrs creates a ReadOnlySpan with the given name and key-value
// attributes. Callers use this to test redaction of attribute values.
func makeSpanWithAttrs(t *testing.T, name string, kvs ...attribute.KeyValue) sdktrace.ReadOnlySpan {
	t.Helper()
	var captured sdktrace.ReadOnlySpan
	recorder := &spanRecorder{onEnd: func(s sdktrace.ReadOnlySpan) { captured = s }}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	tracer := tp.Tracer("test")
	_, sp := tracer.Start(context.Background(), name)
	sp.SetAttributes(kvs...)
	sp.End()
	if captured == nil {
		t.Fatal("span recorder did not capture span")
	}
	return captured
}

// spanRecorder is a simple SpanProcessor for tests.
type spanRecorder struct {
	onEnd func(sdktrace.ReadOnlySpan)
}

func (r *spanRecorder) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (r *spanRecorder) OnEnd(s sdktrace.ReadOnlySpan) {
	if r.onEnd != nil {
		r.onEnd(s)
	}
}
func (r *spanRecorder) Shutdown(context.Context) error  { return nil }
func (r *spanRecorder) ForceFlush(context.Context) error { return nil }

// ── ring buffer tests ──────────────────────────────────────────────────────

// TestFleetSpanExporter_Backpressure verifies that the ring buffer drops the
// oldest span when at capacity.
//
// The approach: use a blocking HTTP server so the early-flush goroutine hangs
// on the network call (can't clear the buffer). That ensures the buffer fills
// up to the count cap and evicts spans, incrementing the dropped counter.
func TestFleetSpanExporter_Backpressure(t *testing.T) {
	t.Parallel()

	// The block channel lets us control when the server unblocks.
	blockCh := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-blockCh // hold here so the flusher goroutine is busy
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	// Unblock the server when the test exits so goroutines can drain.
	defer close(blockCh)

	exp := NewFleetSpanExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Consent:       &stubConsent{level: "full"},
		FlushInterval: 10 * time.Minute, // prevent timer-driven flushes
		NewULID:       func() string { return "test-ulid" },
	})
	defer exp.Shutdown(context.Background()) //nolint:errcheck

	// First: fill to the 80% early-flush threshold (8000 spans). This
	// triggers an early flush that blocks on the HTTP server above.
	sp := makeSpan(t, "backpressure-span")
	earlyBatch := make([]sdktrace.ReadOnlySpan, int(float64(fleetSpanCap)*fleetEarlyFlushFraction)+1)
	for i := range earlyBatch {
		earlyBatch[i] = sp
	}
	_ = exp.ExportSpans(context.Background(), earlyBatch)

	// Give the flusher goroutine a moment to pick up the early-flush signal
	// and block on the HTTP POST (the server is now holding the connection).
	time.Sleep(50 * time.Millisecond)

	// Now inject spans exceeding the count cap. Since the flusher is blocked,
	// the buffer fills up and evictions must happen.
	overflowBatch := make([]sdktrace.ReadOnlySpan, fleetSpanCap+1)
	for i := range overflowBatch {
		overflowBatch[i] = sp
	}
	_ = exp.ExportSpans(context.Background(), overflowBatch)

	if exp.Dropped() == 0 {
		t.Error("want at least 1 drop, got 0")
	}
}

// TestFleetSpanExporter_FlushOnStop verifies that Shutdown triggers a final
// flush, which delivers spans to the test HTTP server.
func TestFleetSpanExporter_FlushOnStop(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var batches []fleetBatch

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b fleetBatch
		if err := json.Unmarshal(body, &b); err == nil {
			mu.Lock()
			batches = append(batches, b)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewFleetSpanExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Consent:       &stubConsent{level: "full"},
		FlushInterval: 10 * time.Minute, // timer won't fire during test
		NewULID:       func() string { return "batch-1" },
	})

	// Push 5 spans.
	sp := makeSpan(t, "span")
	spans := make([]sdktrace.ReadOnlySpan, 5)
	for i := range spans {
		spans[i] = sp
	}
	if err := exp.ExportSpans(context.Background(), spans); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	// Shutdown should flush synchronously.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mu.Lock()
	got := len(batches)
	mu.Unlock()
	if got == 0 {
		t.Error("want at least 1 batch delivered to server, got 0")
	}
}

// TestFleetSpanExporter_NoneConsentDropsSpans verifies that spans are
// silently dropped when consent is "none".
func TestFleetSpanExporter_NoneConsentDropsSpans(t *testing.T) {
	t.Parallel()
	var calls uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddUint64(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewFleetSpanExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Consent:       &stubConsent{level: "none"},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "x" },
	})

	sp := makeSpan(t, "s")
	batch := []sdktrace.ReadOnlySpan{sp}
	if err := exp.ExportSpans(context.Background(), batch); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	if atomic.LoadUint64(&calls) != 0 {
		t.Errorf("want 0 HTTP calls when consent=none, got %d", calls)
	}
}

// TestFleetMetricExporter_BasicFlush verifies that metric records reach the
// server on shutdown.
func TestFleetMetricExporter_BasicFlush(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var batches []fleetBatch
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var b fleetBatch
		if err := json.Unmarshal(body, &b); err == nil {
			mu.Lock()
			batches = append(batches, b)
			mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewFleetMetricExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Consent:       &stubConsent{level: "aggregate"},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "m-1" },
	})

	rm := &metricdata.ResourceMetrics{
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Metrics: []metricdata.Metrics{
					{
						Name: "test.counter",
						Data: metricdata.Sum[int64]{
							DataPoints: []metricdata.DataPoint[int64]{
								{Value: 42, Time: time.Now()},
							},
						},
					},
				},
			},
		},
	}
	_ = exp.Export(context.Background(), rm)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	mu.Lock()
	got := len(batches)
	mu.Unlock()
	if got == 0 {
		t.Error("want at least 1 metric batch, got 0")
	}
}

// TestFleetLogExporter_AggregateConsentDropsLogs verifies the spec rule that
// aggregate consent drops all log records.
func TestFleetLogExporter_AggregateConsentDropsLogs(t *testing.T) {
	t.Parallel()
	var calls uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddUint64(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exp := NewFleetLogExporter(FleetExporterConfig{
		Endpoint:      srv.URL,
		Consent:       &stubConsent{level: "aggregate"},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "l-1" },
	})

	var records []sdklog.Record
	_ = exp.Export(context.Background(), records)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	if atomic.LoadUint64(&calls) != 0 {
		t.Errorf("want 0 HTTP calls under aggregate consent for logs, got %d", calls)
	}
}

// ── redactor-at-enqueue tests (WP02 / WP05) ───────────────────────────────

// TestSpanRecordFrom_RedactsSecretValue verifies that a @secret:... reference
// in a span attribute VALUE is replaced with [REDACTED:secret-ref] before the
// record enters the ring buffer. The KEY itself is not on the deny-list so it
// passes through; only the value is scrubbed.
func TestSpanRecordFrom_RedactsSecretValue(t *testing.T) {
	t.Parallel()

	sp := makeSpanWithAttrs(t, "test-span",
		attribute.String("prompt", "@secret:vault/my-key"),
	)
	rec := spanRecordFrom(sp, &stubConsent{level: "full"})

	got, ok := rec.Attributes["prompt"]
	if !ok {
		t.Fatal("want 'prompt' key in attributes, key missing")
	}
	gotStr, ok := got.(string)
	if !ok {
		t.Fatalf("want string attribute value, got %T", got)
	}
	if gotStr != "[REDACTED:secret-ref]" {
		t.Errorf("want [REDACTED:secret-ref], got %q", gotStr)
	}
}

// TestSpanRecordFrom_RedactsSpanName verifies that a bearer token embedded in
// a span name is replaced with [REDACTED:bearer-token] by the redactor.
func TestSpanRecordFrom_RedactsSpanName(t *testing.T) {
	t.Parallel()

	// A span name containing a bearer pattern (should not happen in production
	// but we test the belt-and-suspenders redaction).
	sp := makeSpan(t, "bearer sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA-BBBBBBBBBBBBBB")
	rec := spanRecordFrom(sp, &stubConsent{level: "full"})

	if strings.Contains(rec.Name, "sk-ant-") {
		t.Errorf("span name still contains raw key after redaction: %q", rec.Name)
	}
	if !strings.Contains(rec.Name, "[REDACTED:") {
		t.Errorf("expected [REDACTED:...] in span name, got: %q", rec.Name)
	}
}

// TestLogRecordsFrom_RedactsBody verifies that a JWT in a log body is replaced
// with [REDACTED:jwt-token] before the log record is stored.
func TestLogRecordsFrom_RedactsBody(t *testing.T) {
	t.Parallel()

	// Construct a log.Record with a JWT-like body using the SDK builder.
	var r sdklog.Record
	r.SetBody(log.StringValue("auth header: eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMSJ9.SIGNATURE"))

	recs := logRecordsFrom([]sdklog.Record{r})
	if len(recs) != 1 {
		t.Fatalf("want 1 log record, got %d", len(recs))
	}
	if strings.Contains(recs[0].Body, "eyJhbGci") {
		t.Errorf("log body still contains raw JWT after redaction: %q", recs[0].Body)
	}
	if !strings.Contains(recs[0].Body, "[REDACTED:jwt-token]") {
		t.Errorf("expected [REDACTED:jwt-token] in log body, got: %q", recs[0].Body)
	}
}

// ── compile-time checks ────────────────────────────────────────────────────

var _ sdkmetric.Exporter = (*FleetMetricExporter)(nil)

// ensure trace.SpanKindInternal exists in our import path (avoids import drift)
var _ = trace.SpanKindInternal
