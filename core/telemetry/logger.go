package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/log"
)

// SlogBridge is the slog.Handler that fans every record into both the
// pre-existing file handler (so ~/.kenaz/harness.log keeps receiving
// lines unchanged) and the OTel logger pipeline (so the same line
// reaches the SQLite store + the OTLP fleet endpoint when configured).
//
// Initialization-order contract: any slog line emitted BEFORE
// telemetry.Init runs only hits the file handler — the bridge isn't
// constructed yet. Once telemetry init replaces the global logger's
// handler with this bridge, every subsequent line lands in both sinks.
//
// Span-context propagation: when ctx carries a SpanContext (i.e. the
// caller is inside a tracer.Start span), the SDK's logger.Emit
// auto-extracts trace_id + span_id from ctx into the OTel record. The
// bridge does not need to copy those fields by hand.
type SlogBridge struct {
	file slog.Handler
	otel log.Logger
}

// NewSlogBridge returns a handler that fans records into file (the
// existing file-backed slog.Handler) and otel (a Logger from the
// global LoggerProvider, normally telemetry.LoggerProvider.Logger).
// Either side may be nil — the bridge skips a nil sink rather than
// panicking, so the caller's wiring code can stay declarative.
func NewSlogBridge(file slog.Handler, otel log.Logger) *SlogBridge {
	return &SlogBridge{file: file, otel: otel}
}

// Enabled defers to the file handler. The OTel side is generally
// always-enabled (the SDK's processor decides what to drop), so the
// file handler — which honours user-set log levels — is the
// authoritative gate.
func (s *SlogBridge) Enabled(ctx context.Context, level slog.Level) bool {
	if s == nil {
		return false
	}
	if s.file != nil {
		return s.file.Enabled(ctx, level)
	}
	return true
}

// Handle fans the record into both sinks. Failures on either side are
// best-effort: we recover panics so a misbehaving sink can't crash
// the producer (chat surface). slog returns errors to the caller via
// Handle, but slog.Logger ignores them at every callsite — the file
// fallback is the only thing that observes a Handle error today, and
// we already preserve that behaviour by forwarding to file last.
func (s *SlogBridge) Handle(ctx context.Context, rec slog.Record) error {
	if s == nil {
		return nil
	}
	// OTel first, then file. Order is intentional: if file write
	// blocks (disk full / EAGAIN), the OTel emit has already happened
	// so the fleet view is still fresh.
	if s.otel != nil {
		s.emitOTel(ctx, rec)
	}
	if s.file != nil {
		return s.file.Handle(ctx, rec)
	}
	return nil
}

// WithAttrs / WithGroup mutate only the file handler's attribute
// state — OTel records carry attributes inline per Emit, so there is
// no scoped-attrs concept on that side that we need to mirror.
func (s *SlogBridge) WithAttrs(attrs []slog.Attr) slog.Handler {
	if s == nil {
		return nil
	}
	cp := *s
	if s.file != nil {
		cp.file = s.file.WithAttrs(attrs)
	}
	return &cp
}

func (s *SlogBridge) WithGroup(name string) slog.Handler {
	if s == nil {
		return nil
	}
	cp := *s
	if s.file != nil {
		cp.file = s.file.WithGroup(name)
	}
	return &cp
}

func (s *SlogBridge) emitOTel(ctx context.Context, rec slog.Record) {
	defer func() {
		// A panic inside the OTel SDK must not crash the producer
		// goroutine. Telemetry must degrade silently (NFR-003).
		_ = recover()
	}()
	var otelRec log.Record
	if !rec.Time.IsZero() {
		otelRec.SetTimestamp(rec.Time)
	}
	otelRec.SetSeverity(slogLevelToOTel(rec.Level))
	otelRec.SetSeverityText(rec.Level.String())
	otelRec.SetBody(log.StringValue(rec.Message))
	rec.Attrs(func(a slog.Attr) bool {
		otelRec.AddAttributes(slogAttrToOTel(a))
		return true
	})
	s.otel.Emit(ctx, otelRec)
}

// slogLevelToOTel maps slog levels to the OTel severity numbers from
// the spec (Debug→5, Info→9, Warn→13, Error→17). slog levels in
// between (e.g. Info+2) round down to the nearest base level.
func slogLevelToOTel(l slog.Level) log.Severity {
	switch {
	case l >= slog.LevelError:
		return log.SeverityError
	case l >= slog.LevelWarn:
		return log.SeverityWarn
	case l >= slog.LevelInfo:
		return log.SeverityInfo
	case l >= slog.LevelDebug:
		return log.SeverityDebug
	default:
		return log.SeverityTrace
	}
}

func slogAttrToOTel(a slog.Attr) log.KeyValue {
	return log.KeyValue{Key: a.Key, Value: slogValueToOTel(a.Value)}
}

func slogValueToOTel(v slog.Value) log.Value {
	switch v.Kind() {
	case slog.KindBool:
		return log.BoolValue(v.Bool())
	case slog.KindInt64:
		return log.Int64Value(v.Int64())
	case slog.KindUint64:
		return log.Int64Value(int64(v.Uint64()))
	case slog.KindFloat64:
		return log.Float64Value(v.Float64())
	case slog.KindString:
		return log.StringValue(v.String())
	case slog.KindDuration:
		return log.Int64Value(int64(v.Duration()))
	case slog.KindTime:
		return log.Int64Value(v.Time().UnixNano())
	case slog.KindGroup:
		group := v.Group()
		kvs := make([]log.KeyValue, 0, len(group))
		for _, ga := range group {
			kvs = append(kvs, slogAttrToOTel(ga))
		}
		return log.MapValue(kvs...)
	case slog.KindLogValuer:
		return slogValueToOTel(v.LogValuer().LogValue())
	default:
		return log.StringValue(fmt.Sprint(v.Any()))
	}
}
