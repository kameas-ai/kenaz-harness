package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otellog "go.opentelemetry.io/otel/log"
	logglobal "go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// ServiceName is the canonical service.name attribute the harness
// emits on every signal. Pinned per FR-002.
const ServiceName = "kaneaz-harness"

// Config drives Init. Only DataDir + Storage are required; the rest
// have safe defaults. WP02 will populate the SpanExporters /
// MetricExporters / LogExporters slices with the OTLP HTTP exporters
// when the user configures a fleet endpoint.
type Config struct {
	// DataDir roots the persisted instance.id and any future telemetry
	// state (retention metadata, etc.).
	DataDir string

	// BuildVersion populates service.version in the resource. Empty
	// strings degrade to "dev" so a local `wails dev` boot still has
	// a stable label.
	BuildVersion string

	// Storage is the unified harness DB the local exporters write
	// into. Required: passing nil returns an error.
	Storage storage.DB

	// Logger receives any internal warnings (write failures, drop
	// counters surfaced under load, etc.). nil falls back to
	// slog.Default.
	Logger *slog.Logger

	// SpanExporters / MetricExporters / LogExporters are additional
	// exporters the caller wants attached to the providers in
	// addition to the local + OTLP composites constructed here. The
	// rpc layer leaves these nil; tests that want to assert SDK
	// pipelining without standing up a fake OTLP receiver use them.
	SpanExporters   []sdktrace.SpanExporter
	MetricExporters []sdkmetric.Exporter
	LogExporters    []sdklog.Exporter

	// OTLPEndpoint, if non-empty, opts the harness into the dual-export
	// fan-out: every signal lands in BOTH the local SQLite store AND
	// the configured OTLP/HTTP endpoint. Empty Endpoint = local-only,
	// no outbound work, NFR-005 invariant intact.
	OTLPEndpoint string
	OTLPHeaders  map[string]string
	OTLPInsecure bool

	// InstallSlogBridge replaces the global logging handler with a
	// bridge that fans every slog line into the OTel logger pipeline
	// in addition to the existing file sink. Off by default so the
	// telemetry tests (which construct Telemetry repeatedly) do not
	// stomp on the process-global logger; core.initTelemetry sets it
	// to true at boot.
	InstallSlogBridge bool
}

// Telemetry is the Init result. The caller holds it for the harness's
// lifetime and calls Shutdown on teardown. WP05 will read InstanceID
// out for the rpc layer's AppInfo response.
type Telemetry struct {
	TracerProvider *sdktrace.TracerProvider
	MeterProvider  *sdkmetric.MeterProvider
	LoggerProvider *sdklog.LoggerProvider

	InstanceID string
	Resource   *resource.Resource

	// LocalSpan / LocalMetric / LocalLog are held so future WPs can
	// read drop-counter metrics off them and the rpc layer can
	// surface row counts through Telemetry_Stats.
	LocalSpan   *LocalSpanExporter
	LocalMetric *LocalMetricExporter
	LocalLog    *LocalLogExporter

	logger      *slog.Logger
	shutdownFns []func(context.Context) error
	shutdownOnce sync.Once
	shutdownErr  error
}

// Init constructs the SDK providers, wires the local SQLite-backed
// exporters, registers the providers as the OTel global, and returns
// a handle for shutdown. Failure modes:
//
//   - cfg.Storage == nil → error (caller decides whether to abort).
//   - EnsureInstanceID fails (filesystem dead) → error.
//   - Local exporter constructors fail → error (only nil-db today).
//
// All other failures degrade silently inside the exporters; the
// providers themselves never error during construction in v1.30.
//
// When cfg.OTLPEndpoint is set, OTLP/HTTP exporters are constructed
// and wrapped together with the local exporters in a CompositeXxx
// fan-out: each signal lands in both sinks per export call. When the
// endpoint is empty no OTLP exporter is constructed (NFR-005:
// local-first invariant — zero outbound network work).
func Init(ctx context.Context, cfg Config) (*Telemetry, error) {
	if cfg.Storage == nil {
		return nil, errors.New("telemetry: Init: Storage required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("telemetry: Init: DataDir required")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	version := cfg.BuildVersion
	if version == "" {
		version = "dev"
	}

	instanceID, err := EnsureInstanceID(cfg.DataDir)
	if err != nil {
		return nil, err
	}

	res := buildResource(version, instanceID)

	localSpan, err := NewLocalSpanExporter(cfg.Storage, logger)
	if err != nil {
		return nil, err
	}
	localMetric, err := NewLocalMetricExporter(cfg.Storage, logger)
	if err != nil {
		return nil, err
	}
	localLog, err := NewLocalLogExporter(cfg.Storage, logger)
	if err != nil {
		return nil, err
	}

	// Build OTLP exporters lazily. NewOTLP*Exporter returns (nil, nil)
	// when cfg.OTLPEndpoint is empty so the composite skips the OTLP
	// branch without ever instantiating an HTTP client (NFR-005).
	var (
		otlpSpan   sdktrace.SpanExporter
		otlpMetric sdkmetric.Exporter
		otlpLog    sdklog.Exporter
	)
	if cfg.OTLPEndpoint != "" {
		otlpSpan, err = NewOTLPSpanExporter(ctx, OTLPSpanConfig{
			Endpoint: cfg.OTLPEndpoint,
			Headers:  cfg.OTLPHeaders,
			Insecure: cfg.OTLPInsecure,
		})
		if err != nil {
			return nil, err
		}
		otlpMetric, err = NewOTLPMetricExporter(ctx, OTLPMetricConfig{
			Endpoint: cfg.OTLPEndpoint,
			Headers:  cfg.OTLPHeaders,
			Insecure: cfg.OTLPInsecure,
		})
		if err != nil {
			return nil, err
		}
		otlpLog, err = NewOTLPLogExporter(ctx, OTLPLogConfig{
			Endpoint: cfg.OTLPEndpoint,
			Headers:  cfg.OTLPHeaders,
			Insecure: cfg.OTLPInsecure,
		})
		if err != nil {
			return nil, err
		}
	}

	// Composite exporters fan-out to local + (optional) OTLP. Local
	// errors propagate to the SDK; OTLP errors are swallowed and
	// warn-logged so a network blip never poisons the chat surface.
	compSpan := &CompositeSpanExporter{Local: localSpan, OTLP: otlpSpan, Logger: logger}
	compMetric := &CompositeMetricExporter{Local: localMetric, OTLP: otlpMetric, Logger: logger}
	compLog := &CompositeLogExporter{Local: localLog, OTLP: otlpLog, Logger: logger}

	// Tracer provider: composite via batcher (async), plus any extra
	// exporters the caller passed in (test seam).
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(compSpan),
	}
	for _, exp := range cfg.SpanExporters {
		if exp != nil {
			tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
		}
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)

	// Meter provider: periodic reader pushing into the composite;
	// extra exporters get their own readers.
	readers := []sdkmetric.Reader{
		sdkmetric.NewPeriodicReader(compMetric),
	}
	for _, exp := range cfg.MetricExporters {
		if exp != nil {
			readers = append(readers, sdkmetric.NewPeriodicReader(exp))
		}
	}
	mpOpts := []sdkmetric.Option{
		sdkmetric.WithResource(res),
	}
	for _, r := range readers {
		mpOpts = append(mpOpts, sdkmetric.WithReader(r))
	}
	mp := sdkmetric.NewMeterProvider(mpOpts...)

	// Logger provider: composite via batch processor; extra exporters
	// land as additional batch processors.
	lpOpts := []sdklog.LoggerProviderOption{
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(compLog)),
	}
	for _, exp := range cfg.LogExporters {
		if exp != nil {
			lpOpts = append(lpOpts, sdklog.WithProcessor(sdklog.NewBatchProcessor(exp)))
		}
	}
	lp := sdklog.NewLoggerProvider(lpOpts...)

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	logglobal.SetLoggerProvider(lp)

	t := &Telemetry{
		TracerProvider: tp,
		MeterProvider:  mp,
		LoggerProvider: lp,
		InstanceID:     instanceID,
		Resource:       res,
		LocalSpan:      localSpan,
		LocalMetric:    localMetric,
		LocalLog:       localLog,
		logger:         logger,
	}
	// Shutdown order: providers first (they flush any buffered
	// signals through their batchers / readers, which in turn close
	// the composite exporters), then the local exporters drain their
	// own queues. Local exporter Shutdown is idempotent so the second
	// call is a no-op; we keep the explicit calls to make the drain
	// observably synchronous when the SDK's batcher hands a partial
	// flush back. OTLP exporters are shut down inside the composites'
	// Shutdown — no separate fn here.
	t.shutdownFns = []func(context.Context) error{
		tp.Shutdown,
		mp.Shutdown,
		lp.Shutdown,
		localSpan.Shutdown,
		localMetric.Shutdown,
		localLog.Shutdown,
	}

	// Slog bridge — when requested, replace the global file-only logger
	// with a fan-out that ALSO emits through the OTel logger pipeline.
	// Lines emitted before this call only land in the file; that
	// initialization-order constraint is documented on SlogBridge.
	if cfg.InstallSlogBridge {
		installSlogBridge(lp, logger)
	}
	_ = ctx // reserved for future wait-for-collector-init paths
	return t, nil
}

// Shutdown flushes and closes every provider/exporter. Errors are
// joined; the first non-nil err is returned but every step is run.
// Idempotent: only the first call runs the shutdown chain; subsequent
// calls return the cached result. The MeterProvider's Shutdown is not
// idempotent on its own (calls after the first return "reader is
// shutdown"), so we gate the whole sequence behind a sync.Once.
func (t *Telemetry) Shutdown(ctx context.Context) error {
	if t == nil {
		return nil
	}
	t.shutdownOnce.Do(func() {
		var firstErr error
		for _, fn := range t.shutdownFns {
			if err := fn(ctx); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		t.shutdownErr = firstErr
	})
	return t.shutdownErr
}

// Tracer returns the named OTel Tracer scoped under the harness's
// instrumentation library. Stable name so emitted spans carry a
// predictable scope tag in the local store + OTLP envelope.
func (t *Telemetry) Tracer(name string) interface{} {
	if t == nil || t.TracerProvider == nil {
		return nil
	}
	return t.TracerProvider.Tracer(name)
}

// Logger returns an OTel logger scoped under name. Returns nil if
// telemetry isn't wired (so callers can no-op-emit).
func (t *Telemetry) Logger(name string) otellog.Logger {
	if t == nil || t.LoggerProvider == nil {
		return nil
	}
	return t.LoggerProvider.Logger(name)
}

// installSlogBridge swaps the process-global slog handler with a
// fan-out that emits each record into BOTH the file handler and the
// OTel logger. Safe to call repeatedly: a second call replaces the
// previous bridge so reconfigured providers (WP05) take effect.
//
// The OTel logger is sourced from the LoggerProvider just constructed
// — the same provider whose pipeline includes the local SQLite
// exporter and the optional OTLP exporter. So each slog line written
// after this call lands in: ~/.kenaz/harness.log, telemetry_logs (via
// local exporter), and the configured OTLP endpoint (when set).
func installSlogBridge(lp *sdklog.LoggerProvider, fallback *slog.Logger) {
	if lp == nil {
		return
	}
	fileH := logging.FileHandler()
	if fileH == nil && fallback != nil {
		fileH = fallback.Handler()
	}
	otelLogger := lp.Logger("kaneaz-harness/slog-bridge")
	bridge := NewSlogBridge(fileH, otelLogger)
	logging.Replace(bridge)
}

// buildResource constructs the resource with the spec's required
// attributes (FR-002). semconv constants pin the canonical attribute
// keys; host.os is intentionally a literal string per spec wording
// even though semconv prefers os.type.
func buildResource(version, instanceID string) *resource.Resource {
	attrs := []attribute.KeyValue{
		semconv.ServiceName(ServiceName),
		semconv.ServiceVersion(version),
		semconv.ServiceInstanceID(instanceID),
		attribute.String("host.os", runtime.GOOS),
		attribute.Int("process.pid", os.Getpid()),
	}
	return resource.NewWithAttributes(semconv.SchemaURL, attrs...)
}
