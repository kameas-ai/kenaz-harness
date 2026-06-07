// Package toolloop — WP06: generic-tool gate extension.
//
// This file implements the Cedar-backed permission gate for MCP tools
// that are tagged with prompt_on_first_use in their Recipe definition.
// It inserts a single evaluation checkpoint between "the toolloop
// decides to dispatch" and "the MCPPool.Call happens".
//
// Gate flow (per spec cedar-credential-policy-01KQ8TDE §WP06):
//
//  1. caller invokes EvaluateToolGate(ctx, GateInput{...}).
//  2. Engine is nil → gate is a no-op (return nil).
//  3. Engine is non-nil → cedar.EvaluateUseTool is called with
//     the tool/server names and the prompt_on_first_use flag.
//  4. Allow   → dispatch proceeds.
//     Deny    → return *ToolPermissionDeniedError; dispatch blocked.
//     NotApplicable + promptOnFirstUse==false → dispatch proceeds
//       (backwards-compat: tools that did not opt into the gate are
//        treated as allowed when the engine returns NotApplicable).
//     NotApplicable + promptOnFirstUse==true  → call
//       registry.RequestInteractive(PromptSurface{...}).
//       • GrantAllowOnce / GrantAllowAlways → record transient grant;
//         dispatch proceeds.
//       • GrantDeny / error → return *ToolPermissionDeniedError.
//
// Redaction contract (spec NFR):
//   PromptSurface.ArgsSummary MUST NOT contain raw argument values,
//   secret bytes, or env-derived data. Callers pass a human-readable
//   structural description such as "calls write_file with 1 argument".
//   The gate does not enforce this — it is a caller convention.
//
// Nil-tolerance:
//   Engine==nil → gate is a no-op (return nil). Registry==nil with a
//   non-nil Engine and NotApplicable+PromptOnFirstUse → safe-fail Deny
//   (no prompt channel to ask the user).
package toolloop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// GrantKind enumerates the outcomes a PromptRegistry can return after
// presenting an interactive tool-permission prompt to the user.
type GrantKind int

const (
	// GrantDeny means the user refused permission. The dispatch is
	// blocked and the caller receives a ToolPermissionDeniedError.
	GrantDeny GrantKind = iota

	// GrantAllowOnce grants permission for the current invocation only.
	// Subsequent calls to the same tool will go through the engine again
	// (which will return NotApplicable again and re-prompt — unless the
	// transient grant cache covers it).
	GrantAllowOnce

	// GrantAllowAlways grants permanent permission. The production
	// wiring layer is expected to write a tool_allow_<name>.cedar
	// snippet and reload the engine so subsequent calls see Allow from
	// Cedar directly. The gate itself records a transient grant so the
	// in-flight session doesn't need to re-prompt.
	GrantAllowAlways
)

// PromptSurface describes the tool invocation that triggered the
// interactive prompt. It is passed to PromptRegistry.RequestInteractive
// so the prompt surface can render a meaningful description.
//
// Args redaction rule: ArgsSummary MUST be a structural description of
// the call shape (e.g. "calls write_file with 1 path argument") and
// MUST NOT include raw argument values, secret bytes, or env-derived
// data. The gate does not validate this — it is a caller convention
// enforced at code review time.
type PromptSurface struct {
	// ServerName is the MCP server name (bare, without the "__" suffix).
	ServerName string
	// ToolName is the bare tool name (without the server prefix).
	ToolName string
	// ArgsSummary is a redaction-safe structural description of the call.
	// Example: "calls write_file with 1 path argument".
	ArgsSummary string
}

// PromptRegistry is the narrow surface the gate hook calls when it
// needs to present an interactive permission prompt. Production wiring
// provides a real UI channel; tests provide a stub.
//
// RequestInteractive MUST be safe for concurrent use — the toolloop
// can fan out dispatches behind a semaphore (WP04).
//
// The context passed in carries the session ID (via WithSessionID) and
// any other dispatch-time context. Implementations may block until the
// user responds; callers should pass a cancellable context so a
// session teardown unblocks in-flight prompts.
type PromptRegistry interface {
	// RequestInteractive presents the permission prompt for the given
	// tool invocation and returns the user's decision. An error return
	// (including context cancellation) is treated as GrantDeny.
	RequestInteractive(ctx context.Context, surface PromptSurface) (GrantKind, error)
}

// TransientGrantCache records per-session tool grants that the prompt
// flow has already resolved. The cache prevents repeat prompts within
// the same session for tools that received GrantAllowOnce.
//
// Implementations must be safe for concurrent use. The cache is
// consulted BEFORE the Cedar engine on each dispatch; it is written
// after a successful prompt resolution.
type TransientGrantCache interface {
	// Has returns true when the (sessionID, toolName) pair has a
	// cached transient grant.
	Has(sessionID, toolName string) bool
	// Set records a transient grant for (sessionID, toolName).
	Set(sessionID, toolName string)
}

// ToolPermissionDeniedError is the typed error returned when the gate
// blocks a tool call. Callers can use errors.As to extract the surface
// details for logging or UI rendering.
type ToolPermissionDeniedError struct {
	// ServerName is the MCP server the tool belongs to.
	ServerName string
	// ToolName is the tool that was denied.
	ToolName string
	// Reason is a human-readable explanation (e.g. "user denied",
	// "policy forbid rule matched", "no prompt channel").
	Reason string
	// Wrapped is the underlying error from the prompt or Cedar when
	// applicable; nil otherwise.
	Wrapped error
}

// Error implements error.
func (e *ToolPermissionDeniedError) Error() string {
	if e == nil {
		return "tool permission denied"
	}
	if e.Reason != "" {
		return fmt.Sprintf("tool permission denied for %s__%s: %s", e.ServerName, e.ToolName, e.Reason)
	}
	return fmt.Sprintf("tool permission denied for %s__%s", e.ServerName, e.ToolName)
}

// Unwrap returns the underlying error for errors.Is/As chaining.
func (e *ToolPermissionDeniedError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Wrapped
}

// IsToolPermissionDenied reports whether err is or wraps a
// *ToolPermissionDeniedError.
func IsToolPermissionDenied(err error) bool {
	var te *ToolPermissionDeniedError
	return errors.As(err, &te)
}

// GateInput is the full set of inputs EvaluateToolGate needs.
// Grouping them in a struct keeps the call-site ergonomic as future
// WPs add attributes without changing every caller.
type GateInput struct {
	// Engine is the Cedar evaluation gate. nil → gate is a no-op.
	Engine cedar.Gate
	// Registry is the interactive prompt channel. nil with a non-nil
	// Engine and NotApplicable outcome → safe-fail Deny.
	Registry PromptRegistry
	// Cache is the transient grant cache. nil → treated as empty
	// (every call goes through the engine).
	Cache TransientGrantCache
	// SessionID is the current session identifier, used as the cache
	// key. May be empty (""); the cache key then uses only the toolName.
	SessionID string
	// ServerName is the bare MCP server name (e.g. "filesystem").
	ServerName string
	// ToolName is the bare tool name (e.g. "write_file").
	ToolName string
	// PromptOnFirstUse is true when the Recipe.PromptOnFirstUse slice
	// contains ToolName, meaning the recipe author opted this tool into
	// the interactive prompt gate.
	PromptOnFirstUse bool
	// ArgsSummary is the redaction-safe structural description of the
	// call passed to PromptSurface when the prompt fires.
	// MUST NOT contain raw argument values or secrets.
	ArgsSummary string
}

// EvaluateToolGate is the single gate-hook helper for MCP tool dispatch
// (WP06). It runs the Cedar engine against the Tool resource family and
// routes through the interactive prompt when appropriate.
//
// Returns nil when the tool call is permitted to proceed, or a
// *ToolPermissionDeniedError when it should be blocked.
//
// Nil-tolerance: Engine==nil is a no-op (return nil). Registry==nil
// with Engine!=nil and NotApplicable+PromptOnFirstUse is a safe-fail
// denial (no channel to ask the user).
func EvaluateToolGate(ctx context.Context, in GateInput) error {
	// Fast path: no engine wired → gate is a no-op.
	if in.Engine == nil {
		return nil
	}

	// Transient cache hit: the user already granted this tool in the
	// current session; skip the engine and dispatch immediately.
	if in.Cache != nil && in.Cache.Has(in.SessionID, in.ToolName) {
		return nil
	}

	decision := cedar.EvaluateUseTool(ctx, in.Engine, in.ServerName, in.ToolName, in.PromptOnFirstUse)

	switch decision.Outcome {
	case cedar.Allow:
		// Explicitly permitted by a Cedar policy — proceed.
		return nil

	case cedar.Deny:
		// A forbid policy matched — block with structured error.
		return &ToolPermissionDeniedError{
			ServerName: in.ServerName,
			ToolName:   in.ToolName,
			Reason:     decision.Reason,
			Wrapped:    &cedar.PolicyDeniedError{Decision: decision},
		}

	case cedar.NotApplicable:
		// No policy matched.
		if !in.PromptOnFirstUse {
			// Tool did not opt into the gate — backwards-compat default:
			// treat as allow (matches the prior behaviour where unknown
			// tools were dispatched without a Cedar check).
			return nil
		}

		// Tool opted into prompt-on-first-use. Ask the user.
		if in.Registry == nil {
			// No prompt channel available — safe-fail denial.
			return &ToolPermissionDeniedError{
				ServerName: in.ServerName,
				ToolName:   in.ToolName,
				Reason:     "no prompt channel available (registry is nil)",
			}
		}

		surface := PromptSurface{
			ServerName:  in.ServerName,
			ToolName:    in.ToolName,
			ArgsSummary: in.ArgsSummary,
		}

		grant, err := in.Registry.RequestInteractive(ctx, surface)
		if err != nil {
			return &ToolPermissionDeniedError{
				ServerName: in.ServerName,
				ToolName:   in.ToolName,
				Reason:     fmt.Sprintf("prompt error: %v", err),
				Wrapped:    err,
			}
		}

		switch grant {
		case GrantAllowOnce, GrantAllowAlways:
			// Record a transient grant so subsequent calls in this
			// session skip the engine (for GrantAllowOnce) or until
			// the persistent Cedar snippet is written and the engine
			// is reloaded (for GrantAllowAlways).
			if in.Cache != nil {
				in.Cache.Set(in.SessionID, in.ToolName)
			}
			return nil

		case GrantDeny:
			return &ToolPermissionDeniedError{
				ServerName: in.ServerName,
				ToolName:   in.ToolName,
				Reason:     "user denied",
			}

		default:
			return &ToolPermissionDeniedError{
				ServerName: in.ServerName,
				ToolName:   in.ToolName,
				Reason:     fmt.Sprintf("unknown grant kind %d", grant),
			}
		}

	default:
		// Unknown outcome — treat as deny for safety.
		return &ToolPermissionDeniedError{
			ServerName: in.ServerName,
			ToolName:   in.ToolName,
			Reason:     fmt.Sprintf("unknown cedar outcome %v", decision.Outcome),
		}
	}
}

// MemTransientGrantCache is a simple in-memory implementation of
// TransientGrantCache. It is provided here so callers in tests and in
// the production wiring layer don't have to write their own.
//
// The map key is "<sessionID>\x00<toolName>" (NUL separator; tool
// names and session IDs never contain NUL per the validateFamilyID
// invariant). MemTransientGrantCache is safe for concurrent use.
type MemTransientGrantCache struct {
	mu    sync.RWMutex
	store map[string]struct{}
}

// NewMemTransientGrantCache returns an empty cache.
func NewMemTransientGrantCache() *MemTransientGrantCache {
	return &MemTransientGrantCache{store: make(map[string]struct{})}
}

// Has reports whether (sessionID, toolName) has a cached transient grant.
func (c *MemTransientGrantCache) Has(sessionID, toolName string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.store[sessionID+"\x00"+toolName]
	return ok
}

// Set records a transient grant for (sessionID, toolName).
func (c *MemTransientGrantCache) Set(sessionID, toolName string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[sessionID+"\x00"+toolName] = struct{}{}
}
