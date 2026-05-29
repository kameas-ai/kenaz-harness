// Package fleet provides fleet-integration components: the FleetExporter
// (OTel → fleet telemetry pipeline), signing helpers, redactor, and consent
// state management.
package fleet

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const (
	// fleetSpanCap is the maximum number of spans held in the ring buffer.
	fleetSpanCap = 10_000
	// fleetByteCap is the maximum total byte budget for serialised span data.
	fleetByteCap = 10 * 1024 * 1024 // 10 MiB
	// fleetFlushInterval is the timer-driven flush period.
	fleetFlushInterval = 30 * time.Second
	// fleetEarlyFlushFraction triggers an early flush at 80% capacity.
	fleetEarlyFlushFraction = 0.8
	// fleetHTTPTimeout is the per-POST deadline.
	fleetHTTPTimeout = 15 * time.Second
)

// spanRecord is the serialisable envelope for a single span in the fleet batch.
type spanRecord struct {
	TraceID    string         `json:"trace_id"`
	SpanID     string         `json:"span_id"`
	ParentID   string         `json:"parent_id,omitempty"`
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	StartNS    int64          `json:"start_ns"`
	EndNS      int64          `json:"end_ns"`
	StatusCode int            `json:"status_code"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

// metricRecord is the serialisable envelope for one metric data point.
type metricRecord struct {
	Name       string         `json:"name"`
	Kind       string         `json:"kind"`
	Unit       string         `json:"unit,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
	// Value is the scalar value. For histograms this is the sum.
	Value float64 `json:"value"`
	// TimeNS is the observation time in nanoseconds since epoch.
	TimeNS int64 `json:"time_ns"`
}

// logRecord is the serialisable envelope for one log entry.
type logRecord struct {
	TimestampNS  int64          `json:"timestamp_ns"`
	SeverityText string         `json:"severity_text,omitempty"`
	Body         string         `json:"body,omitempty"`
	Attributes   map[string]any `json:"attributes,omitempty"`
}

// fleetBatch is the JSON body POSTed to the fleet telemetry endpoint.
type fleetBatch struct {
	BatchID                 string         `json:"batch_id"`
	DevicePubkeyFingerprint string         `json:"device_pubkey_fingerprint"`
	ConsentLevel            string         `json:"consent_level"`
	Signature               string         `json:"signature"`
	Spans                   []spanRecord   `json:"spans"`
	Metrics                 []metricRecord `json:"metrics"`
	Logs                    []logRecord    `json:"logs"`
}

// Signer signs the canonical payload bytes with the device key.
// Implementations are provided by signing.go.
type Signer interface {
	// Sign returns the base64-encoded ed25519 signature over payload.
	Sign(payload []byte) (sig string, pubkeyFP string, err error)
}

// ConsentReader returns the current telemetry consent level.
// "none", "aggregate", or "full".
type ConsentReader interface {
	ConsentLevel() string
}

// ULIDGenerator generates a new ULID string.
type ULIDGenerator func() string

// FleetExporterConfig holds the tunable parameters for FleetExporter.
type FleetExporterConfig struct {
	// Endpoint is the fleet base URL. The exporter POSTs to
	// Endpoint + "/api/v1/telemetry/otel".
	Endpoint string
	// Signer signs each outgoing batch with the device key.
	Signer Signer
	// Consent returns the current consent level. When "none", the
	// exporter silently drops all signals.
	Consent ConsentReader
	// NewULID generates batch IDs.
	NewULID ULIDGenerator
	// HTTPClient is used for POSTing batches. nil → http.DefaultClient.
	HTTPClient *http.Client
	// Logger receives warn-level diagnostics. nil → slog.Default().
	Logger *slog.Logger
	// FlushInterval overrides the 30s default (test seam).
	FlushInterval time.Duration
}

// FleetSpanExporter implements sdktrace.SpanExporter. It buffers spans in a
// bounded ring buffer and flushes them to the fleet telemetry endpoint on a
// 30-second timer or when the buffer reaches 80% of its capacity.
//
// Drop policy: when the ring buffer is full, the oldest span is evicted and
// the dropped counter incremented. The producer goroutine (SDK batcher) never
// blocks (NFR-003).
type FleetSpanExporter struct {
	cfg FleetExporterConfig

	mu      sync.Mutex
	buf     []spanRecord
	bytes   int // approximate byte budget consumed
	dropped uint64

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	flushCh  chan struct{} // unbuffered: trigger early flush
}

// NewFleetSpanExporter constructs the exporter and starts its background flush
// goroutine.
func NewFleetSpanExporter(cfg FleetExporterConfig) *FleetSpanExporter {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: fleetHTTPTimeout}
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = fleetFlushInterval
	}
	e := &FleetSpanExporter{
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		flushCh: make(chan struct{}, 1),
	}
	e.wg.Add(1)
	go e.run()
	return e
}

// ExportSpans implements sdktrace.SpanExporter.
func (e *FleetSpanExporter) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.cfg.Consent != nil && e.cfg.Consent.ConsentLevel() == "none" {
		return nil
	}
	for _, sp := range spans {
		if sp == nil {
			continue
		}
		rec := spanRecordFrom(sp, e.cfg.Consent)
		recBytes := estimateSpanBytes(rec)

		e.mu.Lock()
		// Evict oldest if at capacity (ring buffer drop-oldest policy).
		for len(e.buf) >= fleetSpanCap || e.bytes+recBytes > fleetByteCap {
			if len(e.buf) == 0 {
				break
			}
			oldest := e.buf[0]
			e.buf = e.buf[1:]
			e.bytes -= estimateSpanBytes(oldest)
			atomic.AddUint64(&e.dropped, 1)
		}
		e.buf = append(e.buf, rec)
		e.bytes += recBytes
		needEarlyFlush := float64(len(e.buf)) >= float64(fleetSpanCap)*fleetEarlyFlushFraction ||
			float64(e.bytes) >= float64(fleetByteCap)*fleetEarlyFlushFraction
		e.mu.Unlock()

		if needEarlyFlush {
			select {
			case e.flushCh <- struct{}{}:
			default:
			}
		}
	}
	return nil
}

// Shutdown stops the background goroutine, flushes any remaining spans
// synchronously, and returns.
func (e *FleetSpanExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() { close(e.stopCh) })
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Dropped returns the number of spans evicted from the ring buffer.
func (e *FleetSpanExporter) Dropped() uint64 { return atomic.LoadUint64(&e.dropped) }

func (e *FleetSpanExporter) run() {
	defer e.wg.Done()
	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			e.flush()
			return
		case <-ticker.C:
			e.flush()
		case <-e.flushCh:
			e.flush()
		}
	}
}

func (e *FleetSpanExporter) flush() {
	e.mu.Lock()
	if len(e.buf) == 0 {
		e.mu.Unlock()
		return
	}
	spans := e.buf
	e.buf = nil
	e.bytes = 0
	e.mu.Unlock()

	level := "none"
	if e.cfg.Consent != nil {
		level = e.cfg.Consent.ConsentLevel()
	}
	if level == "none" {
		return
	}

	batchID := ""
	if e.cfg.NewULID != nil {
		batchID = e.cfg.NewULID()
	}

	batch := fleetBatch{
		BatchID:      batchID,
		ConsentLevel: level,
		Spans:        spans,
		Metrics:      []metricRecord{},
		Logs:         []logRecord{},
	}
	e.postBatch(batch)
}

func (e *FleetSpanExporter) postBatch(batch fleetBatch) {
	if e.cfg.Endpoint == "" {
		return
	}

	// Serialise without signature first (signature covers this canonical body).
	payload, err := json.Marshal(batch)
	if err != nil {
		e.cfg.Logger.Warn("fleet.span.marshal_failed", "err", err)
		return
	}

	if e.cfg.Signer != nil {
		sig, fp, sigErr := e.cfg.Signer.Sign(payload)
		if sigErr != nil {
			e.cfg.Logger.Warn("fleet.span.sign_failed", "err", sigErr)
			return
		}
		batch.Signature = sig
		batch.DevicePubkeyFingerprint = fp
		// Re-serialise with signature fields populated.
		payload, err = json.Marshal(batch)
		if err != nil {
			e.cfg.Logger.Warn("fleet.span.marshal_signed_failed", "err", err)
			return
		}
	}

	url := e.cfg.Endpoint + "/api/v1/telemetry/otel"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		e.cfg.Logger.Warn("fleet.span.request_failed", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		e.cfg.Logger.Warn("fleet.span.post_failed", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		e.cfg.Logger.Warn("fleet.span.post_non2xx", "status", resp.StatusCode)
	}
}

// FleetMetricExporter buffers metric snapshots and flushes them alongside the
// span batches. It satisfies sdkmetric.Exporter.
type FleetMetricExporter struct {
	cfg FleetExporterConfig

	mu      sync.Mutex
	buf     []metricRecord
	dropped uint64

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	flushCh  chan struct{}
}

// NewFleetMetricExporter constructs the metric exporter.
func NewFleetMetricExporter(cfg FleetExporterConfig) *FleetMetricExporter {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: fleetHTTPTimeout}
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = fleetFlushInterval
	}
	e := &FleetMetricExporter{
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		flushCh: make(chan struct{}, 1),
	}
	e.wg.Add(1)
	go e.runMetric()
	return e
}

func (e *FleetMetricExporter) Temporality(_ sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (e *FleetMetricExporter) Aggregation(_ sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

// Export implements sdkmetric.Exporter.
func (e *FleetMetricExporter) Export(_ context.Context, rm *metricdata.ResourceMetrics) error {
	if e.cfg.Consent != nil && e.cfg.Consent.ConsentLevel() == "none" {
		return nil
	}
	recs := metricRecordsFrom(rm, e.cfg.Consent)

	e.mu.Lock()
	for len(e.buf)+len(recs) > fleetSpanCap {
		if len(e.buf) == 0 {
			break
		}
		e.buf = e.buf[1:]
		atomic.AddUint64(&e.dropped, 1)
	}
	e.buf = append(e.buf, recs...)
	needEarly := float64(len(e.buf)) >= float64(fleetSpanCap)*fleetEarlyFlushFraction
	e.mu.Unlock()

	if needEarly {
		select {
		case e.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// ForceFlush satisfies the sdkmetric.Exporter interface.
func (e *FleetMetricExporter) ForceFlush(_ context.Context) error { return nil }

// Shutdown stops the metric exporter.
func (e *FleetMetricExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() { close(e.stopCh) })
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *FleetMetricExporter) runMetric() {
	defer e.wg.Done()
	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			e.flushMetrics()
			return
		case <-ticker.C:
			e.flushMetrics()
		case <-e.flushCh:
			e.flushMetrics()
		}
	}
}

func (e *FleetMetricExporter) flushMetrics() {
	e.mu.Lock()
	if len(e.buf) == 0 {
		e.mu.Unlock()
		return
	}
	metrics := e.buf
	e.buf = nil
	e.mu.Unlock()

	level := "none"
	if e.cfg.Consent != nil {
		level = e.cfg.Consent.ConsentLevel()
	}
	if level == "none" {
		return
	}

	batchID := ""
	if e.cfg.NewULID != nil {
		batchID = e.cfg.NewULID()
	}

	batch := fleetBatch{
		BatchID:      batchID,
		ConsentLevel: level,
		Spans:        []spanRecord{},
		Metrics:      metrics,
		Logs:         []logRecord{},
	}
	e.postMetricBatch(batch)
}

func (e *FleetMetricExporter) postMetricBatch(batch fleetBatch) {
	if e.cfg.Endpoint == "" {
		return
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		e.cfg.Logger.Warn("fleet.metric.marshal_failed", "err", err)
		return
	}

	if e.cfg.Signer != nil {
		sig, fp, sigErr := e.cfg.Signer.Sign(payload)
		if sigErr != nil {
			e.cfg.Logger.Warn("fleet.metric.sign_failed", "err", sigErr)
			return
		}
		batch.Signature = sig
		batch.DevicePubkeyFingerprint = fp
		payload, err = json.Marshal(batch)
		if err != nil {
			return
		}
	}

	url := e.cfg.Endpoint + "/api/v1/telemetry/otel"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		e.cfg.Logger.Warn("fleet.metric.post_failed", "err", err)
		return
	}
	defer resp.Body.Close()
}

// FleetLogExporter buffers log records and flushes to fleet. Under
// consent="aggregate" all log records are dropped (per spec: aggregate mode
// drops logs entirely). Only "full" consent ships logs.
type FleetLogExporter struct {
	cfg FleetExporterConfig

	mu      sync.Mutex
	buf     []logRecord
	dropped uint64

	stopCh   chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	flushCh  chan struct{}
}

// NewFleetLogExporter constructs the log exporter.
func NewFleetLogExporter(cfg FleetExporterConfig) *FleetLogExporter {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: fleetHTTPTimeout}
	}
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = fleetFlushInterval
	}
	e := &FleetLogExporter{
		cfg:     cfg,
		stopCh:  make(chan struct{}),
		flushCh: make(chan struct{}, 1),
	}
	e.wg.Add(1)
	go e.runLog()
	return e
}

// Export implements sdklog.Exporter. Logs are dropped when consent != "full".
func (e *FleetLogExporter) Export(_ context.Context, records []sdklog.Record) error {
	level := "none"
	if e.cfg.Consent != nil {
		level = e.cfg.Consent.ConsentLevel()
	}
	// Per spec: aggregate drops logs; full ships redactor-cleaned logs.
	if level != "full" {
		return nil
	}

	recs := logRecordsFrom(records)
	e.mu.Lock()
	for len(e.buf)+len(recs) > fleetSpanCap {
		if len(e.buf) == 0 {
			break
		}
		e.buf = e.buf[1:]
		atomic.AddUint64(&e.dropped, 1)
	}
	e.buf = append(e.buf, recs...)
	needEarly := float64(len(e.buf)) >= float64(fleetSpanCap)*fleetEarlyFlushFraction
	e.mu.Unlock()

	if needEarly {
		select {
		case e.flushCh <- struct{}{}:
		default:
		}
	}
	return nil
}

// ForceFlush satisfies sdklog.Exporter.
func (e *FleetLogExporter) ForceFlush(_ context.Context) error { return nil }

// Shutdown stops the log exporter.
func (e *FleetLogExporter) Shutdown(ctx context.Context) error {
	e.stopOnce.Do(func() { close(e.stopCh) })
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *FleetLogExporter) runLog() {
	defer e.wg.Done()
	ticker := time.NewTicker(e.cfg.FlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stopCh:
			e.flushLogs()
			return
		case <-ticker.C:
			e.flushLogs()
		case <-e.flushCh:
			e.flushLogs()
		}
	}
}

func (e *FleetLogExporter) flushLogs() {
	e.mu.Lock()
	if len(e.buf) == 0 {
		e.mu.Unlock()
		return
	}
	logs := e.buf
	e.buf = nil
	e.mu.Unlock()

	batchID := ""
	if e.cfg.NewULID != nil {
		batchID = e.cfg.NewULID()
	}
	batch := fleetBatch{
		BatchID:      batchID,
		ConsentLevel: "full",
		Spans:        []spanRecord{},
		Metrics:      []metricRecord{},
		Logs:         logs,
	}
	e.postLogBatch(batch)
}

func (e *FleetLogExporter) postLogBatch(batch fleetBatch) {
	if e.cfg.Endpoint == "" {
		return
	}
	payload, err := json.Marshal(batch)
	if err != nil {
		e.cfg.Logger.Warn("fleet.log.marshal_failed", "err", err)
		return
	}
	if e.cfg.Signer != nil {
		sig, fp, sigErr := e.cfg.Signer.Sign(payload)
		if sigErr != nil {
			e.cfg.Logger.Warn("fleet.log.sign_failed", "err", sigErr)
			return
		}
		batch.Signature = sig
		batch.DevicePubkeyFingerprint = fp
		payload, err = json.Marshal(batch)
		if err != nil {
			return
		}
	}
	url := e.cfg.Endpoint + "/api/v1/telemetry/otel"
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.cfg.HTTPClient.Do(req)
	if err != nil {
		e.cfg.Logger.Warn("fleet.log.post_failed", "err", err)
		return
	}
	defer resp.Body.Close()
}

// ── conversion helpers ─────────────────────────────────────────────────────

func spanRecordFrom(sp sdktrace.ReadOnlySpan, consent ConsentReader) spanRecord {
	sc := sp.SpanContext()
	parent := sp.Parent()

	rec := spanRecord{
		TraceID:    sc.TraceID().String(),
		SpanID:     sc.SpanID().String(),
		Name:       sp.Name(),
		StartNS:    sp.StartTime().UnixNano(),
		EndNS:      sp.EndTime().UnixNano(),
		StatusCode: int(sp.Status().Code),
	}
	if parent.IsValid() && parent.HasSpanID() {
		rec.ParentID = parent.SpanID().String()
	}
	// Under aggregate consent, drop string attribute values — keep name +
	// status only.
	if consent == nil || consent.ConsentLevel() == "full" {
		rawAttrs := sp.Attributes()
		if len(rawAttrs) > 0 {
			attrs := make(map[string]any, len(rawAttrs))
			for _, a := range rawAttrs {
				attrs[string(a.Key)] = a.Value.Emit()
			}
			rec.Attributes = attrs
		}
	}
	return rec
}

func metricRecordsFrom(rm *metricdata.ResourceMetrics, consent ConsentReader) []metricRecord {
	if rm == nil {
		return nil
	}
	var recs []metricRecord
	for _, scope := range rm.ScopeMetrics {
		for _, m := range scope.Metrics {
			switch d := m.Data.(type) {
			case metricdata.Gauge[int64]:
				for _, dp := range d.DataPoints {
					recs = append(recs, metricRecord{
						Name:   m.Name,
						Kind:   "gauge_int64",
						Unit:   m.Unit,
						Value:  float64(dp.Value),
						TimeNS: dp.Time.UnixNano(),
					})
				}
			case metricdata.Gauge[float64]:
				for _, dp := range d.DataPoints {
					recs = append(recs, metricRecord{
						Name:   m.Name,
						Kind:   "gauge_float64",
						Unit:   m.Unit,
						Value:  dp.Value,
						TimeNS: dp.Time.UnixNano(),
					})
				}
			case metricdata.Sum[int64]:
				for _, dp := range d.DataPoints {
					recs = append(recs, metricRecord{
						Name:   m.Name,
						Kind:   "sum_int64",
						Unit:   m.Unit,
						Value:  float64(dp.Value),
						TimeNS: dp.Time.UnixNano(),
					})
				}
			case metricdata.Sum[float64]:
				for _, dp := range d.DataPoints {
					recs = append(recs, metricRecord{
						Name:   m.Name,
						Kind:   "sum_float64",
						Unit:   m.Unit,
						Value:  dp.Value,
						TimeNS: dp.Time.UnixNano(),
					})
				}
			case metricdata.Histogram[float64]:
				for _, dp := range d.DataPoints {
					recs = append(recs, metricRecord{
						Name:   m.Name,
						Kind:   "histogram",
						Unit:   m.Unit,
						Value:  dp.Sum,
						TimeNS: dp.Time.UnixNano(),
					})
				}
			}
		}
	}
	_ = consent // future: filter attributes by consent level
	return recs
}

func logRecordsFrom(records []sdklog.Record) []logRecord {
	recs := make([]logRecord, 0, len(records))
	for _, r := range records {
		var body string
		if v := r.Body(); v.Kind().String() != "" {
			body = v.AsString()
		}
		rec := logRecord{
			TimestampNS:  r.Timestamp().UnixNano(),
			SeverityText: r.SeverityText(),
			Body:         body,
		}
		recs = append(recs, rec)
	}
	return recs
}

// estimateSpanBytes returns a rough byte estimate for a span record.
func estimateSpanBytes(rec spanRecord) int {
	b, _ := json.Marshal(rec)
	return len(b)
}
