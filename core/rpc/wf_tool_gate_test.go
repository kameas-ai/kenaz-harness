package rpc

// wf_tool_gate_test.go — workflow-tool-permission-gate.
//
// Before this fix, wfMCPCallerAdapter.Call (mcp_call steps) and
// wfToolDispatcherAdapter.Dispatch (model_turn's tool loop) called
// pool.Call directly — no toolloop.PermissionResolver.Resolve, no
// cedar.CheckTool, no confirm-each. A scheduled (unattended) workflow
// could invoke any configured MCP tool, including write-capable ones,
// with nothing standing in the way. These tests prove the closed gap
// rather than asserting it:
//
//   - TestWfMCPCaller_ToolExecForbidDiscriminates and
//     TestWfToolDispatcher_ToolExecForbidDiscriminates drive a REAL
//     cedar.Engine against BOTH dispatch paths with the SAME forbid
//     shape default_policy.cedar invites a user to write, paired with a
//     permit leg on an unrelated tool (CLAUDE.md's enforce()-
//     NotApplicable trap: a deny-only test cannot distinguish "the
//     policy denied this" from "the adapter blanket-refuses
//     everything").
//   - TestWfToolGate_PermsDenyBlocksDispatch proves the perms-ladder
//     leg (independent of Cedar) on both paths.
//   - TestWfToolGate_ConfirmEach_* prove the confirm-each ladder:
//     no-channel denies by default, and a live bus honours the parked
//     decision — mirroring slashToolDispatcherAdapter's own tests
//     exactly, since wfToolGate reuses that adapter's ladder.
//   - TestWfToolGate_UnattendedConfirmEach_DeniesEvenWithDeploymentHeadlessAllow
//     is THE design-decision test the mission asked for: an unattended
//     workflow run must deny a confirm_each verdict unconditionally,
//     even when the deployment is configured HeadlessAllow — mirroring
//     chat's kernel_tool_adapter_unattended_test.go
//     (TestConfirmLadder_UnattendedIgnoresDeploymentHeadlessAllow)
//     exactly, because it is the SAME core/runposture mechanism.
//     TestWfToolGate_AttendedConfirmEach_SameConfigDispatches is the
//     paired "not a blanket regression" leg: the identical
//     HeadlessAllow config, without the unattended marker, still
//     dispatches.
//   - TestWfSchedDispatcher_ScheduledMarksContextUnattended_RunNowDoesNot
//     proves the OTHER half of the wire: wfSchedDispatcher.Dispatch
//     marks ctx unattended only for a cron tick (scheduled=true), not
//     for a human-clicked "Run now" (scheduled=false).
//   - TestWfMCPCaller_ProductionWire_ToolExecForbidBlocksBeforePoolLookup
//     drives the ACTUAL core/rpc/api.go wiring (cedarWiringAPI: real
//     core.New + rpc.New, real Cedar, real sqlite-backed DataDir) end
//     to end through api.wfScheduler.RunNow, proving New() actually
//     assigns a non-nil wfToolGate to wfDeps.MCP — not just that the
//     type enforces the ladder when hand-built with one. Denying an
//     mcp_call step naming a nonexistent server proves the gate runs
//     BEFORE the pool lookup: if the gate had not run, the visible
//     failure would be "server not found", not "denied".

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	"github.com/kameas-ai/kenaz-harness/core/runposture"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	corewf "github.com/kameas-ai/kenaz-harness/core/workflows"
)

// ─── shared fakes ─────────────────────────────────────────────────────────

// mcpCallGatePool is a race-safe fake coremcp.Pool (Open/Close/Tools/Call)
// for wfMCPCallerAdapter — coremcp.Pool is a wider interface than
// toolloop.MCPPool (adds Open/Close), so recordingPool (defined in
// slash_tool_dispatcher_adapter_test.go, same package) doesn't satisfy
// it. CLAUDE.md's race-safe-fakes pattern: writes go through mu, reads
// go through snapshot().
type mcpCallGatePool struct {
	mu     sync.Mutex
	calls  []recordedCall
	output json.RawMessage
	err    error
}

func (p *mcpCallGatePool) Open(context.Context, []coremcp.ServerSpec) error { return nil }
func (p *mcpCallGatePool) Close(context.Context) error                      { return nil }
func (p *mcpCallGatePool) Tools(context.Context) ([]coremcp.Tool, error)    { return nil, nil }

func (p *mcpCallGatePool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.calls = append(p.calls, recordedCall{Server: server, Tool: tool, Args: append(json.RawMessage(nil), args...)})
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return p.output, nil
}

func (p *mcpCallGatePool) snapshot() []recordedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// ─── mcp_call path (wfMCPCallerAdapter) ────────────────────────────────────

// TestWfMCPCaller_ToolExecForbidDiscriminates proves the mcp_call
// dispatch path (wfMCPCallerAdapter.Call) consults a real Cedar engine
// via wfToolGate before ever reaching the pool — the EXACT forbid shape
// default_policy.cedar invites a user to write
// (`forbid (..., action == Action::"tool_exec", resource ==
// Tool::"...")`), paired with a permit leg on an unrelated tool.
func TestWfMCPCaller_ToolExecForbidDiscriminates(t *testing.T) {
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	if err := e.SetPolicyText("tool-exec-forbid.cedar", []byte(`
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"gmail__send_email"
);
`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	t.Run("deny leg: forbidden tool", func(t *testing.T) {
		pool := &mcpCallGatePool{output: json.RawMessage(`"ok"`)}
		adapter := &wfMCPCallerAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		_, err := adapter.Call(context.Background(), "gmail", "send_email", map[string]any{"to": "x@example.com"})
		if err == nil {
			t.Fatal("expected a denial for gmail__send_email (tool_exec forbid), got nil")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Errorf("err = %v, want it to name a denial", err)
		}
		if !cedar.IsPolicyDenied(err) {
			t.Errorf("err = %v, want it to wrap a *cedar.PolicyDeniedError", err)
		}
		if n := len(pool.snapshot()); n != 0 {
			t.Errorf("pool was called %d times, want 0 — a tool_exec-denied call must never reach the pool", n)
		}
	})

	t.Run("permit leg: same forbid, different tool", func(t *testing.T) {
		pool := &mcpCallGatePool{output: json.RawMessage(`"ok"`)}
		adapter := &wfMCPCallerAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		_, err := adapter.Call(context.Background(), "gmail", "search_threads", map[string]any{"query": "is:unread"})
		if err != nil {
			t.Fatalf("gmail__search_threads: unexpected denial: %v", err)
		}
		if n := len(pool.snapshot()); n != 1 {
			t.Fatalf("pool was called %d times, want 1 — an unrelated tool must still dispatch", n)
		}
	})
}

// TestWfMCPCaller_ProductionWire_ToolExecForbidBlocksBeforePoolLookup
// drives the REAL core/rpc/api.go wiring end to end: cedarWiringAPI
// boots a real core.New + rpc.New over a real (sqlite-backed) temp
// DataDir with a real Cedar engine. A workflow with a single mcp_call
// step names a server that was never configured ("ghost-server") — if
// wfDeps.MCP were still the pre-fix ungated adapter, the visible
// failure would be a pool-lookup error ("not installed" /
// "unknown server"); wfMCPCallerAdapter.Call now consults the gate
// BEFORE ever calling pool.Call, so a tool_exec forbid on the exact
// (ghost-server, ghost-tool) pair must surface as a permission denial
// instead — proof New() actually assigned a non-nil gate, not just that
// the adapter type enforces one when hand-built.
func TestWfMCPCaller_ProductionWire_ToolExecForbidBlocksBeforePoolLookup(t *testing.T) {
	api := cedarWiringAPI(t, `
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"ghost-server__ghost-tool"
);
`)
	ctx := context.Background()

	const yaml = `
id: zz-wf-mcpcall-gate-probe
name: "wf mcp_call gate probe"
version: 1
steps:
  - name: call_it
    kind: mcp_call
    server: ghost-server
    tool_name: ghost-tool
`
	saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
	if serr != nil {
		t.Fatalf("Save: %v", serr)
	}
	if api.wfScheduler == nil {
		t.Fatal("wfScheduler is nil")
	}
	api.SetContext(ctx)

	_, err := api.wfScheduler.RunNow(ctx, saved.ID)
	if err == nil {
		t.Fatal("RunNow succeeded against a ghost MCP server under a tool_exec forbid — " +
			"either the gate never ran, or a pool-lookup failure masked the missing check")
	}
	if strings.Contains(err.Error(), "not installed") || strings.Contains(err.Error(), "unknown server") ||
		strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("err = %v names a pool-lookup failure, not a permission denial — "+
			"the gate did not run before the pool lookup", err)
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Fatalf("err = %v, want it to name a Cedar denial", err)
	}
}

// ─── model_turn path (wfToolDispatcherAdapter) ─────────────────────────────

// TestWfToolDispatcher_ToolExecForbidDiscriminates is the SAME proof as
// TestWfMCPCaller_ToolExecForbidDiscriminates for the OTHER dispatch
// path: model_turn's tool loop, via wfToolDispatcherAdapter.Dispatch.
// Both paths must share the identical gate — this and the mcp_call test
// above are the "check both dispatch paths" pairing.
func TestWfToolDispatcher_ToolExecForbidDiscriminates(t *testing.T) {
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	if err := e.SetPolicyText("tool-exec-forbid.cedar", []byte(`
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"kenaz__bash"
);
`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	t.Run("deny leg: forbidden tool", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		adapter := &wfToolDispatcherAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		_, isErr, err := adapter.Dispatch(context.Background(), "kenaz__bash", []byte(`{"cmd":"echo hi"}`))
		if err == nil {
			t.Fatal("expected a denial for kenaz__bash (tool_exec forbid), got nil")
		}
		if !isErr {
			t.Error("Dispatch returned err != nil but isError == false")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Errorf("err = %v, want it to name a denial", err)
		}
		if n := len(pool.snapshot()); n != 0 {
			t.Errorf("pool was called %d times, want 0 — a tool_exec-denied call must never reach the pool", n)
		}
	})

	t.Run("permit leg: same forbid, different tool", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		adapter := &wfToolDispatcherAdapter{pool: pool, gate: &wfToolGate{gate: e}}
		_, _, err := adapter.Dispatch(context.Background(), "kenaz__websearch", []byte(`{"query":"x"}`))
		if err != nil {
			t.Fatalf("kenaz__websearch: unexpected denial: %v", err)
		}
		if n := len(pool.snapshot()); n != 1 {
			t.Fatalf("pool was called %d times, want 1 — an unrelated tool must still dispatch", n)
		}
	})
}

// TestWfToolGate_PermsDenyBlocksDispatch proves the perms-ladder leg
// (independent of Cedar) on the model_turn path: a PolicyDeny
// resolution must never reach the pool.
func TestWfToolGate_PermsDenyBlocksDispatch(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	adapter := &wfToolDispatcherAdapter{
		pool: pool,
		gate: &wfToolGate{perms: fixedResolver{policy: toolloop.PolicyDeny, reason: "blocked by test policy"}},
	}
	_, isErr, err := adapter.Dispatch(context.Background(), "kenaz__bash", []byte(`{}`))
	if err == nil {
		t.Fatal("expected a denial from PolicyDeny, got nil")
	}
	if !isErr {
		t.Error("Dispatch returned err != nil but isError == false")
	}
	if !strings.Contains(err.Error(), "blocked by test policy") {
		t.Errorf("err = %v, want it to carry the resolver's reason", err)
	}
	if n := len(pool.snapshot()); n != 0 {
		t.Errorf("pool was called %d times, want 0 — a PolicyDeny call must never dispatch", n)
	}
}

// ─── confirm-each ladder ────────────────────────────────────────────────

// TestWfToolGate_ConfirmEach_NoChannel_DeniesByDefault mirrors
// TestSlashToolDispatcher_ConfirmEach_NoChannel_DeniesByDefault: a
// confirm_each verdict with no ConfirmBus attached must not silently
// dispatch.
func TestWfToolGate_ConfirmEach_NoChannel_DeniesByDefault(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	adapter := &wfToolDispatcherAdapter{
		pool: pool,
		gate: &wfToolGate{perms: fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"}},
		// confirm left nil: no channel.
	}
	_, isErr, err := adapter.Dispatch(context.Background(), "kenaz__bash", []byte(`{}`))
	if err == nil {
		t.Fatal("no-channel confirm_each: expected a denial, got nil error")
	}
	if !isErr {
		t.Error("Dispatch returned err != nil but isError == false")
	}
	if n := len(pool.snapshot()); n != 0 {
		t.Errorf("pool was called %d times, want 0 — an unanswered confirm_each must never dispatch", n)
	}
}

// TestWfToolGate_ConfirmEach_ParksOnSameBusAndHonoursDecision proves a
// confirm_each verdict parks on a REAL *toolloop.ConfirmBus — the same
// bus type chat and slash park on — and honours the answered decision.
func TestWfToolGate_ConfirmEach_ParksOnSameBusAndHonoursDecision(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		var bus *toolloop.ConfirmBus
		bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
			if err := bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true}); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
		})
		adapter := &wfToolDispatcherAdapter{
			pool: pool,
			gate: &wfToolGate{
				perms:   fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
				confirm: bus,
			},
		}
		_, _, err := adapter.Dispatch(toolloop.WithSessionID(context.Background(), "sess-approve"), "kenaz__bash", []byte(`{}`))
		if err != nil {
			t.Fatalf("approved confirm_each: unexpected error: %v", err)
		}
		if n := len(pool.snapshot()); n != 1 {
			t.Fatalf("pool was called %d times, want 1", n)
		}
	})

	t.Run("denied", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		var bus *toolloop.ConfirmBus
		bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
			if err := bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: false, Reason: "user said no"}); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
		})
		adapter := &wfToolDispatcherAdapter{
			pool: pool,
			gate: &wfToolGate{
				perms:   fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
				confirm: bus,
			},
		}
		_, isErr, err := adapter.Dispatch(toolloop.WithSessionID(context.Background(), "sess-deny"), "kenaz__bash", []byte(`{}`))
		if err == nil {
			t.Fatal("denied confirm_each: expected an error, got nil")
		}
		if !isErr {
			t.Error("Dispatch returned err != nil but isError == false")
		}
		if !strings.Contains(err.Error(), "user said no") {
			t.Errorf("err = %v, want it to carry the user's reason", err)
		}
		if n := len(pool.snapshot()); n != 0 {
			t.Errorf("pool was called %d times, want 0 — a denied confirmation must never dispatch", n)
		}
	})
}

// ─── THE design decision: unattended posture ───────────────────────────────

// TestWfToolGate_UnattendedConfirmEach_DeniesEvenWithDeploymentHeadlessAllow
// is the mission's core design-decision test. Mirrors chat's
// TestConfirmLadder_UnattendedIgnoresDeploymentHeadlessAllow
// (core/rpc/views/agentgraph/chat/kernel_tool_adapter_unattended_test.go)
// exactly, because it is the SAME core/runposture mechanism: an
// unattended workflow run (runposture.Unattended) must deny a
// confirm_each verdict unconditionally — even when the deployment is
// explicitly configured HeadlessAllow, which answers "does this whole
// deployment have a human anywhere," not "is THIS scheduled run
// attended." A live confirm channel is attached (HasChannel()==true) so
// the only thing that can explain a deny is the unattended override,
// not the "no channel" default.
func TestWfToolGate_UnattendedConfirmEach_DeniesEvenWithDeploymentHeadlessAllow(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	bus := toolloop.NewConfirmBus(func(toolloop.ConfirmRequest) {
		t.Fatal("unattended confirm_each must not reach the prompt rung at all")
	})
	if !bus.HasChannel() {
		t.Fatal("fixture bus reports no channel — this test requires a live channel to isolate the unattended override")
	}
	adapter := &wfToolDispatcherAdapter{
		pool: pool,
		gate: &wfToolGate{
			perms:            fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
			confirm:          bus,
			headless:         toolloop.HeadlessAllow,
			headlessExplicit: true,
		},
	}
	_, isErr, err := adapter.Dispatch(runposture.Unattended(context.Background()), "kenaz__bash", []byte(`{}`))
	if err == nil {
		t.Fatal("unattended run dispatched under a deployment HeadlessAllow — must deny by construction")
	}
	if !isErr {
		t.Error("Dispatch returned err != nil but isError == false")
	}
	if !strings.Contains(err.Error(), "unattended") {
		t.Errorf("deny reason does not name the unattended posture: %v", err)
	}
	if n := len(pool.snapshot()); n != 0 {
		t.Fatalf("dispatched %d calls under an unattended deny, want 0", n)
	}
}

// TestWfToolGate_AttendedConfirmEach_SameConfigDispatches is the paired
// "not a blanket regression" leg: the IDENTICAL adapter configuration
// (HeadlessAllow, headlessExplicit, live channel), called WITHOUT the
// unattended marker, still dispatches — proving the override is
// specifically about the unattended posture, not a general confirm_each
// tightening.
func TestWfToolGate_AttendedConfirmEach_SameConfigDispatches(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	promptCount := 0
	bus := toolloop.NewConfirmBus(func(toolloop.ConfirmRequest) {
		promptCount++
	})
	adapter := &wfToolDispatcherAdapter{
		pool: pool,
		gate: &wfToolGate{
			perms:            fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
			confirm:          bus,
			headless:         toolloop.HeadlessAllow,
			headlessExplicit: true,
		},
	}
	// headlessExplicit short-circuits to the headless rung even with a
	// live channel (matching slashToolDispatcherAdapter /
	// kernelToolAdapter's own ladder ordering) — so this attended call
	// resolves via HeadlessAllow, not a prompt. The point of this test
	// is narrower than "it prompts": it is that attended + HeadlessAllow
	// dispatches, where unattended + the SAME HeadlessAllow denies.
	_, _, err := adapter.Dispatch(context.Background(), "kenaz__bash", []byte(`{}`))
	if err != nil {
		t.Fatalf("attended call under deployment HeadlessAllow denied: %v", err)
	}
	if n := len(pool.snapshot()); n != 1 {
		t.Fatalf("dispatched %d calls, want 1 (attended + HeadlessAllow must still dispatch)", n)
	}
}

// ─── wfSchedDispatcher: the other half of the unattended wire ─────────────

// unattendedSpyRunner is a race-safe fake corewf.StepRunner that records
// whether runposture.IsUnattended(ctx) was true when it ran — the probe
// for TestWfSchedDispatcher_ScheduledMarksContextUnattended_RunNowDoesNot.
type unattendedSpyRunner struct {
	mu   sync.Mutex
	seen []bool
}

func (r *unattendedSpyRunner) Validate(corewf.Step) error { return nil }

func (r *unattendedSpyRunner) Run(ctx context.Context, _ corewf.Step, _ *corewf.RunContext) (corewf.TypedValue, error) {
	r.mu.Lock()
	r.seen = append(r.seen, runposture.IsUnattended(ctx))
	r.mu.Unlock()
	return corewf.TypedValue{Type: corewf.ValueTypeText, Text: "ok"}, nil
}

func (r *unattendedSpyRunner) snapshot() []bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]bool, len(r.seen))
	copy(out, r.seen)
	return out
}

// TestWfSchedDispatcher_ScheduledMarksContextUnattended_RunNowDoesNot
// proves the OTHER half of the unattended wire: wfSchedDispatcher.Dispatch
// marks ctx unattended only for scheduled=true (a cron tick — nobody is
// watching), not for scheduled=false (a human-clicked "Run now"). Drives
// the real corewf.Engine.Run / runLinear step loop with a spy runner
// substituted for mcp_call, so the assertion is on what a step runner
// actually observes, not on a status field.
func TestWfSchedDispatcher_ScheduledMarksContextUnattended_RunNowDoesNot(t *testing.T) {
	spy := &unattendedSpyRunner{}
	engine := &corewf.Engine{Runners: map[corewf.StepKind]corewf.StepRunner{
		corewf.StepKindMCPCall: spy,
	}}
	wf := corewf.Workflow{
		ID:      "zz-unattended-probe",
		Name:    "unattended probe",
		Version: 1,
		Steps: []corewf.Step{
			{Name: "s1", Kind: corewf.StepKindMCPCall, Server: "probe-server", ToolName: "probe-tool"},
		},
	}
	wfAPI := workflowsview.New(workflowsview.Config{Engine: engine, Catalog: []corewf.Workflow{wf}})
	api := &API{workflowsAPI: wfAPI}
	d := &wfSchedDispatcher{api: api}

	if _, err := d.Dispatch(context.Background(), wf.ID, true); err != nil {
		t.Fatalf("scheduled dispatch: %v", err)
	}
	if _, err := d.Dispatch(context.Background(), wf.ID, false); err != nil {
		t.Fatalf("RunNow-style dispatch: %v", err)
	}

	seen := spy.snapshot()
	if len(seen) != 2 {
		t.Fatalf("spy runner ran %d times, want 2", len(seen))
	}
	if !seen[0] {
		t.Error("scheduled=true dispatch did not mark ctx unattended")
	}
	if seen[1] {
		t.Error("scheduled=false (\"Run now\") dispatch marked ctx unattended — a human clicked this, it must stay attended")
	}
}
