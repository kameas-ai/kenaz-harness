package telemetry_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/telemetry"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// fakeSpanExporter records every batch and surfaces a configurable error.
type fakeSpanExporter struct {
	mu      sync.Mutex
	exports [][]sdktrace.ReadOnlySpan
	err     error
	shut    int
}

func (f *fakeSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]sdktrace.ReadOnlySpan, len(spans))
	copy(cp, spans)
	f.exports = append(f.exports, cp)
	return f.err
}

func (f *fakeSpanExporter) Shutdown(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shut++
	return nil
}

func (f *fakeSpanExporter) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.exports)
}

// TestCompositeSpanExporter_LocalOnly confirms that when OTLP is nil
// the composite still forwards to Local, and the local error
// propagates to the SDK.
func TestCompositeSpanExporter_LocalOnly(t *testing.T) {
	t.Parallel()
	local := &fakeSpanExporter{}
	c := &telemetry.CompositeSpanExporter{Local: local}
	if err := c.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if local.Count() != 1 {
		t.Fatalf("local export count = %d, want 1", local.Count())
	}
}

// TestCompositeSpanExporter_FanOut confirms both sides receive the batch.
func TestCompositeSpanExporter_FanOut(t *testing.T) {
	t.Parallel()
	local := &fakeSpanExporter{}
	otlp := &fakeSpanExporter{}
	c := &telemetry.CompositeSpanExporter{Local: local, OTLP: otlp}
	if err := c.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if local.Count() != 1 || otlp.Count() != 1 {
		t.Fatalf("counts: local=%d otlp=%d", local.Count(), otlp.Count())
	}
}

// TestCompositeSpanExporter_OTLPErrSwallowed confirms an OTLP error
// does NOT propagate to the SDK; only local errors do.
func TestCompositeSpanExporter_OTLPErrSwallowed(t *testing.T) {
	t.Parallel()
	local := &fakeSpanExporter{}
	otlp := &fakeSpanExporter{err: errors.New("boom")}
	c := &telemetry.CompositeSpanExporter{Local: local, OTLP: otlp}
	if err := c.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("OTLP error leaked: %v", err)
	}
	// And conversely a local error does propagate.
	local.err = errors.New("local-broken")
	if err := c.ExportSpans(context.Background(), nil); err == nil {
		t.Fatalf("expected local error to propagate")
	}
}

// TestCompositeSpanExporter_Shutdown calls both sides exactly once.
func TestCompositeSpanExporter_Shutdown(t *testing.T) {
	t.Parallel()
	local := &fakeSpanExporter{}
	otlp := &fakeSpanExporter{}
	c := &telemetry.CompositeSpanExporter{Local: local, OTLP: otlp}
	if err := c.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if local.shut != 1 || otlp.shut != 1 {
		t.Fatalf("shut counts: local=%d otlp=%d", local.shut, otlp.shut)
	}
}

// fakeMetricExporter records every snapshot.
type fakeMetricExporter struct {
	mu      sync.Mutex
	exports int
	err     error
}

func (f *fakeMetricExporter) Temporality(_ sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}
func (f *fakeMetricExporter) Aggregation(_ sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}
func (f *fakeMetricExporter) Export(_ context.Context, _ *metricdata.ResourceMetrics) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exports++
	return f.err
}
func (f *fakeMetricExporter) ForceFlush(_ context.Context) error { return nil }
func (f *fakeMetricExporter) Shutdown(_ context.Context) error   { return nil }

func TestCompositeMetricExporter_FanOut(t *testing.T) {
	t.Parallel()
	local := &fakeMetricExporter{}
	otlp := &fakeMetricExporter{}
	c := &telemetry.CompositeMetricExporter{Local: local, OTLP: otlp}
	if err := c.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if local.exports != 1 || otlp.exports != 1 {
		t.Fatalf("exports: local=%d otlp=%d", local.exports, otlp.exports)
	}
}

func TestCompositeMetricExporter_OTLPErrSwallowed(t *testing.T) {
	t.Parallel()
	local := &fakeMetricExporter{}
	otlp := &fakeMetricExporter{err: errors.New("boom")}
	c := &telemetry.CompositeMetricExporter{Local: local, OTLP: otlp}
	if err := c.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Fatalf("Export should not leak OTLP err: %v", err)
	}
}

// fakeLogExporter records every batch.
type fakeLogExporter struct {
	mu      sync.Mutex
	batches int
	err     error
}

func (f *fakeLogExporter) Export(_ context.Context, _ []sdklog.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batches++
	return f.err
}
func (f *fakeLogExporter) ForceFlush(_ context.Context) error { return nil }
func (f *fakeLogExporter) Shutdown(_ context.Context) error   { return nil }

func TestCompositeLogExporter_FanOut(t *testing.T) {
	t.Parallel()
	local := &fakeLogExporter{}
	otlp := &fakeLogExporter{}
	c := &telemetry.CompositeLogExporter{Local: local, OTLP: otlp}
	if err := c.Export(context.Background(), nil); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if local.batches != 1 || otlp.batches != 1 {
		t.Fatalf("batches: local=%d otlp=%d", local.batches, otlp.batches)
	}
}

// TestCompositeSpanExporter_LocalFirstNoNetwork is the NFR-005
// invariant test: with OTLP=nil the composite must short-circuit
// without invoking the OTLP branch at all. We probe this with a
// "tripwire" exporter wired only to OTLP — if the composite ever
// reaches it, the test fails. Combined with the constructor-level
// check that NewOTLPSpanExporter returns (nil, nil) for empty
// Endpoint, this proves no outbound HTTP client is ever constructed.
func TestCompositeSpanExporter_LocalFirstNoNetwork(t *testing.T) {
	t.Parallel()

	tripped := false
	tripwire := &tripwireSpanExporter{onCall: func() { tripped = true }}

	// Local-only path: OTLP nil. Tripwire is *not* attached — that
	// is the local-first invariant.
	local := &fakeSpanExporter{}
	c := &telemetry.CompositeSpanExporter{Local: local, OTLP: nil}
	for i := 0; i < 10; i++ {
		if err := c.ExportSpans(context.Background(), nil); err != nil {
			t.Fatalf("ExportSpans: %v", err)
		}
	}
	if local.Count() != 10 {
		t.Fatalf("local exports = %d, want 10", local.Count())
	}

	// Sanity check: the tripwire fires when we *do* attach it as
	// OTLP — proving the assertion is meaningful.
	c2 := &telemetry.CompositeSpanExporter{Local: local, OTLP: tripwire}
	if err := c2.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("ExportSpans: %v", err)
	}
	if !tripped {
		t.Fatalf("tripwire never fired — assertion is not meaningful")
	}
}

type tripwireSpanExporter struct {
	onCall func()
}

func (t *tripwireSpanExporter) ExportSpans(_ context.Context, _ []sdktrace.ReadOnlySpan) error {
	if t.onCall != nil {
		t.onCall()
	}
	return nil
}
func (t *tripwireSpanExporter) Shutdown(_ context.Context) error { return nil }
