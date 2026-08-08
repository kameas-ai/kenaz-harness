package fleet_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// spaFallbackHandler imitates a CloudFront-fronted S3 SPA bucket: every path,
// including POST /otlp/v1/traces, answers 200 with index.html. This is the
// exact behaviour of https://fleet.kameas.ai, and the reason telemetry aimed
// at the dashboard origin disappeared without a single error.
func spaFallbackHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("<!doctype html><html><head><title>Kameas Fleet</title></head><body><div id=\"root\"></div></body></html>"))
	})
}

// otlpAckHandler imitates a real OTLP/HTTP receiver: 200 with a protobuf
// ExportServiceResponse. (An empty ExportTraceServiceResponse message
// serialises to zero bytes, which is what a receiver actually sends.)
func otlpAckHandler(t *testing.T, seen *int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*seen++
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	})
}

func bearerOK() fleet.BearerProvider {
	return func() (string, error) { return "test-token", nil }
}

// ── The regression that matters ──────────────────────────────────────────────

// TestExportSpans_SPAFallbackIsNotASuccessfulExport is the core test for this
// fix: a 200 response that is not an OTLP acknowledgement must NOT be reported
// as a successful export. Without the ack round-tripper this test fails —
// ExportSpans returns nil and the spans are silently dropped.
func TestExportSpans_SPAFallbackIsNotASuccessfulExport(t *testing.T) {
	srv := httptest.NewServer(spaFallbackHandler())
	defer srv.Close()

	exp := newTraceExporter(t, srv.URL, fleet.NewOTLPAckRoundTripper(
		fleet.NewTokenRoundTripper(bearerOK(), nil),
	))
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	err := exp.ExportSpans(context.Background(), recordedSpans(t))
	if err == nil {
		t.Fatal("ExportSpans returned nil for a 200 text/html SPA fall-through — " +
			"a non-OTLP success is being treated as a successful export; " +
			"telemetry would be silently discarded")
	}
	if !strings.Contains(err.Error(), "not an OTLP acknowledgement") {
		t.Errorf("error should identify the response as a non-ack, got: %v", err)
	}
}

// TestExportSpans_RealAckSucceeds is the control: with the same wrapper in
// place, a genuine OTLP receiver still exports cleanly. The fence must not
// break the happy path.
func TestExportSpans_RealAckSucceeds(t *testing.T) {
	var seen int32
	srv := httptest.NewServer(otlpAckHandler(t, &seen))
	defer srv.Close()

	exp := newTraceExporter(t, srv.URL, fleet.NewOTLPAckRoundTripper(
		fleet.NewTokenRoundTripper(bearerOK(), nil),
	))
	t.Cleanup(func() { _ = exp.Shutdown(context.Background()) })

	if err := exp.ExportSpans(context.Background(), recordedSpans(t)); err != nil {
		t.Fatalf("ExportSpans against a real OTLP ack failed: %v", err)
	}
	if seen == 0 {
		t.Error("receiver never saw the export request")
	}
}

// ── Round-tripper unit coverage ──────────────────────────────────────────────

func TestOTLPAckRoundTripper_Responses(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		wantErr     bool
	}{
		{
			name:        "200 text/html SPA fall-through is rejected",
			status:      200,
			contentType: "text/html",
			body:        "<!doctype html><html></html>",
			wantErr:     true,
		},
		{
			name:        "200 text/html with charset is rejected",
			status:      200,
			contentType: "text/html; charset=utf-8",
			body:        "<!doctype html>",
			wantErr:     true,
		},
		{
			name:        "200 text/plain is rejected",
			status:      200,
			contentType: "text/plain",
			body:        "ok",
			wantErr:     true,
		},
		{
			name:        "200 application/x-protobuf is accepted",
			status:      200,
			contentType: "application/x-protobuf",
			wantErr:     false,
		},
		{
			name:        "200 application/json is accepted",
			status:      200,
			contentType: "application/json",
			body:        "{}",
			wantErr:     false,
		},
		{
			name:        "202 application/x-protobuf is accepted",
			status:      202,
			contentType: "application/x-protobuf",
			wantErr:     false,
		},
		{
			name:    "200 with no content-type and an empty body is accepted",
			status:  200,
			wantErr: false,
		},
		{
			name:    "200 with no content-type but a body is rejected",
			status:  200,
			body:    "<html>surprise</html>",
			wantErr: true,
		},
		// Non-2xx passes through untouched so the SDK exporter keeps its own
		// retry/backoff semantics for 429 and 5xx.
		{
			name:        "503 passes through without a synthetic error",
			status:      503,
			contentType: "text/html",
			body:        "<html>gateway</html>",
			wantErr:     false,
		},
		{
			name:        "401 passes through without a synthetic error",
			status:      401,
			contentType: "text/html",
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tt.contentType != "" {
					w.Header().Set("Content-Type", tt.contentType)
				} else {
					// Stop net/http from sniffing a Content-Type for us.
					w.Header()["Content-Type"] = nil
				}
				w.WriteHeader(tt.status)
				if tt.body != "" {
					_, _ = w.Write([]byte(tt.body))
				}
			}))
			defer srv.Close()

			client := &http.Client{Transport: fleet.NewOTLPAckRoundTripper(nil)}
			resp, err := client.Post(srv.URL+"/v1/traces", "application/x-protobuf", strings.NewReader(""))
			if resp != nil {
				_ = resp.Body.Close()
			}

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error for a success response that is not an OTLP ack")
				}
				if !errors.Is(err, fleet.ErrNotOTLPAck) {
					t.Errorf("error should wrap ErrNotOTLPAck, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if resp.StatusCode != tt.status {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.status)
			}
		})
	}
}

// TestOTLPAckRoundTripper_PreservesAuthHeader confirms composition order:
// the ack wrapper sits outside the token round-tripper and must not interfere
// with the Bearer injection it performs.
func TestOTLPAckRoundTripper_PreservesAuthHeader(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := &http.Client{
		Transport: fleet.NewOTLPAckRoundTripper(fleet.NewTokenRoundTripper(bearerOK(), nil)),
	}
	resp, err := client.Post(srv.URL+"/v1/traces", "application/x-protobuf", strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = resp.Body.Close()

	if got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer test-token")
	}
}

// TestOTLPAckRoundTripper_PropagatesInnerError confirms a genuine transport
// failure is passed through unchanged rather than reshaped into an ack error.
func TestOTLPAckRoundTripper_PropagatesInnerError(t *testing.T) {
	client := &http.Client{
		Transport: fleet.NewOTLPAckRoundTripper(fleet.NewTokenRoundTripper(
			func() (string, error) { return "", nil }, // no token → request skipped
			nil,
		)),
	}
	_, err := client.Post("http://127.0.0.1:1/v1/traces", "application/x-protobuf", strings.NewReader(""))
	if err == nil {
		t.Fatal("expected the inner no-token error to propagate")
	}
	if errors.Is(err, fleet.ErrNotOTLPAck) {
		t.Errorf("inner transport error should not be reclassified as an ack failure: %v", err)
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

func newTraceExporter(t *testing.T, baseURL string, rt http.RoundTripper) *otlptrace.Exporter {
	t.Helper()
	exp, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpointURL(baseURL+"/otlp"),
		otlptracehttp.WithURLPath("/v1/traces"),
		otlptracehttp.WithHTTPClient(&http.Client{Transport: rt, Timeout: 5 * time.Second}),
		// Keep failures immediate: this suite asserts on the first outcome,
		// not on the SDK's backoff behaviour.
		otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
	)
	if err != nil {
		t.Fatalf("construct exporter: %v", err)
	}
	return exp
}

func recordedSpans(t *testing.T) []sdktrace.ReadOnlySpan {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	_, span := tp.Tracer("otlp-ack-test").Start(context.Background(), "unit")
	span.End()
	spans := sr.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 recorded span, got %d", len(spans))
	}
	return spans
}
