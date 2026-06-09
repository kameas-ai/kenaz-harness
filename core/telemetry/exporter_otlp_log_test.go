package telemetry_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/telemetry"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// TestNewOTLPLogExporter_RoundTrip: a log record makes it through
// the SDK's batch processor to /v1/logs.
func TestNewOTLPLogExporter_RoundTrip(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPLogExporter(context.Background(), telemetry.OTLPLogConfig{
		Endpoint: rec.URL(),
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewOTLPLogExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("exporter nil")
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp,
			sdklog.WithExportInterval(50*time.Millisecond))),
	)
	defer lp.Shutdown(context.Background())

	var r log.Record
	r.SetTimestamp(time.Now())
	r.SetSeverity(log.SeverityInfo)
	r.SetBody(log.StringValue("hello-otlp"))
	lp.Logger("test").Emit(context.Background(), r)
	if err := lp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	waitForRequests(t, rec, 1, 2*time.Second)
	got := rec.Requests()[0]
	if got.Path != "/v1/logs" {
		t.Errorf("path = %q, want /v1/logs", got.Path)
	}
	if !strings.HasPrefix(got.ContentType, "application/x-protobuf") {
		t.Errorf("content-type = %q", got.ContentType)
	}
}

func TestNewOTLPLogExporter_HeadersForwarded(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPLogExporter(context.Background(), telemetry.OTLPLogConfig{
		Endpoint: rec.URL(),
		Insecure: true,
		Headers:  map[string]string{"X-Tenant": "kenaz"},
	})
	if err != nil {
		t.Fatalf("NewOTLPLogExporter: %v", err)
	}
	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exp,
			sdklog.WithExportInterval(50*time.Millisecond))),
	)
	defer lp.Shutdown(context.Background())
	var r log.Record
	r.SetTimestamp(time.Now())
	r.SetSeverity(log.SeverityInfo)
	r.SetBody(log.StringValue("auth-log"))
	lp.Logger("test").Emit(context.Background(), r)
	_ = lp.ForceFlush(context.Background())

	waitForRequests(t, rec, 1, 2*time.Second)
	if got := rec.Requests()[0].Headers.Get("X-Tenant"); got != "kenaz" {
		t.Errorf("X-Tenant = %q, want kenaz", got)
	}
}

func TestNewOTLPLogExporter_EmptyEndpoint(t *testing.T) {
	t.Parallel()
	exp, err := telemetry.NewOTLPLogExporter(context.Background(), telemetry.OTLPLogConfig{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if exp != nil {
		t.Fatalf("exporter = %v, want nil", exp)
	}
}

func TestNewOTLPLogExporter_HTTPRequiresInsecure(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewOTLPLogExporter(context.Background(), telemetry.OTLPLogConfig{
		Endpoint: "http://localhost:4318",
	}); err == nil {
		t.Errorf("expected error")
	}
}
