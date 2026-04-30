package cedar

import (
	"context"
	"errors"

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

// CheckFileRead is the gate-hook helper for filesystem reads. Mirrors
// CheckFileWrite: callers SHOULD pass an absolute, cleaned path so
// policy authors get deterministic matching. The harness's State
// `read_file` kind calls this from the executor; tool-side reads
// (filesystem MCP) call it via the same helper.
func CheckFileRead(ctx context.Context, g Gate, path string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionFileRead,
		FilesystemUID(path),
		nil,
	)
	return enforce(d)
}

// CheckStateRead is the gate-hook helper for the FR-058b finer-grained
// "Read::<source>" action. State `read_file` / `read_bash_output`
// executors call this AFTER the broader file-read gate so a policy
// can deny a particular source class without forbidding every
// filesystem read.
func CheckStateRead(ctx context.Context, g Gate, source string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionStateRead,
		StateSourceUID(source),
		nil,
	)
	return enforce(d)
}

// CheckStateWrite is the FR-058b counterpart for State write kinds
// ("write_file", "artifact"). The action carries the target class
// ("file", "artifact") so policy authors can write rules like
// `forbid (action == Action::"state_write", resource == State::"file")`
// to disable file writes without breaking artifact emission.
func CheckStateWrite(ctx context.Context, g Gate, target string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionStateWrite,
		StateTargetUID(target),
		nil,
	)
	return enforce(d)
}

// EvaluateUseTool runs the Cedar engine against the Tool resource family
// for the given (serverName, toolName) pair and returns the raw Decision.
// The context attribute map is pre-populated with the tool-family attrs
// (server_name, tool_name, prompt_on_first_use) before evaluation.
//
// This helper is the gate-hook entry point for WP06: callers that need
// to act differently on Allow / Deny / NotApplicable call this rather
// than CheckTool so they can inspect the Outcome directly.
//
// g may be nil — returns a NotApplicable Decision with reason "no engine".
// toolName is the bare tool name (without server prefix). serverName is
// the bare server name. promptOnFirstUse is the recipe-metadata flag
// (FR-024) — it is injected into context so Cedar policies can inspect it.
func EvaluateUseTool(
	ctx context.Context,
	g Gate,
	serverName, toolName string,
	promptOnFirstUse bool,
) Decision {
	if g == nil {
		return Decision{
			Outcome:  NotApplicable,
			Action:   ActionUseTool,
			Resource: PermissionToolUID(serverName + "__" + toolName).String(),
			Reason:   "no engine (nil gate)",
		}
	}
	fqName := toolName
	if serverName != "" {
		fqName = serverName + "__" + toolName
	}
	attrs := map[cedar.String]cedar.Value{
		cedar.String(CtxKeyServerName):       cedar.String(serverName),
		cedar.String(CtxKeyToolName):         cedar.String(toolName),
		cedar.String(CtxKeyPromptOnFirstUse): cedar.Boolean(promptOnFirstUse),
	}
	return g.Evaluate(ctx, UserUID(), ActionUseTool, PermissionToolUID(fqName), attrs)
}

// CheckRecipeAdd is the gate-hook helper for AddRecipe / EditRecipe RPC
// paths (mission mcp-server-install-01KQ8TDP, WP10). recipeID is the
// canonical recipe identifier; command is Command[0] (first argv element,
// e.g. "npx", "uvx", "/usr/local/bin/my-server"); transport is "stdio",
// "http", or "sse".
//
// When g is nil the helper returns nil (boot-stage default-allow). Cedar
// evaluation errors (engine not fully loaded) are also mapped to nil
// (default-permit) to keep the chassis from blocking on a Cedar bug.
func CheckRecipeAdd(ctx context.Context, g Gate, recipeID, command, transport string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionAddRecipe,
		MCPRecipeUID(recipeID),
		map[cedar.String]cedar.Value{
			cedar.String(CtxKeyRecipeID):        cedar.String(recipeID),
			cedar.String(CtxKeyRecipeCommand):   cedar.String(command),
			cedar.String(CtxKeyRecipeTransport): cedar.String(transport),
		},
	)
	return enforce(d)
}

// CheckRecipeSpawn is the gate-hook helper for the pool's OpenOne path
// (mission mcp-server-install-01KQ8TDP, WP10). recipeID is the canonical
// recipe identifier; command is Command[0]; transport is "stdio"/"http"/"sse".
//
// When g is nil the helper returns nil (boot-stage default-allow). Cedar
// evaluation errors are mapped to nil (default-permit — best-effort gate).
func CheckRecipeSpawn(ctx context.Context, g Gate, recipeID, command, transport string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionSpawnRecipe,
		MCPRecipeUID(recipeID),
		map[cedar.String]cedar.Value{
			cedar.String(CtxKeyRecipeID):        cedar.String(recipeID),
			cedar.String(CtxKeyRecipeCommand):   cedar.String(command),
			cedar.String(CtxKeyRecipeTransport): cedar.String(transport),
		},
	)
	return enforce(d)
}

// CheckCredentialAccess is the gate-hook helper for credstore.Use
// (mission cedar-credential-policy-01KQ8TDE, WP05). It fires inside
// Use BEFORE the secret bytes are returned so an explicit Deny always
// blocks resolution.
//
// refID is the credential-provider identifier (e.g. "openai");
// purpose is the string form of an AccessPurpose (e.g.
// "provider_call", "manual_export", "mcp_spawn"). The Cedar resource
// entity is built as Credential::"<refID>::<purpose>" per spec §3.
//
// strictMode controls NotApplicable handling for non-mcp_spawn
// purposes:
//   - false (default / lenient): NotApplicable → nil (allow).
//   - true (strict): NotApplicable + purpose != "mcp_spawn" →
//     credstore.ErrCredentialAccessDenied.
//
// For purpose == "mcp_spawn" the gate fires best-effort regardless of
// strictMode; the IssueForMCPSpawn interactive-prompt path is deferred
// to credstore WP06.
//
// Nil gate → nil error (default-allow; no engine wired).
// Evaluation errors on the gate itself → nil (best-effort; only
// explicit Deny outcomes block).
func CheckCredentialAccess(ctx context.Context, g Gate, refID, purpose string, strictMode bool) error {
	if g == nil {
		return nil
	}
	ctxAttrs := map[cedar.String]cedar.Value{
		cedar.String("purpose"): cedar.String(purpose),
		cedar.String("ref_id"):  cedar.String(refID),
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionUseCredential,
		CredentialUID(refID, purpose),
		ctxAttrs,
	)
	switch d.Outcome {
	case Allow:
		return nil
	case Deny:
		return &PolicyDeniedError{Decision: d}
	case NotApplicable:
		// Strict mode: for non-mcp_spawn purposes, treat no-match as
		// deny so the store is fail-closed when the operator requests it.
		// mcp_spawn is always lenient here because its full interactive
		// gate lands in credstore WP06.
		if strictMode && purpose != "mcp_spawn" {
			return errCredentialAccessDenied
		}
		return nil
	default:
		return nil
	}
}

// errCredentialAccessDenied is the package-local sentinel returned by
// CheckCredentialAccess in strict-mode / NotApplicable cases. Callers
// in credstore surface this as credstore.ErrCredentialAccessDenied;
// the cedar package avoids importing credstore to prevent a cycle.
var errCredentialAccessDenied = &credentialAccessDeniedError{}

type credentialAccessDeniedError struct{}

func (*credentialAccessDeniedError) Error() string {
	return "credstore: credential access denied by policy"
}

// IsCredentialAccessDenied reports whether err is the strict-mode
// NotApplicable denial produced by CheckCredentialAccess. Credstore
// uses this to map the error onto its own ErrCredentialAccessDenied
// sentinel before returning it to callers.
func IsCredentialAccessDenied(err error) bool {
	var e *credentialAccessDeniedError
	return errors.As(err, &e)
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
