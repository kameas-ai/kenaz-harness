package cedar

import (
	"context"

	cedar "github.com/cedar-policy/cedar-go"
)

// Gate is the small adapter surface gate-hook callers depend on. It
// is the engine.Evaluate-shaped contract callers in core/llm,
// core/memory, core/tools/* import — taking a Gate (interface) rather
// than a *Engine (struct) keeps those packages decoupled from the
// concrete Cedar implementation and lets tests pass a stub.
type Gate interface {
	// Evaluate runs the active policy and returns a Decision. See
	// Engine.Evaluate for semantics.
	Evaluate(
		ctx context.Context,
		principal cedar.EntityUID,
		action string,
		resource cedar.EntityUID,
		contextAttrs map[cedar.String]cedar.Value,
	) Decision
}

// Compile-time witness: *Engine satisfies Gate.
var _ Gate = (*Engine)(nil)

// AllowAll is the no-op gate test code and pre-mission boot stages
// install when no Engine has been constructed yet. Every Evaluate
// call returns NotApplicable / Allow so existing call sites keep
// working unchanged.
type AllowAll struct{}

// Evaluate implements Gate. Always returns a NotApplicable decision —
// callers that pattern-match on Outcome treat this as allow.
func (AllowAll) Evaluate(
	_ context.Context,
	principal cedar.EntityUID,
	action string,
	resource cedar.EntityUID,
	_ map[cedar.String]cedar.Value,
) Decision {
	if principal.IsZero() {
		principal = UserUID()
	}
	return Decision{
		Outcome:   NotApplicable,
		Action:    action,
		Principal: principal.String(),
		Resource:  resource.String(),
		Reason:    "no engine wired (AllowAll fallback)",
	}
}

// CheckTool is the gate-hook helper for tool dispatch. Wrap the tool
// dispatch call site with this; on Deny, return the PolicyDeniedError
// to the caller so the frontend can surface the denial.
//
// server / tool follow the kaneaz-harness "<server>__<tool>" naming;
// pass server="" for first-party tools.
func CheckTool(ctx context.Context, g Gate, server, tool string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionToolExec,
		ToolUID(server, tool),
		nil,
	)
	return enforce(d)
}

// CheckModel is the gate-hook helper for LLM model selection. Wrap
// the call boundary in core/llm with this. Deny short-circuits the
// stream-construction path.
func CheckModel(ctx context.Context, g Gate, provider, modelID string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionModelSelect,
		ModelUID(provider, modelID),
		nil,
	)
	return enforce(d)
}

// CheckMemoryWrite is the gate-hook helper for memory writes. Wrap
// the core/memory.Store.Add call boundary; the kernel and the explicit
// MemoryNode are the only callers per FR-026.
//
// scope is one of "global", "project", "session" per FR-029.
func CheckMemoryWrite(ctx context.Context, g Gate, scope string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionMemoryWrite,
		MemoryUID(scope),
		nil,
	)
	return enforce(d)
}

// CheckNetwork is the gate-hook helper for network requests issued
// from tools (e.g. websearch fetches). host is the target hostname;
// the resource entity is normalised lowercase + trailing-dot-stripped.
func CheckNetwork(ctx context.Context, g Gate, host string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionNetworkRequest,
		NetworkUID(host),
		nil,
	)
	return enforce(d)
}

// CheckFileWrite is the gate-hook helper for filesystem writes. path
// SHOULD be absolute + cleaned by the caller for deterministic
// matching; the helper does not normalise on the caller's behalf so
// the policy file's resource constants stay literal.
func CheckFileWrite(ctx context.Context, g Gate, path string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionFileWrite,
		FilesystemUID(path),
		nil,
	)
	return enforce(d)
}

// enforce maps a Decision to a Go error. Allow + NotApplicable both
// return nil (default-allow stance); Deny returns *PolicyDeniedError.
func enforce(d Decision) error {
	switch d.Outcome {
	case Allow, NotApplicable:
		return nil
	case Deny:
		return &PolicyDeniedError{Decision: d}
	default:
		return nil
	}
}
