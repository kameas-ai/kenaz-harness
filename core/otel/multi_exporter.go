// Package otel provides OTel exporter utilities beyond the local telemetry
// package. The primary export is MultiExporter, a fan-out wrapper that
// propagates each signal to N child exporters.
//
// Design note: the telemetry package (core/telemetry) already ships
// CompositeSpanExporter / CompositeMetricExporter / CompositeLogExporter for
// the specific local+OTLP fan-out pattern. MultiExporter is a generalised
// N-child wrapper used when a third exporter (e.g. FleetExporter) needs to be
// attached without modifying the existing composite.
package otel

import (
	"context"
	"errors"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// MultiSpanExporter fans ExportSpans calls to every child. Each child is
// called in order; the first non-nil error is returned but every child is
// always called (fail-through). Nil children are silently skipped.
type MultiSpanExporter struct {
	children []sdktrace.SpanExporter
}

// NewMultiSpanExporter constructs a MultiSpanExporter from the supplied
// children. nil entries are dropped at construction time.
func NewMultiSpanExporter(children ...sdktrace.SpanExporter) *MultiSpanExporter {
	out := make([]sdktrace.SpanExporter, 0, len(children))
	for _, c := range children {
		if c != nil {
			out = append(out, c)
		}
	}
	return &MultiSpanExporter{children: out}
}

// ExportSpans fans the batch to every child. Returns the first non-nil error;
// all children are still called even after a failure.
func (m *MultiSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	var first error
	for _, c := range m.children {
		if err := c.ExportSpans(ctx, spans); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Shutdown calls Shutdown on every child. Returns the joined errors.
func (m *MultiSpanExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, c := range m.children {
		if err := c.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// MultiMetricExporter fans Export calls to every child metric exporter.
// Temporality and Aggregation are delegated to the first child; if there
// are no children the cumulative/default are returned.
type MultiMetricExporter struct {
	children []sdkmetric.Exporter
}

// NewMultiMetricExporter constructs a MultiMetricExporter. nil children are
// dropped.
func NewMultiMetricExporter(children ...sdkmetric.Exporter) *MultiMetricExporter {
	out := make([]sdkmetric.Exporter, 0, len(children))
	for _, c := range children {
		if c != nil {
			out = append(out, c)
		}
	}
	return &MultiMetricExporter{children: out}
}

// Temporality delegates to the first child; falls back to cumulative.
func (m *MultiMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	if len(m.children) > 0 {
		return m.children[0].Temporality(k)
	}
	return metricdata.CumulativeTemporality
}

// Aggregation delegates to the first child; falls back to default.
func (m *MultiMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	if len(m.children) > 0 {
		return m.children[0].Aggregation(k)
	}
	return sdkmetric.AggregationDefault{}
}

// Export fans the call to every child. Returns the first non-nil error.
func (m *MultiMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	var first error
	for _, c := range m.children {
		if err := c.Export(ctx, rm); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ForceFlush flushes every child; returns the first error.
func (m *MultiMetricExporter) ForceFlush(ctx context.Context) error {
	var first error
	for _, c := range m.children {
		if err := c.ForceFlush(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Shutdown shuts down every child; returns joined errors.
func (m *MultiMetricExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, c := range m.children {
		if err := c.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// MultiLogExporter fans Export calls to every child log exporter.
type MultiLogExporter struct {
	children []sdklog.Exporter
}

// NewMultiLogExporter constructs a MultiLogExporter. nil children are
// dropped.
func NewMultiLogExporter(children ...sdklog.Exporter) *MultiLogExporter {
	out := make([]sdklog.Exporter, 0, len(children))
	for _, c := range children {
		if c != nil {
			out = append(out, c)
		}
	}
	return &MultiLogExporter{children: out}
}

// Export fans the batch to every child; returns the first error.
func (m *MultiLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	var first error
	for _, c := range m.children {
		if err := c.Export(ctx, records); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ForceFlush flushes every child; returns the first error.
func (m *MultiLogExporter) ForceFlush(ctx context.Context) error {
	var first error
	for _, c := range m.children {
		if err := c.ForceFlush(ctx); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Shutdown shuts down every child; returns joined errors.
func (m *MultiLogExporter) Shutdown(ctx context.Context) error {
	var errs []error
	for _, c := range m.children {
		if err := c.Shutdown(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
