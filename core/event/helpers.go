package event

import (
	"context"
	"time"
)

// CancellationInfo carries structured fields for a Cancellation event.
type CancellationInfo struct {
	Reason    string         `json:"reason"`
	RequestID string         `json:"request_id,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

// ErrorInfo carries structured fields for an Error event.
type ErrorInfo struct {
	Code    string         `json:"code,omitempty"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// TimeoutInfo carries structured fields for a Timeout event.
type TimeoutInfo struct {
	Operation string        `json:"operation"`
	After     time.Duration `json:"after_ns"`
	Details   map[string]any `json:"details,omitempty"`
}

// uniformPayload is the shape every cancel/error/timeout event uses
// (FR-018). Consumers get a consistent shape across emitters.
type uniformPayload struct {
	Class string `json:"class"` // "cancellation" | "error" | "timeout"
	Info  any    `json:"info"`
}

// Cancellation produces an AppendInput with the canonical cancellation
// shape (FR-018). The session id is taken from the context if present
// via WithSession; the caller passes the result to Emitter.Append.
func Cancellation(ctx context.Context, emitter EmitterID, info CancellationInfo) AppendInput {
	return AppendInput{
		SessionID: sessionFromCtx(ctx),
		EmitterID: emitter,
		Kind:      Kind("event-log.cancellation"),
		Payload:   uniformPayload{Class: "cancellation", Info: info},
	}
}

// Error produces an AppendInput with the canonical error shape (FR-018).
func Error(ctx context.Context, emitter EmitterID, info ErrorInfo) AppendInput {
	return AppendInput{
		SessionID: sessionFromCtx(ctx),
		EmitterID: emitter,
		Kind:      Kind("event-log.error"),
		Payload:   uniformPayload{Class: "error", Info: info},
	}
}

// Timeout produces an AppendInput with the canonical timeout shape
// (FR-018).
func Timeout(ctx context.Context, emitter EmitterID, info TimeoutInfo) AppendInput {
	return AppendInput{
		SessionID: sessionFromCtx(ctx),
		EmitterID: emitter,
		Kind:      Kind("event-log.timeout"),
		Payload:   uniformPayload{Class: "timeout", Info: info},
	}
}

type sessionCtxKey struct{}

// WithSession attaches a session id to ctx so helper constructors can
// pick it up without forcing every caller to pass it explicitly.
func WithSession(ctx context.Context, sid ULID) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, sid)
}

// SessionFromContext returns the session id attached by WithSession,
// or zero if none. Exported for the RPC layer; the internal package
// uses sessionFromCtx for the *ULID return shape AppendInput needs.
func SessionFromContext(ctx context.Context) (ULID, bool) {
	v, ok := ctx.Value(sessionCtxKey{}).(ULID)
	return v, ok && !v.IsZero()
}

func sessionFromCtx(ctx context.Context) *ULID {
	if sid, ok := SessionFromContext(ctx); ok {
		return &sid
	}
	return nil
}

// The raw-replay authorization pair (AuthorizeRawReplay /
// IsRawReplayAuthorized, plus its context key) was DELETED here on
// 2026-08-13 by agentgraph-total-convergence-01PMGX01 WP17.
//
// Its doc claimed "the Replayer rejects ReplayRaw cursors opened
// without this marker (ErrPolicyViolation)". No Replayer rejects
// anything, because no Replayer exists: the interface is declared in
// api.go and has zero implementations repo-wide, ReplayCursor and
// ReplayOpts have zero references outside their own declarations, and
// core/event/replay contains a doc.go and nothing else. Both helpers
// had exactly one caller between them — api_test.go, asserting that the
// setter round-trips to the getter.
//
// This is the shape spec §1.7 names: a control-flow function whose doc
// comment describes an enforcement contract nothing enforces. Whoever
// implements Replayer re-adds the marker WITH the check that reads it,
// in the same change — three lines of context plumbing are not the hard
// part, and shipping them early is how they come to look load-bearing.
