package telemetry

import (
	"context"
	"log/slog"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// CompositeSpanExporter fans Export calls into the always-on local
// exporter and, when configured, the OTLP exporter. Local errors
// surface to the SDK (real bug — local writes are blocking-on-disk
// only); OTLP errors are swallowed and logged at warn so a network
// blip never poisons the local-first invariant (NFR-005). The SDK's
// own retry/backoff inside the OTLP client handles transient errors;
// the warning log surfaces a persistent fault for the operator.
//
// OTLP is nil when the user has not configured an endpoint — the
// composite then short-circuits without ever touching the network.
type CompositeSpanExporter struct {
	Local  sdktrace.SpanExporter
	OTLP   sdktrace.SpanExporter
	Logger *slog.Logger
}

// ExportSpans forwards the batch to both sides. Local always runs;
// OTLP runs second and never blocks the local path's success.
func (c *CompositeSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if c == nil {
		return nil
	}
	var localErr error
	if c.Local != nil {
		localErr = c.Local.ExportSpans(ctx, spans)
	}
	if c.OTLP != nil {
		if err := c.OTLP.ExportSpans(ctx, spans); err != nil {
			c.warn("telemetry.otlp_span.export_failed", err)
		}
	}
	return localErr
}

// Shutdown flushes both sides. The first non-nil error is returned;
// every step still runs so a failing OTLP shutdown cannot prevent the
// local exporter from draining.
func (c *CompositeSpanExporter) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.Local != nil {
		if err := c.Local.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.OTLP != nil {
		if err := c.OTLP.Shutdown(ctx); err != nil {
			c.warn("telemetry.otlp_span.shutdown_failed", err)
		}
	}
	return firstErr
}

func (c *CompositeSpanExporter) warn(msg string, err error) {
	if c.Logger == nil {
		return
	}
	c.Logger.Warn(msg, "err", err.Error())
}

// CompositeMetricExporter mirrors CompositeSpanExporter for the metric
// pipeline. Both sides see the same ResourceMetrics snapshot; OTLP
// errors are logged at warn and swallowed.
type CompositeMetricExporter struct {
	Local  sdkmetric.Exporter
	OTLP   sdkmetric.Exporter
	Logger *slog.Logger
}

func (c *CompositeMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	if c == nil || c.Local == nil {
		return metricdata.CumulativeTemporality
	}
	return c.Local.Temporality(k)
}

func (c *CompositeMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	if c == nil || c.Local == nil {
		return sdkmetric.AggregationDefault{}
	}
	return c.Local.Aggregation(k)
}

func (c *CompositeMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if c == nil {
		return nil
	}
	var localErr error
	if c.Local != nil {
		localErr = c.Local.Export(ctx, rm)
	}
	if c.OTLP != nil {
		if err := c.OTLP.Export(ctx, rm); err != nil {
			c.warn("telemetry.otlp_metric.export_failed", err)
		}
	}
	return localErr
}

func (c *CompositeMetricExporter) ForceFlush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.Local != nil {
		if err := c.Local.ForceFlush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.OTLP != nil {
		if err := c.OTLP.ForceFlush(ctx); err != nil {
			c.warn("telemetry.otlp_metric.flush_failed", err)
		}
	}
	return firstErr
}

func (c *CompositeMetricExporter) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.Local != nil {
		if err := c.Local.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.OTLP != nil {
		if err := c.OTLP.Shutdown(ctx); err != nil {
			c.warn("telemetry.otlp_metric.shutdown_failed", err)
		}
	}
	return firstErr
}

func (c *CompositeMetricExporter) warn(msg string, err error) {
	if c.Logger == nil {
		return
	}
	c.Logger.Warn(msg, "err", err.Error())
}

// CompositeLogExporter mirrors CompositeSpanExporter for the log
// pipeline.
type CompositeLogExporter struct {
	Local  sdklog.Exporter
	OTLP   sdklog.Exporter
	Logger *slog.Logger
}

func (c *CompositeLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	if c == nil {
		return nil
	}
	var localErr error
	if c.Local != nil {
		localErr = c.Local.Export(ctx, records)
	}
	if c.OTLP != nil {
		if err := c.OTLP.Export(ctx, records); err != nil {
			c.warn("telemetry.otlp_log.export_failed", err)
		}
	}
	return localErr
}

func (c *CompositeLogExporter) ForceFlush(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.Local != nil {
		if err := c.Local.ForceFlush(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.OTLP != nil {
		if err := c.OTLP.ForceFlush(ctx); err != nil {
			c.warn("telemetry.otlp_log.flush_failed", err)
		}
	}
	return firstErr
}

func (c *CompositeLogExporter) Shutdown(ctx context.Context) error {
	if c == nil {
		return nil
	}
	var firstErr error
	if c.Local != nil {
		if err := c.Local.Shutdown(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if c.OTLP != nil {
		if err := c.OTLP.Shutdown(ctx); err != nil {
			c.warn("telemetry.otlp_log.shutdown_failed", err)
		}
	}
	return firstErr
}

func (c *CompositeLogExporter) warn(msg string, err error) {
	if c.Logger == nil {
		return
	}
	c.Logger.Warn(msg, "err", err.Error())
}
