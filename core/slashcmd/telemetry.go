// telemetry.go — OTel span helpers for user-defined slash commands.
//
// TraceRun wraps Dispatch.Run with an OTel span named
// "harness.slashcmd.run". The span carries only classification
// attributes — no rendered argument values, no prompt body bytes —
// to satisfy the privacy CI invariant (NFR-007).
//
// The tracer is obtained from the process-global OTel TracerProvider
// (set by the core/telemetry init path). If no provider is registered,
// otel.GetTracerProvider() returns the no-op provider; the code path is
// unchanged.
//
// user-slash-commands-01KQ8TD9 WP08.
package slashcmd

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

const slashcmdTracerName = "harness.slashcmd"

// TraceRun starts an OTel span for a slash command run and delegates
// to d.Run. Callers who want tracing should call TraceRun instead of
// Run directly; the span is ended when TraceRun returns.
//
// Span attributes:
//   - slashcmd.name         string  — command name
//   - slashcmd.kind         string  — "text" | "tool" | "prompt"
//   - slashcmd.scope        string  — "global" | "project"
//   - slashcmd.model_invokable bool
//   - slashcmd.arg_count    int     — number of user-supplied args
//   - slashcmd.tool         string  — tool name for kind=tool (empty otherwise)
//
// Excluded from attributes (privacy invariant):
//   - arg values
//   - rendered template body
//   - tool output
//   - session names / content
func (d *Dispatch) TraceRun(
	ctx context.Context,
	name string,
	args map[string]string,
	sc SessionContext,
) (RunResult, error) {
	tracer := otel.GetTracerProvider().Tracer(slashcmdTracerName)
	ctx, span := tracer.Start(ctx, "harness.slashcmd.run")
	defer span.End()

	// Resolve cmd metadata for span attrs (lightweight — loads from DB).
	cmd, loadErr := d.store.LoadUserOne(ctx, name, sc.ProjectID)
	if loadErr != nil {
		span.RecordError(loadErr)
		span.SetStatus(codes.Error, "command_not_found")
		// Delegate to Run so it returns the canonical error shape.
		return d.Run(ctx, name, args, sc)
	}

	span.SetAttributes(
		attribute.String("slashcmd.name", cmd.Name),
		attribute.String("slashcmd.kind", string(cmd.Kind)),
		attribute.String("slashcmd.scope", string(cmd.Scope)),
		attribute.Bool("slashcmd.model_invokable", cmd.ModelInvokable),
		attribute.Int("slashcmd.arg_count", len(args)),
		attribute.String("slashcmd.tool", cmd.Tool), // empty for non-tool kinds
	)

	res, err := d.Run(ctx, name, args, sc)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, classifyRunError(err))
	} else {
		span.SetStatus(codes.Ok, "")
	}
	return res, err
}
