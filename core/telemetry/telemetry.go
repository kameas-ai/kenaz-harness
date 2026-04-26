package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"runtime"
	"sync"

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
	// addition to the local ones constructed here. WP02's OTLP
	// exporters land via this seam.
	SpanExporters   []sdktrace.SpanExporter
	MetricExporters []sdkmetric.Exporter
	LogExporters    []sdklog.Exporter
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
// Init does NOT install the slog-bridge or any OTLP exporters — those
// are WP02's scope. The caller passes additional exporters via
// cfg.SpanExporters / cfg.MetricExporters / cfg.LogExporters.
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

	// Tracer provider: local exporter via batcher (async), plus any
	// extra exporters the caller passed in (WP02 OTLP).
	tpOpts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(localSpan),
	}
	for _, exp := range cfg.SpanExporters {
		if exp != nil {
			tpOpts = append(tpOpts, sdktrace.WithBatcher(exp))
		}
	}
	tp := sdktrace.NewTracerProvider(tpOpts...)

	// Meter provider: periodic reader pushing into our local
	// exporter; extra exporters get their own readers.
	readers := []sdkmetric.Reader{
		sdkmetric.NewPeriodicReader(localMetric),
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

	// Logger provider: local exporter via batch processor; extra
	// exporters land as additional batch processors.
	lpOpts := []sdklog.LoggerProviderOption{
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(localLog)),
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
	// signals through their batchers / readers), then the local
	// exporters drain their own queues. Reverse-construction.
	t.shutdownFns = []func(context.Context) error{
		tp.Shutdown,
		mp.Shutdown,
		lp.Shutdown,
		localSpan.Shutdown,
		localMetric.Shutdown,
		localLog.Shutdown,
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
