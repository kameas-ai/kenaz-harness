// telemetry.go — OTel observability for hook dispatch.
//
// Every Runner.Fire / Runner.FireAsync call emits one span:
//
//	Name: "harness.hook.fire"
//	Attributes:
//	  - harness.hook.event       — event name (e.g. "pre_tool_use")
//	  - harness.hook.kind        — hook kind (builtin / shell / mcp)
//	  - harness.hook.outcome     — success | non_blocking_error | cancelled
//	  - harness.hook.decision    — "approve" | "block" | "" (when set)
//	  - harness.hook.async       — true | false
//	  - harness.hook.latency_ms  — wall time in ms
//
// Privacy invariant: raw stdin / stdout are NEVER included in default
// attributes. They appear only when the HARNESS_VERBOSE_ATTRS env var
// is set, and only in local-SQLite spans (never OTLP).
//
// Counter: "harness.hook.fires_total" with labels {event, decision}.
package hooks

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	// TracerName is the OTel tracer name for hook spans.
	TracerName = "harness/hooks"

	// SpanHookFire is the span name for hook dispatch.
	SpanHookFire = "harness.hook.fire"

	// Attribute keys — stable contract with dashboards and OTel processors.
	attrEvent     = "harness.hook.event"
	attrKind      = "harness.hook.kind"
	attrOutcome   = "harness.hook.outcome"
	attrDecision  = "harness.hook.decision"
	attrAsync     = "harness.hook.async"
	attrLatencyMs = "harness.hook.latency_ms"
	attrHookID    = "harness.hook.id"

	// verboseAttrsEnv is the opt-in env var that enables raw stdin/stdout
	// in span attributes. NEVER set in production; local debugging only.
	verboseAttrsEnv = "HARNESS_VERBOSE_ATTRS"
)

// Outcome vocabulary.
const (
	OutcomeSuccess          = "success"
	OutcomeNonBlockingError = "non_blocking_error"
	OutcomeCancelled        = "cancelled"
)

// hookSpan is a helper that starts a span for a single hook dispatch
// and records the outcome + latency on end.
type hookSpan struct {
	span  trace.Span
	start time.Time
}

// startHookSpan opens a span for the given hook fire. The caller must
// call end() when the hook completes.
func startHookSpan(ctx context.Context, event, kind, hookID string, async bool) (context.Context, *hookSpan) {
	tracer := otel.Tracer(TracerName)
	ctx, span := tracer.Start(ctx, SpanHookFire,
		trace.WithAttributes(
			attribute.String(attrEvent, event),
			attribute.String(attrKind, kind),
			attribute.String(attrHookID, hookID),
			attribute.Bool(attrAsync, async),
		),
	)
	return ctx, &hookSpan{span: span, start: time.Now()}
}

// end records the outcome, decision, and latency then closes the span.
func (s *hookSpan) end(outcome, decision string) {
	if s == nil || s.span == nil {
		return
	}
	latencyMs := time.Since(s.start).Milliseconds()
	s.span.SetAttributes(
		attribute.String(attrOutcome, outcome),
		attribute.String(attrDecision, decision),
		attribute.Int64(attrLatencyMs, latencyMs),
	)
	s.span.End()
}

// verboseAttrsEnabled returns true when the caller explicitly opts in to
// raw stdin/stdout attributes via the env var. This function is the
// single enforcement point for the privacy invariant.
func verboseAttrsEnabled() bool {
	return os.Getenv(verboseAttrsEnv) != ""
}

// addVerboseAttrs adds raw stdin/stdout to a span ONLY when
// verboseAttrsEnabled() is true. Call sites pass the actual bytes; this
// function is the gate.
//
// This is a no-op in production (env var not set). The CI invariant test
// asserts this function is never called with a non-empty payload when
// the env var is unset.
func addVerboseAttrs(span trace.Span, stdinBody, stdoutBody []byte) {
	if !verboseAttrsEnabled() {
		return
	}
	if len(stdinBody) > 0 {
		span.SetAttributes(attribute.String("harness.hook.debug.stdin", string(stdinBody)))
	}
	if len(stdoutBody) > 0 {
		span.SetAttributes(attribute.String("harness.hook.debug.stdout", string(stdoutBody)))
	}
}
