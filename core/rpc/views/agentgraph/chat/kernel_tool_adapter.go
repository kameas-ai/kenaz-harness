package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
)

// AutonomyKnobsProvider is the narrow surface the kernel tool adapter
// needs to apply the autonomy posture before each tool call. The
// production wiring binds this to a closure over the session's resolved
// autonomy knobs (autonomy-dial WP04). nil disables posture-aware
// short-circuiting — the adapter falls through to the permission
// resolver on every call (v0.3.0 baseline behaviour).
//
// Session-scoped (autonomy-knobs-live-01PMAG02 WP01): the three-layer
// chain (global → project → session) can only be walked once the
// session id is known, so the provider takes it as a parameter rather
// than closing over a single ambient session. Production wiring loads
// the global/project/session autonomy.Layer values fresh per call —
// see core/rpc/api.go's newLLMStack for the concrete resolver.
//
// Takes a ctx (fix F8): the production resolver does real I/O (global/
// project/session store reads). ChatRunner.StartStream resolves this
// exactly once per run, with the run's real ctx (not
// context.Background()), and threads the resulting autonomy.
// ResolvedKnobs value down as a trivial no-I/O closure everywhere else
// it's needed (this adapter's per-tool-call Call(), the LLM provider
// adapter's per-Generate resolvers) — see chat_runner.go's StartStream.
// Without that, a chatty turn (many Generate calls, many tool calls)
// re-ran the full three-layer resolution — several real store reads —
// on every single one of them.
type AutonomyKnobsProvider func(ctx context.Context, sessionID string) autonomy.ResolvedKnobs

// kernelToolAdapter satisfies agentgraph.ToolRegistry by routing
// dispatch through the chat-package-local ToolPool surface (which the
// chassis wires to its real MCP stdio pool + builtin registry). It is
// the only implementation: it depends on the package-local ToolPool /
// ToolPermissionResolver interfaces rather than on toolloop wire types.
//
// (This comment used to describe it as "the kernel-facing companion to
// ToolRegistryAdapter" and promise "WP07 retires ToolRegistryAdapter in
// favour of this one". WP07 did retire it — ToolRegistryAdapter no
// longer exists anywhere in the tree, and this comment was its last
// remaining reference. Corrected under 01PMGX01 invariant I8,
// 2026-08-13.)
type kernelToolAdapter struct {
	pool      ToolPool
	perms     ToolPermissionResolver
	sessionID string
	catalog   []ToolEntry

	// moves is the turn's move journal
	// (model-moves-transcript-01PMCH01 WP02). Non-nil on the chat path;
	// nil (or inert) leaves Call byte-identical to the pre-mission path.
	moves *turnJournal

	// autonomy is the optional knobs provider for autonomy-dial WP04.
	// When non-nil the adapter reads AutoApproveFamilies before each
	// tool call to determine whether the permission-resolver prompt path
	// can be bypassed for the "tool" family.
	autonomy AutonomyKnobsProvider

	// confirm is the confirm-each pause seam
	// (confirm-each-enforcement-01PMAG05 WP01). When non-nil, a
	// "confirm_each" verdict parks the call on the bus and blocks until
	// the user answers or the run's context is cancelled. When nil there
	// is no prompt channel and the headless policy decides — see
	// resolveConfirmEach.
	confirm *toolloop.ConfirmBus

	// confirmEnabled is Settings.ConfirmEachEnabled()'s Go consumer
	// (WP02 / FR-006). It was plumbed through the settings store and
	// read by nothing outside its own package; the chassis now binds it
	// here. false ⇒ the prompt is never offered and a confirm_each
	// verdict behaves as auto-allow, with an audit record naming the
	// toggle so the widened posture is legible after the fact.
	//
	// nil means "enabled" — a caller that does not wire the toggle gets
	// the safe behaviour (prompt), never the silent one.
	confirmEnabled func() bool

	// sessionGrants is the "allow for this session" cache (WP03).
	// Process-lifetime, never on disk. nil disables session grants
	// entirely: every call re-prompts.
	sessionGrants *toolloop.SessionGrantCache

	// persistGrants is the durable "always allow" store (WP03). nil
	// disables the persist control: the modal's "always allow" answer
	// degrades to a one-shot approve rather than silently claiming to
	// have written a rule.
	persistGrants toolloop.PersistentGrantStore

	// headless is the policy applied when a confirm_each verdict arrives
	// with no prompt channel attached (WP05 / owner decision 4). The
	// zero value is HeadlessDeny.
	headless toolloop.HeadlessConfirmPolicy

	// headlessExplicit records that the OPERATOR declared this
	// deployment headless (HARNESS_CONFIRM_EACH_HEADLESS set to a
	// recognised value). When true the headless policy applies even
	// though a prompt channel exists structurally (adversarial review
	// 2026-08-13): the production bus always has a broker publisher, so
	// HasChannel() is true in every shipped binary and the
	// channel-presence check alone can never route a served-no-UI
	// deployment to its configured policy — it would park forever
	// instead. An explicit declaration is the deployment saying "no
	// human answers prompts here"; honour it.
	headlessExplicit bool

	// auditEmitter records every confirmation decision on every path
	// (WP05 / FR-007). nil silences the trail; production always wires
	// it.
	auditEmitter audit.Emitter

	// now is the injected clock for audit timestamps. nil → time.Now.
	now func() time.Time
}

// newKernelToolAdapter wraps the chassis-side pool + resolver.
func newKernelToolAdapter(pool ToolPool, perms ToolPermissionResolver, sessionID string) *kernelToolAdapter {
	return &kernelToolAdapter{
		pool:      pool,
		perms:     perms,
		sessionID: sessionID,
	}
}

// withMoves attaches the turn's move journal so each dispatch lands a
// tool_call / tool_result pair in the transcript
// (model-moves-transcript-01PMCH01 WP02). Returns the same pointer so
// callers can chain at construction time.
func (a *kernelToolAdapter) withMoves(j *turnJournal) *kernelToolAdapter {
	a.moves = j
	return a
}

// withAutonomy attaches an AutonomyKnobsProvider to the adapter.
// Returns the same pointer so callers can chain at construction time.
func (a *kernelToolAdapter) withAutonomy(provider AutonomyKnobsProvider) *kernelToolAdapter {
	a.autonomy = provider
	return a
}

// withConfirm attaches the confirm-each pause seam
// (confirm-each-enforcement-01PMAG05 WP01). Returns the same pointer so
// callers can chain at construction time.
//
// The chassis owns bus construction because the bus needs a broker
// publisher and the reverse (Resolve) leg needs an RPC binding; WP02
// wires both. An adapter with a nil bus has no prompt channel and falls
// to the headless policy, whose default is deny — never silent allow.
func (a *kernelToolAdapter) withConfirm(bus *toolloop.ConfirmBus) *kernelToolAdapter {
	a.confirm = bus
	return a
}

// withConfirmDeps attaches everything the confirm-each decision path
// needs beyond the bus itself: the Settings toggle (FR-006), the two
// grant layers (WP03), the headless policy (WP05), and the audit
// emitter (FR-007). Every field is optional; the zero value of each is
// the conservative one (prompt, no grants, deny when headless, silent
// audit).
func (a *kernelToolAdapter) withConfirmDeps(d ConfirmDeps) *kernelToolAdapter {
	a.confirmEnabled = d.Enabled
	a.sessionGrants = d.SessionGrants
	a.persistGrants = d.PersistGrants
	a.headless = d.Headless
	a.headlessExplicit = d.HeadlessExplicit
	a.auditEmitter = d.Audit
	a.now = d.Now
	return a
}

// ConfirmDeps bundles the collaborators of the confirm-each decision
// path so the chassis wires them in one place and the adapter's
// constructor does not grow six more parameters.
type ConfirmDeps struct {
	// Enabled is Settings.ConfirmEachEnabled(). nil ⇒ enabled.
	Enabled func() bool

	// SessionGrants backs "allow for this session". nil ⇒ no session
	// grants; every call re-prompts.
	SessionGrants *toolloop.SessionGrantCache

	// PersistGrants backs "always allow". nil ⇒ the control degrades to
	// a one-shot approve.
	PersistGrants toolloop.PersistentGrantStore

	// Headless is the no-prompt-channel policy. Zero value is deny.
	Headless toolloop.HeadlessConfirmPolicy

	// HeadlessExplicit means the operator explicitly configured the
	// headless policy for this deployment (env var set to a recognised
	// value, not merely defaulted). When true, Headless applies to
	// every confirm_each verdict regardless of whether a prompt
	// channel is attached — the declaration is authoritative. False
	// (the default) keeps prompt-first behaviour whenever a channel
	// exists.
	HeadlessExplicit bool

	// Audit receives one record per decision, on every path.
	Audit audit.Emitter

	// Now is the injected clock for audit timestamps. nil ⇒ time.Now.
	Now func() time.Time
}

// Has satisfies agentgraph.ToolRegistry. Lazy-loads the catalog on
// first call; a discovery failure surfaces as "no tools registered"
// so the kernel produces a clean "unknown tool" error.
func (a *kernelToolAdapter) Has(name string) bool {
	if a == nil || a.pool == nil {
		return false
	}
	if a.catalog == nil {
		tools, err := a.pool.Tools(context.Background())
		if err != nil {
			// Surface the discovery failure: otherwise Has() returns false
			// and the kernel reports a misleading "unknown tool" with no
			// trace of the real cause (network / permission / pool error).
			logging.L().Warn("chat.tool_adapter.catalog_load_failed",
				"at", "Has", "err", err.Error())
			return false
		}
		a.catalog = tools
	}
	server, tool, ok := splitName(name)
	if ok {
		for _, t := range a.catalog {
			if t.Server == server && t.Name == tool {
				return true
			}
		}
		return false
	}
	for _, t := range a.catalog {
		if t.Name == name {
			return true
		}
	}
	return false
}

// Call dispatches one tool and records the pair of transcript moves it
// produces (model-moves-transcript-01PMCH01 WP02).
//
// The tool_call entry is written BEFORE dispatch, not after: a tool the
// user stops mid-flight, or one that never returns, must still leave the
// request in the transcript — otherwise the interrupt path's synthetic
// is_error result would be an orphan, and an orphaned tool_result is the
// classic provider 400 the moment WP03 feeds these entries back as
// history.
//
// A call that never reaches dispatch (unknown tool, permission resolve
// error) records nothing: the model asked, the harness refused before
// asking anything on its behalf, and the kernel surfaces that error up
// the run.
func (a *kernelToolAdapter) Call(ctx context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	if !a.moves.records() {
		return a.dispatch(ctx, call)
	}
	a.moves.RecordToolCall(ctx, call)
	res, err := a.dispatch(ctx, call)
	if err != nil {
		// The dispatch itself failed rather than the tool returning an
		// error result. Close the pair anyway so the tool_call is not
		// left dangling.
		a.moves.RecordToolResult(ctx, call, coreag.ToolResult{
			Content: err.Error(),
			IsError: true,
		})
		return res, err
	}
	a.moves.RecordToolResult(ctx, call, res)
	return res, nil
}

// dispatch is Call's body: permission resolution followed by the pool
// invocation. Returns IsError=true on deny / pool failure so the kernel
// surfaces the failure to the model without crashing the run.
func (a *kernelToolAdapter) dispatch(ctx context.Context, call coreag.ToolCall) (coreag.ToolResult, error) {
	if a == nil || a.pool == nil {
		logging.L().Error("chat.tool_adapter.nil_pool", "tool", call.Name)
		return coreag.ToolResult{}, errors.New("chat: nil tool pool adapter")
	}
	server, tool, ok := splitName(call.Name)
	if !ok {
		// Bare name fallback: find the first catalog match.
		if a.catalog == nil {
			tools, err := a.pool.Tools(ctx)
			if err != nil {
				logging.L().Warn("chat.tool_adapter.catalog_load_failed",
					"at", "Call", "tool", call.Name, "err", err.Error())
				return coreag.ToolResult{}, fmt.Errorf("chat: tool catalog: %w", err)
			}
			a.catalog = tools
		}
		for _, t := range a.catalog {
			if t.Name == call.Name {
				server = t.Server
				tool = t.Name
				ok = true
				break
			}
		}
		if !ok {
			return coreag.ToolResult{}, fmt.Errorf("chat: unknown tool %q", call.Name)
		}
	}
	if a.perms != nil {
		v, err := a.perms.Resolve(ctx, a.sessionID, server, tool)
		if err != nil {
			return coreag.ToolResult{}, fmt.Errorf("chat: permission resolve: %w", err)
		}
		switch v.Policy {
		case string(toolloop.PolicyAutoAllow):
			// Nothing to do — fall through to dispatch.

		case string(toolloop.PolicyDeny):
			// The floor. Evaluated before the prompt so a denied call is
			// never surfaced to the user as an approvable row.
			reason := v.Reason
			if reason == "" {
				reason = "denied by permission policy"
			}
			return coreag.ToolResult{
				Content: fmt.Sprintf("tool %q denied: %s", call.Name, reason),
				IsError: true,
			}, nil

		case string(toolloop.PolicyConfirmEach):
			// confirm-each-enforcement-01PMAG05 WP01/WP02/WP03/WP05: the
			// third verdict finally does something.
			res, proceed, err := a.resolveConfirmEach(ctx, call, server, tool, v.Reason)
			if err != nil {
				return coreag.ToolResult{}, err
			}
			if !proceed {
				return res, nil
			}

		default:
			// An unrecognised or empty policy string is a configuration
			// error, and a configuration error on the permission surface
			// must not read as "allow".
			//
			// The switch previously had no default: any value that was
			// not deny and not confirm_each — a typo in mcp_servers.json,
			// a policy string from a newer build, an empty verdict from a
			// resolver that errored without erroring — fell through to
			// dispatch. That is the same shape of hole the whole mission
			// exists to close, one layer up: a gate whose unhandled case
			// is the permissive one. validatePolicy rejects unknown
			// policies at config load, so reaching here means something
			// bypassed that path; deny and say so.
			logging.L().Error("chat.tool_adapter.unknown_policy",
				"session_id", a.sessionID, "server", server, "tool", tool,
				"policy", v.Policy)
			return coreag.ToolResult{
				Content: fmt.Sprintf("tool %q denied: unrecognised permission policy %q", call.Name, v.Policy),
				IsError: true,
			}, nil
		}
	}

	argsJSON, err := json.Marshal(call.Args)
	if err != nil {
		return coreag.ToolResult{}, fmt.Errorf("chat: marshal args: %w", err)
	}

	// Stuff the session ID into ctx so built-in tools that need it
	// (kenaz__save_artifact) can pull it out without a parameter.
	// Tools that don't read it pay nothing.
	out, err := a.pool.Call(toolloop.WithSessionID(ctx, a.sessionID), server, tool, argsJSON)
	if err != nil {
		// WP02 (tool-error-legibility-01PMDL02): append a conservative
		// environment-drift hint when the raw error signature-matches a
		// well-known case. Never rewrites err.Error().
		return coreag.ToolResult{
			Content: coreag.AppendEnvironmentDriftHint(err.Error()),
			IsError: true,
		}, nil
	}
	return coreag.ToolResult{
		Content: string(out),
		IsError: false,
	}, nil
}

// autonomy-knobs-live-01PMAG02 WP07: declare promptSkipSet as the
// runtime consumer for the two knobs it reads.
func init() {
	knobcoverage.Register[autonomy.ResolvedKnobs]("AutoApproveFamilies", "chat.promptSkipSet")
	knobcoverage.Register[autonomy.ResolvedKnobs]("DestructiveActionPosture", "chat.promptSkipSet")
}

// promptSkipSet translates resolved autonomy knobs into the set of tool
// families whose confirm_each verdicts skip the confirmation prompt
// (confirm-each-enforcement-01PMAG05 WP04). This is the mechanism FR-004
// needs; 01PMAG02 WP05 owns which postures feed it.
//
// Two knobs feed the set:
//
//   - autoApproveFamilies contributes its members directly. The four
//     canonical names are shared by value with core/toolloop, so the
//     translation is a copy, not a mapping table.
//   - destructiveActionPosture: "cedar-only" adds the destructive
//     family — the posture's whole meaning is "let Cedar decide, don't
//     ask me". Under "confirm" (every tier below bold) destructive tools
//     confirm even when their sibling families are auto-approved.
//
// Nothing here can widen past an explicit Cedar deny: the deny verdict
// is handled before the skip set is consulted.
func promptSkipSet(knobs autonomy.ResolvedKnobs) toolloop.PromptSkipSet {
	set := toolloop.NewPromptSkipSet(knobs.AutoApproveFamilies.Sorted()...)
	if knobs.DestructiveActionPosture == autonomy.DestructiveCedarOnly {
		set = set.Add(toolloop.FamilyDestructive)
	}
	return set
}

// resolveConfirmEach decides a `confirm_each` verdict
// (confirm-each-enforcement-01PMAG05 WP01–WP05).
//
// Returns (result, proceed, err):
//
//	proceed=true               → dispatch the call.
//	proceed=false, err==nil    → denied; `result` is the tool error the
//	                             model reads (same shape as a policy deny).
//	err!=nil                   → the run's context was cancelled while
//	                             parked; the kernel aborts the turn.
//
// # The order of the ladder
//
// Six branches can decide, and the order is the point. Cheapest and
// most-specific first, prompt last, and every rung emits exactly one
// audit record naming which rung it was (FR-007) — a security control
// that decides silently is the defect this mission repaired, and "decides
// silently on a path nobody thought about" is the same defect.
//
//  1. Autonomy prompt-skip set — the session's posture auto-approves
//     this tool's FAMILY. Per-family, per-call: `autoApproveFamilies:
//     [read]` auto-approves reads and still confirms writes. A tool the
//     classifier cannot place is FamilyUnknown and never skipped.
//  2. Settings.ConfirmEachEnabled() == false — the user turned the
//     feature off, so the prompt is not offered at all (FR-006).
//  3. Session grant — a prior "allow for this session" answer.
//  4. Persisted grant — a durable "always allow" rule.
//  5. Headless — the deployment's headless policy decides (default
//     deny). Reached two ways: no prompt channel is attached, or the
//     operator explicitly declared the deployment headless
//     (HARNESS_CONFIRM_EACH_HEADLESS set). The second leg exists
//     because every shipped binary attaches a broker publisher, so
//     channel presence alone can never be false in production — an
//     explicit declaration is the only way a served-no-UI deployment
//     can reach its configured policy instead of parking forever.
//  6. Prompt. Park on the bus and block until the user answers.
//
// Explicit Cedar deny is not on this ladder because it never reaches it:
// the deny verdict short-circuits in Call, before any of this, under
// every posture and every knob value (FR-005).
//
// There is no timeout on rung 6: an unanswered confirmation parks the
// run until the user answers, the batch is cancelled, or ctx is
// cancelled (owner decision 1). Do not add a deadline here.
func (a *kernelToolAdapter) resolveConfirmEach(
	ctx context.Context,
	call coreag.ToolCall,
	server, tool, reason string,
) (coreag.ToolResult, bool, error) {
	family := toolloop.ClassifyToolFamily(server, tool)

	// 1. Autonomy prompt-skip set (WP04). This is what makes the two
	//    permission branches genuinely different: skipPrompt finally has
	//    a prompt to skip.
	if a.autonomy != nil && promptSkipSet(a.autonomy(ctx, a.sessionID)).SkipsTool(server, tool) {
		a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Family:    family,
			Path:      audit.ToolConfirmPathSkipSet,
			Approved:  true,
			Reason:    "autonomy posture auto-approves family " + family,
		})
		return coreag.ToolResult{}, true, nil
	}

	// 2. Settings.ConfirmEachEnabled() (FR-006). The toggle's Go
	//    consumer: off means the prompt is never offered and the verdict
	//    behaves as auto-allow. Documented here and audited below so the
	//    widened posture is recoverable from the trail rather than being
	//    indistinguishable from an approval nobody gave.
	if a.confirmEnabled != nil && !a.confirmEnabled() {
		a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Family:    family,
			Path:      audit.ToolConfirmPathToggleOff,
			Approved:  true,
			Reason:    "confirm-each disabled in Settings",
		})
		return coreag.ToolResult{}, true, nil
	}

	// 3. Session grant — "allow for this session" (WP03).
	if a.sessionGrants.Has(a.sessionID, server, tool) {
		a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Family:    family,
			Path:      audit.ToolConfirmPathSessionGrant,
			Approved:  true,
			Reason:    "session grant",
		})
		return coreag.ToolResult{}, true, nil
	}

	// 4. Persisted grant — "always allow" (WP03). Consulted on every
	//    call rather than cached, so revoking from Settings takes effect
	//    immediately.
	if a.persistGrants != nil && a.persistGrants.HasGrant(server, tool) {
		a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Family:    family,
			Path:      audit.ToolConfirmPathPersistedGrant,
			Approved:  true,
			Reason:    "persisted allow rule",
		})
		return coreag.ToolResult{}, true, nil
	}

	// 5. Headless (WP05, owner decision 4). Three ways in: no prompt
	//    channel is attached, the operator explicitly declared the
	//    deployment headless, or this specific run is unattended
	//    (model-scheduled-jobs-01PMSJ01 WP05, spec.md §5.4 H-1). Auto-
	//    allowing was the old silent behaviour; it is now reachable only
	//    as an explicit, audited operator choice, and the default is
	//    deny.
	unattended := runposture.IsUnattended(ctx)
	if unattended || a.headlessExplicit || a.confirm == nil || !a.confirm.HasChannel() {
		policy := a.headless
		if unattended {
			// A per-run unattended posture always denies, regardless of
			// the DEPLOYMENT's configured headless-allow policy.
			// HeadlessAllow answers "does this whole deployment have a
			// human anywhere" (a served, no-UI install); it must not
			// silently permit one SCHEDULED run inside an otherwise-
			// interactive desktop build just because the two conditions
			// happen to route through the same rung. Spec.md §5.4 is
			// explicit: "confirm_each resolves at rung 5 with
			// toolloop.HeadlessDeny" — unconditionally, for this class.
			policy = toolloop.HeadlessDeny
		}
		if policy != toolloop.HeadlessAllow {
			policy = toolloop.HeadlessDeny
		}
		approved := policy == toolloop.HeadlessAllow
		policyReason := "no confirmation channel is attached; headless policy: " + string(policy)
		if unattended {
			policyReason = "unattended run (scheduled dispatch): confirm_each denies by construction"
		} else if a.headlessExplicit {
			policyReason = "deployment declared headless by operator; headless policy: " + string(policy)
		}
		a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Family:    family,
			Path:      audit.ToolConfirmPathHeadlessPolicy,
			Approved:  approved,
			Reason:    policyReason,
		})
		if approved {
			logging.L().Warn("chat.tool_adapter.confirm_headless_allow",
				"session_id", a.sessionID, "server", server, "tool", tool,
				"detail", "confirm_each dispatched without confirmation under an explicit headless allow policy")
			return coreag.ToolResult{}, true, nil
		}
		logging.L().Warn("chat.tool_adapter.confirm_no_channel",
			"session_id", a.sessionID, "server", server, "tool", tool)
		return coreag.ToolResult{
			Content: fmt.Sprintf("tool %q denied: %s", call.Name, policyReason),
			IsError: true,
		}, false, nil
	}

	// 6. Prompt.
	//
	// One batch per turn: every call dispatched under the same
	// WithConfirmBatch context shares an ID so the frontend renders one
	// modal with N rows. Ungrouped callers get a batch of one.
	batchID := toolloop.ConfirmBatchFromContext(ctx)
	if batchID == "" {
		batchID = toolloop.NewConfirmID("batch")
	}

	req := toolloop.ConfirmRequest{
		SessionID: a.sessionID,
		CallID:    toolloop.NewConfirmID("confirm"),
		BatchID:   batchID,
		Server:    server,
		Tool:      tool,
		// Structural description only — never raw argument values.
		ArgsSummary: toolloop.SummarizeArgs(call.Args),
		Reason:      reason,
	}

	decision, err := a.confirm.Pending(ctx, req)
	if err != nil {
		// Context cancellation (run stopped / session torn down) or a
		// caller bug. Either way the call must not dispatch. No audit
		// record: nothing was decided, which is exactly owner decision
		// 1's "elapsed time resolves to nothing".
		return coreag.ToolResult{}, false, fmt.Errorf("chat: tool confirmation: %w", err)
	}

	persisted := false
	if decision.Approved {
		// "Remember" only ever widens on an approval. "Deny, but
		// remember to allow" is not a thing a user can mean.
		if decision.RememberSession {
			a.sessionGrants.Grant(a.sessionID, server, tool)
		}
		if decision.Persist {
			persisted = a.writePersistentGrant(ctx, server, tool)
		}
	}

	a.auditConfirm(ctx, audit.ToolConfirmDecisionPayload{
		SessionID:       a.sessionID,
		CallID:          req.CallID,
		BatchID:         req.BatchID,
		Server:          server,
		Tool:            tool,
		Family:          family,
		Path:            audit.ToolConfirmPathPrompted,
		Approved:        decision.Approved,
		RememberSession: decision.Approved && decision.RememberSession,
		Persisted:       persisted,
		Reason:          decision.Reason,
	})

	if !decision.Approved {
		denyReason := decision.Reason
		if denyReason == "" {
			denyReason = "not approved by user"
		}
		return coreag.ToolResult{
			Content: fmt.Sprintf("tool %q denied: %s", call.Name, denyReason),
			IsError: true,
		}, false, nil
	}
	return coreag.ToolResult{}, true, nil
}

// writePersistentGrant handles the "always allow" control. Returns
// whether a durable rule was actually written.
//
// A failed write is audited as Written=false rather than swallowed: the
// call still dispatched (the user did approve it), but the user was told
// they would not be asked again, and if that promise did not land the
// trail has to say so. Silently degrading to a one-shot approve is how
// a user discovers the grant did not stick three prompts later.
func (a *kernelToolAdapter) writePersistentGrant(ctx context.Context, server, tool string) bool {
	if a.persistGrants == nil {
		logging.L().Warn("chat.tool_adapter.confirm_persist_unavailable",
			"session_id", a.sessionID, "server", server, "tool", tool,
			"detail", "always-allow requested but no persistent grant store is wired; approving once")
		a.auditGrant(ctx, audit.ToolConfirmGrantWrittenPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Written:   false,
			Error:     "no persistent grant store wired",
		})
		return false
	}
	if err := a.persistGrants.WriteGrant(server, tool); err != nil {
		logging.L().Error("chat.tool_adapter.confirm_persist_failed",
			"session_id", a.sessionID, "server", server, "tool", tool,
			"err", err.Error())
		a.auditGrant(ctx, audit.ToolConfirmGrantWrittenPayload{
			SessionID: a.sessionID,
			Server:    server,
			Tool:      tool,
			Written:   false,
			Error:     err.Error(),
		})
		return false
	}
	grantID := ""
	if idr, ok := a.persistGrants.(interface {
		GrantID(server, tool string) string
	}); ok {
		grantID = idr.GrantID(server, tool)
	}
	a.auditGrant(ctx, audit.ToolConfirmGrantWrittenPayload{
		SessionID: a.sessionID,
		Server:    server,
		Tool:      tool,
		GrantID:   grantID,
		Written:   true,
	})
	return true
}

// auditConfirm emits one KindToolConfirmDecision record. Never blocks
// the decision: a missing emitter is silence, a marshal failure is a
// WARN via MustEmit.
func (a *kernelToolAdapter) auditConfirm(ctx context.Context, p audit.ToolConfirmDecisionPayload) {
	if a.auditEmitter == nil {
		return
	}
	audit.MustEmit(ctx, a.auditEmitter, audit.KindToolConfirmDecision, p, a.clock())
}

// auditGrant emits one KindToolConfirmGrantWritten record.
func (a *kernelToolAdapter) auditGrant(ctx context.Context, p audit.ToolConfirmGrantWrittenPayload) {
	if a.auditEmitter == nil {
		return
	}
	audit.MustEmit(ctx, a.auditEmitter, audit.KindToolConfirmGrantWritten, p, a.clock())
}

func (a *kernelToolAdapter) clock() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// splitName parses a "<server>__<tool>" name. Returns ok=false when
// the input has no separator or either side is empty after split.
const toolNameSeparator = "__"

func splitName(name string) (server, tool string, ok bool) {
	for i := 0; i+len(toolNameSeparator) <= len(name); i++ {
		if name[i:i+len(toolNameSeparator)] == toolNameSeparator {
			s := name[:i]
			t := name[i+len(toolNameSeparator):]
			if s == "" || t == "" {
				return "", "", false
			}
			return s, t, true
		}
	}
	return "", "", false
}
