package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTLPBaseURL derives the OTLP ingest endpoint from the resolved fleet
// config's API host. Appending "/otlp" means the standard SDK subpaths
// resolve to /otlp/v1/{traces,metrics} which matches the fleet receiver
// routes. Returns "" when APIBaseURL is empty (config not yet resolved /
// OSS / prod-before-ldflags).
//
// # Why this takes FleetConfig and not EnvProfile
//
// It used to read EnvProfile.FleetBaseURL. That field is the *dashboard*
// origin — the SPA host a user types in, the host that serves /config.json.
// Fleet is a two-hostname SaaS shape (the Stripe / GitHub / Linear
// convention): the SPA host serves the UI, a separate api.* host serves
// /api/v1/* and OTLP ingest. Every other API call already routes through
// Client.APIURL → FleetConfig.APIBaseURL; telemetry was the one caller left
// pointing at the dashboard.
//
// The failure that caused was silent, not loud. https://fleet.kameas.ai is
// CloudFront in front of an S3 SPA bucket, and an SPA bucket answers *any*
// path with 200 + index.html. So POST /otlp/v1/traces returned 200, the
// exporter recorded a successful export, and every span went into a void.
// There was no error to find.
//
// The two origins must stay distinct rather than one string being repointed:
// FleetBaseURL is still the correct input to /config.json discovery, which is
// how APIBaseURL is learned in the first place.
//
// NewOTLPAckRoundTripper is the other half of this fix. Correct routing stops
// today's data loss; rejecting a 200 that is not an OTLP acknowledgement is
// what makes the *next* misroute loud instead of silent.
func OTLPBaseURL(cfg FleetConfig) string {
	if cfg.APIBaseURL == "" {
		return ""
	}
	return strings.TrimRight(cfg.APIBaseURL, "/") + "/otlp"
}

// FleetOTLPPipeline manages the post-login OTLP export side-channel for the
// three signal types (traces/metrics/logs).
//
// # Constraint 1: Resource immutability
//
// The OTel SDK Resource is frozen at provider construction time.  The fleet
// receiver requires kameas.user.id / kameas.org.id / kameas.machine.id on the
// OTLP Resource but these are only known after login.
//
// Solution:
//   - Traces:   RegisterSpanProcessor on the existing TracerProvider.
//     The BatchSpanProcessor's exporter (resourceOverrideSpanExporter) wraps
//     each ReadOnlySpan and overrides Resource() to return the identity resource.
//     The OTLP transform layer reads Resource() per span, so the fleet receiver
//     sees the correct resource on every ResourceSpans envelope.
//   - Metrics + Logs: neither has a per-batch Resource() hook, so the
//     boot-time providers can never carry the identity. Activate instead
//     builds a DEDICATED MeterProvider and LoggerProvider post-enroll, whose
//     resource is the identity resource from construction, and Deactivate
//     tears them down (otlp_usage_lane.go). Only the closed usage vocabulary
//     (UsageEmitter) can write to them.
//   - The boot-time exporters (MetricExporter / LogExporter) stay registered
//     but are never given an inner exporter: the process-wide instruments and
//     the slog-bridged application log stream do not leave the machine.
//
// # Constraint 2: Dynamic auth
//
// otlphttp only accepts static WithHeaders at construction. We supply a custom
// http.Client with a tokenRoundTripper that reads the current Bearer token from
// the OS keychain on every flush (FR-001/FR-002).
//
// # Lifecycle
//
//  1. NewFleetOTLPPipeline at boot.
//  2. At boot, add MetricExporter() and LogExporter() to
//     telemetry.Config.MetricExporters / LogExporters.
//  3. After successful enroll, call Activate — idempotent.
//  4. On sign-out, account change, or consent withdrawal, call Deactivate —
//     queued data is discarded, and Activate may be called again later.
//  5. On harness teardown, call Shutdown (flushes, then closes).
type FleetOTLPPipeline struct {
	mu     sync.Mutex
	logger *slog.Logger

	// active span processor (registered on TracerProvider) so Shutdown drains it.
	activeSpanProc sdktrace.SpanProcessor

	// Lazy metric and log exporters registered at boot. Neither is ever given
	// an inner exporter: they sit on the process-wide providers, which carry
	// undeclared instruments and the slog-bridged application log stream.
	// Account-attributed export goes through `usage` instead.
	lazyMetricExp *resourceOverrideMetricExporter
	lazyLogExp    *kindGatedLogExporter

	// Back-ref to the TracerProvider we registered on.
	tp *sdktrace.TracerProvider

	// spanClosed lets Deactivate discard the span processor's shutdown flush
	// (see closableSpanExporter). Replaced on every Activate.
	spanClosed *atomic.Bool

	// usage is the account-attributed event + counter lane for the live
	// activation; nil while inactive. See otlp_usage_lane.go.
	usage          *usageLane
	activeIdentity IdentityAttrs

	// optIns is the last Fleet opt-in snapshot, retained so an Activate that
	// happens AFTER the snapshot arrived still starts narrowed correctly.
	optIns []TelemetryOptInItem

	// logLaneEnabled mirrors (effective consent == full). Default false: a
	// pipeline nobody has told about consent sends no event records.
	logLaneEnabled bool

	onUnauthorized func()
	stats          *pipelineStats

	// Export cadence overrides; zero means the SDK defaults. Tests shorten
	// them, production leaves them alone.
	metricInterval time.Duration
	logBatchDelay  time.Duration
}

// NewFleetOTLPPipeline creates an inactive pipeline. Call Activate after login.
func NewFleetOTLPPipeline(logger *slog.Logger) *FleetOTLPPipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetOTLPPipeline{
		logger:        logger,
		lazyMetricExp: &resourceOverrideMetricExporter{},
		lazyLogExp:    &kindGatedLogExporter{},
		stats:         &pipelineStats{},
	}
}

// SetExportCadence overrides how often the usage lanes export. Zero keeps the
// SDK default for that lane. Takes effect at the next Activate.
func (p *FleetOTLPPipeline) SetExportCadence(metricInterval, logBatchDelay time.Duration) {
	p.mu.Lock()
	p.metricInterval = metricInterval
	p.logBatchDelay = logBatchDelay
	p.mu.Unlock()
}

// MetricExporter returns the lazy metric exporter registered at boot time in
// telemetry.Config.MetricExporters. It exports nothing: the process-wide
// MeterProvider carries instruments and labels outside the declared metric
// budget. Declared counters leave through the usage lane (AddCount).
func (p *FleetOTLPPipeline) MetricExporter() sdkmetric.Exporter {
	return p.lazyMetricExp
}

// LogExporter returns the fleet log exporter registered at boot time in
// telemetry.Config.LogExporters.
//
// It exports nothing. This gate sits on the boot-time LoggerProvider, which
// the slog→OTel bridge feeds, and it is never given an inner exporter — so the
// application log stream cannot leave even if a line were somehow kind-tagged.
// Kind-tagged event records leave through the separate usage lane (EmitEvent),
// which has its own instance of the same gate.
func (p *FleetOTLPPipeline) LogExporter() sdklog.Exporter {
	return p.lazyLogExp
}

// SetTelemetryOptIns supplies Fleet's per-class opt-in snapshot — the runtime
// NARROWING channel for the log lane.
//
// The snapshot intersects with the compiled ceiling; it cannot widen it. A
// class Fleet opts in that no compiled kind maps to contributes nothing, and
// no field of TelemetryOptInItem can name a kind at all. See
// LogKindsAdmittedBy for the intersection and log_event_kind.go for why the
// ceiling is deliberately not remotely settable.
//
// Safe to call repeatedly; each call replaces the previous snapshot. Passing
// nil (e.g. on sign-out) returns the lane to admitting nothing.
func (p *FleetOTLPPipeline) SetTelemetryOptIns(optIns []TelemetryOptInItem) {
	p.lazyLogExp.setOptIns(optIns)
	p.mu.Lock()
	p.optIns = append([]TelemetryOptInItem(nil), optIns...)
	lane := p.usage
	p.mu.Unlock()
	if lane != nil {
		lane.gate.setOptIns(optIns)
	}
}

// IdentityAttrs holds the OTel Resource attributes required by the fleet
// receiver (§2.3 of the integration contract). All three must be set for
// the receiver to accept the batch.
type IdentityAttrs struct {
	UserID    string // kameas.user.id — must equal JWT sub
	OrgID     string // kameas.org.id — must equal Zitadel resource-owner claim
	MachineID string // kameas.machine.id — per-(org,machine) rate-limit key
}

// Activate wires the OTLP export pipeline post-login. Idempotent: a second
// call replaces the previous activation (handles re-login).
//
// Parameters:
//   - ctx: used to initialize the OTLP exporters.
//   - otlpBase: the OTLP endpoint base URL from OTLPBaseURL(profile). Pass ""
//     to no-op (unconfigured profile / consent off).
//   - baseRes: the startup resource from telemetry.Init; merged with identity attrs.
//   - identity: the user/org/machine attrs known post-login.
//   - bearer: function that returns the current access token on demand.
//   - tp: the TracerProvider from telemetry.Init. Required for trace export.
func (p *FleetOTLPPipeline) Activate(
	ctx context.Context,
	otlpBase string,
	baseRes *resource.Resource,
	identity IdentityAttrs,
	bearer BearerProvider,
	tp *sdktrace.TracerProvider,
) error {
	if otlpBase == "" {
		p.logger.Debug("fleet.otlp.activate.skipped", "reason", "no_endpoint")
		return nil
	}
	if identity.UserID == "" {
		p.logger.Debug("fleet.otlp.activate.skipped", "reason", "no_user_id")
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Build the identity resource by merging service attrs from the base
	// resource with the kameas.* identity attrs required by the receiver.
	identityRes, err := buildIdentityResource(baseRes, identity)
	if err != nil {
		return fmt.Errorf("fleet/otlp: build identity resource: %w", err)
	}

	// Shared transport, inside → out:
	//
	//   token   — reads the live bearer per flush, but only while its `sub`
	//             still equals the identity this activation is stamped with
	//             (boundBearer): a batch never rides another account's token.
	//   observe — records status codes; a 401 nudges the auth layer to renew.
	//   ack     — turns a 2xx that is not an OTLP acknowledgement into an
	//             export error, so a misrouted endpoint fails loudly instead
	//             of reporting success into a void (NewOTLPAckRoundTripper).
	httpClient := &http.Client{
		Transport: NewOTLPAckRoundTripper(&exportObserver{
			inner:          NewTokenRoundTripper(boundBearer(bearer, identity.UserID, p.stats), nil),
			stats:          p.stats,
			onUnauthorized: p.onUnauthorized,
		}),
	}

	// A re-activation (re-login, account change) replaces the previous usage
	// lane. Whatever it still had queued belongs to the previous activation
	// and is discarded rather than flushed under a session that may be gone.
	if p.usage != nil {
		p.usage.close(ctx, true)
		p.usage = nil
	}

	// ── Traces ────────────────────────────────────────────────────────────────
	if tp != nil {
		// Drain previous span processor if any.
		if p.activeSpanProc != nil && p.tp != nil {
			if p.spanClosed != nil {
				p.spanClosed.Store(true)
			}
			p.tp.UnregisterSpanProcessor(p.activeSpanProc)
			_ = p.activeSpanProc.Shutdown(ctx)
			p.activeSpanProc = nil
		}

		// The signal path is part of the endpoint URL. WithEndpointURL takes
		// the path from the URL verbatim; a separate WithURLPath("/v1/traces")
		// REPLACES it, which silently dropped the "/otlp" prefix and sent
		// spans to <api-host>/v1/traces — a route Fleet does not serve.
		spanExp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(otlpBase+"/v1/traces"),
			otlptracehttp.WithHTTPClient(httpClient),
		)
		if err != nil {
			return fmt.Errorf("fleet/otlp: span exporter: %w", err)
		}
		// Wrap to substitute resource on every export call.
		spanClosed := &atomic.Bool{}
		withResource := &resourceOverrideSpanExporter{
			inner: &closableSpanExporter{inner: spanExp, closed: spanClosed},
			res:   identityRes,
		}
		p.spanClosed = spanClosed
		// Wrap again to redact span name + attributes before they leave the
		// process boundary (security fix: FR-005 / NFR-001 on the live OTLP
		// path — harness-fleet-otlp-export-01NTLMEX01). The redacting wrapper
		// sits between the BatchSpanProcessor and the OTLP HTTP exporter so
		// all spans are clean before serialisation.
		redactedSpanExp := &redactingSpanExporter{inner: withResource}
		proc := sdktrace.NewBatchSpanProcessor(redactedSpanExp)
		tp.RegisterSpanProcessor(proc)
		p.activeSpanProc = proc
		p.tp = tp
		p.logger.Info("fleet.otlp.span_pipeline.activated",
			"endpoint", otlpBase,
			"user_id", identity.UserID,
			"org_id", identity.OrgID,
		)
	}

	// ── Usage lanes: events (logs) + counters (metrics) ──────────────────────
	//
	// Both are built here, post-enroll, on providers this pipeline owns, so
	// their resource carries the identity attrs Fleet requires — the blocker
	// that previously kept the log lane off. See otlp_usage_lane.go for the
	// full argument, and for why the slog bridge cannot reach them.
	//
	// The generic boot-time metric exporter (lazyMetricExp) is deliberately NOT
	// given an inner exporter any more. It used to forward every instrument of
	// the process-wide MeterProvider — names and labels nobody had declared —
	// to Fleet. The counter lane replaces it with a closed, label-less set.
	// The boot-time log gate (lazyLogExp) likewise keeps a nil inner: it is fed
	// by the slog bridge, and application log lines stay on the machine.
	lane, err := buildUsageLane(ctx, usageLaneConfig{
		otlpBase:       otlpBase,
		identityRes:    identityRes,
		httpClient:     httpClient,
		optIns:         p.optIns,
		logLaneEnabled: p.logLaneEnabled,
		metricInterval: p.metricInterval,
		logBatchDelay:  p.logBatchDelay,
	})
	if err != nil {
		p.logger.Warn("fleet.otlp.usage_lane.failed", "err", err)
		// Non-fatal: traces still proceed.
		return nil
	}
	p.usage = lane
	p.activeIdentity = identity
	p.logger.Info("fleet.otlp.usage_lane.activated",
		"endpoint", otlpBase,
		"log_lane_enabled", p.logLaneEnabled,
	)

	return nil
}

// Deactivate stops all account-attributed export and DISCARDS whatever is
// still queued. It is the sign-out / account-change / consent-withdrawn path:
// the session the queued data was collected under is gone or no longer
// consented, so flushing it would be wrong even where it would succeed.
//
// Idempotent. The pipeline can be Activated again afterwards.
func (p *FleetOTLPPipeline) Deactivate(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()
	wasActive := p.usage != nil || p.activeSpanProc != nil
	if p.spanClosed != nil {
		p.spanClosed.Store(true)
	}
	if p.activeSpanProc != nil && p.tp != nil {
		p.tp.UnregisterSpanProcessor(p.activeSpanProc)
		_ = p.activeSpanProc.Shutdown(ctx)
		p.activeSpanProc = nil
	}
	if p.usage != nil {
		p.usage.close(ctx, true)
		p.usage = nil
	}
	p.activeIdentity = IdentityAttrs{}
	if wasActive {
		p.logger.Info("fleet.otlp.deactivated")
	}
}

// Shutdown drains and shuts down all active processors/exporters.
func (p *FleetOTLPPipeline) Shutdown(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.activeSpanProc != nil && p.tp != nil {
		p.tp.UnregisterSpanProcessor(p.activeSpanProc)
		_ = p.activeSpanProc.Shutdown(ctx)
		p.activeSpanProc = nil
	}
	// Clean process shutdown: the session is still valid, so flush rather
	// than discard (contrast Deactivate).
	if p.usage != nil {
		p.usage.close(ctx, false)
		p.usage = nil
	}
	p.activeIdentity = IdentityAttrs{}
	_ = p.lazyMetricExp.Shutdown(ctx)
	_ = p.lazyLogExp.Shutdown(ctx)
	return nil
}

// ── resource helpers ──────────────────────────────────────────────────────────

const (
	attrUserID    = "kameas.user.id"
	attrOrgID     = "kameas.org.id"
	attrMachineID = "kameas.machine.id"
)

// buildIdentityResource merges the service-level attrs from baseRes with the
// fleet-required identity attrs. The result is the resource that goes on
// every OTLP ResourceSpans / ResourceMetrics / ResourceLogs envelope.
func buildIdentityResource(base *resource.Resource, id IdentityAttrs) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		attribute.String(attrUserID, id.UserID),
	}
	if id.OrgID != "" {
		attrs = append(attrs, attribute.String(attrOrgID, id.OrgID))
	}
	if id.MachineID != "" {
		attrs = append(attrs, attribute.String(attrMachineID, id.MachineID))
	}
	identityRes := resource.NewWithAttributes(semconv.SchemaURL, attrs...)
	if base == nil {
		return identityRes, nil
	}
	return resource.Merge(base, identityRes)
}

// ── resourceOverrideSpanExporter ─────────────────────────────────────────────

// resourceOverrideSpanExporter wraps an sdktrace.SpanExporter and substitutes
// each span's Resource() with the identity resource before delegating.
//
// The OTel OTLP trace transform reads span.Resource() to populate
// ResourceSpans.Resource. By overriding it here we inject the fleet-required
// identity attrs without needing to re-init the TracerProvider.
type resourceOverrideSpanExporter struct {
	inner sdktrace.SpanExporter
	res   *resource.Resource
}

func (e *resourceOverrideSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	wrapped := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		wrapped[i] = &spanWithResource{ReadOnlySpan: s, res: e.res}
	}
	return e.inner.ExportSpans(ctx, wrapped)
}

func (e *resourceOverrideSpanExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

// spanWithResource is a ReadOnlySpan decorator that overrides Resource().
// All other methods delegate to the embedded span.
type spanWithResource struct {
	sdktrace.ReadOnlySpan
	res *resource.Resource
}

func (s *spanWithResource) Resource() *resource.Resource { return s.res }

// ── redactingSpanExporter ─────────────────────────────────────────────────────

// redactingSpanExporter wraps an sdktrace.SpanExporter and applies
// DefaultRedactor to every span's name and attributes before delegating to the
// inner exporter.  This is the security fence for the live OTLP path:
// credential/prompt-bearing span attributes and span names are scrubbed before
// the OTLP HTTP client serialises and transmits them.
//
// Applied immediately before the BatchSpanProcessor's underlying exporter so
// redaction is guaranteed regardless of consent level — if a span reaches the
// exporter, it is already clean.
//
// (harness-fleet-otlp-export-01NTLMEX01 FR-005 / NFR-001 security fix)
type redactingSpanExporter struct {
	inner sdktrace.SpanExporter
}

func (e *redactingSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	if len(spans) == 0 {
		return nil
	}
	redacted := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		redacted[i] = &redactedSpan{ReadOnlySpan: s}
	}
	return e.inner.ExportSpans(ctx, redacted)
}

func (e *redactingSpanExporter) Shutdown(ctx context.Context) error {
	return e.inner.Shutdown(ctx)
}

// redactedSpan is a ReadOnlySpan decorator that returns redacted Name() and
// Attributes(). All other methods delegate to the embedded span unchanged.
type redactedSpan struct {
	sdktrace.ReadOnlySpan
}

func (s *redactedSpan) Name() string {
	return DefaultRedactor.RedactSpanName(s.ReadOnlySpan.Name())
}

func (s *redactedSpan) Attributes() []attribute.KeyValue {
	raw := s.ReadOnlySpan.Attributes()
	if len(raw) == 0 {
		return raw
	}
	// Convert to map, redact, convert back.
	m := make(map[string]any, len(raw))
	for _, kv := range raw {
		m[string(kv.Key)] = kv.Value.Emit()
	}
	cleaned := DefaultRedactor.RedactAttributes(m)
	out := make([]attribute.KeyValue, 0, len(cleaned))
	for k, v := range cleaned {
		switch val := v.(type) {
		case string:
			out = append(out, attribute.String(k, val))
		default:
			// Non-string values were not modified by the redactor; re-use
			// the original KeyValue to preserve the original type.
			for _, orig := range raw {
				if string(orig.Key) == k {
					out = append(out, orig)
					break
				}
			}
		}
	}
	return out
}

// ── resourceOverrideMetricExporter ───────────────────────────────────────────

// resourceOverrideMetricExporter is a lazy sdkmetric.Exporter that is
// registered at boot time (so it gets a PeriodicReader from telemetry.Init)
// and no-ops until Activate swaps in a real OTLP exporter with the identity
// resource. Thread-safe.
type resourceOverrideMetricExporter struct {
	mu    sync.RWMutex
	inner sdkmetric.Exporter
	res   *resource.Resource
}

func (e *resourceOverrideMetricExporter) Temporality(ik sdkmetric.InstrumentKind) metricdata.Temporality {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return metricdata.CumulativeTemporality
	}
	return inner.Temporality(ik)
}

func (e *resourceOverrideMetricExporter) Aggregation(ik sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return sdkmetric.AggregationDefault{}
	}
	return inner.Aggregation(ik)
}

func (e *resourceOverrideMetricExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	e.mu.RLock()
	inner := e.inner
	res := e.res
	e.mu.RUnlock()
	if inner == nil {
		return nil
	}
	rm2 := *rm
	rm2.Resource = res
	return inner.Export(ctx, &rm2)
}

func (e *resourceOverrideMetricExporter) ForceFlush(ctx context.Context) error {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return nil
	}
	return inner.ForceFlush(ctx)
}

func (e *resourceOverrideMetricExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	inner := e.inner
	e.inner = nil
	e.mu.Unlock()
	if inner == nil {
		return nil
	}
	return inner.Shutdown(ctx)
}

func (e *resourceOverrideMetricExporter) swapInner(ctx context.Context, newInner sdkmetric.Exporter, newRes *resource.Resource) {
	e.mu.Lock()
	old := e.inner
	e.inner = newInner
	e.res = newRes
	e.mu.Unlock()
	if old != nil {
		_ = old.Shutdown(ctx)
	}
}

// ── kindGatedLogExporter ─────────────────────────────────────────────────────

// kindGatedLogExporter is the fleet log lane's admission gate.
//
// It admits a log record only when the record carries a recognised,
// allowlisted kameas.event.kind (see log_event_kind.go). Unknown or absent
// kind ⇒ the record is dropped locally, in this process, before any bytes are
// serialised. A plain slog line — which is what the slog→OTel bridge feeds
// into this pipeline, and which is content-bearing by nature — never carries a
// kind and therefore can never leave.
//
// Fail-closed by construction: the gate is an allowlist, not a denylist, so a
// new record shape is non-exportable until someone deliberately tags it.
//
// There are two instances in a live pipeline. The boot-time one
// (FleetOTLPPipeline.lazyLogExp) sits behind the slog bridge and never gets an
// inner exporter. The usage-lane one (usageLane.gate) fronts the only OTLP log
// exporter in the process, on a provider the slog bridge cannot reach. The
// gate is what guarantees that even that second lane can carry nothing but
// allowlisted, opted-in kinds.
//
// Consent composes on top of, not instead of, the gate: activateOTLPPipeline
// (core/rpc/views/settings/fleet.go) refuses to call Activate at all while
// consent is "none", and the receiver applies the per-class opt-in and org-tier
// checks on anything that does arrive. The kind allowlist is the innermost of
// the three and the only one that runs before the bytes leave the machine.
//
// # Ceiling ∩ narrowing
//
// Two inputs decide admission, and they compose in one direction only:
//
//   - the compiled ceiling (log_event_kind.go) — what this binary is capable
//     of transmitting. Not remotely widenable.
//   - Fleet's per-class opt-in snapshot, supplied via SetTelemetryOptIns —
//     which can only remove kinds from the ceiling, never add to it.
//
// A nil snapshot admits nothing.
//
// # On the log resource
//
// sdklog.Record carries no Resource() — the LoggerProvider's resource is
// attached at the OTLP encoding layer and frozen at provider construction.
// There is no per-record override hook the way there is for spans
// (resourceOverrideSpanExporter), and Fleet's HandleLogs rejects the entire
// request with 401 when kameas.user.id != the JWT sub. That is why the usage
// lane builds its LoggerProvider inside Activate, post-enroll: the identity is
// known by then, so the frozen resource is the right one. The boot-time
// provider can never satisfy this, which is one more reason its gate keeps a
// nil inner exporter.
type kindGatedLogExporter struct {
	mu    sync.RWMutex
	inner sdklog.Exporter

	// admitted is ceiling ∩ Fleet opt-ins, recomputed whenever Fleet supplies
	// a new opt-in snapshot. nil ⇒ nothing is admissible.
	admitted map[LogEventKind]struct{}

	// optedInClasses is the same snapshot by class, for the counter lane,
	// which is gated per class rather than per kind. nil ⇒ nothing.
	optedInClasses map[string]bool

	// disabled closes the lane regardless of opt-ins. Set when effective
	// consent is below "full": the aggregate tier opts usage classes in (so
	// its counters pass), and without this switch those classes would admit
	// their event kinds too. Zero value is "not disabled" so a bare gate
	// behaves exactly as it did before the switch existed.
	disabled bool
}

// setEnabled opens (true) or closes (false) the lane independently of the
// per-class opt-ins.
func (e *kindGatedLogExporter) setEnabled(enabled bool) {
	e.mu.Lock()
	e.disabled = !enabled
	e.mu.Unlock()
}

// admits reports whether kind would currently pass the gate.
func (e *kindGatedLogExporter) admits(kind LogEventKind) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.disabled || !LogEventKindAllowed(string(kind)) {
		return false
	}
	_, ok := e.admitted[kind]
	return ok
}

// classOptedIn reports whether Fleet's snapshot opts class in. Unlike admits
// it ignores the lane switch: counters are the aggregate tier's lane and must
// pass while event records are closed.
func (e *kindGatedLogExporter) classOptedIn(class string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.optedInClasses[class]
}

// setOptIns recomputes the admitted set from a Fleet opt-in snapshot. The
// snapshot can only narrow: LogKindsAdmittedBy iterates the compiled ceiling
// and uses optIns solely to exclude.
func (e *kindGatedLogExporter) setOptIns(optIns []TelemetryOptInItem) {
	next := LogKindsAdmittedBy(optIns)
	classes := make(map[string]bool, len(optIns))
	for _, item := range optIns {
		// Only `true` is recorded, and only for classes the compiled ceiling
		// or the compiled counter set actually names — an opt-in for a class
		// this binary knows nothing about contributes nothing.
		if item.OptedIn && knownGatingClass(item.Class) {
			classes[item.Class] = true
		}
	}
	e.mu.Lock()
	e.admitted = next
	e.optedInClasses = classes
	e.mu.Unlock()
}

// knownGatingClass reports whether class gates anything this binary can emit.
func knownGatingClass(class string) bool {
	for _, c := range logKindCeiling {
		if c == class {
			return true
		}
	}
	for _, c := range usageCounterClass {
		if c == class {
			return true
		}
	}
	return false
}

func (e *kindGatedLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.RLock()
	inner := e.inner
	allowed := e.admitted
	disabled := e.disabled
	e.mu.RUnlock()
	if inner == nil || disabled {
		return nil
	}

	// Admission first: drop anything without an allowlisted kind. Records that
	// do not pass are never serialised, never buffered, never sent.
	admitted := make([]sdklog.Record, 0, len(records))
	for _, r := range records {
		if !logRecordExportable(r, allowed) {
			continue
		}
		// Redact log bodies before they leave the process boundary
		// (security fix: NFR-001 / FR-005 on the live OTLP path —
		// harness-fleet-otlp-export-01NTLMEX01).
		body := r.Body()
		if body.Kind() == otellog.KindString {
			cleaned := DefaultRedactor.RedactLogBody(body.AsString())
			if cleaned != body.AsString() {
				r.SetBody(otellog.StringValue(cleaned))
			}
		}
		admitted = append(admitted, r)
	}
	if len(admitted) == 0 {
		// No HTTP request at all — an empty export would still be a POST.
		return nil
	}
	return inner.Export(ctx, admitted)
}

func (e *kindGatedLogExporter) ForceFlush(ctx context.Context) error {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return nil
	}
	return inner.ForceFlush(ctx)
}

func (e *kindGatedLogExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	inner := e.inner
	e.inner = nil
	e.mu.Unlock()
	if inner == nil {
		return nil
	}
	return inner.Shutdown(ctx)
}

func (e *kindGatedLogExporter) swapInner(_ context.Context, newInner sdklog.Exporter) {
	e.mu.Lock()
	old := e.inner
	e.inner = newInner
	e.mu.Unlock()
	if old != nil {
		_ = old.Shutdown(context.Background())
	}
}
