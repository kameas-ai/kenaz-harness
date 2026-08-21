package cedar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
// server / tool follow the kenaz-harness "<server>__<tool>" naming;
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
// strictMode controls NotApplicable handling:
//   - false (default / lenient): NotApplicable → nil (allow).
//   - true (strict): NotApplicable → credstore.ErrCredentialAccessDenied.
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
		if strictMode {
			return errCredentialAccessDenied
		}
		return nil
	default:
		return nil
	}
}

// GateMCPSpawn is the full WP05 credential gate for MCP spawn. It:
//
//  1. Evaluates the Cedar engine with purpose="mcp_spawn" and the
//     recipe id as the Credential resource UID.
//  2. Allow → returns nil.
//  3. Deny → returns *PolicyDeniedError.
//  4. NotApplicable + registry != nil → fires
//     Registry.RequestInteractive with a CredPromptSurface
//     (ProviderID = recipeID, Purpose = "mcp_spawn"). Resolves to
//     Allow-once / Allow-always → nil; Deny / timeout / overflow →
//     returns errMCPSpawnDenied.
//  5. NotApplicable + registry == nil → nil (default-allow; pre-boot
//     posture so a nil-registry chassis never blocks).
//
// When the interactive prompt resolves to DecisionAllowAlways AND
// dataDir + engine are both non-nil, a persistent Cedar policy snippet
// is written to <dataDir>/policy/cred_allow_mcp_<sanitized>.cedar so
// the grant survives a process restart. The engine is then reloaded so
// the new permit takes effect immediately without a restart. Both the
// persistent snippet and the existing in-memory transient grant (seeded
// by DecisionAllowOnce via Registry.Resolve) cover the current session.
//
// g may be nil (no engine wired) → nil (default-allow).
// registry may be nil → NotApplicable treated as allow (step 5).
// dataDir / engine may be nil → AllowAlways behaves as AllowOnce (no
// persistent grant written, same behaviour as before this follow-up).
func GateMCPSpawn(
	ctx context.Context,
	g Gate,
	registry *Registry,
	recipeID string,
	dataDir string,
	engine *Engine,
) error {
	ctxAttrs := map[cedar.String]cedar.Value{
		cedar.String("purpose"): cedar.String("mcp_spawn"),
		cedar.String("ref_id"):  cedar.String(recipeID),
	}

	var outcome Outcome
	if g != nil {
		d := g.Evaluate(
			ctx,
			UserUID(),
			ActionUseCredential,
			CredentialUID(recipeID, "mcp_spawn"),
			ctxAttrs,
		)
		switch d.Outcome {
		case Allow:
			return nil
		case Deny:
			return &PolicyDeniedError{Decision: d}
		default:
			outcome = NotApplicable
		}
	} else {
		outcome = NotApplicable
	}

	_ = outcome // NotApplicable path

	if registry == nil {
		// No registry — default-allow (pre-boot / nil-registry posture).
		return nil
	}

	// Fire the interactive prompt for the Credential family.
	surface := PromptSurface{
		Cred: &CredPromptSurface{
			ProviderID: recipeID,
			Purpose:    "mcp_spawn",
		},
	}
	res, err := registry.RequestInteractive(ctx, surface)
	if err != nil {
		return errMCPSpawnDenied
	}
	switch res.Decision {
	case DecisionAllowOnce:
		return nil
	case DecisionAllowAlways:
		// Write a persistent Cedar snippet so the grant survives restarts,
		// then reload the engine so the permit is active immediately.
		// Failure is non-fatal: the user approved the spawn this session;
		// future sessions will re-prompt if the write failed.
		if dataDir != "" && engine != nil {
			if writeErr := WriteMCPSpawnSnippet(ctx, dataDir, engine, recipeID); writeErr != nil {
				// Log-only: do not block the spawn on a write error.
				_ = writeErr
			}
		}
		return nil
	default:
		return errMCPSpawnDenied
	}
}

// WriteMCPSpawnSnippet writes a persistent Cedar permit snippet for an
// mcp_spawn grant to <dataDir>/policy/cred_allow_mcp_<sanitized>.cedar.
//
// The snippet body is a single permit that allows
// Action::"use_credential" on Credential::"<recipeID>::mcp_spawn".
// After writing, Engine.Reload is called so the new permit takes effect
// in the current process without a restart.
//
// Filename sanitisation: recipeID characters outside [a-zA-Z0-9\-.] are
// replaced with underscores, then lowercased, giving a filename that:
//   - matches familyFromFilename's "cred_allow_" prefix check in the
//     permissions view (ListGrants / RevokeGrant path);
//   - satisfies isPolicyFilename's path-traversal guard.
//
// When dataDir is empty, or the engine is nil, the function is a no-op
// (callers on the pre-boot / test path pass nil engine). Errors from
// mkdir or file-write are returned; callers SHOULD treat them as
// non-fatal (the interactive grant covers the current session).
func WriteMCPSpawnSnippet(ctx context.Context, dataDir string, engine *Engine, recipeID string) error {
	if dataDir == "" || engine == nil {
		return nil
	}
	sanitized := sanitizeMCPIDForFilename(recipeID)
	dir := filepath.Join(dataDir, PolicyDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	filename := filepath.Join(dir, "cred_allow_mcp_"+sanitized+".cedar")
	// Credential resource UID encodes both provider and purpose in the
	// "<provider>::<purpose>" shape (spec §3). A permit on the exact UID
	// Credential::"<recipeID>::mcp_spawn" covers only this recipe's spawn.
	body := "permit(\n" +
		"  principal,\n" +
		"  action == Action::\"use_credential\",\n" +
		"  resource == Credential::\"" + recipeID + "::mcp_spawn\"\n" +
		");\n"
	if err := os.WriteFile(filename, []byte(body), 0o644); err != nil {
		return err
	}
	// Best-effort reload: failure is non-fatal; the current session's
	// transient grant (seeded by DecisionAllowOnce on the Registry.Resolve
	// path) already permits the spawn.
	if err := engine.Reload(ctx); err != nil {
		return err
	}
	return nil
}

// sanitizeMCPIDForFilename replaces characters outside [a-zA-Z0-9\-.]
// with underscores and lowercases the result. Mirrors the same logic
// used by core/tools/bash.sanitizePatternForFilename so the filename
// safety invariant is shared across all snippet writers.
func sanitizeMCPIDForFilename(id string) string {
	var sb strings.Builder
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteRune('_')
		}
	}
	return sb.String()
}

// errMCPSpawnDenied is the package-local sentinel returned by
// GateMCPSpawn when the interactive prompt denies the request.
// credstore maps this onto ErrMCPSpawnDenied before surfacing to
// callers.
var errMCPSpawnDenied = errors.New("cedar: mcp_spawn credential denied by prompt")

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

// GateWorkflowRun is the gate-hook helper for workflow dispatch
// (mission workflows-01KQ8TDG, WP11). It evaluates the Cedar engine
// against Workflow::"<workflowID>" with Action::"workflow.run" and the
// supplied (mode, stepKinds) context attrs.
//
//   - g may be nil — the helper short-circuits to nil (default-allow).
//   - mode is "permissive" or "strict"; pass "" and the helper coerces
//     to "permissive" (the default posture).
//   - stepKinds is the sorted+joined list of kinds in the workflow
//     (e.g. "model_turn,shell"). Used by the policy bundle's strict-
//     mode forbid rules.
//
// Returns nil on Allow / NotApplicable, *PolicyDeniedError on Deny.
// NotApplicable callers SHOULD treat the decision as "fire interactive
// prompt"; the chassis layer (workflows RPC impl) does that today.
func GateWorkflowRun(ctx context.Context, g Gate, workflowID, mode, stepKinds string) (Decision, error) {
	if mode == "" {
		mode = "permissive"
	}
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionWorkflowRun,
			Resource: WorkflowUID(workflowID).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionWorkflowRun,
		WorkflowUID(workflowID),
		map[cedar.String]cedar.Value{
			cedar.String("mode"):       cedar.String(mode),
			cedar.String("step_kinds"): cedar.String(stepKinds),
		},
	)
	return d, enforce(d)
}

// GateWorkflowSave is the gate-hook helper for workflow persistence.
// Mirrors GateWorkflowRun's contract for the save action; the policy
// bundle's strict-mode forbid rule denies workflows containing a
// `shell` step at save time.
func GateWorkflowSave(ctx context.Context, g Gate, workflowID, mode, stepKinds string) (Decision, error) {
	if mode == "" {
		mode = "permissive"
	}
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionWorkflowSave,
			Resource: WorkflowUID(workflowID).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionWorkflowSave,
		WorkflowUID(workflowID),
		map[cedar.String]cedar.Value{
			cedar.String("mode"):       cedar.String(mode),
			cedar.String("step_kinds"): cedar.String(stepKinds),
		},
	)
	return d, enforce(d)
}

// GateWorkflowDelete is the gate-hook helper for workflow deletion.
// Returns nil on Allow / NotApplicable; *PolicyDeniedError on Deny.
// The default policy permits delete unconditionally so the chassis can
// always emit `workflow.deleted` audit; strict overrides layer a
// forbid rule on top.
func GateWorkflowDelete(ctx context.Context, g Gate, workflowID string) (Decision, error) {
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionWorkflowDelete,
			Resource: WorkflowUID(workflowID).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionWorkflowDelete,
		WorkflowUID(workflowID),
		nil,
	)
	return d, enforce(d)
}

// GateScheduledChatCreate is the gate-hook helper for scheduled chat run
// create and update operations (mission scheduled-chat-runs-01KX5R8B, WP03).
// Returns nil on Allow / NotApplicable; *PolicyDeniedError on Deny.
// Default-allow when g is nil.
func GateScheduledChatCreate(ctx context.Context, g Gate, id string) (Decision, error) {
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionScheduledRunCreate,
			Resource: ScheduledChatRunUID(id).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(ctx, UserUID(), ActionScheduledRunCreate, ScheduledChatRunUID(id), nil)
	return d, enforce(d)
}

// GateScheduledChatDelete is the gate-hook helper for scheduled chat run
// delete operations. Returns nil on Allow / NotApplicable; *PolicyDeniedError
// on Deny. Default-allow when g is nil.
func GateScheduledChatDelete(ctx context.Context, g Gate, id string) (Decision, error) {
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionScheduledRunDelete,
			Resource: ScheduledChatRunUID(id).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(ctx, UserUID(), ActionScheduledRunDelete, ScheduledChatRunUID(id), nil)
	return d, enforce(d)
}

// GateScheduledChatExecute is the gate-hook helper for scheduled chat run
// dispatch (both cron-triggered and RunNow paths). Returns nil on
// Allow / NotApplicable; *PolicyDeniedError on Deny. Default-allow when
// g is nil.
func GateScheduledChatExecute(ctx context.Context, g Gate, id string) (Decision, error) {
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionScheduledRunExecute,
			Resource: ScheduledChatRunUID(id).String(),
			Reason:   "no engine wired (default-allow)",
		}, nil
	}
	d := g.Evaluate(ctx, UserUID(), ActionScheduledRunExecute, ScheduledChatRunUID(id), nil)
	return d, enforce(d)
}

// CheckExportSession is the gate-hook helper for the Sessions_Export RPC
// (session-export-01NDFSEX05 WP01). Returns nil on Allow / NotApplicable;
// *PolicyDeniedError on Deny. Default-allow when g is nil.
//
// sessionID is the session's primary-key string (used as the resource UID);
// format is "markdown" or "json" (passed as a context attribute so policy
// authors can write format-specific rules in the future).
func CheckExportSession(ctx context.Context, g Gate, sessionID, format string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionExportSession,
		SessionUID(sessionID),
		map[cedar.String]cedar.Value{
			cedar.String("format"): cedar.String(format),
		},
	)
	return enforce(d)
}

// CheckApprovalResolve is the gate-hook helper for the
// Graph_ResolveApproval RPC (approval-node-01PMZC12 UNIT-3, FR-003).
// Returns nil on Allow / NotApplicable; *PolicyDeniedError on Deny.
// Default-allow when g is nil (pre-boot / test posture) — a human
// resolving their own paused run is not blocked by the absence of a
// policy engine.
//
// This gate covers ONLY the human-verb path
// (Bindings.Graph_ResolveApproval). The no-watcher fail-closed timeout
// and the auto_approve_window_seconds timeout (UNIT-4/UNIT-5) resolve
// server-side and do not call this — they are the safety mechanism a
// Cedar misconfiguration must not be able to block (spec.md §5.3: a
// missing approver must not read as "approved", and symmetrically a
// gate denial must not be able to leave a run parked forever when the
// system itself decided to fail closed).
func CheckApprovalResolve(ctx context.Context, g Gate, runID, nodeID string, approved bool) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionApprovalResolve,
		ApprovalUID(runID, nodeID),
		map[cedar.String]cedar.Value{
			cedar.String("approved"): cedar.String(fmt.Sprintf("%t", approved)),
		},
	)
	return enforce(d)
}

// CheckAuditBulkPurge is the gate-hook helper for the Audit_BulkPurge RPC
// (F-001 security fix). This must be called BEFORE the delete loop in
// the BulkPurge implementation. On Deny the caller must return the
// PolicyDeniedError to the RPC caller without executing any deletions and
// emit KindAuditBulkPurgeBlockedByPolicy.
//
// The resource UID is AuditLog::"events" (singleton). Default-allow when
// g is nil (pre-boot / test posture). NotApplicable is treated as Deny
// because bulk purge is default-forbid per spec — only an explicit Cedar
// permit should allow it.
func CheckAuditBulkPurge(ctx context.Context, g Gate) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionAuditBulkPurge,
		AuditLogUID("events"),
		nil,
	)
	// Fail-closed: NotApplicable → Deny because ActionAuditBulkPurge is
	// default-forbid per spec. Only an explicit Cedar permit should allow it.
	switch d.Outcome {
	case Allow:
		return nil
	default:
		return &PolicyDeniedError{Decision: d}
	}
}

// CheckLLMFallback is the gate-hook helper for per-turn LLM fallback chain
// hops (model-fallback-routing-01NDFSEX04 WP03). The connector's retry
// loop calls this before issuing each hop. Returns nil on Allow /
// NotApplicable; *PolicyDeniedError on Deny (fail-closed: the connector
// then surfaces the primary error unchanged).
//
// chainID is the chain's slug identifier (e.g.
// "anthropic-with-openrouter-fallback"). Default-allow when g is nil.
func CheckLLMFallback(ctx context.Context, g Gate, chainID string) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionLLMFallback,
		FallbackChainUID(chainID),
		nil,
	)
	return enforce(d)
}

// CheckContextBootstrapRun is the gate-hook helper for the context-bootstrap
// run-dispatch path (context-bootstrap-harness-integration WP05). Default-allow
// for the local user; a Cedar forbid rule against ActionContextBootstrapRun
// disables the bootstrap engine. Returns *PolicyDeniedError on deny.
func CheckContextBootstrapRun(ctx context.Context, g Gate) error {
	if g == nil {
		return nil
	}
	d := g.Evaluate(
		ctx,
		UserUID(),
		ActionContextBootstrapRun,
		ContextBootstrapUID("run"),
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

// ResolveContext carries the per-resolution context attributes that
// EvaluateSecretReferenceResolve injects into the Cedar evaluation.
// Callers populate only the fields that are known at the call site;
// zero values degrade gracefully.
type ResolveContext struct {
	// ToolName is the name of the tool invoking the resolution
	// (e.g. "web_fetch", "bash", "mcp:<server>").
	ToolName string
	// DestinationHost is the outbound request host when known
	// (e.g. "api.example.com"). Empty for bash invocations.
	DestinationHost string
	// IsStreaming is true when the containing session is currently
	// streaming response tokens.
	IsStreaming bool
	// Budget is the remaining per-session-per-locator resolution budget.
	// Zero means unlimited (no budget enforcement active).
	Budget int64
	// AgentKind is "trusted" or "untrusted". Untrusted (un-audited MCP
	// servers) are default-denied for any resolution.
	AgentKind string
}

// EvaluateSecretReferenceResolve runs the Cedar engine against the
// SecretReference family for the given locator and returns the raw
// Decision. The function is the gate-hook entry point for the
// model-secret-references mission (WP02).
//
// The default policy ships in
// core/policy/cedar/policies/secret_reference.cedar and evaluates:
//   - permit when locator is in the session's exposed_secrets set (the
//     caller's responsibility to express via rctx).
//   - forbid when agent_kind == "untrusted".
//   - forbid when budget == 0 (caller must set 0 to signal exhaustion).
//
// g may be nil — returns a Decision with Outcome=Allow and reason "no
// engine wired (default-allow)" so the test harness and pre-boot paths
// behave safely.
func EvaluateSecretReferenceResolve(
	ctx context.Context,
	g Gate,
	locator string,
	rctx ResolveContext,
) Decision {
	if g == nil {
		return Decision{
			Outcome:  Allow,
			Action:   ActionSecretReferenceResolve,
			Resource: SecretReferenceUID(locator).String(),
			Reason:   "no engine wired (default-allow)",
		}
	}
	agentKind := rctx.AgentKind
	if agentKind == "" {
		agentKind = "trusted"
	}
	attrs := map[cedar.String]cedar.Value{
		cedar.String(CtxKeySecretToolName):        cedar.String(rctx.ToolName),
		cedar.String(CtxKeySecretDestinationHost): cedar.String(rctx.DestinationHost),
		cedar.String(CtxKeySecretIsStreaming):     cedar.Boolean(rctx.IsStreaming),
		cedar.String(CtxKeySecretBudget):          cedar.Long(rctx.Budget),
		cedar.String(CtxKeySecretAgentKind):       cedar.String(agentKind),
	}
	return g.Evaluate(ctx, UserUID(), ActionSecretReferenceResolve, SecretReferenceUID(locator), attrs)
}
