package harness

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/event/kind"
)

// AuditSink is the audit emission surface used by the harness-self
// server. Mirrors the session.Audit shape so wiring can pass the same
// adapter for both surfaces.
type AuditSink interface {
	Emit(ctx context.Context, k string, payload map[string]any)
}

// noopAudit drops every emission. The default when the server is built
// without an explicit AuditSink.
type noopAudit struct{}

func (noopAudit) Emit(_ context.Context, _ string, _ map[string]any) {}

// WithAudit returns a new server whose tool handlers emit
// KindHarnessSelfToolCalled on every dispatch. Args are redacted per
// the ToolSpec.Redact list before emission so secret material (api_key)
// never reaches the audit log.
//
// Implementation note: rather than rewriting Server itself, this helper
// returns a wrapper Server that re-registers each tool with an
// audit-instrumented handler. Callers wire WithAudit(srv, sink) AFTER
// RegisterAll has installed the canonical tool surface.
func WithAudit(srv *Server, sink AuditSink) *Server {
	if srv == nil {
		return nil
	}
	if sink == nil {
		sink = noopAudit{}
	}
	for _, spec := range srv.Tools() {
		spec := spec
		original := spec.Handler
		spec.Handler = func(ctx context.Context, args json.RawMessage) (any, error) {
			start := time.Now()
			result, err := original(ctx, args)
			payload := map[string]any{
				"tool_name":   spec.Name,
				"success":     err == nil,
				"duration_ms": time.Since(start).Milliseconds(),
			}
			// Argument keys NOT in the Redact list go in for debuggability.
			// Explicitly filter Redact even though we don't echo full args
			// today — keeps the door open for richer audit payloads later.
			if len(spec.Redact) > 0 {
				payload["redacted_keys"] = spec.Redact
			}
			sink.Emit(ctx, string(kind.KindHarnessSelfToolCalled), payload)
			return result, err
		}
		srv.Register(spec)
	}
	return srv
}
