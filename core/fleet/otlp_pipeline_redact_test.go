package fleet

// otlp_pipeline_redact_test.go — integration tests proving that the live
// FleetOTLPPipeline path (otlptracehttp → redactingSpanExporter →
// resourceOverrideSpanExporter → OTLP HTTP) scrubs secrets before they
// leave the process boundary.
//
// These tests exercise the REAL pipeline, not the dead-code JSON batcher
// (FleetSpanExporter). A fake OTLP HTTP server captures the protobuf binary
// payload and a substring search proves the secret pattern is absent.
//
// String values in protobuf/proto3 are encoded as UTF-8, so the secret
// appears verbatim in the binary payload when redaction is absent — making
// the substring check reliable without a full proto decode.
//
// (harness-fleet-otlp-export-01NTLMEX01 FR-005 / NFR-001 security fix)

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// fakeOTLPServer captures all POST bodies sent to /v1/traces.
type fakeOTLPServer struct {
	mu   sync.Mutex
	reqs [][]byte // raw request bodies
}

func newFakeOTLPServer() (*httptest.Server, *fakeOTLPServer) {
	f := &fakeOTLPServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	// Accept /otlp/v1/traces as well (pipeline appends /otlp to base URL).
	mux.HandleFunc("/otlp/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.reqs = append(f.reqs, body)
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return httptest.NewServer(mux), f
}

func (f *fakeOTLPServer) bodies() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.reqs))
	copy(out, f.reqs)
	return out
}

// combinedBody concatenates all captured request bodies into a single byte
// slice for pattern searching.
func (f *fakeOTLPServer) combinedBody() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	var buf bytes.Buffer
	for _, b := range f.reqs {
		buf.Write(b)
	}
	return buf.Bytes()
}

// fakeBearerProvider returns a fixed token for the tokenRoundTripper.
func fakeBearerProvider(token string) BearerProvider {
	return func() (string, error) { return token, nil }
}

// TestOTLPPipeline_RedactsSecretAttributeBeforeTransmission is the core
// security regression test: a span attribute value containing an Anthropic
// API key must not appear in the raw OTLP payload sent to the fleet endpoint.
//
// The test exercises the real FleetOTLPPipeline.Activate → BatchSpanProcessor
// → redactingSpanExporter → resourceOverrideSpanExporter → otlptracehttp chain.
func TestOTLPPipeline_RedactsSecretAttributeBeforeTransmission(t *testing.T) {
	t.Parallel()

	srv, fake := newFakeOTLPServer()
	defer srv.Close()

	// The secret value we plant in the span attribute.
	const secretKey = "llm.api_key"
	const secretVal = "sk-ant-api03-AAAAAAAAAAAAAAAAAAAAAA-BBBBBBBBBBBBBBBBBBBBBBBB-CCCCCCCCCCCCCCCCCCCCCC-AAAA"
	const redactedLabel = "[REDACTED:anthropic-key]"

	// Build a real TracerProvider so the pipeline can register its
	// BatchSpanProcessor on it.
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			attribute.String("service.name", "harness-test"),
		)),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// Construct the FleetOTLPPipeline — the live OTLP path under test.
	pipeline := NewFleetOTLPPipeline(nil)

	// baseRes: minimal startup resource.
	baseRes := resource.NewWithAttributes(semconv.SchemaURL,
		attribute.String("service.name", "harness-test"),
	)

	bearer := fakeBearerProvider("test-pipeline-token")

	// Activate against the fake OTLP server. The pipeline appends "/v1/traces"
	// to the base URL (matching the mux handler above).
	otlpBase := srv.URL
	identity := IdentityAttrs{
		UserID:    "user-test-123",
		OrgID:     "org-test-456",
		MachineID: "machine-test-789",
	}
	ctx := context.Background()
	if err := pipeline.Activate(ctx, otlpBase, baseRes, identity, bearer, tp); err != nil {
		t.Fatalf("pipeline.Activate: %v", err)
	}

	// Emit a span containing the secret attribute via the real tracer.
	tracer := tp.Tracer("pipeline-redact-test")
	_, sp := tracer.Start(ctx, "sensitive-operation")
	sp.SetAttributes(attribute.String(secretKey, secretVal))
	sp.End()

	// Force flush so the BatchSpanProcessor ships the span before Shutdown.
	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	// Shutdown the pipeline (drains processors).
	shutCtx, shutCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutCancel()
	if err := pipeline.Shutdown(shutCtx); err != nil {
		t.Fatalf("pipeline.Shutdown: %v", err)
	}

	// ── Assertions ────────────────────────────────────────────────────────────

	combined := fake.combinedBody()
	if len(combined) == 0 {
		t.Fatal("fake OTLP server received no POST requests — spans never flushed")
	}

	// The raw secret must not appear in the transmitted binary payload.
	// String values in protobuf/proto3 are UTF-8 encoded verbatim, so a
	// substring match reliably catches unredacted secrets.
	if bytes.Contains(combined, []byte(secretVal)) {
		t.Errorf("SECURITY: raw secret %q found in OTLP payload — redaction did not fire on the live pipeline path", secretVal)
	}

	// The redacted label must be present (confirms redaction happened, not
	// that the attribute was simply dropped).
	if !bytes.Contains(combined, []byte(redactedLabel)) {
		t.Errorf("redacted label %q not found in OTLP payload — expected attribute to be redacted, not dropped; combined payload length=%d", redactedLabel, len(combined))
	}
}

// TestOTLPPipeline_RedactsSecretRef plants a @secret:<locator> span name and
// proves the live OTLP path replaces it before transmission.
func TestOTLPPipeline_RedactsSecretRef(t *testing.T) {
	t.Parallel()

	srv, fake := newFakeOTLPServer()
	defer srv.Close()

	const secretSpanName = "@secret:vault/prod-api-key-handle"
	const redactedLabel = "[REDACTED:secret-ref]"

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			attribute.String("service.name", "harness-test"),
		)),
	)
	defer func() { _ = tp.Shutdown(context.Background()) }()

	pipeline := NewFleetOTLPPipeline(nil)
	baseRes := resource.NewWithAttributes(semconv.SchemaURL,
		attribute.String("service.name", "harness-test"),
	)

	ctx := context.Background()
	if err := pipeline.Activate(ctx, srv.URL, baseRes, IdentityAttrs{
		UserID: "user-secret-ref-test",
		OrgID:  "org-secret-ref-test",
	}, fakeBearerProvider("token-secret-ref"), tp); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	tracer := tp.Tracer("secret-ref-test")
	_, sp := tracer.Start(ctx, secretSpanName)
	sp.End()

	flushCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := tp.ForceFlush(flushCtx); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	shutCtx, shutCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutCancel()
	_ = pipeline.Shutdown(shutCtx)

	combined := fake.combinedBody()
	if len(combined) == 0 {
		t.Fatal("no OTLP payload received — spans never flushed")
	}

	if bytes.Contains(combined, []byte(secretSpanName)) {
		t.Errorf("SECURITY: raw @secret span name %q found in OTLP payload", secretSpanName)
	}
	if !bytes.Contains(combined, []byte(redactedLabel)) {
		t.Errorf("redacted label %q not found in OTLP payload", redactedLabel)
	}
}

// TestOTLPPipeline_NoActivation_NoTraffic verifies that an unactivated pipeline
// (no endpoint wired) never sends HTTP traffic, even when spans are emitted.
func TestOTLPPipeline_NoActivation_NoTraffic(t *testing.T) {
	t.Parallel()

	srv, fake := newFakeOTLPServer()
	defer srv.Close()

	tp := sdktrace.NewTracerProvider()
	defer func() { _ = tp.Shutdown(context.Background()) }()

	// Construct but do NOT activate the pipeline.
	pipeline := NewFleetOTLPPipeline(nil)
	_ = pipeline // not activated — tp has no fleet processor registered

	tracer := tp.Tracer("no-activate-test")
	ctx := context.Background()
	_, sp := tracer.Start(ctx, "should-not-be-shipped")
	sp.End()

	flushCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = tp.ForceFlush(flushCtx)

	bodies := fake.bodies()
	if len(bodies) != 0 {
		t.Errorf("want 0 OTLP POSTs when pipeline is not activated, got %d", len(bodies))
	}
}
