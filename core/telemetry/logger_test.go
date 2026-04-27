package telemetry_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/log/embedded"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// recordingLogger captures every Emit call so the bridge tests can
// assert what reached the OTel side without standing up the full
// LoggerProvider pipeline.
type recordingLogger struct {
	embedded.Logger
	mu      sync.Mutex
	records []recordedLog
}

type recordedLog struct {
	Body     string
	Severity log.Severity
	Attrs    map[string]any
	TraceID  trace.TraceID
	SpanID   trace.SpanID
}

func (r *recordingLogger) Emit(ctx context.Context, rec log.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rl := recordedLog{
		Body:     rec.Body().String(),
		Severity: rec.Severity(),
		Attrs:    map[string]any{},
	}
	rec.WalkAttributes(func(kv log.KeyValue) bool {
		rl.Attrs[kv.Key] = kv.Value.String()
		return true
	})
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		rl.TraceID = sc.TraceID()
		rl.SpanID = sc.SpanID()
	}
	r.records = append(r.records, rl)
}

func (r *recordingLogger) Enabled(_ context.Context, _ log.EnabledParameters) bool { return true }

func (r *recordingLogger) Records() []recordedLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]recordedLog, len(r.records))
	copy(cp, r.records)
	return cp
}

// TestSlogBridge_FanOut: a record emitted through the bridge lands in
// both the file handler and the OTel logger.
func TestSlogBridge_FanOut(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	fileH := slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	otel := &recordingLogger{}
	bridge := telemetry.NewSlogBridge(fileH, otel)

	logger := slog.New(bridge)
	logger.Info("chat.turn.started", "session_id", "sess-1")

	if !strings.Contains(buf.String(), "chat.turn.started") {
		t.Errorf("file sink missing line, got %q", buf.String())
	}
	recs := otel.Records()
	if len(recs) != 1 {
		t.Fatalf("otel records = %d, want 1", len(recs))
	}
	if recs[0].Body != "chat.turn.started" {
		t.Errorf("body = %q", recs[0].Body)
	}
	if recs[0].Severity != log.SeverityInfo {
		t.Errorf("severity = %d, want %d", recs[0].Severity, log.SeverityInfo)
	}
}

// TestSlogBridge_SeverityMapping pins the spec table:
// Debug→5, Info→9, Warn→13, Error→17.
func TestSlogBridge_SeverityMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		level slog.Level
		want  log.Severity
	}{
		{slog.LevelDebug, 5},
		{slog.LevelInfo, 9},
		{slog.LevelWarn, 13},
		{slog.LevelError, 17},
	}
	for _, tc := range cases {
		var buf bytes.Buffer
		otel := &recordingLogger{}
		bridge := telemetry.NewSlogBridge(
			slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}),
			otel,
		)
		logger := slog.New(bridge)
		logger.Log(context.Background(), tc.level, "msg")
		recs := otel.Records()
		if len(recs) != 1 {
			t.Fatalf("level=%v: records = %d", tc.level, len(recs))
		}
		if recs[0].Severity != tc.want {
			t.Errorf("level=%v: severity = %d, want %d", tc.level, recs[0].Severity, tc.want)
		}
	}
}

// TestSlogBridge_SpanContextPropagation: a span on ctx propagates to
// the OTel record's trace_id + span_id (the SDK's logger pulls them
// from ctx automatically; the bridge passes ctx through unchanged).
func TestSlogBridge_SpanContextPropagation(t *testing.T) {
	t.Parallel()
	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background())
	ctx, span := tp.Tracer("test").Start(context.Background(), "scope")
	defer span.End()

	otel := &recordingLogger{}
	bridge := telemetry.NewSlogBridge(slog.NewTextHandler(&bytes.Buffer{}, nil), otel)
	logger := slog.New(bridge)
	logger.InfoContext(ctx, "in.span")

	recs := otel.Records()
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	wantTID := span.SpanContext().TraceID()
	wantSID := span.SpanContext().SpanID()
	if recs[0].TraceID != wantTID {
		t.Errorf("trace_id = %v, want %v", recs[0].TraceID, wantTID)
	}
	if recs[0].SpanID != wantSID {
		t.Errorf("span_id = %v, want %v", recs[0].SpanID, wantSID)
	}
}

// TestSlogBridge_NoSpanInCtx: bridging without an active span leaves
// trace_id / span_id zeroed (no panic, no fabrication).
func TestSlogBridge_NoSpanInCtx(t *testing.T) {
	t.Parallel()
	otel := &recordingLogger{}
	bridge := telemetry.NewSlogBridge(slog.NewTextHandler(&bytes.Buffer{}, nil), otel)
	logger := slog.New(bridge)
	logger.Info("orphan")
	recs := otel.Records()
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}
	var zeroTID trace.TraceID
	var zeroSID trace.SpanID
	if recs[0].TraceID != zeroTID || recs[0].SpanID != zeroSID {
		t.Errorf("expected zero trace/span ID, got tid=%v sid=%v", recs[0].TraceID, recs[0].SpanID)
	}
}

// TestSlogBridge_NilSinks: bridge tolerates either or both sinks nil.
func TestSlogBridge_NilSinks(t *testing.T) {
	t.Parallel()
	// Both nil — Handle is a no-op.
	bridge := telemetry.NewSlogBridge(nil, nil)
	logger := slog.New(bridge)
	logger.Info("does.not.panic")

	// File only.
	var buf bytes.Buffer
	bridge2 := telemetry.NewSlogBridge(slog.NewTextHandler(&buf, nil), nil)
	slog.New(bridge2).Info("file.only")
	if !strings.Contains(buf.String(), "file.only") {
		t.Errorf("file sink missing line")
	}

	// OTel only.
	otel := &recordingLogger{}
	slog.New(telemetry.NewSlogBridge(nil, otel)).Info("otel.only")
	if len(otel.Records()) != 1 {
		t.Errorf("otel records = %d, want 1", len(otel.Records()))
	}
}

// TestSlogBridge_EndToEndOTel: a slog line wired through a real
// LoggerProvider (with a recording sdk-log exporter underneath)
// reaches the SDK's processor pipeline — proving the WP02 wiring
// from "logging.L().Info" to "OTel logs" works end-to-end.
func TestSlogBridge_EndToEndOTel(t *testing.T) {
	t.Parallel()
	captured := &captureLogExporter{}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(captured,
			sdklog.WithExportInterval(50*time.Millisecond))),
	)
	defer lp.Shutdown(context.Background())

	otelLogger := lp.Logger("test")
	bridge := telemetry.NewSlogBridge(slog.NewTextHandler(&bytes.Buffer{}, nil), otelLogger)
	logger := slog.New(bridge)
	logger.Info("e2e.line", "k", "v")
	if err := lp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	got := captured.Records()
	if len(got) != 1 {
		t.Fatalf("captured records = %d, want 1", len(got))
	}
	if got[0].Body().String() != "e2e.line" {
		t.Errorf("body = %q", got[0].Body().String())
	}
}

type captureLogExporter struct {
	mu      sync.Mutex
	records []sdklog.Record
}

func (c *captureLogExporter) Export(_ context.Context, r []sdklog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range r {
		c.records = append(c.records, r[i].Clone())
	}
	return nil
}
func (c *captureLogExporter) ForceFlush(_ context.Context) error { return nil }
func (c *captureLogExporter) Shutdown(_ context.Context) error   { return nil }
func (c *captureLogExporter) Records() []sdklog.Record {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := make([]sdklog.Record, len(c.records))
	copy(cp, c.records)
	return cp
}
