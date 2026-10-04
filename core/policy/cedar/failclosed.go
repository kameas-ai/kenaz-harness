package cedar

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// FailClosedActions is the set of Cedar actions the graph-path gate
// refuses while a user policy source failed to load (graph-fs-gate-
// 01GFSG01 WP01, FR-3, owner ruling 2026-10-03: "corrupt policy fails
// closed"). It is exactly what the agent-graph PolicyGateAdapter
// evaluates for side-effecting work — file read/write, state write and
// tool execution (both the coarse use_tool family and the finer
// tool_exec sibling). ActionStateRead is deliberately NOT here: the
// read_file node evaluates ActionFileRead first, so a file read is
// already refused before the finer state_read check runs, and
// non-file state reads (read_bash_output) touch only harness-owned
// buffers.
var FailClosedActions = []string{
	ActionFileRead,
	ActionFileWrite,
	ActionStateWrite,
	ActionToolExec,
	ActionUseTool,
}

// UserPolicyLoadError reports why the user's on-disk policy bundle is
// not fully in force, or nil when it is.
//
// Engine.Reload deliberately does not abort on a per-file parse
// failure (the embedded defaults still load), and records the failure
// only in the per-file status ListPolicies returns. That degraded mode
// is silently PERMISSIVE: the shipped defaults permit file_write /
// file_read for every resource, so a user forbid rule in a file that
// failed to parse simply stops existing. This accessor is the signal a
// fail-closed caller needs to tell "the user's policy is in force"
// apart from "the user's policy was dropped".
//
// Only non-embedded entries count — the embedded bundle is compiled
// into the binary and covered by its own tests. A PolicyDir read error
// (recorded by Reload as a non-embedded entry named PolicyDir) counts
// too: an unreadable policy directory hides the user's rules exactly
// as a parse failure does. The result is computed on every call, so a
// SavePolicy + ReloadPolicies that fixes the file lifts the
// fail-closed posture without a restart.
func (e *Engine) UserPolicyLoadError() error {
	if e == nil {
		return nil
	}
	e.filesMu.RLock()
	defer e.filesMu.RUnlock()
	var msgs []string
	for _, f := range e.files {
		if f.Embedded || f.ParseOK {
			continue
		}
		msgs = append(msgs, fmt.Sprintf("%s: %s", f.Name, f.ParseErr))
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("policy failed to load (%s)", strings.Join(msgs, "; "))
}

// RecordDecision appends d to the engine's audit-decision log (the
// same store Evaluate writes and RecentDecisions / the policy view's
// audit panel read). For wrappers that decide WITHOUT consulting the
// bundle — the fail-closed gate below — so their denials are audited
// exactly like a forbid-rule match. nil-safe.
func (e *Engine) RecordDecision(d Decision) {
	if e == nil || e.decisions == nil {
		return
	}
	e.decisions.Append(d)
}

// FailClosedOnLoadError returns the Gate the agent-graph path binds
// (graph-fs-gate-01GFSG01 WP01 / FR-3).
//
// Three states, kept deliberately distinct:
//
//   - engine != nil: every Evaluate delegates to the engine, EXCEPT
//     that a FailClosedActions action is denied while
//     engine.UserPolicyLoadError() is non-nil (a corrupt or unreadable
//     user policy). The denial names the failing file(s) and is
//     appended to the engine's decision log.
//   - engine == nil && bootErr != nil: the engine could not be built
//     at all. FailClosedActions are denied with bootErr as the reason;
//     every other action stays NotApplicable (the pre-existing
//     AllowAll posture for actions this mission does not cover).
//   - engine == nil && bootErr == nil: no policy configured at all
//     (the nil-Core / empty-DataDir test chassis). That is absence, not
//     corruption — AllowAll, exactly as before.
func FailClosedOnLoadError(engine *Engine, bootErr error) Gate {
	if engine == nil && bootErr == nil {
		return AllowAll{}
	}
	return &failClosedGate{engine: engine, bootErr: bootErr}
}

type failClosedGate struct {
	engine  *Engine
	bootErr error
}

func (g *failClosedGate) Evaluate(
	ctx context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	contextAttrs map[cedarlib.String]cedarlib.Value,
) Decision {
	loadErr := g.bootErr
	if g.engine != nil {
		loadErr = g.engine.UserPolicyLoadError()
	}
	if loadErr == nil || !isFailClosedAction(action) {
		if g.engine == nil {
			return AllowAll{}.Evaluate(ctx, principal, action, resource, contextAttrs)
		}
		return g.engine.Evaluate(ctx, principal, action, resource, contextAttrs)
	}
	if principal.IsZero() {
		principal = UserUID()
	}
	d := Decision{
		Outcome:       Deny,
		Action:        action,
		Principal:     principal.String(),
		Resource:      resource.String(),
		MatchedPolicy: "fail-closed/policy-load-error",
		Reason: fmt.Sprintf("%v — until it is fixed and reloaded, chat tool calls and agent-graph file reads/writes are blocked (Settings › Policy lists the failing file)",
			loadErr),
		EvaluatedAt: time.Now().UTC(),
	}
	if g.engine != nil {
		g.engine.RecordDecision(d)
	}
	slog.Error("cedar.graph_fail_closed", "action", action, "resource", d.Resource, "err", loadErr.Error())
	return d
}

func isFailClosedAction(action string) bool {
	for _, a := range FailClosedActions {
		if a == action {
			return true
		}
	}
	return false
}

var _ Gate = (*failClosedGate)(nil)
