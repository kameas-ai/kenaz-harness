package telemetry_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// otlpRecorder is a minimal httptest.Server that records every
// inbound OTLP/HTTP request and replies with an empty 200 response.
// We do NOT decode the protobuf body — verifying the request reached
// the right path with the right headers + non-empty body suffices for
// the "OTLP exporter wires through" assertion. Full protobuf decoding
// would drag the OTLP collector proto sub-modules in just for tests.
type otlpRecorder struct {
	server *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

type recordedRequest struct {
	Path        string
	ContentType string
	Headers     http.Header
	Body        []byte
}

func newOTLPRecorder(t *testing.T) *otlpRecorder {
	t.Helper()
	r := &otlpRecorder{}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		_ = req.Body.Close()
		r.mu.Lock()
		r.requests = append(r.requests, recordedRequest{
			Path:        req.URL.Path,
			ContentType: req.Header.Get("Content-Type"),
			Headers:     req.Header.Clone(),
			Body:        body,
		})
		r.mu.Unlock()
		// OTLP/HTTP success responses carry an empty protobuf — any
		// 200 with valid Content-Type is accepted by the SDK.
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(nil)
	}))
	t.Cleanup(r.server.Close)
	return r
}

func (r *otlpRecorder) URL() string { return r.server.URL }

func (r *otlpRecorder) Requests() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]recordedRequest, len(r.requests))
	copy(cp, r.requests)
	return cp
}

func waitForRequests(t *testing.T, r *otlpRecorder, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(r.Requests()) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("recorder saw %d requests, want >= %d", len(r.Requests()), want)
}

// TestNewOTLPSpanExporter_RoundTrip: a span exported via the SDK
// reaches the fake collector at /v1/traces with content-type
// application/x-protobuf.
func TestNewOTLPSpanExporter_RoundTrip(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{
		Endpoint: rec.URL(),
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewOTLPSpanExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("exporter nil")
	}
	defer exp.Shutdown(context.Background())

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer tp.Shutdown(context.Background())
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "otlp.span", trace.WithSpanKind(trace.SpanKindInternal))
	span.End()
	if err := tp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	waitForRequests(t, rec, 1, 2*time.Second)
	got := rec.Requests()[0]
	if got.Path != "/v1/traces" {
		t.Errorf("path = %q, want /v1/traces", got.Path)
	}
	if !strings.HasPrefix(got.ContentType, "application/x-protobuf") {
		t.Errorf("content-type = %q, want application/x-protobuf prefix", got.ContentType)
	}
	if len(got.Body) == 0 {
		t.Errorf("body empty, want non-zero protobuf payload")
	}
}

// TestNewOTLPSpanExporter_HeadersForwarded asserts custom auth
// headers reach the collector.
func TestNewOTLPSpanExporter_HeadersForwarded(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{
		Endpoint: rec.URL(),
		Insecure: true,
		Headers:  map[string]string{"Authorization": "Bearer xyz"},
	})
	if err != nil {
		t.Fatalf("NewOTLPSpanExporter: %v", err)
	}
	defer exp.Shutdown(context.Background())

	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exp))
	defer tp.Shutdown(context.Background())
	_, span := tp.Tracer("test").Start(context.Background(), "auth.span")
	span.End()
	_ = tp.ForceFlush(context.Background())

	waitForRequests(t, rec, 1, 2*time.Second)
	if got := rec.Requests()[0].Headers.Get("Authorization"); got != "Bearer xyz" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer xyz")
	}
}

// TestNewOTLPSpanExporter_EmptyEndpoint returns (nil, nil) so the
// composite can skip the OTLP branch without error handling.
func TestNewOTLPSpanExporter_EmptyEndpoint(t *testing.T) {
	t.Parallel()
	exp, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{})
	if err != nil {
		t.Fatalf("err = %v, want nil for empty endpoint", err)
	}
	if exp != nil {
		t.Fatalf("exporter = %v, want nil for empty endpoint", exp)
	}
}

// TestNewOTLPSpanExporter_HTTPRequiresInsecure pins the safety check:
// http:// endpoints must explicitly opt-in to cleartext.
func TestNewOTLPSpanExporter_HTTPRequiresInsecure(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{
		Endpoint: "http://localhost:4318",
		Insecure: false,
	}); err == nil {
		t.Errorf("expected error for http:// without Insecure")
	}
}

// TestNewOTLPSpanExporter_RejectsBogusScheme rejects ftp/etc.
func TestNewOTLPSpanExporter_RejectsBogusScheme(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{
		Endpoint: "ftp://nope:9999",
	}); err == nil {
		t.Errorf("expected error for ftp:// scheme")
	}
}

// TestNewOTLPSpanExporter_BackoffOnDeadEndpoint asserts the exporter
// does not crash when the receiver is unreachable. The SDK's batcher
// retries internally; we just ensure ForceFlush returns within a
// bounded window without panic.
func TestNewOTLPSpanExporter_BackoffOnDeadEndpoint(t *testing.T) {
	t.Parallel()
	// Bind, immediately close — port is now refused.
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()

	exp, err := telemetry.NewOTLPSpanExporter(context.Background(), telemetry.OTLPSpanConfig{
		Endpoint: url,
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewOTLPSpanExporter: %v", err)
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp,
		sdktrace.WithBatchTimeout(20*time.Millisecond)))
	tracer := tp.Tracer("test")
	_, span := tracer.Start(context.Background(), "dead.span")
	span.End()

	done := make(chan struct{})
	var didShutdown atomic.Bool
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		defer cancel()
		_ = tp.Shutdown(ctx)
		didShutdown.Store(true)
		close(done)
	}()
	select {
	case <-done:
		// Successful or bounded shutdown; either is fine — we just
		// need "no panic, no hang past the bound".
	case <-time.After(3 * time.Second):
		t.Fatalf("Shutdown hung past 3s on dead endpoint")
	}
	if !didShutdown.Load() {
		t.Fatalf("Shutdown did not complete")
	}
}
