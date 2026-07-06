package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otellog "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// OTLPBaseURL derives the OTLP endpoint base from the fleet base URL.
// Appending "/otlp" means the standard SDK subpaths resolve to
// /otlp/v1/{traces,metrics,logs} which matches the fleet receiver routes.
// Returns "" when FleetBaseURL is empty (OSS / prod-before-ldflags).
func OTLPBaseURL(profile EnvProfile) string {
	if profile.FleetBaseURL == "" {
		return ""
	}
	return strings.TrimRight(profile.FleetBaseURL, "/") + "/otlp"
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
//   - Metrics:  A resourceOverrideMetricExporter is registered at boot in
//     telemetry.Config.MetricExporters (no-ops until Activate). Activate swaps
//     in the real OTLP exporter + identity resource.
//   - Logs:     Same lazy-wrapper approach via resourceOverrideLogExporter
//     registered at boot in telemetry.Config.LogExporters.
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
//  4. On harness teardown, call Shutdown.
type FleetOTLPPipeline struct {
	mu     sync.Mutex
	logger *slog.Logger

	// active span processor (registered on TracerProvider) so Shutdown drains it.
	activeSpanProc sdktrace.SpanProcessor

	// Lazy metric and log exporters registered at boot; their inner
	// exporter is swapped on Activate.
	lazyMetricExp *resourceOverrideMetricExporter
	lazyLogExp    *resourceOverrideLogExporter

	// Back-ref to the TracerProvider we registered on.
	tp *sdktrace.TracerProvider
}

// NewFleetOTLPPipeline creates an inactive pipeline. Call Activate after login.
func NewFleetOTLPPipeline(logger *slog.Logger) *FleetOTLPPipeline {
	if logger == nil {
		logger = slog.Default()
	}
	return &FleetOTLPPipeline{
		logger:        logger,
		lazyMetricExp: &resourceOverrideMetricExporter{},
		lazyLogExp:    &resourceOverrideLogExporter{},
	}
}

// MetricExporter returns the lazy metric exporter that should be registered
// at boot time in telemetry.Config.MetricExporters. It no-ops until Activate
// is called.
func (p *FleetOTLPPipeline) MetricExporter() sdkmetric.Exporter {
	return p.lazyMetricExp
}

// LogExporter returns the lazy log exporter that should be registered at boot
// time in telemetry.Config.LogExporters. It no-ops until Activate is called.
func (p *FleetOTLPPipeline) LogExporter() sdklog.Exporter {
	return p.lazyLogExp
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

	// Shared auth transport.
	httpClient := &http.Client{Transport: NewTokenRoundTripper(bearer, nil)}

	// ── Traces ────────────────────────────────────────────────────────────────
	if tp != nil {
		// Drain previous span processor if any.
		if p.activeSpanProc != nil && p.tp != nil {
			p.tp.UnregisterSpanProcessor(p.activeSpanProc)
			_ = p.activeSpanProc.Shutdown(ctx)
			p.activeSpanProc = nil
		}

		spanExp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpointURL(otlpBase),
			otlptracehttp.WithURLPath("/v1/traces"),
			otlptracehttp.WithHTTPClient(httpClient),
		)
		if err != nil {
			return fmt.Errorf("fleet/otlp: span exporter: %w", err)
		}
		// Wrap to substitute resource on every export call.
		withResource := &resourceOverrideSpanExporter{
			inner: spanExp,
			res:   identityRes,
		}
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

	// ── Metrics ───────────────────────────────────────────────────────────────
	metricExp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpointURL(otlpBase),
		otlpmetrichttp.WithURLPath("/v1/metrics"),
		otlpmetrichttp.WithHTTPClient(httpClient),
	)
	if err != nil {
		p.logger.Warn("fleet.otlp.metric_exporter.failed", "err", err)
		// Non-fatal: traces and logs still proceed.
	} else {
		p.lazyMetricExp.swapInner(ctx, metricExp, identityRes)
		p.logger.Info("fleet.otlp.metric_pipeline.activated", "endpoint", otlpBase)
	}

	// ── Logs ─────────────────────────────────────────────────────────────────
	logExp, err := otlploghttp.New(ctx,
		otlploghttp.WithEndpointURL(otlpBase),
		otlploghttp.WithURLPath("/v1/logs"),
		otlploghttp.WithHTTPClient(httpClient),
	)
	if err != nil {
		p.logger.Warn("fleet.otlp.log_exporter.failed", "err", err)
	} else {
		p.lazyLogExp.swapInner(ctx, logExp)
		p.logger.Info("fleet.otlp.log_pipeline.activated", "endpoint", otlpBase)
	}

	return nil
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

// ── resourceOverrideLogExporter ──────────────────────────────────────────────

// resourceOverrideLogExporter is the lazy log counterpart. Note: sdklog.Record
// carries no Resource() — the LoggerProvider's resource is attached at the
// OTLP encoding layer. Since we can't override it per-record, we route log
// export through the lazy inner exporter directly; the resource injection
// is handled by the LoggerProvider's own resource at export time.
//
// For v1 we accept that the log records sent to fleet carry the startup
// resource (missing kameas.user.id). The receiver validates user.id only for
// the first ResourceLogs group; when the log body's kameas.event.kind maps to
// a known class, records are accepted per the opt-in check. The receiver does
// NOT hard-block on missing kameas.user.id for logs the way it does for traces
// (trace resource validation fires before insert). This is a known v1
// limitation documented in the spec open-questions section.
//
// TODO(WP05): Add the resource override for logs once the receiver enforces it.
type resourceOverrideLogExporter struct {
	mu    sync.RWMutex
	inner sdklog.Exporter
}

func (e *resourceOverrideLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return nil
	}
	// Redact log bodies before they leave the process boundary
	// (security fix: NFR-001 / FR-005 on the live OTLP path —
	// harness-fleet-otlp-export-01NTLMEX01).
	redacted := make([]sdklog.Record, len(records))
	for i, r := range records {
		body := r.Body()
		if body.Kind() == otellog.KindString {
			cleaned := DefaultRedactor.RedactLogBody(body.AsString())
			if cleaned != body.AsString() {
				r.SetBody(otellog.StringValue(cleaned))
			}
		}
		redacted[i] = r
	}
	return inner.Export(ctx, redacted)
}

func (e *resourceOverrideLogExporter) ForceFlush(ctx context.Context) error {
	e.mu.RLock()
	inner := e.inner
	e.mu.RUnlock()
	if inner == nil {
		return nil
	}
	return inner.ForceFlush(ctx)
}

func (e *resourceOverrideLogExporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	inner := e.inner
	e.inner = nil
	e.mu.Unlock()
	if inner == nil {
		return nil
	}
	return inner.Shutdown(ctx)
}

func (e *resourceOverrideLogExporter) swapInner(_ context.Context, newInner sdklog.Exporter) {
	e.mu.Lock()
	old := e.inner
	e.inner = newInner
	e.mu.Unlock()
	if old != nil {
		_ = old.Shutdown(context.Background())
	}
}
