package cedar

import (
	"context"
	"time"

	cedarlib "github.com/cedar-policy/cedar-go"
)

// PostureModePlanMode is the canonical string value for the plan_mode
// autonomy posture. Declared here so Cedar consumers can reference it
// without importing core/autonomy (which is a leaf package). The value
// is identical to autonomy.PostureModePlanMode.
const PostureModePlanMode = "plan_mode"

// PlanModeDeniedFamilies lists the four Cedar action families that are
// denied when the session is in plan_mode. Exported so downstream
// consumers (branch-subagent, cedar-policy-editor-ui) can enumerate
// the denied surface without reimplementing it.
//
// The families match the spec FR-003 taxonomy:
//   - "tool.write.*"    — write-class builtin tools
//   - "bash.write_paths" — bash commands that mutate the filesystem
//   - "mcp.write.*"     — MCP server write-family tools
//   - "memory.persist"  — memory writes
//
// In Cedar wire-shape these map to the Action* constants below.
var PlanModeDeniedFamilies = []string{
	"tool.write",
	"bash.write_paths",
	"mcp.write",
	"memory.persist",
}

// PlanModeDeniedActions is the canonical set of Cedar action IDs that are
// denied while in plan_mode. This slice is the machine-readable source of
// truth consumed by:
//   - WithPostureMode (this package) — enforcement
//   - Cedar.ListPlanModeActions RPC (WP08) — UI surfacing
//   - branch-subagent-interactive-01KZNP3B — default posture wiring
//
// Write-class actions:
//
//	ActionFileWrite          — direct file write gate
//	ActionWriteFilesystem    — FilesystemOp write gate (universal-permission)
//	ActionStateWrite         — State write gate (agent-kernel-graph)
//	ActionMemoryWrite        — memory write gate
//	ActionRunBashCommand     — bash commands (all are potentially write;
//	                           plan_mode denies the entire family)
//	ActionWorkflowSave       — workflow.save
//	ActionWorkflowDelete     — workflow.delete
//	ActionArtifactUpdate     — artifact update (write variant)
//	ActionScheduledRunCreate  — scheduled run creation/update
//	ActionScheduledRunDelete  — scheduled run deletion
//	ActionScheduledRunExecute — scheduled run execution (added WP09,
//	                            model-scheduled-jobs-01PMSJ01, ruling
//	                            B-3: "plan mode should not fire
//	                            schedules" — closes the gap where Create
//	                            and Delete were denied in plan_mode but
//	                            Execute was not).
//
// Read-class / passive actions (NOT in this list):
//
//	ActionFileRead, ActionReadFilesystem, ActionStateRead,
//	ActionUseTool (most), ActionToolReadGlob, ActionToolReadGrep,
//	ActionWorkflowRun, ActionPassive, ActionElicitAsk,
//	ActionToolTodoRead, ActionToolTodoWrite.
//
// Note: ActionToolTodoWrite is left out of the deny set — the todo list
// is session-internal state and plan-mode navigation is enhanced by
// letting the model maintain a todo list during exploration.
var PlanModeDeniedActions = []string{
	ActionFileWrite,
	ActionWriteFilesystem,
	ActionStateWrite,
	ActionMemoryWrite,
	ActionRunBashCommand,
	ActionWorkflowSave,
	ActionWorkflowDelete,
	ActionArtifactUpdate,
	ActionScheduledRunCreate,
	ActionScheduledRunDelete,
	ActionScheduledRunExecute,
}

// postureModeGate wraps a real Gate and intercepts Evaluate calls when
// the session is in plan_mode, denying write-class actions before they
// reach the underlying policy bundle.
//
// The gate is stateless: the PostureMode is captured at construction and
// the underlying gate is consulted on every non-denied action. This
// composability means existing Cedar rules still apply for allowed
// families — plan_mode narrows, never broadens.
type postureModeGate struct {
	mode      string
	inner     Gate
	deniedSet map[string]struct{}
}

// WithPostureMode returns a Gate decoration that, when mode equals
// PostureModePlanMode, enforces the read-only constraint before delegating
// to inner. For any other mode value the decoration is a transparent
// pass-through. inner must not be nil.
//
// The returned Gate is safe for concurrent use. It holds no mutable state;
// the denied-action set is constructed once at decoration time.
//
// Usage (in the toolloop / Cedar wiring):
//
//	gate := cedar.WithPostureMode(sessionPostureMode, cedarEngine)
//	// gate.Evaluate now denies writes when in plan_mode.
func WithPostureMode(mode string, inner Gate) Gate {
	if inner == nil {
		panic("cedar.WithPostureMode: nil inner Gate")
	}
	if mode != PostureModePlanMode {
		// No active posture mode restriction — pass-through.
		return inner
	}
	denied := make(map[string]struct{}, len(PlanModeDeniedActions))
	for _, a := range PlanModeDeniedActions {
		denied[a] = struct{}{}
	}
	return &postureModeGate{
		mode:      mode,
		inner:     inner,
		deniedSet: denied,
	}
}

// Evaluate intercepts write-class actions and denies them with a
// plan_mode:read_only reason before reaching the inner gate. All other
// actions are forwarded to inner unchanged.
func (g *postureModeGate) Evaluate(
	ctx context.Context,
	principal cedarlib.EntityUID,
	action string,
	resource cedarlib.EntityUID,
	contextAttrs map[cedarlib.String]cedarlib.Value,
) Decision {
	if _, denied := g.deniedSet[action]; denied {
		if principal.IsZero() {
			principal = UserUID()
		}
		return Decision{
			Outcome:       Deny,
			Action:        action,
			Principal:     principal.String(),
			Resource:      resource.String(),
			MatchedPolicy: "plan_mode:read_only",
			Reason:        "plan_mode:read_only",
			EvaluatedAt:   time.Now().UTC(),
		}
	}
	return g.inner.Evaluate(ctx, principal, action, resource, contextAttrs)
}
