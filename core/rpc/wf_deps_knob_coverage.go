// wf_deps_knob_coverage.go registers every exported field of
// corewf.Deps with core/wiring/knobcoverage (automation-actually-runs-
// 01PMZ404 UNIT-17, G-1b).
//
// Why this exists (spec §8 G-1b): G-1a (scripts/ci/cmd/checkseams)
// answers "does this interface have a non-test implementer anywhere in
// the tree" — it cannot see (a) an interface that HAS an implementer
// but is never assigned into the production Deps literal, or (b) a
// plain (non-interface) field like DefaultLLMProfile / SessionID,
// which isn't an interface at all. Both classes are exactly what made
// five of corewf.Deps's thirteen fields silently inert for a release
// cycle (UNIT-5/6/7/8/10's findings) before this mission's manual
// sweep found them. G-1b is the general per-struct mechanism
// (core/wiring/knobcoverage) already built for autonomy.ResolvedKnobs
// and agentgraph.ModelAttrs, applied here for the first time to
// corewf.Deps.
//
// Registrations are grouped by construction site in core/rpc/api.go
// (the ONLY production corewf.Deps literal, api.go:~2991 `wfDeps :=
// corewf.Deps{}`) and core/rpc/wf_adapters.go (the adapter types those
// assignments construct).
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/wiring/knobcoverage"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

func init() {
	knobcoverage.Register[corewf.Deps]("LLM",
		"core/rpc/api.go wfDeps.LLM = &wfLLMStreamerAdapter{...} (guarded on stack.reg != nil); "+
			"consumed by modelTurnRunner for the model_turn step kind (runners.go)")
	// automation-actually-runs-01PMZ404 UNIT-17: DefaultLLMProfile has a
	// real READER (runners.go:76-103, the model_turn profile-resolution
	// fallback chain, and the "no profile" error text at :173 names it
	// explicitly) but NO production WRITER — core/rpc/api.go never
	// assigns wfDeps.DefaultLLMProfile (grep -n
	// 'wfDeps\.DefaultLLMProfile\s*=' core/rpc/api.go: zero hits).
	// DefaultProfileFunc (registered below) is the field production
	// actually wires for this purpose; DefaultLLMProfile exists for
	// "back-compat with existing tests" per its own doc comment
	// (types.go:646-647) and is always empty in the running app. This
	// is precisely the class G-1b exists to see that G-1a cannot: a
	// non-interface field with a reader but no production value.
	knobcoverage.RegisterDeferred[corewf.Deps]("DefaultLLMProfile",
		"no production writer assigns this field (DefaultProfileFunc supersedes it in "+
			"core/rpc/api.go); it is read only as a test back-compat fallback per its own doc "+
			"comment (core/workflows/types.go:646-647). Blocker: none planned — this is a "+
			"documented always-empty-in-production field, not a gap awaiting a fix. Owner: alec. "+
			"Date: 2026-09-12.")
	knobcoverage.Register[corewf.Deps]("DefaultProfileFunc",
		"core/rpc/api.go wfDeps.DefaultProfileFunc = func() string {...} (guarded on "+
			"personalForLLM != nil); resolved lazily at Run time by modelTurnRunner (runners.go)")
	knobcoverage.Register[corewf.Deps]("ToolDiscoverer",
		"core/rpc/api.go wfDeps.ToolDiscoverer = &wfToolDiscovererAdapter{...} (guarded on "+
			"stack.toolDiscoverer != nil); consumed by modelTurnRunner's tool loop (runners.go)")
	knobcoverage.Register[corewf.Deps]("ToolDispatcher",
		"core/rpc/api.go wfDeps.ToolDispatcher = &wfToolDispatcherAdapter{...} (guarded on "+
			"stack.wrappedPool != nil); consumed by modelTurnRunner's bounded tool loop (runners.go)")
	knobcoverage.Register[corewf.Deps]("Tools",
		"core/rpc/api.go wfDeps.Tools = &wfToolCallerAdapter{...} (automation-actually-runs-"+
			"01PMZ404 UNIT-6, same guard as ToolDispatcher above); consumed by toolCallRunner "+
			"for the tool_call step kind (runners.go)")
	knobcoverage.Register[corewf.Deps]("MCP",
		"core/rpc/api.go wfDeps.MCP = &wfMCPCallerAdapter{...} (guarded on stack.dispatchPool "+
			"!= nil); consumed by mcpCallRunner for the mcp_call step kind (runners.go)")
	knobcoverage.Register[corewf.Deps]("Artifacts",
		"core/rpc/api.go wfDeps.Artifacts = &wfArtifactsAdapter{...} (automation-actually-runs-"+
			"01PMZ404 UNIT-5, guarded on artStore != nil && artMgr != nil); consumed by "+
			"readArtifactRunner / writeArtifactRunner (runners.go)")
	// automation-actually-runs-01PMZ404 UNIT-17: SessionID is the field
	// spec.md §8 G-1b names by name as the seed case for this exact
	// gap. It has a real READER (writeArtifactRunner's session-id
	// fallback, runners.go) but no production WRITER — the run-scoped
	// value arrives via RunContext.ParentSessionID instead (UNIT-5's
	// five-link chain: slashcmd.Env.SessionID -> WorkflowRunOptions ->
	// RunRequest -> RunOptions.ParentSessionID -> RunContext), which
	// wins whenever it is non-empty per the field's own doc comment
	// (types.go:645-654). Deps.SessionID stays as the documented
	// construction-time fallback for a caller that has a session before
	// Deps is built; no production caller does today.
	knobcoverage.RegisterDeferred[corewf.Deps]("SessionID",
		"no production writer assigns this field; the run-scoped session id arrives via "+
			"RunContext.ParentSessionID instead (UNIT-5's chain) and wins when non-empty. "+
			"Blocker: none planned — this is the documented construction-time fallback, not a "+
			"gap awaiting a fix; see core/workflows/types.go:645-654. Owner: alec. Date: 2026-09-12.")
	knobcoverage.Register[corewf.Deps]("NetAuthz",
		"core/rpc/api.go wfDeps.NetAuthz = &wfNetworkAuthorizerAdapter{...} (unconditional); "+
			"consumed by web_fetch / web_scrape runners' authz check (runners.go), with an "+
			"explicit forbid rule in default_workflows_policy.cedar's strict arm (UNIT-7)")
	knobcoverage.Register[corewf.Deps]("Notifier",
		"core/rpc/api.go wfDeps.Notifier = &wfNotifierAdapter{...} (unconditional); consumed "+
			"by notifyRunner for the \"os\" notify surface (runners_notify.go)")
	knobcoverage.Register[corewf.Deps]("Audit",
		"core/rpc/api.go wfDeps.Audit = &wfNotifyAuditBridge{...} (automation-actually-runs-"+
			"01PMZ404 UNIT-8, unconditional); consumed by notifyRunner.emitSent for every "+
			"surface a notify step dispatches to successfully (runners_notify.go)")
	knobcoverage.Register[corewf.Deps]("NetworkAudit",
		"core/rpc/api.go wfDeps.NetworkAudit = &acpAuditBridge{...} (audit-that-tells-the-"+
			"truth-01PMZA10 UNIT-5, unconditional); consumed by web_fetch / web_scrape runners "+
			"for KindWorkflowNetworkFetch (runners.go)")
}
