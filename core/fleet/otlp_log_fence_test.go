package fleet

// otlp_log_fence_test.go — the fleet log lane's regression suite.
//
// The bug these tests exist to prevent: the harness wired its slog stream into
// an OTel LoggerProvider whose exporter POSTed every application log line to
// Fleet's /v1/logs. Application log bodies carry file paths, error strings and
// identifiers; none of it was admissible at the receiver (which requires a
// kameas.event.kind attribute nothing in any Kameas repo sets), so 100% of it
// was discarded server-side — after crossing the network and terminating TLS
// on Kameas infrastructure.
//
// Every assertion here is on bytes observed at an HTTP server, not on internal
// counters, because "we dropped it" and "we never sent it" are exactly the two
// states this file has to tell apart.
//
// See scripts/ci/check-fleet-log-export-fence.sh — the tests named here are
// required to exist.

import (
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/kameas-ai/kenaz-harness/core/telemetry"

	// fleet-log-fence-allow: the fence tests need a real OTLP log exporter to
	// prove the gate — not the absence of an exporter — is what withholds
	// plain slog lines. Production never constructs one; see the "Logs"
	// section of FleetOTLPPipeline.Activate.
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
)

// ── fake OTLP endpoint ───────────────────────────────────────────────────────

// recordingOTLPServer captures every request body per URL path so a test can
// ask both "was anything sent to /v1/logs?" and "what bytes went out?".
type recordingOTLPServer struct {
	mu     sync.Mutex
	byPath map[string][][]byte
}

func newRecordingOTLPServer(t *testing.T) (*httptest.Server, *recordingOTLPServer) {
	t.Helper()
	rec := &recordingOTLPServer{byPath: make(map[string][][]byte)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The OTLP HTTP exporters gzip by default. Decompress here so the
		// content assertions below search the plaintext that was actually
		// transmitted — a substring test against gzipped bytes would pass
		// for the wrong reason.
		var body []byte
		if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
			zr, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Errorf("gzip reader for %s: %v", r.URL.Path, err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body, _ = io.ReadAll(zr)
			_ = zr.Close()
		} else {
			body, _ = io.ReadAll(r.Body)
		}
		rec.mu.Lock()
		rec.byPath[r.URL.Path] = append(rec.byPath[r.URL.Path], body)
		rec.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// logRequests returns every body POSTed to a path ending in /v1/logs.
func (s *recordingOTLPServer) logRequests() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out [][]byte
	for path, bodies := range s.byPath {
		if strings.HasSuffix(path, "/v1/logs") {
			out = append(out, bodies...)
		}
	}
	return out
}

// allBytes concatenates every captured body across every path. Proto3 encodes
// strings as raw UTF-8, so a substring search over the wire bytes is a sound
// test for "did this string leave the process".
func (s *recordingOTLPServer) allBytes() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var sb strings.Builder
	for _, bodies := range s.byPath {
		for _, b := range bodies {
			sb.Write(b)
		}
	}
	return sb.String()
}

func (s *recordingOTLPServer) pathsSeen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.byPath))
	for p := range s.byPath {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ── the headline regression test ─────────────────────────────────────────────

// TestFleetLogLane_PlainSlogLineNeverLeavesTheProcess is the test that would
// have caught the original bug.
//
// It drives a real slog line — through the real SlogBridge, a real
// LoggerProvider, a real BatchProcessor, and the pipeline's real registered
// log exporter — at a fully activated FleetOTLPPipeline pointed at a live HTTP
// server, and asserts that nothing at all is POSTed to /v1/logs and that the
// line's content never appears in any bytes the process emitted.
//
// The pipeline is activated, not left inert: the trace lane is exercised in
// the same test so a passing result cannot be explained by "the pipeline was
// never switched on".
func TestFleetLogLane_PlainSlogLineNeverLeavesTheProcess(t *testing.T) {
	t.Parallel()

	srv, rec := newRecordingOTLPServer(t)
	ctx := context.Background()

	// A content-bearing line of exactly the shape application logs carry:
	// a filesystem path and an error string.
	const secretPath = "/Users/someone/private-repo/internal/billing/keys.go"
	const errText = "failed to parse ledger entry 8831: unexpected EOF"

	pipeline := NewFleetOTLPPipeline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = pipeline.Shutdown(ctx) })

	// Real LoggerProvider wired exactly as core.go wires it: the fleet log
	// exporter behind a BatchProcessor.
	lp := sdklog.NewLoggerProvider(
		sdklog.WithResource(resource.NewWithAttributes(semconv.SchemaURL,
			semconv.ServiceName("kenaz-harness"))),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(pipeline.LogExporter())),
	)
	t.Cleanup(func() { _ = lp.Shutdown(ctx) })

	// Real TracerProvider so Activate has a live trace lane to wire up.
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(ctx) })

	if err := pipeline.Activate(ctx, srv.URL, nil, IdentityAttrs{
		UserID:    "11111111-1111-1111-1111-111111111111",
		OrgID:     "22222222-2222-2222-2222-222222222222",
		MachineID: "33333333-3333-3333-3333-333333333333",
	}, fakeBearerProvider("fence-test-token"), tp); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// Emit through the real slog→OTel bridge, the same construction
	// telemetry.installSlogBridge performs in production.
	bridge := telemetry.NewSlogBridge(
		slog.NewTextHandler(io.Discard, nil),
		lp.Logger("kenaz-harness/slog-bridge"),
	)
	appLogger := slog.New(bridge)
	for i := 0; i < 20; i++ {
		appLogger.Error("compile step failed",
			"file", secretPath,
			"err", errText,
		)
	}

	// Also produce a span, so the test proves the pipeline is genuinely live
	// and that only the log lane is withheld.
	_, span := tp.Tracer("fence-test").Start(ctx, "fence-test-span")
	span.End()

	if err := lp.ForceFlush(ctx); err != nil {
		t.Fatalf("logger ForceFlush: %v", err)
	}
	if err := tp.ForceFlush(ctx); err != nil {
		t.Fatalf("tracer ForceFlush: %v", err)
	}
	// BatchSpanProcessor exports asynchronously; give the trace POST a moment
	// so the "pipeline is live" half of the assertion is not flaky-negative.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(rec.pathsSeen()) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// ── The assertion that matters ──
	if got := rec.logRequests(); len(got) != 0 {
		t.Fatalf("the fleet log lane POSTed %d request(s) to /v1/logs; "+
			"a plain slog line must never leave the process", len(got))
	}

	wire := rec.allBytes()
	if strings.Contains(wire, secretPath) {
		t.Errorf("log content leaked onto the wire: file path %q found in transmitted bytes", secretPath)
	}
	if strings.Contains(wire, errText) {
		t.Errorf("log content leaked onto the wire: error string %q found in transmitted bytes", errText)
	}
	if strings.Contains(wire, "compile step failed") {
		t.Errorf("log content leaked onto the wire: log message found in transmitted bytes")
	}

	// Sanity: the pipeline really was live. If this fails the test above
	// proved nothing, so treat it as a hard failure rather than a skip.
	if len(rec.pathsSeen()) == 0 {
		t.Fatal("no OTLP traffic at all — the pipeline was not active, so the " +
			"log-lane assertion is vacuous")
	}
}

// ── the gate itself ──────────────────────────────────────────────────────────

// TestKindGatedLogExporter_AdmitsOnlyAllowlistedKinds proves the fence, not
// merely the absent exporter, is what withholds untagged records.
//
// Production leaves the inner exporter nil (Activate never builds one), so
// this test installs a real otlploghttp exporter behind the gate and asserts
// on the wire bytes: an allowlisted kind gets through, an untagged record and
// an unrecognised kind do not.
func TestKindGatedLogExporter_AdmitsOnlyAllowlistedKinds(t *testing.T) {
	t.Parallel()

	srv, rec := newRecordingOTLPServer(t)
	ctx := context.Background()

	inner, err := otlploghttp.New(ctx, // fleet-log-fence-allow: see file header
		otlploghttp.WithEndpointURL(srv.URL),
		otlploghttp.WithURLPath("/v1/logs"),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("otlploghttp.New: %v", err)
	}

	gate := &kindGatedLogExporter{}
	gate.swapInner(ctx, inner)
	// Widest legitimate narrowing: every class opted in. Isolates the ceiling
	// so a drop here is the ceiling's doing, not the intersection's.
	gate.setOptIns(allOptedIn())
	t.Cleanup(func() { _ = gate.Shutdown(ctx) })

	const (
		markerUntagged = "MARKER-untagged-plain-slog-line"
		markerUnknown  = "MARKER-unrecognised-kind-record"
		markerAllowed  = "MARKER-allowlisted-tool-invoked"
	)

	records := []sdklog.Record{
		newTestLogRecord(markerUntagged),
		newTestLogRecord(markerUnknown, otellog.String(AttrEventKind, "harness.definitely_not_a_kind")),
		newTestLogRecord(markerAllowed, otellog.String(AttrEventKind, string(LogKindHarnessToolInvoked))),
	}

	if err := gate.Export(ctx, records); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := gate.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	wire := rec.allBytes()
	if strings.Contains(wire, markerUntagged) {
		t.Errorf("untagged record reached the wire — the kind gate did not fail closed")
	}
	if strings.Contains(wire, markerUnknown) {
		t.Errorf("record with an unrecognised kind reached the wire — the allowlist is not an allowlist")
	}
	if !strings.Contains(wire, markerAllowed) {
		t.Errorf("allowlisted record did NOT reach the wire; the gate is dropping admissible records.\n"+
			"paths seen: %v", rec.pathsSeen())
	}
}

// TestKindGatedLogExporter_AllDroppedMeansNoRequest verifies that a batch in
// which nothing is admissible produces no HTTP request at all — not an empty
// one. An empty POST still crosses the network and still reveals traffic
// patterns.
func TestKindGatedLogExporter_AllDroppedMeansNoRequest(t *testing.T) {
	t.Parallel()

	srv, rec := newRecordingOTLPServer(t)
	ctx := context.Background()

	inner, err := otlploghttp.New(ctx, // fleet-log-fence-allow: see file header
		otlploghttp.WithEndpointURL(srv.URL),
		otlploghttp.WithURLPath("/v1/logs"),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("otlploghttp.New: %v", err)
	}

	gate := &kindGatedLogExporter{}
	gate.swapInner(ctx, inner)
	gate.setOptIns(allOptedIn())
	t.Cleanup(func() { _ = gate.Shutdown(ctx) })

	records := make([]sdklog.Record, 0, 64)
	for i := 0; i < 64; i++ {
		records = append(records, newTestLogRecord("plain line, no kind attribute"))
	}
	if err := gate.Export(ctx, records); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := gate.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	if got := len(rec.logRequests()); got != 0 {
		t.Errorf("an all-dropped batch produced %d HTTP request(s); want 0", got)
	}
}

// TestKindGatedLogExporter_RedactsAdmittedBodies keeps the pre-existing
// redaction guarantee attached to the records that do pass the gate. Redaction
// is the second fence, not a replacement for the first.
func TestKindGatedLogExporter_RedactsAdmittedBodies(t *testing.T) {
	t.Parallel()

	srv, rec := newRecordingOTLPServer(t)
	ctx := context.Background()

	inner, err := otlploghttp.New(ctx, // fleet-log-fence-allow: see file header
		otlploghttp.WithEndpointURL(srv.URL),
		otlploghttp.WithURLPath("/v1/logs"),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("otlploghttp.New: %v", err)
	}

	gate := &kindGatedLogExporter{}
	gate.swapInner(ctx, inner)
	gate.setOptIns(allOptedIn())
	t.Cleanup(func() { _ = gate.Shutdown(ctx) })

	const secret = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	body := "tool call rejected: " + secret

	rc := newTestLogRecord(body, otellog.String(AttrEventKind, string(LogKindHarnessError)))
	if err := gate.Export(ctx, []sdklog.Record{rc}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := gate.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	wire := rec.allBytes()
	if len(rec.logRequests()) == 0 {
		t.Fatal("allowlisted record was not transmitted; nothing to check redaction against")
	}
	if strings.Contains(wire, secret) {
		t.Error("credential survived redaction on an admitted log record")
	}
}

// ── the compiled ceiling and the runtime intersection ────────────────────────

// allOptedIn is the widest legitimate narrowing snapshot Fleet can send: every
// class the harness knows about, opted in. Used where a test wants the
// intersection to be a no-op so it can isolate the ceiling.
func allOptedIn() []TelemetryOptInItem {
	out := make([]TelemetryOptInItem, 0, len(KnownTelemetryClasses))
	for _, c := range KnownTelemetryClasses {
		out = append(out, TelemetryOptInItem{Class: c, OptedIn: true})
	}
	return out
}

// TestFleetCannotWidenTheCompiledCeiling is the guarantee behind the "ceiling,
// not a synchronised copy" design.
//
// It feeds the gate a Fleet opt-in response that names classes and kinds
// OUTSIDE the compiled table — the shape a compromised, coerced or simply
// misconfigured Fleet would send to make clients start emitting something new
// — and asserts that none of it becomes transmittable. Authentication is not
// the boundary (constitution §XII condition 6); the binary is.
func TestFleetCannotWidenTheCompiledCeiling(t *testing.T) {
	t.Parallel()

	srv, rec := newRecordingOTLPServer(t)
	ctx := context.Background()

	inner, err := otlploghttp.New(ctx, // fleet-log-fence-allow: see file header
		otlploghttp.WithEndpointURL(srv.URL),
		otlploghttp.WithURLPath("/v1/logs"),
		otlploghttp.WithInsecure(),
	)
	if err != nil {
		t.Fatalf("otlploghttp.New: %v", err)
	}

	gate := &kindGatedLogExporter{}
	gate.swapInner(ctx, inner)
	t.Cleanup(func() { _ = gate.Shutdown(ctx) })

	// A hostile/erroneous Fleet response: real classes opted in, PLUS classes
	// invented by the server, PLUS entries whose "class" is spelled like a
	// kind in the hope the client keys off it.
	hostile := allOptedIn()
	hostile = append(hostile,
		TelemetryOptInItem{Class: "harness.prompt_bodies", OptedIn: true},
		TelemetryOptInItem{Class: "harness.raw_logs", OptedIn: true},
		TelemetryOptInItem{Class: "harness.everything", OptedIn: true},
		TelemetryOptInItem{Class: "harness.slog_line", OptedIn: true},
		TelemetryOptInItem{Class: "*", OptedIn: true},
		TelemetryOptInItem{Class: "", OptedIn: true},
	)
	gate.setOptIns(hostile)

	// The admitted set must never exceed the ceiling, whatever Fleet says.
	admitted := LogKindsAdmittedBy(hostile)
	for kind := range admitted {
		if !LogEventKindAllowed(string(kind)) {
			t.Fatalf("Fleet widened the ceiling: %q became admissible", kind)
		}
	}
	if len(admitted) > len(CeilingLogEventKinds()) {
		t.Fatalf("admitted set (%d) is larger than the compiled ceiling (%d)",
			len(admitted), len(CeilingLogEventKinds()))
	}

	const (
		markerServerNamed = "MARKER-kind-the-server-invented"
		markerRawLog      = "MARKER-raw-application-log-line"
		markerInCeiling   = "MARKER-genuinely-allowlisted-kind"
	)

	records := []sdklog.Record{
		// A record tagged with a kind the server "authorised" but which the
		// binary does not know.
		newTestLogRecord(markerServerNamed, otellog.String(AttrEventKind, "harness.raw_logs")),
		newTestLogRecord(markerServerNamed+"-2", otellog.String(AttrEventKind, "harness.everything")),
		// A plain slog line, exactly what a widened ceiling would let escape.
		newTestLogRecord(markerRawLog),
		// The control: something genuinely in the ceiling still flows.
		newTestLogRecord(markerInCeiling, otellog.String(AttrEventKind, string(LogKindHarnessToolInvoked))),
	}
	if err := gate.Export(ctx, records); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if err := gate.ForceFlush(ctx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	wire := rec.allBytes()
	if strings.Contains(wire, markerServerNamed) {
		t.Error("a kind named only by the Fleet response reached the wire — " +
			"the compiled ceiling is remotely widenable")
	}
	if strings.Contains(wire, markerRawLog) {
		t.Error("a plain log line reached the wire under a permissive Fleet response")
	}
	if !strings.Contains(wire, markerInCeiling) {
		t.Error("the control record (a real ceiling kind, opted in) did not reach the wire; " +
			"the intersection is over-narrowing and the test above proves nothing")
	}
}

// TestFleetNarrowingActuallyNarrows is the other direction: Fleet keeps every
// legitimate control it had. Opting a class out must stop its kinds, even
// though they are within the ceiling.
func TestFleetNarrowingActuallyNarrows(t *testing.T) {
	t.Parallel()

	// harness.tool_invoked is in the ceiling and belongs to harness.tool_calls.
	optIns := []TelemetryOptInItem{
		{Class: "harness.tool_calls", OptedIn: false},
		{Class: "harness.errors", OptedIn: true},
	}
	admitted := LogKindsAdmittedBy(optIns)

	if _, ok := admitted[LogKindHarnessToolInvoked]; ok {
		t.Error("harness.tool_invoked admitted despite its class being opted out — " +
			"Fleet lost its narrowing control")
	}
	if _, ok := admitted[LogKindHarnessError]; !ok {
		t.Error("harness.error not admitted despite harness.errors being opted in")
	}
	// Classes never mentioned are not admitted either: absence is not consent.
	if _, ok := admitted[LogKindHarnessConversationStarted]; ok {
		t.Error("a class absent from the snapshot was treated as opted in")
	}
}

// TestNoOptInSnapshotAdmitsNothing — "we have not been told what is allowed"
// and "nothing is allowed" must be the same answer. A dropped or failed
// GetTelemetryOptIns fetch must not be quietly permissive.
func TestNoOptInSnapshotAdmitsNothing(t *testing.T) {
	t.Parallel()

	for name, optIns := range map[string][]TelemetryOptInItem{
		"nil":                nil,
		"empty":              {},
		"all explicitly off": {{Class: "harness.errors", OptedIn: false}},
	} {
		if got := LogKindsAdmittedBy(optIns); len(got) != 0 {
			t.Errorf("%s snapshot admitted %d kind(s); want 0", name, len(got))
		}
	}

	// And at the record level: a ceiling kind with no snapshot is still
	// refused.
	rc := newTestLogRecord("body", otellog.String(AttrEventKind, string(LogKindHarnessError)))
	if logRecordExportable(rc, nil) {
		t.Error("a ceiling kind was judged exportable with no Fleet opt-in snapshot")
	}
}

// TestCeilingClassesAreKnownToTheHarness cross-checks the vendored kind→class
// table against KnownTelemetryClasses (which mirrors the same Fleet package's
// Class set). A kind pointing at a class the harness does not know about means
// one of the two mirrors is stale — and would also make that kind permanently
// unreachable through the intersection, since opt-ins are keyed by class.
func TestCeilingClassesAreKnownToTheHarness(t *testing.T) {
	t.Parallel()

	known := make(map[string]bool, len(KnownTelemetryClasses))
	for _, c := range KnownTelemetryClasses {
		known[c] = true
	}
	for _, kind := range CeilingLogEventKinds() {
		class, _ := LogEventKindClass(kind)
		if !known[class] {
			t.Errorf("kind %q maps to class %q, which is not in KnownTelemetryClasses — "+
				"one of the two Fleet schema mirrors is stale", kind, class)
		}
	}
}

// TestLogEventKindAllowed_FailsClosed enumerates the ways a record can fail to
// name a kind within the ceiling and asserts each one is refused.
func TestLogEventKindAllowed_FailsClosed(t *testing.T) {
	t.Parallel()

	for _, kind := range []string{
		"",
		" ",
		"harness",
		"harness.",
		"harness.tool_invoked ", // trailing space — not the same string
		"HARNESS.TOOL_INVOKED",  // wrong case
		"harness.tool_invoked\x00",
		"sigil.made_up",
		"*",
	} {
		if LogEventKindAllowed(kind) {
			t.Errorf("LogEventKindAllowed(%q) = true; the ceiling must fail closed", kind)
		}
	}
}

// TestLogRecordEventKind_NonStringKindIsRefused covers the case where a caller
// sets kameas.event.kind to a non-string value: the receiver reads it as a
// string, so anything else is not a kind.
func TestLogRecordEventKind_NonStringKindIsRefused(t *testing.T) {
	t.Parallel()

	rc := newTestLogRecord("body", otellog.Int(AttrEventKind, 7))
	if _, ok := logRecordEventKind(rc); ok {
		t.Error("a non-string kameas.event.kind was accepted as a kind")
	}
	if logRecordExportable(rc, LogKindsAdmittedBy(allOptedIn())) {
		t.Error("a record with a non-string kind was judged exportable")
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// newTestLogRecord builds an sdklog.Record the way the SDK builds one in
// production: by emitting through a LoggerProvider and capturing what the
// processor receives.
//
// Constructing a bare `var r sdklog.Record` and calling AddAttributes on it
// does NOT work: a zero-value Record carries a zero attribute-value length
// limit, so every string attribute is silently truncated to "". Records only
// pick up the real limits from the provider that emits them. Tests that build
// records by hand therefore appear to strip kameas.event.kind and would make
// the gate look stricter than it is.
func newTestLogRecord(body string, attrs ...otellog.KeyValue) sdklog.Record {
	var captured sdklog.Record
	cp := &capturingProcessor{onEmit: func(r sdklog.Record) { captured = r }}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(cp))

	var rec otellog.Record
	rec.SetTimestamp(time.Now())
	rec.SetObservedTimestamp(time.Now())
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetBody(otellog.StringValue(body))
	if len(attrs) > 0 {
		rec.AddAttributes(attrs...)
	}
	lp.Logger("fence-test").Emit(context.Background(), rec)
	_ = lp.Shutdown(context.Background())
	return captured
}

// capturingProcessor hands each emitted Record to a callback. Emission is
// synchronous within Emit, so no locking is needed for the single-goroutine
// use in newTestLogRecord.
type capturingProcessor struct {
	onEmit func(sdklog.Record)
}

func (p *capturingProcessor) OnEmit(_ context.Context, r *sdklog.Record) error {
	if p.onEmit != nil && r != nil {
		p.onEmit(*r)
	}
	return nil
}

func (p *capturingProcessor) Enabled(context.Context, sdklog.EnabledParameters) bool { return true }
func (p *capturingProcessor) Shutdown(context.Context) error                         { return nil }
func (p *capturingProcessor) ForceFlush(context.Context) error                       { return nil }
