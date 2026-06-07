package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// localSpanQueueSize bounds the in-memory queue for spans waiting to
// be flushed to SQLite. NFR-003: telemetry must never block the
// producer; once the queue is full the oldest pending span is
// dropped (and the dropped counter incremented) so the producer's
// channel send keeps progressing.
const localSpanQueueSize = 1024

// localSpanBatchSize is the soft cap on per-tx span inserts. Larger
// batches amortise BeginTx; we trade a bit of latency for fewer
// transactions on busy harnesses.
const localSpanBatchSize = 100

// localSpanFlushInterval triggers a flush even when the batch is
// short; keeps the inspector view fresh under low chat load.
const localSpanFlushInterval = 1 * time.Second

// LocalSpanExporter is the SQLite-backed sdktrace.SpanExporter the
// TracerProvider plugs into via WithBatcher. WP02 wraps this exporter
// in a composite that fan-outs to the OTLP HTTP exporter when the
// user has configured a fleet endpoint.
//
// Lifecycle:
//   - NewLocalSpanExporter starts the background flusher goroutine.
//   - ExportSpans pushes ReadOnlySpans onto the bounded queue and
//     returns immediately; the flusher writes them in batches.
//   - Shutdown closes the producer side, drains the queue, and
//     flushes the final batch synchronously.
type LocalSpanExporter struct {
	db     storage.DB
	logger *slog.Logger

	queue   chan sdktrace.ReadOnlySpan
	dropped uint64 // atomic — read by Dropped() for the WP04 metric

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewLocalSpanExporter constructs the exporter and starts its
// flusher goroutine. db must be non-nil; nil logger falls back to
// slog.Default. The exporter holds db but never owns its lifetime —
// closing it is the caller's job (Core.Shutdown).
func NewLocalSpanExporter(db storage.DB, logger *slog.Logger) (*LocalSpanExporter, error) {
	if db == nil {
		return nil, errors.New("telemetry: NewLocalSpanExporter: nil db")
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &LocalSpanExporter{
		db:     db,
		logger: logger,
		queue:  make(chan sdktrace.ReadOnlySpan, localSpanQueueSize),
		stopCh: make(chan struct{}),
	}
	e.wg.Add(1)
	go e.run()
	return e, nil
}

// ExportSpans is the SDK's entry point. It is called synchronously
// from a span processor; we copy the slice header and push each span
// onto the queue without allocating a goroutine.
//
// Drop policy: when the queue is full we discard the span and bump
// the dropped counter. We deliberately do NOT block on the producer —
// blocking the SDK would block the chat surface (NFR-003).
func (e *LocalSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, sp := range spans {
		if sp == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopCh:
			return nil
		case e.queue <- sp:
			// enqueued
		default:
			atomic.AddUint64(&e.dropped, 1)
		}
	}
	return nil
}

// Shutdown closes the producer side, waits for the flusher to drain,
// and returns. Idempotent: subsequent calls are no-ops.
func (e *LocalSpanExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() {
		close(e.stopCh)
	})
	done := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dropped returns the count of spans dropped because the queue was
// full. The WP04 metric `telemetry.dropped` reads this.
func (e *LocalSpanExporter) Dropped() uint64 {
	return atomic.LoadUint64(&e.dropped)
}

func (e *LocalSpanExporter) run() {
	defer e.wg.Done()
	batch := make([]sdktrace.ReadOnlySpan, 0, localSpanBatchSize)
	ticker := time.NewTicker(localSpanFlushInterval)
	defer ticker.Stop()
	flush := func() {
		if len(batch) == 0 {
			return
		}
		e.write(batch)
		batch = batch[:0]
	}
	for {
		select {
		case <-e.stopCh:
			// Drain remaining queued spans before exiting so Shutdown
			// is observably synchronous.
			for {
				select {
				case sp := <-e.queue:
					if sp != nil {
						batch = append(batch, sp)
						if len(batch) >= localSpanBatchSize {
							flush()
						}
					}
				default:
					flush()
					return
				}
			}
		case sp := <-e.queue:
			if sp != nil {
				batch = append(batch, sp)
				if len(batch) >= localSpanBatchSize {
					flush()
				}
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (e *LocalSpanExporter) write(spans []sdktrace.ReadOnlySpan) {
	rows := make([]spanRow, 0, len(spans))
	for _, sp := range spans {
		rows = append(rows, spanRowFrom(sp))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		for _, r := range rows {
			if _, err := tx.Exec(ctx, `INSERT INTO telemetry_spans
                (trace_id, span_id, parent_span_id, name, kind, start_ns, end_ns,
                 status_code, status_message, attributes_json, events_json)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                ON CONFLICT(span_id) DO NOTHING`,
				r.TraceID, r.SpanID, r.ParentSpanID, r.Name, r.Kind,
				r.StartNS, r.EndNS, r.StatusCode, r.StatusMessage,
				r.AttributesJSON, r.EventsJSON); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		// Telemetry must degrade silently — log and move on so the
		// SDK doesn't propagate the error to instrumentation sites.
		e.logger.Warn("telemetry.local_span.write_failed",
			"err", err.Error(), "batch_size", len(rows))
	}
}

// spanRow holds the SQL-row shape for a single span. Pulling the
// projection into a struct keeps the INSERT statement readable and
// the test assertions concise.
type spanRow struct {
	TraceID        string
	SpanID         string
	ParentSpanID   any
	Name           string
	Kind           string
	StartNS        int64
	EndNS          int64
	StatusCode     int
	StatusMessage  string
	AttributesJSON string
	EventsJSON     string
}

func spanRowFrom(sp sdktrace.ReadOnlySpan) spanRow {
	sc := sp.SpanContext()
	parent := sp.Parent()
	row := spanRow{
		TraceID:       sc.TraceID().String(),
		SpanID:        sc.SpanID().String(),
		Name:          sp.Name(),
		Kind:          spanKindString(sp.SpanKind()),
		StartNS:       sp.StartTime().UnixNano(),
		EndNS:         sp.EndTime().UnixNano(),
		StatusCode:    statusCodeInt(sp.Status()),
		StatusMessage: sp.Status().Description,
	}
	if parent.HasSpanID() {
		row.ParentSpanID = parent.SpanID().String()
	}
	row.AttributesJSON = encodeAttributes(sp.Attributes())
	row.EventsJSON = encodeEvents(sp.Events())
	return row
}

func spanKindString(k trace.SpanKind) string {
	switch k {
	case trace.SpanKindServer:
		return "server"
	case trace.SpanKindClient:
		return "client"
	case trace.SpanKindProducer:
		return "producer"
	case trace.SpanKindConsumer:
		return "consumer"
	default:
		return "internal"
	}
}

// statusCodeInt maps the OTel status code to the integer the schema
// pins (0=unset, 1=ok, 2=error).
func statusCodeInt(s sdktrace.Status) int {
	// codes.Unset = 0, codes.Ok = 1, codes.Error = 2 — matches our
	// schema verbatim. Cast through int to avoid leaking the codes
	// package into the schema column type.
	return int(s.Code)
}

func encodeAttributes(attrs []attribute.KeyValue) string {
	if len(attrs) == 0 {
		return "{}"
	}
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[string(a.Key)] = attrValue(a.Value)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func encodeEvents(events []sdktrace.Event) string {
	if len(events) == 0 {
		return "[]"
	}
	out := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		row := map[string]any{
			"name":  ev.Name,
			"ts_ns": ev.Time.UnixNano(),
		}
		if len(ev.Attributes) > 0 {
			row["attributes"] = decodeAttrSlice(ev.Attributes)
		}
		out = append(out, row)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeAttrSlice(attrs []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(attrs))
	for _, a := range attrs {
		m[string(a.Key)] = attrValue(a.Value)
	}
	return m
}

// attrValue projects an attribute.Value into a JSON-serialisable Go
// type. The OTel attribute system has a closed set of value kinds;
// anything unknown falls back to its String() form.
func attrValue(v attribute.Value) any {
	switch v.Type() {
	case attribute.BOOL:
		return v.AsBool()
	case attribute.INT64:
		return v.AsInt64()
	case attribute.FLOAT64:
		return v.AsFloat64()
	case attribute.STRING:
		return v.AsString()
	case attribute.BOOLSLICE:
		return v.AsBoolSlice()
	case attribute.INT64SLICE:
		return v.AsInt64Slice()
	case attribute.FLOAT64SLICE:
		return v.AsFloat64Slice()
	case attribute.STRINGSLICE:
		return v.AsStringSlice()
	default:
		return v.Emit()
	}
}
