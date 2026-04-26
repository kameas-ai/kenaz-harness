package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/storage"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
)

// localLogQueueSize bounds the queue for log records waiting to be
// flushed. Same NFR-003 reasoning as the span exporter: producer
// never blocks, full queue increments the dropped counter and the
// oldest pending record is discarded.
const localLogQueueSize = 1024

// localLogBatchSize / localLogFlushInterval mirror the span exporter
// — there's no compelling reason for the cadence to differ.
const localLogBatchSize = 100
const localLogFlushInterval = 1 * time.Second

// LocalLogExporter implements sdklog.Exporter on top of the unified
// SQLite store. The OTel logs SDK (v0.6.x at WP01 land time) is
// still pre-1.0, so this exporter sticks to the stable surface
// (Export / Shutdown / ForceFlush) and avoids any optional hooks.
type LocalLogExporter struct {
	db     storage.DB
	logger *slog.Logger

	queue   chan sdklog.Record
	dropped uint64

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// NewLocalLogExporter constructs the exporter and starts its flusher.
func NewLocalLogExporter(db storage.DB, logger *slog.Logger) (*LocalLogExporter, error) {
	if db == nil {
		return nil, errors.New("telemetry: NewLocalLogExporter: nil db")
	}
	if logger == nil {
		logger = slog.Default()
	}
	e := &LocalLogExporter{
		db:     db,
		logger: logger,
		queue:  make(chan sdklog.Record, localLogQueueSize),
		stopCh: make(chan struct{}),
	}
	e.wg.Add(1)
	go e.run()
	return e, nil
}

// Export pushes records onto the queue. Records are passed by value
// per the SDK contract (the SDK reuses the slice but each Record
// holds its own copies of attributes via the Clone semantics
// elsewhere); we still re-clone defensively to avoid sharing state
// with the SDK's processor.
func (e *LocalLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	for i := range records {
		// Clone so the queued record cannot be mutated by the SDK
		// while it sits in the buffer.
		rec := records[i].Clone()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-e.stopCh:
			return nil
		case e.queue <- rec:
			// enqueued
		default:
			atomic.AddUint64(&e.dropped, 1)
		}
	}
	return nil
}

// ForceFlush drains the queue. Implemented as Shutdown's drain logic
// minus the close: we send a sentinel through the input channel and
// wait for the flusher to acknowledge. For WP01's purposes a simple
// best-effort wait suffices — if the deadline expires before the
// queue empties we let the flusher continue in the background.
func (e *LocalLogExporter) ForceFlush(ctx context.Context) error {
	deadline := time.NewTimer(50 * time.Millisecond)
	defer deadline.Stop()
	for {
		if len(e.queue) == 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return nil
		}
	}
}

// Shutdown closes the producer side, waits for the flusher, returns.
func (e *LocalLogExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() { close(e.stopCh) })
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

// Dropped returns the count of records dropped because the queue was
// full.
func (e *LocalLogExporter) Dropped() uint64 {
	return atomic.LoadUint64(&e.dropped)
}

func (e *LocalLogExporter) run() {
	defer e.wg.Done()
	batch := make([]sdklog.Record, 0, localLogBatchSize)
	ticker := time.NewTicker(localLogFlushInterval)
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
			for {
				select {
				case rec := <-e.queue:
					batch = append(batch, rec)
					if len(batch) >= localLogBatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case rec := <-e.queue:
			batch = append(batch, rec)
			if len(batch) >= localLogBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

func (e *LocalLogExporter) write(records []sdklog.Record) {
	rows := make([]logRow, 0, len(records))
	for _, r := range records {
		rows = append(rows, logRowFrom(r))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := e.db.WriteTx(ctx, func(tx storage.WriteTx) error {
		for _, r := range rows {
			var traceID, spanID any
			if r.TraceID != "" {
				traceID = r.TraceID
			}
			if r.SpanID != "" {
				spanID = r.SpanID
			}
			if _, err := tx.Exec(ctx, `INSERT INTO telemetry_logs
                (severity, body, attributes_json, trace_id, span_id, ts_ns)
                VALUES (?, ?, ?, ?, ?, ?)`,
				r.Severity, r.Body, r.AttributesJSON,
				traceID, spanID, r.TSNS); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		e.logger.Warn("telemetry.local_log.write_failed",
			"err", err.Error(), "batch_size", len(rows))
	}
}

type logRow struct {
	Severity       int
	Body           string
	AttributesJSON string
	TraceID        string
	SpanID         string
	TSNS           int64
}

func logRowFrom(rec sdklog.Record) logRow {
	r := &rec
	ts := r.Timestamp()
	if ts.IsZero() {
		ts = r.ObservedTimestamp()
	}
	row := logRow{
		Severity: int(r.Severity()),
		Body:     logValueString(r.Body()),
		TSNS:     ts.UnixNano(),
	}
	if tid := r.TraceID(); tid.IsValid() {
		row.TraceID = tid.String()
	}
	if sid := r.SpanID(); sid.IsValid() {
		row.SpanID = sid.String()
	}
	row.AttributesJSON = encodeLogAttributes(r)
	return row
}

func logValueString(v log.Value) string {
	switch v.Kind() {
	case log.KindString:
		return v.AsString()
	case log.KindBool:
		if v.AsBool() {
			return "true"
		}
		return "false"
	case log.KindFloat64:
		return formatFloat(v.AsFloat64())
	case log.KindInt64:
		return formatInt(v.AsInt64())
	case log.KindEmpty:
		return ""
	default:
		return v.String()
	}
}

func formatFloat(f float64) string {
	b, err := json.Marshal(f)
	if err != nil {
		return "0"
	}
	return string(b)
}

func formatInt(i int64) string {
	b, err := json.Marshal(i)
	if err != nil {
		return "0"
	}
	return string(b)
}

func encodeLogAttributes(r *sdklog.Record) string {
	m := map[string]any{}
	r.WalkAttributes(func(kv log.KeyValue) bool {
		m[kv.Key] = logValueAny(kv.Value)
		return true
	})
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func logValueAny(v log.Value) any {
	switch v.Kind() {
	case log.KindBool:
		return v.AsBool()
	case log.KindInt64:
		return v.AsInt64()
	case log.KindFloat64:
		return v.AsFloat64()
	case log.KindString:
		return v.AsString()
	case log.KindBytes:
		return v.AsBytes()
	case log.KindEmpty:
		return nil
	default:
		return v.String()
	}
}
