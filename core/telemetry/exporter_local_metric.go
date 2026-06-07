package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// LocalMetricExporter implements sdkmetric.Exporter on top of the
// unified SQLite store. The metric SDK pushes ResourceMetrics
// snapshots through Export on the periodic reader's tick; we flatten
// each snapshot into telemetry_metrics rows (one per data point) so
// the inspector view (WP06) can render time series without
// understanding the SDK's nested aggregation shape.
//
// Like the span exporter, writes happen inside a single WriteTx so
// the periodic reader is never starved by lock contention.
type LocalMetricExporter struct {
	db     storage.DB
	logger *slog.Logger

	mu       sync.Mutex
	stopped  bool
	stoppedC chan struct{}
}

// NewLocalMetricExporter constructs the exporter. db must be non-nil.
func NewLocalMetricExporter(db storage.DB, logger *slog.Logger) (*LocalMetricExporter, error) {
	if db == nil {
		return nil, errors.New("telemetry: NewLocalMetricExporter: nil db")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &LocalMetricExporter{
		db:       db,
		logger:   logger,
		stoppedC: make(chan struct{}),
	}, nil
}

// Temporality picks Cumulative for every instrument kind. Cumulative
// is the OTLP default and matches what most observability backends
// expect; Delta is opt-in via WP02 if a future backend needs it.
func (e *LocalMetricExporter) Temporality(_ sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

// Aggregation defers to the SDK's defaults. The local table holds
// raw data points so the choice doesn't influence on-disk shape.
func (e *LocalMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

// Export flattens the snapshot into rows and writes them in a single
// WriteTx. The SDK's periodic reader calls this synchronously on its
// own goroutine — we don't need a producer queue here, but we still
// guard the DB write with a context timeout so a stuck transaction
// can't wedge the reader.
func (e *LocalMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return sdkmetric.ErrExporterShutdown
	}
	e.mu.Unlock()

	if rm == nil {
		return nil
	}
	rows := flattenResourceMetrics(rm)
	if len(rows) == 0 {
		return nil
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := e.db.WriteTx(wctx, func(tx storage.WriteTx) error {
		for _, r := range rows {
			if _, err := tx.Exec(wctx, `INSERT INTO telemetry_metrics
                (name, kind, attributes_json, value, ts_ns)
                VALUES (?, ?, ?, ?, ?)`,
				r.Name, r.Kind, r.AttributesJSON, r.Value, r.TSNS); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		e.logger.Warn("telemetry.local_metric.write_failed",
			"err", err.Error(), "row_count", len(rows))
		// Returning the error would propagate to the SDK error
		// handler which logs it again; we already logged here.
		return nil
	}
	return nil
}

// ForceFlush is a no-op: writes are synchronous in Export.
func (e *LocalMetricExporter) ForceFlush(_ context.Context) error { return nil }

// Shutdown marks the exporter as stopped so any further Export calls
// return ErrExporterShutdown. Idempotent.
func (e *LocalMetricExporter) Shutdown(_ context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return nil
	}
	e.stopped = true
	close(e.stoppedC)
	return nil
}

// metricRow is the SQL-row shape for a single data point.
type metricRow struct {
	Name           string
	Kind           string
	AttributesJSON string
	Value          float64
	TSNS           int64
}

func flattenResourceMetrics(rm *metricdata.ResourceMetrics) []metricRow {
	var out []metricRow
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			out = append(out, flattenMetric(m)...)
		}
	}
	return out
}

func flattenMetric(m metricdata.Metrics) []metricRow {
	switch agg := m.Data.(type) {
	case metricdata.Sum[int64]:
		return flattenSumInt64(m.Name, agg)
	case metricdata.Sum[float64]:
		return flattenSumFloat64(m.Name, agg)
	case metricdata.Gauge[int64]:
		return flattenGaugeInt64(m.Name, agg)
	case metricdata.Gauge[float64]:
		return flattenGaugeFloat64(m.Name, agg)
	case metricdata.Histogram[int64]:
		return flattenHistogramInt64(m.Name, agg)
	case metricdata.Histogram[float64]:
		return flattenHistogramFloat64(m.Name, agg)
	default:
		return nil
	}
}

// kindForSum maps a Sum's monotonicity flag to the schema enum used
// by the inspector. UpDownCounter aggregates as a non-monotonic Sum.
func kindForSum(monotonic bool) string {
	if monotonic {
		return "counter"
	}
	return "updown"
}

func flattenSumInt64(name string, sum metricdata.Sum[int64]) []metricRow {
	out := make([]metricRow, 0, len(sum.DataPoints))
	for _, dp := range sum.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           kindForSum(sum.IsMonotonic),
			AttributesJSON: encodeAttrSet(dp.Attributes),
			Value:          float64(dp.Value),
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

func flattenSumFloat64(name string, sum metricdata.Sum[float64]) []metricRow {
	out := make([]metricRow, 0, len(sum.DataPoints))
	for _, dp := range sum.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           kindForSum(sum.IsMonotonic),
			AttributesJSON: encodeAttrSet(dp.Attributes),
			Value:          dp.Value,
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

func flattenGaugeInt64(name string, g metricdata.Gauge[int64]) []metricRow {
	out := make([]metricRow, 0, len(g.DataPoints))
	for _, dp := range g.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           "gauge",
			AttributesJSON: encodeAttrSet(dp.Attributes),
			Value:          float64(dp.Value),
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

func flattenGaugeFloat64(name string, g metricdata.Gauge[float64]) []metricRow {
	out := make([]metricRow, 0, len(g.DataPoints))
	for _, dp := range g.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           "gauge",
			AttributesJSON: encodeAttrSet(dp.Attributes),
			Value:          dp.Value,
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

// flattenHistogram* records the histogram's running sum as the row
// value. The inspector renders the count + sum as p50-ish summary;
// per-bucket fan-out lives in WP02 if a backend wants the full curve.
// Sum is the most useful single number for a quick scan.
func flattenHistogramInt64(name string, h metricdata.Histogram[int64]) []metricRow {
	out := make([]metricRow, 0, len(h.DataPoints))
	for _, dp := range h.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           "histogram",
			AttributesJSON: encodeHistogramAttrs(dp.Attributes, dp.Count, dp.Sum),
			Value:          float64(dp.Sum),
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

func flattenHistogramFloat64(name string, h metricdata.Histogram[float64]) []metricRow {
	out := make([]metricRow, 0, len(h.DataPoints))
	for _, dp := range h.DataPoints {
		out = append(out, metricRow{
			Name:           name,
			Kind:           "histogram",
			AttributesJSON: encodeHistogramAttrs(dp.Attributes, dp.Count, dp.Sum),
			Value:          dp.Sum,
			TSNS:           dp.Time.UnixNano(),
		})
	}
	return out
}

func encodeAttrSet(set attribute.Set) string {
	if set.Len() == 0 {
		return "{}"
	}
	m := make(map[string]any, set.Len())
	for iter := set.Iter(); iter.Next(); {
		a := iter.Attribute()
		m[string(a.Key)] = attrValue(a.Value)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func encodeHistogramAttrs[N int64 | float64](set attribute.Set, count uint64, sum N) string {
	m := make(map[string]any, set.Len()+2)
	for iter := set.Iter(); iter.Next(); {
		a := iter.Attribute()
		m[string(a.Key)] = attrValue(a.Value)
	}
	m["__count"] = count
	m["__sum"] = sum
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
