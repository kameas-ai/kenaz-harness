package fleet

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
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
//   - Metrics:  A resourceOverrideMetricExporter is registered at boot in
//     telemetry.Config.MetricExporters (no-ops until Activate). Activate swaps
//     in the real OTLP exporter + identity resource.
//   - Logs:     Not exported. kindGatedLogExporter is registered at boot in
//     telemetry.Config.LogExporters but Activate never gives it an inner
//     exporter — the resource problem above has no per-record solution for
//     logs, and nothing emits kind-tagged log records. See the "Logs" section
//     of Activate.
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

	// Lazy metric and log exporters registered at boot; the metric
	// exporter's inner exporter is swapped on Activate. The log exporter's
	// inner exporter is deliberately never swapped in — see the comment on
	// kindGatedLogExporter and the "Logs" section of Activate.
	lazyMetricExp *resourceOverrideMetricExporter
	lazyLogExp    *kindGatedLogExporter

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
		lazyLogExp:    &kindGatedLogExporter{},
	}
}

// MetricExporter returns the lazy metric exporter that should be registered
// at boot time in telemetry.Config.MetricExporters. It no-ops until Activate
// is called.
func (p *FleetOTLPPipeline) MetricExporter() sdkmetric.Exporter {
	return p.lazyMetricExp
}

// LogExporter returns the fleet log exporter registered at boot time in
// telemetry.Config.LogExporters.
//
// It exports nothing. The fleet log lane is off (see the "Logs" section of
// Activate for why, and what must change to turn it back on); this returns the
// gate rather than nil so that the wiring, and the fail-closed admission rule
// it enforces, stay in the live pipeline.
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

	// Shared transport: auth inside, ack validation outside. The ack wrapper
	// turns a 2xx that is not an OTLP acknowledgement into an export error, so
	// a misrouted endpoint fails loudly instead of reporting success into a
	// void (see NewOTLPAckRoundTripper).
	httpClient := &http.Client{
		Transport: NewOTLPAckRoundTripper(NewTokenRoundTripper(bearer, nil)),
	}

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
	//
	// Deliberately not activated: no OTLP log exporter is constructed here, so
	// the fleet log lane makes no network calls and ships no records. This is
	// a decision, not an omission.
	//
	// What used to happen: an otlploghttp exporter was wired to /v1/logs and
	// the harness's entire slog stream — every application log line, via the
	// slog→OTel bridge installed by telemetry.Init — was batched and POSTed to
	// Fleet. Application log bodies are content-bearing by nature (file paths,
	// error strings, identifiers), which constitution §IX does not permit to
	// leave the machine.
	//
	// Two independent facts make that traffic pure cost:
	//
	//  1. Nothing emits a kameas.event.kind attribute. The Fleet receiver
	//     admits a log record only when ClassFor(kameas.event.kind) resolves
	//     (kenaz-fleet service/telemetry/receiver.go, HandleLogs). No code in
	//     any Kameas repo sets that attribute on a log record, so every record
	//     the harness sent was counted DroppedInvalid — after crossing the
	//     network and terminating TLS on Kameas infrastructure.
	//
	//  2. The batch is rejected before the kind check anyway. Log records
	//     carry the LoggerProvider's resource, which is frozen at boot and has
	//     no kameas.user.id (see Constraint 1 above — there is no per-record
	//     Resource() hook to override, the way there is for spans). HandleLogs
	//     validates the first ResourceLogs group's resource attrs up front and
	//     401s the WHOLE request when kameas.user.id != the JWT sub.
	//
	// To turn the lane back on, both must be fixed, in this order:
	//
	//   a. Solve the resource problem — the records must carry
	//      kameas.user.id / kameas.org.id / kameas.machine.id — e.g. by
	//      building the ResourceLogs envelope directly instead of relying on
	//      the boot-time LoggerProvider resource.
	//   b. Emit records that actually carry an allowlisted AttrEventKind.
	//      Everything else stays dropped by kindGatedLogExporter regardless.
	//
	// Re-adding an OTLP log exporter requires the fence's explicit opt-out
	// annotation at the call site; scripts/ci/check-fleet-log-export-fence.sh
	// documents it and fails the build without it.
	p.logger.Debug("fleet.otlp.log_pipeline.disabled",
		"reason", "no_kind_tagged_emitters_and_no_identity_resource")

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
// This is the fence, not the switch. The lane is additionally off at the
// source: Activate never installs an inner exporter, so inner is nil in
// production and nothing is transmitted at all. The gate stays in the live
// pipeline so that installing an inner exporter — the one change that would
// re-open the lane — cannot on its own put raw application logs on the wire.
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
// attached at the OTLP encoding layer, and that resource is frozen at boot,
// before login, so it has no kameas.user.id. There is no per-record override
// hook the way there is for spans (resourceOverrideSpanExporter). Fleet's
// HandleLogs validates the first ResourceLogs group's resource attrs and
// rejects the entire request with 401 when kameas.user.id != the JWT sub, so
// this is a hard blocker on the lane rather than a cosmetic gap. It is
// recorded here, and in the "Logs" section of Activate, as a precondition for
// re-enabling log export — it is not a live-pipeline TODO, because there is no
// live log pipeline.
type kindGatedLogExporter struct {
	mu    sync.RWMutex
	inner sdklog.Exporter

	// admitted is ceiling ∩ Fleet opt-ins, recomputed whenever Fleet supplies
	// a new opt-in snapshot. nil ⇒ nothing is admissible.
	admitted map[LogEventKind]struct{}
}

// setOptIns recomputes the admitted set from a Fleet opt-in snapshot. The
// snapshot can only narrow: LogKindsAdmittedBy iterates the compiled ceiling
// and uses optIns solely to exclude.
func (e *kindGatedLogExporter) setOptIns(optIns []TelemetryOptInItem) {
	next := LogKindsAdmittedBy(optIns)
	e.mu.Lock()
	e.admitted = next
	e.mu.Unlock()
}

func (e *kindGatedLogExporter) Export(ctx context.Context, records []sdklog.Record) error {
	e.mu.RLock()
	inner := e.inner
	allowed := e.admitted
	e.mu.RUnlock()
	if inner == nil {
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
