package otel_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	coreotel "github.com/kameas-ai/kenaz-harness/core/otel"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ── fake span exporter ────────────────────────────────────────────────────

type fakeSpanExporter struct {
	mu      sync.Mutex
	batches [][]sdktrace.ReadOnlySpan
	retErr  error
	closed  bool
}

func (f *fakeSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]sdktrace.ReadOnlySpan, len(spans))
	copy(cp, spans)
	f.batches = append(f.batches, cp)
	return f.retErr
}

func (f *fakeSpanExporter) Shutdown(_ context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return f.retErr
}

func (f *fakeSpanExporter) snapshot() [][]sdktrace.ReadOnlySpan {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]sdktrace.ReadOnlySpan, len(f.batches))
	copy(out, f.batches)
	return out
}

// ── fake metric exporter ─────────────────────────────────────────────────

type fakeMetricExporter struct {
	mu      sync.Mutex
	exports int
	retErr  error
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
	return f.retErr
}
func (f *fakeMetricExporter) ForceFlush(_ context.Context) error { return f.retErr }
func (f *fakeMetricExporter) Shutdown(_ context.Context) error   { return f.retErr }

func (f *fakeMetricExporter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exports
}

// ── fake log exporter ─────────────────────────────────────────────────────

type fakeLogExporter struct {
	mu      sync.Mutex
	exports int
	retErr  error
}

func (f *fakeLogExporter) Export(_ context.Context, _ []sdklog.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exports++
	return f.retErr
}
func (f *fakeLogExporter) ForceFlush(_ context.Context) error { return f.retErr }
func (f *fakeLogExporter) Shutdown(_ context.Context) error   { return f.retErr }

func (f *fakeLogExporter) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.exports
}

// ── tests ─────────────────────────────────────────────────────────────────

func TestMultiSpanExporter_TwoChildrenReceiveBatch(t *testing.T) {
	t.Parallel()
	a := &fakeSpanExporter{}
	b := &fakeSpanExporter{}
	m := coreotel.NewMultiSpanExporter(a, b)

	if err := m.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.snapshot()) != 1 {
		t.Errorf("child a: want 1 batch, got %d", len(a.snapshot()))
	}
	if len(b.snapshot()) != 1 {
		t.Errorf("child b: want 1 batch, got %d", len(b.snapshot()))
	}
}

func TestMultiSpanExporter_FirstErrorReturned_AllChildrenCalled(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("child a exploded")
	a := &fakeSpanExporter{retErr: sentinel}
	b := &fakeSpanExporter{} // no error

	m := coreotel.NewMultiSpanExporter(a, b)
	err := m.ExportSpans(context.Background(), nil)
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
	// child b must still have been called despite a's error
	if len(b.snapshot()) != 1 {
		t.Errorf("child b should still be called; got %d batches", len(b.snapshot()))
	}
}

func TestMultiSpanExporter_NilChildDropped(t *testing.T) {
	t.Parallel()
	a := &fakeSpanExporter{}
	m := coreotel.NewMultiSpanExporter(nil, a, nil)
	if err := m.ExportSpans(context.Background(), nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.snapshot()) != 1 {
		t.Errorf("want 1 batch on child a, got %d", len(a.snapshot()))
	}
}

func TestMultiSpanExporter_Shutdown(t *testing.T) {
	t.Parallel()
	a := &fakeSpanExporter{}
	b := &fakeSpanExporter{}
	m := coreotel.NewMultiSpanExporter(a, b)
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	a.mu.Lock()
	bClosed := a.closed
	a.mu.Unlock()
	if !bClosed {
		t.Error("child a should be closed")
	}
}

func TestMultiMetricExporter_TwoChildrenReceiveExport(t *testing.T) {
	t.Parallel()
	a := &fakeMetricExporter{}
	b := &fakeMetricExporter{}
	m := coreotel.NewMultiMetricExporter(a, b)
	if err := m.Export(context.Background(), &metricdata.ResourceMetrics{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.count() != 1 {
		t.Errorf("child a: want 1 export, got %d", a.count())
	}
	if b.count() != 1 {
		t.Errorf("child b: want 1 export, got %d", b.count())
	}
}

func TestMultiLogExporter_TwoChildrenReceiveExport(t *testing.T) {
	t.Parallel()
	a := &fakeLogExporter{}
	b := &fakeLogExporter{}
	m := coreotel.NewMultiLogExporter(a, b)
	if err := m.Export(context.Background(), []sdklog.Record{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.count() != 1 {
		t.Errorf("child a: want 1 export, got %d", a.count())
	}
	if b.count() != 1 {
		t.Errorf("child b: want 1 export, got %d", b.count())
	}
}
