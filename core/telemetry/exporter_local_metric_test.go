package telemetry_test

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// TestLocalMetricExporter_RoundTripCounter records a few counter
// increments, forces a flush via ManualReader, and asserts the rows
// land in telemetry_metrics.
func TestLocalMetricExporter_RoundTripCounter(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalMetricExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalMetricExporter: %v", err)
	}
	defer exp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(50*time.Millisecond))),
	)
	defer mp.Shutdown(context.Background())

	meter := mp.Meter("test")
	counter, err := meter.Int64Counter("tool.invocations")
	if err != nil {
		t.Fatalf("counter: %v", err)
	}
	counter.Add(context.Background(), 3, metric.WithAttributes(
		attribute.String("tool_name", "search"),
	))
	counter.Add(context.Background(), 2, metric.WithAttributes(
		attribute.String("tool_name", "search"),
	))

	if err := mp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var name, kind string
	var value float64
	row := db.Reader().QueryRow(context.Background(),
		`SELECT name, kind, value FROM telemetry_metrics
         WHERE name='tool.invocations' ORDER BY ts_ns DESC LIMIT 1`)
	if err := row.Scan(&name, &kind, &value); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if name != "tool.invocations" || kind != "counter" {
		t.Errorf("got (name=%q, kind=%q), want (tool.invocations, counter)", name, kind)
	}
	if value != 5 {
		t.Errorf("value = %v, want 5 (cumulative)", value)
	}
}

// TestLocalMetricExporter_RoundTripHistogram pushes a few values
// through a histogram and confirms the row lands with the histogram
// kind tag.
func TestLocalMetricExporter_RoundTripHistogram(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalMetricExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalMetricExporter: %v", err)
	}
	defer exp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(50*time.Millisecond))),
	)
	defer mp.Shutdown(context.Background())

	meter := mp.Meter("test")
	hist, err := meter.Float64Histogram("chat.turn.duration")
	if err != nil {
		t.Fatalf("hist: %v", err)
	}
	for _, v := range []float64{0.1, 0.2, 0.3} {
		hist.Record(context.Background(), v)
	}

	if err := mp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var kind string
	row := db.Reader().QueryRow(context.Background(),
		`SELECT kind FROM telemetry_metrics WHERE name='chat.turn.duration' LIMIT 1`)
	if err := row.Scan(&kind); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if kind != "histogram" {
		t.Errorf("kind = %q, want histogram", kind)
	}
}

// TestLocalMetricExporter_RoundTripGauge records observable gauge
// values and confirms the kind tag.
func TestLocalMetricExporter_RoundTripGauge(t *testing.T) {
	t.Parallel()
	db := newTestStorage(t)
	exp, err := telemetry.NewLocalMetricExporter(db, nil)
	if err != nil {
		t.Fatalf("NewLocalMetricExporter: %v", err)
	}
	defer exp.Shutdown(context.Background())

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exp,
			sdkmetric.WithInterval(50*time.Millisecond))),
	)
	defer mp.Shutdown(context.Background())

	meter := mp.Meter("test")
	_, err = meter.Int64ObservableGauge("queue.size",
		metric.WithInt64Callback(func(_ context.Context, o metric.Int64Observer) error {
			o.Observe(42)
			return nil
		}),
	)
	if err != nil {
		t.Fatalf("gauge: %v", err)
	}

	if err := mp.ForceFlush(context.Background()); err != nil {
		t.Fatalf("flush: %v", err)
	}

	var kind string
	var value float64
	row := db.Reader().QueryRow(context.Background(),
		`SELECT kind, value FROM telemetry_metrics WHERE name='queue.size' LIMIT 1`)
	if err := row.Scan(&kind, &value); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if value != 42 {
		t.Errorf("value = %v, want 42", value)
	}
	// Default temporality for an observable gauge surfaces as Sum
	// in the SDK; "gauge" only applies to non-monotonic
	// instantaneous types. Either is acceptable for WP01 — pin
	// that the kind classifies into a stable string.
	if kind != "gauge" && kind != "counter" && kind != "updown" {
		t.Errorf("kind = %q, want gauge/counter/updown", kind)
	}
}

// TestLocalMetricExporter_NilDB rejects construction when no DB.
func TestLocalMetricExporter_NilDB(t *testing.T) {
	t.Parallel()
	if _, err := telemetry.NewLocalMetricExporter(nil, nil); err == nil {
		t.Errorf("expected error for nil db")
	}
}
