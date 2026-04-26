// Confirm-each modal flow for the toolloop (WP05).
//
// When the permission resolver returns PolicyConfirmEach the loop pauses
// before dispatch and asks a ConfirmGateway whether the user wants to
// allow this call. Decisions:
//
//	allow         — dispatch this single call
//	deny          — synthetic tool_blocked, conversation continues
//	always_allow  — dispatch this call AND write a session-scoped
//	                policy=auto_allow override for (server, tool) so
//	                subsequent turns skip the modal
//	always_deny   — synthetic tool_blocked AND write a session-scoped
//	                policy=deny override for (server, tool)
//
// The gateway is wired by the rpc layer to the chassis stream broker:
// it surfaces ConfirmRequest events on the existing llm:stream-chunk
// topic and waits for a Tools_RespondToConfirmation binding to flip a
// pending-request map entry. WP05 deliberately reuses that broker so
// the chat surface picks up confirmations through the same pump it
// already drives (no new fan-out plumbing).
//
// Feature flag: when ConfirmEachEnabled is false the loop treats
// PolicyConfirmEach exactly like PolicyAutoAllow — gateway is never
// invoked, no override writes happen, and the WP04 surface is exactly
// what existed before WP05. The default is ON (the rpc layer's wiring
// reads Settings.ConfirmEachEnabled).
package toolloop

import (
	"context"
	"encoding/json"
)

// ConfirmDecision is the user's choice on a confirm-each modal.
type ConfirmDecision string

const (
	// ConfirmAllow dispatches this call only — the next call to the
	// same (server, tool) re-prompts the user.
	ConfirmAllow ConfirmDecision = "allow"
	// ConfirmDeny short-circuits this call only — same per-call scope
	// as Allow, just inverted.
	ConfirmDeny ConfirmDecision = "deny"
	// ConfirmAlwaysAllow dispatches this call and writes a session
	// override (policy=auto_allow) for (server, tool) so subsequent
	// turns in the same session skip the modal.
	ConfirmAlwaysAllow ConfirmDecision = "always_allow"
	// ConfirmAlwaysDeny short-circuits this call and writes a session
	// override (policy=deny) for (server, tool) so subsequent turns
	// in the same session block automatically.
	ConfirmAlwaysDeny ConfirmDecision = "always_deny"
)

// ConfirmRequest is the payload the loop hands the gateway when it
// pauses on a confirm_each verdict. RequestID is unique per-call so the
// gateway / frontend can correlate the asynchronous response back to
// the right blocked goroutine.
//
// ArgsRedacted is the serialized args the user sees in the modal; the
// rpc layer is responsible for running the redaction pipeline before
// surfacing this payload to the renderer (NFR-003). The default
// gateway implementation copies Args verbatim — production wiring
// substitutes a redacted version.
type ConfirmRequest struct {
	RequestID    string          `json:"request_id"`
	SessionID    string          `json:"session_id"`
	ParentSubID  string          `json:"parent_sub_id"`
	Server       string          `json:"server"`
	Tool         string          `json:"tool"`
	ToolUseID    string          `json:"tool_use_id"`
	Args         json.RawMessage `json:"args,omitempty"`
	ArgsRedacted string          `json:"args_redacted,omitempty"`
	Reason       string          `json:"reason,omitempty"`
}

// ConfirmGateway is the surface the loop calls to obtain a user
// decision. Implementations MUST be safe for concurrent use — multiple
// confirm_each calls in a single turn dispatch concurrently and each
// blocks on its own RequestConfirm.
//
// RequestConfirm honors ctx cancellation: when the host context goes
// down (user clicks Stop), the gateway should return ctx.Err() so the
// loop can synthesize a tool_cancelled record and unwind cleanly.
type ConfirmGateway interface {
	RequestConfirm(ctx context.Context, req ConfirmRequest) (ConfirmDecision, error)
}

// noopConfirmGateway is the loop's default when Config.Confirm is nil
// or Config.ConfirmEachEnabled is false. Every call resolves to allow
// without surfacing a request — equivalent to treating confirm_each as
// auto_allow.
type noopConfirmGateway struct{}

func (noopConfirmGateway) RequestConfirm(_ context.Context, _ ConfirmRequest) (ConfirmDecision, error) {
	return ConfirmAllow, nil
}

// SessionOverrideWriter persists per-session (server, tool, policy)
// overrides so the resolver picks them up on the next call. WP05 uses
// this to satisfy the always_allow / always_deny decisions: a single
// click escalates to "do not ask again this session" by writing the
// matching override.
//
// The session manager (and the C2 session-overrides surface) is
// expected to grow a matching writer; until then the rpc layer wires
// NoopSessionOverrideWriter so the always_* decisions degrade to a
// per-call allow / deny without persistence. Implementations MUST be
// safe for concurrent use.
type SessionOverrideWriter interface {
	SetMCPOverride(ctx context.Context, sessionID string, override MCPOverride) error
}

// NoopSessionOverrideWriter is the default when Config.OverrideWriter
// is nil. Every Set is dropped on the floor; the loop emits a warn log
// for visibility but the call still proceeds normally.
type NoopSessionOverrideWriter struct{}

func (NoopSessionOverrideWriter) SetMCPOverride(_ context.Context, _ string, _ MCPOverride) error {
	return nil
}
