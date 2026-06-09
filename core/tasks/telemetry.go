package tasks

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("github.com/kameas-ai/kenaz-harness/core/tasks")

// spanCreate records a harness.task.create span. The span covers the
// synchronous registration path only — it does NOT include the async
// process lifetime. Command bodies and session IDs are NOT included as
// span attributes (privacy invariant: no user-authored text in default attrs).
func spanCreate(ctx context.Context, taskID, kind string) (context.Context, oteltrace.Span) {
	ctx, span := tracer.Start(ctx, "harness.task.create")
	span.SetAttributes(
		attribute.String("task.id", taskID),
		attribute.String("task.kind", kind),
		// task.cmd and task.owner_session_id are intentionally omitted:
		// cmd may contain user-authored content; session ID is PII-adjacent.
	)
	return ctx, span
}

// spanComplete records a harness.task.complete span.
// Called from the monitor goroutine after the process exits.
func spanComplete(taskID, kind string, exitCode int, durationMs int64) {
	_, span := tracer.Start(context.Background(), "harness.task.complete")
	defer span.End()
	span.SetAttributes(
		attribute.String("task.id", taskID),
		attribute.String("task.kind", kind),
		attribute.Int("task.exit_code", exitCode),
		attribute.Int64("task.duration_ms", durationMs),
	)
}
