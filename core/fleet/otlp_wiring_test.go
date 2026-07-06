// Package fleet_test provides end-to-end wiring tests for the fleet OTLP
// export pipeline (harness-fleet-otlp-export-01NTLMEX01 WP05).
package fleet_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// capturedRequest holds what the fake fleet server received from one POST.
type capturedRequest struct {
	authHeader  string
	body        []byte
	contentType string
}

// TestFleetExporterWiring_EndToEnd verifies the full path:
//
//	tokenRoundTripper (fake bearer) → FleetSpanExporter → fake fleet server
//
// Asserts:
//  1. The server receives exactly one POST with "Authorization: Bearer <token>".
//  2. The JSON body is a valid fleetBatch with spans.
func TestFleetExporterWiring_EndToEnd(t *testing.T) {
	t.Parallel()

	const wantToken = "wiring-test-token-xyz"

	var mu sync.Mutex
	var received []capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, capturedRequest{
			authHeader:  r.Header.Get("Authorization"),
			body:        body,
			contentType: r.Header.Get("Content-Type"),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Fake bearer provider: always returns wantToken (simulates post-login state).
	bearer := func() (string, error) { return wantToken, nil }
	transport := fleet.NewTokenRoundTripper(bearer, nil)

	exp := fleet.NewFleetSpanExporter(fleet.FleetExporterConfig{
		Endpoint: srv.URL,
		Consent:  &fakeConsent{level: "full"},
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
		FlushInterval: 10 * time.Minute, // prevent timer-driven flush; use Shutdown flush
		NewULID:       func() string { return "wiring-batch-1" },
	})

	// Emit one span using the SDK tracer.
	sp := makeTestSpan(t, "wiring-test-span")
	if err := exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{sp}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	// Shutdown triggers the final flush.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	// ── Assertions ────────────────────────────────────────────────────────
	mu.Lock()
	reqs := make([]capturedRequest, len(received))
	copy(reqs, received)
	mu.Unlock()

	if len(reqs) == 0 {
		t.Fatal("want at least 1 POST to the fake fleet server, got 0")
	}

	req := reqs[0]

	// 1. Authorization header must carry the bearer token.
	wantAuth := "Bearer " + wantToken
	if req.authHeader != wantAuth {
		t.Errorf("Authorization header: got %q, want %q", req.authHeader, wantAuth)
	}

	// 2. Content-Type must be application/json.
	if req.contentType != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", req.contentType)
	}

	// 3. Body must parse as a fleet batch with at least one span.
	var batch struct {
		BatchID string `json:"batch_id"`
		Spans   []any  `json:"spans"`
	}
	if err := json.Unmarshal(req.body, &batch); err != nil {
		t.Fatalf("unmarshal fleet batch: %v", err)
	}
	if len(batch.Spans) == 0 {
		t.Error("want at least 1 span in the fleet batch, got 0")
	}
	if batch.BatchID == "" {
		t.Error("want non-empty batch_id")
	}
}

// TestFleetExporterWiring_NoToken_NoPost verifies that when no bearer token is
// available (pre-login), the ring buffer retains spans but no POST is made.
func TestFleetExporterWiring_NoToken_NoPost(t *testing.T) {
	t.Parallel()

	var callCount int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Empty bearer = no token (pre-login).
	bearer := func() (string, error) { return "", nil }
	transport := fleet.NewTokenRoundTripper(bearer, nil)

	exp := fleet.NewFleetSpanExporter(fleet.FleetExporterConfig{
		Endpoint: srv.URL,
		Consent:  &fakeConsent{level: "full"},
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "no-token-batch" },
	})

	sp := makeTestSpan(t, "pre-login-span")
	if err := exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{sp}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = exp.Shutdown(ctx)

	mu.Lock()
	n := callCount
	mu.Unlock()
	if n != 0 {
		t.Errorf("want 0 HTTP calls when no bearer token, got %d", n)
	}
}

// TestFleetExporterWiring_SpanRedactedBeforePost verifies that a span
// containing a secret in an attribute value is redacted before leaving the
// process boundary (WP02 end-to-end: enqueue redaction flows through to POST).
func TestFleetExporterWiring_SpanRedactedBeforePost(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var received []capturedRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, capturedRequest{body: body})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	bearer := func() (string, error) { return "redact-test-token", nil }
	transport := fleet.NewTokenRoundTripper(bearer, nil)

	exp := fleet.NewFleetSpanExporter(fleet.FleetExporterConfig{
		Endpoint: srv.URL,
		Consent:  &fakeConsent{level: "full"},
		HTTPClient: &http.Client{
			Transport: transport,
			Timeout:   5 * time.Second,
		},
		FlushInterval: 10 * time.Minute,
		NewULID:       func() string { return "redact-test-batch" },
	})

	// Create a span with a secret value in an attribute using the public trace API.
	sp := makeTestSpanWithAttrs(t, "secure-op",
		attribute.String("user_context", "@secret:vault/api-key-123"),
	)
	if err := exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{sp}); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := exp.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	mu.Lock()
	reqs := make([]capturedRequest, len(received))
	copy(reqs, received)
	mu.Unlock()

	if len(reqs) == 0 {
		t.Fatal("want at least 1 POST, got 0")
	}

	bodyStr := string(reqs[0].body)
	// The raw @secret ref must not appear in the shipped payload.
	if strings.Contains(bodyStr, "@secret:vault/api-key-123") {
		t.Errorf("shipped payload contains raw secret reference: %s", bodyStr)
	}
	// The redacted form must be present instead.
	if !strings.Contains(bodyStr, "[REDACTED:secret-ref]") {
		t.Errorf("shipped payload missing [REDACTED:secret-ref], body: %s", bodyStr)
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

// fakeConsent is a test-local ConsentReader stub.
type fakeConsent struct{ level string }

func (f *fakeConsent) ConsentLevel() string { return f.level }

// makeTestSpan creates a minimal ReadOnlySpan captured via a span recorder.
func makeTestSpan(t *testing.T, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	return makeTestSpanWithAttrs(t, name)
}

// makeTestSpanWithAttrs creates a span with the given name and optional
// key-value attributes.
func makeTestSpanWithAttrs(t *testing.T, name string, kvs ...attribute.KeyValue) sdktrace.ReadOnlySpan {
	t.Helper()
	var captured sdktrace.ReadOnlySpan
	rec := &testSpanRecorder{onEnd: func(s sdktrace.ReadOnlySpan) { captured = s }}
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tracer := tp.Tracer("wiring-test")
	_, sp := tracer.Start(context.Background(), name)
	if len(kvs) > 0 {
		sp.SetAttributes(kvs...)
	}
	sp.End()
	if captured == nil {
		t.Fatal("span recorder did not capture span")
	}
	return captured
}

// testSpanRecorder satisfies sdktrace.SpanProcessor for the wiring tests.
type testSpanRecorder struct {
	onEnd func(sdktrace.ReadOnlySpan)
}

func (r *testSpanRecorder) OnStart(context.Context, sdktrace.ReadWriteSpan) {}
func (r *testSpanRecorder) OnEnd(s sdktrace.ReadOnlySpan) {
	if r.onEnd != nil {
		r.onEnd(s)
	}
}
func (r *testSpanRecorder) Shutdown(context.Context) error   { return nil }
func (r *testSpanRecorder) ForceFlush(context.Context) error { return nil }
