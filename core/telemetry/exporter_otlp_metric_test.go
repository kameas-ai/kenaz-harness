package telemetry_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// TestNewOTLPMetricExporter_RoundTrip: the metric SDK's periodic
// reader pushes through to the receiver at /v1/metrics.
func TestNewOTLPMetricExporter_RoundTrip(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPMetricExporter(context.Background(), telemetry.OTLPMetricConfig{
		Endpoint: rec.URL(),
		Insecure: true,
	})
	if err != nil {
		t.Fatalf("NewOTLPMetricExporter: %v", err)
	}
	if exp == nil {
		t.Fatal("exporter nil")
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(50*time.Millisecond))),
	)
	defer mp.Shutdown(context.Background())

	counter, err := mp.Meter("test").Int64Counter("otlp.metric.count")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(context.Background(), 7)
	if err := mp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("ForceFlush: %v", err)
	}

	waitForRequests(t, rec, 1, 2*time.Second)
	got := rec.Requests()[0]
	if got.Path != "/v1/metrics" {
		t.Errorf("path = %q, want /v1/metrics", got.Path)
	}
	if !strings.HasPrefix(got.ContentType, "application/x-protobuf") {
		t.Errorf("content-type = %q", got.ContentType)
	}
}

func TestNewOTLPMetricExporter_HeadersForwarded(t *testing.T) {
	t.Parallel()
	rec := newOTLPRecorder(t)
	exp, err := telemetry.NewOTLPMetricExporter(context.Background(), telemetry.OTLPMetricConfig{
		Endpoint: rec.URL(),
		Insecure: true,
		Headers:  map[string]string{"Authorization": "Bearer abc"},
	})
	if err != nil {
		t.Fatalf("NewOTLPMetricExporter: %v", err)
	}
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(50*time.Millisecond))),
	)
	defer mp.Shutdown(context.Background())
	counter, _ := mp.Meter("test").Int64Counter("auth.metric")
	counter.Add(context.Background(), 1)
	_ = mp.ForceFlush(context.Background())

	waitForRequests(t, rec, 1, 2*time.Second)
	if got := rec.Requests()[0].Headers.Get("Authorization"); got != "Bearer abc" {
		t.Errorf("Authorization = %q, want Bearer abc", got)
	}
}

func TestNewOTLPMetricExporter_EmptyEndpoint(t *testing.T) {
	t.Parallel()
	exp, err := telemetry.NewOTLPMetricExporter(context.Background(), telemetry.OTLPMetricConfig{})
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if exp != nil {
		t.Fatalf("exporter = %v, want nil", exp)
	}
}

func TestNewOTLPMetricExporter_HTTPRequiresInsecure(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewOTLPMetricExporter(context.Background(), telemetry.OTLPMetricConfig{
		Endpoint: "http://localhost:4318",
	}); err == nil {
		t.Errorf("expected error")
	}
}
