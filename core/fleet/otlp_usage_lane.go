package fleet

// otlp_usage_lane.go — the account-attributed usage lanes of the fleet OTLP
// pipeline: kind-tagged event records (logs) and label-less counters
// (metrics).
//
// # Why these lanes own their providers
//
// The boot-time LoggerProvider / MeterProvider built by telemetry.Init are
// frozen before login, so their resource has no kameas.user.id and Fleet
// rejects every batch (see "Constraint 1" on FleetOTLPPipeline). Spans solve
// that with a per-span Resource() override; log records and metric batches
// have no such hook.
//
// The fix is not an override, it is ordering: these lanes construct their OWN
// providers inside Activate, after enroll, when the identity is known. A
// provider built post-login is frozen with the right resource. Deactivate
// tears them down, so a sign-out or account change cannot leave a provider
// stamped with the previous account.
//
// Owning the providers has a second, deliberate effect: nothing else in the
// process can write to them. The slog→OTel bridge feeds the boot-time
// LoggerProvider, never this one, so a plain application log line cannot
// reach the event lane even in principle. The kind gate and body redactor
// still sit in front of the exporter — belt and braces, and what the fence
// tests pin.
//
// # What may be written
//
// Only the closed vocabulary in usage_emitter.go. EmitEvent takes a compiled
// LogEventKind and a closed body; AddCount takes a compiled UsageCounter.
// There is no free-form entry point.

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/metric"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// usageScopeName is the instrumentation scope for both usage lanes.
const usageScopeName = "github.com/kameas-ai/kenaz-harness/core/fleet/usage"

// UsageCounter names a label-less counter in the declared metric budget
// (ADR-fleet-deployment-models Budget T; mirrored by kenaz-fleet
// schema/v1 AllowedMetrics). The set is compiled in: like the log kind
// ceiling, no server response can add a name.
type UsageCounter string

const (
	CounterConversationsStarted UsageCounter = "harness.conversations.started"
	CounterConversationsEnded   UsageCounter = "harness.conversations.ended"
	CounterToolInvocations      UsageCounter = "harness.tool.invocations"
	CounterErrors               UsageCounter = "harness.errors"
	CounterTokensInput          UsageCounter = "harness.tokens.input"
	CounterTokensOutput         UsageCounter = "harness.tokens.output"
)

// usageCounterClass maps each counter to the telemetry class that gates it.
// Fleet applies the same mapping server-side; the client applies it first so
// an opted-out class costs no bytes.
var usageCounterClass = map[UsageCounter]string{
	CounterConversationsStarted: "harness.usage_counts",
	CounterConversationsEnded:   "harness.usage_counts",
	CounterTokensInput:          "harness.usage_counts",
	CounterTokensOutput:         "harness.usage_counts",
	CounterToolInvocations:      "harness.tool_calls",
	CounterErrors:               "harness.errors",
}

// UsageCounters returns the compiled counter set, in a stable order.
func UsageCounters() []UsageCounter {
	return []UsageCounter{
		CounterConversationsStarted,
		CounterConversationsEnded,
		CounterToolInvocations,
		CounterErrors,
		CounterTokensInput,
		CounterTokensOutput,
	}
}

// UsageCounterClass returns the gating class for c, and whether c is declared.
func UsageCounterClass(c UsageCounter) (string, bool) {
	class, ok := usageCounterClass[c]
	return class, ok
}

// PipelineStatus is a payload-free health snapshot of the fleet export
// pipeline. Counts and reason classes only — never a body, a name, or an
// identifier. Safe to surface in Settings and in logs.
type PipelineStatus struct {
	Active         bool   `json:"active"`
	LogLaneEnabled bool   `json:"log_lane_enabled"`
	LastExportAt   string `json:"last_export_at,omitempty"`
	LastExportCode int    `json:"last_export_code,omitempty"`

	EventsAccepted        uint64 `json:"events_accepted"`
	EventsDroppedInactive uint64 `json:"events_dropped_inactive"`
	EventsDroppedGated    uint64 `json:"events_dropped_gated"`
	CountsRecorded        uint64 `json:"counts_recorded"`
	CountsDroppedInactive uint64 `json:"counts_dropped_inactive"`
	CountsDroppedGated    uint64 `json:"counts_dropped_gated"`
	ExportsOK             uint64 `json:"exports_ok"`
	ExportsFailed         uint64 `json:"exports_failed"`
	ExportsUnauthorized   uint64 `json:"exports_unauthorized"`
	ExportsIdentityMism   uint64 `json:"exports_identity_mismatch"`
}

// usageLane is the per-activation state. A new one is built on every
// Activate; Deactivate closes and discards it.
type usageLane struct {
	closed atomic.Bool

	logProvider *sdklog.LoggerProvider
	logger      otellog.Logger
	gate        *kindGatedLogExporter

	meterProvider *sdkmetric.MeterProvider
	counters      map[UsageCounter]metric.Int64Counter
}

// pipelineStats holds the atomics behind PipelineStatus.
type pipelineStats struct {
	eventsAccepted        atomic.Uint64
	eventsDroppedInactive atomic.Uint64
	eventsDroppedGated    atomic.Uint64
	countsRecorded        atomic.Uint64
	countsDroppedInactive atomic.Uint64
	countsDroppedGated    atomic.Uint64
	exportsOK             atomic.Uint64
	exportsFailed         atomic.Uint64
	exportsUnauthorized   atomic.Uint64
	exportsIdentityMism   atomic.Uint64

	mu             sync.Mutex
	lastExportAt   time.Time
	lastExportCode int
}

// ── identity-bound bearer ────────────────────────────────────────────────────

// errIdentityMismatch is returned by the bound transport when the live token
// belongs to a different subject than the activation it would be sent under.
type errIdentityMismatch struct{}

func (errIdentityMismatch) Error() string {
	return "fleet/otlp: current token subject differs from the activated identity; refusing to export"
}

// boundBearer wraps inner so it yields a token only while that token's `sub`
// equals sub — the kameas.user.id this activation was built for.
//
// The identity resource is frozen at Activate; the bearer is read on every
// flush. Without this binding there is a window, between a host account
// change and the supervisor re-activating, in which account A's queued batch
// would be POSTed with account B's token. Fleet would 401 it (the receiver
// compares kameas.user.id to the JWT sub), so nothing would be mis-stored —
// but "the far end rejects it" is not the property we want for account
// attribution. This makes the client refuse first, before any bytes move.
func boundBearer(inner BearerProvider, sub string, stats *pipelineStats) BearerProvider {
	return func() (string, error) {
		tok, err := inner()
		if err != nil || tok == "" {
			return tok, err
		}
		got, subErr := subjectFromJWT(tok)
		if subErr != nil || got != sub {
			if stats != nil {
				stats.exportsIdentityMism.Add(1)
			}
			return "", errIdentityMismatch{}
		}
		return tok, nil
	}
}

// ── export observer ──────────────────────────────────────────────────────────

// exportObserver records the outcome of each export request and surfaces 401s
// to the auth layer. It sees status codes only; it never reads a body.
type exportObserver struct {
	inner          http.RoundTripper
	stats          *pipelineStats
	onUnauthorized func()
}

func (t *exportObserver) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.inner.RoundTrip(req)
	if err != nil {
		t.stats.exportsFailed.Add(1)
		return nil, err
	}
	t.stats.mu.Lock()
	t.stats.lastExportAt = time.Now()
	t.stats.lastExportCode = resp.StatusCode
	t.stats.mu.Unlock()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		t.stats.exportsUnauthorized.Add(1)
		if t.onUnauthorized != nil {
			// The token may have been revoked or rotated under us. In a
			// workbench this nudges the broker session into an immediate
			// renewal instead of waiting out the expiry timer.
			t.onUnauthorized()
		}
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		t.stats.exportsOK.Add(1)
	default:
		t.stats.exportsFailed.Add(1)
	}
	return resp, nil
}

// ── droppable exporters ──────────────────────────────────────────────────────

// closableMetricExporter lets Deactivate discard the final collection a
// MeterProvider performs on Shutdown. Without it, tearing down account A's
// lane would flush A's last interval — acceptable on a clean shutdown, wrong
// on a sign-out, where the user has just withdrawn the session.
type closableMetricExporter struct {
	inner  sdkmetric.Exporter
	closed *atomic.Bool
}

func (e *closableMetricExporter) Temporality(k sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.inner.Temporality(k)
}

func (e *closableMetricExporter) Aggregation(k sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.inner.Aggregation(k)
}

func (e *closableMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if e.closed.Load() {
		return nil
	}
	// No HTTP request at all for an empty interval — an empty export would
	// still be an authenticated POST stamped with the user's identity, i.e. a
	// presence beacon. Same rule kindGatedLogExporter applies to logs.
	if !hasDataPoints(rm) {
		return nil
	}
	return e.inner.Export(ctx, rm)
}

// hasDataPoints reports whether rm carries at least one data point. Only
// integer sums exist in the usage lane; anything else counts as empty and is
// not sent.
func hasDataPoints(rm *metricdata.ResourceMetrics) bool {
	if rm == nil {
		return false
	}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if sum, ok := m.Data.(metricdata.Sum[int64]); ok && len(sum.DataPoints) > 0 {
				return true
			}
		}
	}
	return false
}

func (e *closableMetricExporter) ForceFlush(ctx context.Context) error {
	if e.closed.Load() {
		return nil
	}
	return e.inner.ForceFlush(ctx)
}

func (e *closableMetricExporter) Shutdown(ctx context.Context) error { return e.inner.Shutdown(ctx) }

// closableSpanExporter is the span-lane equivalent. It additionally applies
// the span lane's class gate at export time (FleetOTLPPipeline.spansAdmitted),
// so a consent or opt-in change takes effect on the next batch without
// re-registering the processor.
type closableSpanExporter struct {
	inner    sdktrace.SpanExporter
	closed   *atomic.Bool
	admitted *atomic.Bool
}

func (e *closableSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if e.closed.Load() {
		return nil
	}
	if e.admitted != nil && !e.admitted.Load() {
		return nil
	}
	return e.inner.ExportSpans(ctx, spans)
}

func (e *closableSpanExporter) Shutdown(ctx context.Context) error { return e.inner.Shutdown(ctx) }

// deltaTemporality makes every usage counter report per-interval deltas.
//
// Fleet stores one row per data point and sums them. Cumulative temporality
// would re-send the running total every interval and the sum would grow
// quadratically. Delta also has the property we want at the consent boundary:
// an interval is exported or it is gone — there is no running total that
// could later ship activity recorded before the user opted in.
func deltaTemporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.DeltaTemporality
}

// ── lane construction ────────────────────────────────────────────────────────

// usageLaneConfig is everything buildUsageLane needs.
type usageLaneConfig struct {
	otlpBase       string
	identityRes    *resource.Resource
	httpClient     *http.Client
	optIns         []TelemetryOptInItem
	logLaneEnabled bool
	metricInterval time.Duration
	logBatchDelay  time.Duration
}

func buildUsageLane(ctx context.Context, cfg usageLaneConfig) (*usageLane, error) {
	lane := &usageLane{counters: make(map[UsageCounter]metric.Int64Counter, len(usageCounterClass))}

	// ── events ──
	//
	// This is the one place the fleet log lane touches the network. It is
	// reachable only through kindGatedLogExporter, on a LoggerProvider this
	// package owns and the slog bridge never feeds. Both historical blockers
	// are closed: the provider is built post-enroll so its resource carries
	// kameas.user.id / org.id / machine.id, and UsageEmitter is the emitter of
	// kind-tagged, closed-body records.
	//
	// fleet-log-fence-allow: gated, identity-stamped, closed-vocabulary event lane (see above)
	logExp, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(cfg.otlpBase+"/v1/logs"),
		otlploghttp.WithHTTPClient(cfg.httpClient),
		otlploghttp.WithRetry(otlploghttp.RetryConfig{
			Enabled:         true,
			InitialInterval: 2 * time.Second,
			MaxInterval:     30 * time.Second,
			MaxElapsedTime:  5 * time.Minute,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("fleet/otlp: log exporter: %w", err)
	}
	gate := &kindGatedLogExporter{inner: logExp}
	gate.setOptIns(cfg.optIns)
	gate.setEnabled(cfg.logLaneEnabled)
	lane.gate = gate

	batchOpts := []sdklog.BatchProcessorOption{}
	if cfg.logBatchDelay > 0 {
		batchOpts = append(batchOpts, sdklog.WithExportInterval(cfg.logBatchDelay))
	}
	lane.logProvider = sdklog.NewLoggerProvider(
		sdklog.WithResource(cfg.identityRes),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(gate, batchOpts...)),
	)
	lane.logger = lane.logProvider.Logger(usageScopeName)

	// ── counters ──
	metricExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(cfg.otlpBase+"/v1/metrics"),
		otlpmetrichttp.WithHTTPClient(cfg.httpClient),
		otlpmetrichttp.WithTemporalitySelector(deltaTemporality),
		otlpmetrichttp.WithRetry(otlpmetrichttp.RetryConfig{
			Enabled:         true,
			InitialInterval: 2 * time.Second,
			MaxInterval:     30 * time.Second,
			MaxElapsedTime:  5 * time.Minute,
		}),
	)
	if err != nil {
		_ = lane.logProvider.Shutdown(ctx)
		return nil, fmt.Errorf("fleet/otlp: metric exporter: %w", err)
	}
	readerOpts := []sdkmetric.PeriodicReaderOption{}
	if cfg.metricInterval > 0 {
		readerOpts = append(readerOpts, sdkmetric.WithInterval(cfg.metricInterval))
	}
	lane.meterProvider = sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(cfg.identityRes),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(
			&closableMetricExporter{inner: metricExp, closed: &lane.closed}, readerOpts...)),
	)
	meter := lane.meterProvider.Meter(usageScopeName)
	for _, name := range UsageCounters() {
		c, cErr := meter.Int64Counter(string(name))
		if cErr != nil {
			_ = lane.logProvider.Shutdown(ctx)
			_ = lane.meterProvider.Shutdown(ctx)
			return nil, fmt.Errorf("fleet/otlp: counter %s: %w", name, cErr)
		}
		lane.counters[name] = c
	}
	return lane, nil
}

// close tears the lane down. discard=true drops anything still queued (the
// sign-out / account-change / consent-withdrawn path); discard=false flushes
// first (clean process shutdown, where the session is still valid).
func (l *usageLane) close(ctx context.Context, discard bool) {
	if l == nil {
		return
	}
	if discard {
		l.closed.Store(true)
		// Detach the network exporter before the provider's shutdown flush so
		// the flush has nowhere to go.
		l.gate.swapInner(ctx, nil)
	}
	if l.logProvider != nil {
		_ = l.logProvider.Shutdown(ctx)
	}
	if l.meterProvider != nil {
		_ = l.meterProvider.Shutdown(ctx)
	}
	l.closed.Store(true)
}

// ── emission entry points ────────────────────────────────────────────────────

// EmitEvent queues one kind-tagged event record for export and reports whether
// it was accepted.
//
// Accepted means: the pipeline is active, the log lane is enabled (effective
// consent is "full"), and kind is inside ceiling ∩ Fleet opt-ins. The same
// admission runs again inside the exporter gate; checking it here as well is
// what makes the return value truthful, and callers rely on that to keep
// started/ended records paired.
//
// body must be a closed, content-free shape. This package's only caller is
// UsageEmitter, which builds bodies from typed structs.
func (p *FleetOTLPPipeline) EmitEvent(ctx context.Context, kind LogEventKind, body []otellog.KeyValue) bool {
	p.mu.Lock()
	lane := p.usage
	p.mu.Unlock()
	if lane == nil || lane.closed.Load() {
		p.stats.eventsDroppedInactive.Add(1)
		return false
	}
	if !lane.gate.admits(kind) {
		p.stats.eventsDroppedGated.Add(1)
		return false
	}
	var rec otellog.Record
	now := time.Now()
	rec.SetTimestamp(now)
	rec.SetObservedTimestamp(now)
	rec.SetSeverity(otellog.SeverityInfo)
	rec.SetBody(otellog.MapValue(body...))
	rec.AddAttributes(otellog.String(AttrEventKind, string(kind)))
	lane.logger.Emit(ctx, rec)
	p.stats.eventsAccepted.Add(1)
	return true
}

// AddCount adds n to a declared label-less counter and reports whether it was
// recorded. No attributes are ever attached: the metric budget declares no
// labels, and a label is exactly where content would leak.
func (p *FleetOTLPPipeline) AddCount(ctx context.Context, name UsageCounter, n int64) bool {
	if n <= 0 {
		return false
	}
	class, declared := usageCounterClass[name]
	if !declared {
		return false
	}
	p.mu.Lock()
	lane := p.usage
	p.mu.Unlock()
	if lane == nil || lane.closed.Load() {
		p.stats.countsDroppedInactive.Add(1)
		return false
	}
	if !lane.gate.classOptedIn(class) {
		p.stats.countsDroppedGated.Add(1)
		return false
	}
	c, ok := lane.counters[name]
	if !ok {
		return false
	}
	c.Add(ctx, n)
	p.stats.countsRecorded.Add(1)
	return true
}

// Active reports whether an account-attributed activation is live.
func (p *FleetOTLPPipeline) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.usage != nil && !p.usage.closed.Load()
}

// ActiveIdentity returns the identity of the live activation, or the zero
// value when inactive.
func (p *FleetOTLPPipeline) ActiveIdentity() IdentityAttrs {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.usage == nil || p.usage.closed.Load() {
		return IdentityAttrs{}
	}
	return p.activeIdentity
}

// SetLogLaneEnabled opens or closes the event (log) lane independently of the
// per-class opt-ins. The settings layer sets it to (effective consent ==
// full). It exists because the aggregate tier opts usage classes IN — that is
// what lets its counters through — and those same classes would otherwise
// admit their event kinds. "No log records under Aggregate" is therefore
// enforced here, in code, rather than implied by an empty opt-in vector.
func (p *FleetOTLPPipeline) SetLogLaneEnabled(enabled bool) {
	p.mu.Lock()
	p.logLaneEnabled = enabled
	lane := p.usage
	p.recomputeSpanAdmissionLocked()
	p.mu.Unlock()
	if lane != nil {
		lane.gate.setEnabled(enabled)
	}
}

// SetOnUnauthorized installs a callback fired when an export is answered 401.
// Served mode wires it to the broker session's NotifyOn401.
func (p *FleetOTLPPipeline) SetOnUnauthorized(fn func()) {
	p.mu.Lock()
	p.onUnauthorized = fn
	p.mu.Unlock()
}

// Flush forces both usage lanes to export now. Best-effort; used on graceful
// shutdown and by tests.
func (p *FleetOTLPPipeline) Flush(ctx context.Context) {
	p.mu.Lock()
	lane := p.usage
	p.mu.Unlock()
	if lane == nil || lane.closed.Load() {
		return
	}
	_ = lane.logProvider.ForceFlush(ctx)
	_ = lane.meterProvider.ForceFlush(ctx)
}

// Status returns a payload-free health snapshot.
func (p *FleetOTLPPipeline) Status() PipelineStatus {
	p.mu.Lock()
	active := p.usage != nil && !p.usage.closed.Load()
	logLane := p.logLaneEnabled
	p.mu.Unlock()

	s := PipelineStatus{
		Active:                active,
		LogLaneEnabled:        logLane,
		EventsAccepted:        p.stats.eventsAccepted.Load(),
		EventsDroppedInactive: p.stats.eventsDroppedInactive.Load(),
		EventsDroppedGated:    p.stats.eventsDroppedGated.Load(),
		CountsRecorded:        p.stats.countsRecorded.Load(),
		CountsDroppedInactive: p.stats.countsDroppedInactive.Load(),
		CountsDroppedGated:    p.stats.countsDroppedGated.Load(),
		ExportsOK:             p.stats.exportsOK.Load(),
		ExportsFailed:         p.stats.exportsFailed.Load(),
		ExportsUnauthorized:   p.stats.exportsUnauthorized.Load(),
		ExportsIdentityMism:   p.stats.exportsIdentityMism.Load(),
	}
	p.stats.mu.Lock()
	if !p.stats.lastExportAt.IsZero() {
		s.LastExportAt = p.stats.lastExportAt.UTC().Format(time.RFC3339)
		s.LastExportCode = p.stats.lastExportCode
	}
	p.stats.mu.Unlock()
	return s
}
