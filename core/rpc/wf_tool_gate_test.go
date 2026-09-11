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
//   - TestWfToolGate_ConfirmEach_UnansweredParkTimesOutToDenial is the
//     hang-fix regression pin: the frontend surface that answers a
//     confirm_each park (ConfirmToolModal.vue) used to mount only inside
//     SessionsView, so a "Run now" click from /workflows could park a
//     call nobody was subscribed to hear about — ConfirmBus.Pending has
//     no deadline of its own (by design, for chat), so the run hung
//     forever. wfToolGate.resolveConfirmEach now bounds its own wait
//     (confirmTimeout / defaultWorkflowConfirmTimeout in wf_adapters.go,
//     mirroring elicit's OpenDialog). This test proves the bound fires,
//     denies, and never dispatches — with the test's own bounded
//     select/time.After so a regression (someone deletes the wrapping)
//     fails FAST instead of hanging the test binary.
//   - TestWfSchedDispatcher_ProductionWire_ScheduledDeniesViaMergedResolver_EmptyParentSessionID
//     closes the reviewer's nit: every other wfToolGate test injects a
//     fake PermissionResolver, and the one production-wire test above
//     drives RunNow (attended, scheduled=false). This drives
//     scheduled=true — a cron tick, whose RunOptions carry no
//     ParentSessionID — through the REAL merged resolver
//     (api.toolPermsResolver / stack.perms) with an ACTIVE static deny
//     rule, proving the empty session id does not fail open. Paired with
//     a permit leg (CLAUDE.md's enforce()-NotApplicable trap) on a
//     different tool with no matching rule.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core"
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

// ─── the hang fix: an unanswered park must not block forever ──────────────

// TestWfToolGate_ConfirmEach_UnansweredParkTimesOutToDenial is the
// regression pin for the newly-introduced hang PR #339's follow-up
// closes: ConfirmToolModal.vue (the only UI surface that can answer a
// confirm_each park) used to mount exclusively inside SessionsView, so
// a "Run now" click from the Workflows view — having never opened
// Sessions — could park a call with a live channel (HasChannel() is
// process-global) that nothing was actually subscribed to answer. Before
// this fix that hung the run forever: confirm.go's ConfirmBus.Pending
// has no deadline of its own by design (see its package doc), and
// nothing wrapped the workflow dispatch context with one either.
//
// The fixture's ConfirmBus publisher deliberately does nothing — it
// never calls Resolve or Cancel — which is exactly "parked with nobody
// present to answer." confirmTimeout is set to a few milliseconds so
// the test does not wait out the real 10-minute production bound; the
// outer select/time.After(2s) is the CI safety net called for by the
// mission brief — if a future change deletes the context.WithTimeout
// wrapping in resolveConfirmEach, this test fails FAST instead of
// hanging the whole `go test` invocation.
func TestWfToolGate_ConfirmEach_UnansweredParkTimesOutToDenial(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	bus := toolloop.NewConfirmBus(func(toolloop.ConfirmRequest) {
		// Nobody answers. This is the bug scenario: a live channel with
		// no one actually watching it.
	})
	if !bus.HasChannel() {
		t.Fatal("fixture bus reports no channel — this test requires a live channel so the call takes the prompt rung, not the no-channel default-deny rung")
	}
	adapter := &wfToolDispatcherAdapter{
		pool: pool,
		gate: &wfToolGate{
			perms:          fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
			confirm:        bus,
			confirmTimeout: 20 * time.Millisecond,
		},
	}

	type result struct {
		isErr bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		_, isErr, err := adapter.Dispatch(context.Background(), "kenaz__bash", []byte(`{}`))
		done <- result{isErr: isErr, err: err}
	}()

	select {
	case r := <-done:
		if r.err == nil {
			t.Fatal("unanswered confirm_each park returned nil error — an unbounded park must deny, never silently dispatch")
		}
		if !r.isErr {
			t.Error("Dispatch returned err != nil but isError == false")
		}
		if !strings.Contains(r.err.Error(), "denied") {
			t.Errorf("err = %v, want it to name a denial", r.err)
		}
		if n := len(pool.snapshot()); n != 0 {
			t.Fatalf("pool was called %d times, want 0 — a timed-out park must never dispatch", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Dispatch did not return within 2s of an unanswered confirm_each park — THIS is the hang: " +
			"ConfirmBus.Pending has no deadline of its own, and nothing bounded the caller's wait, " +
			"so a park nobody answers blocks the run forever")
	}
}

// ─── production-wire: scheduled=true through the REAL merged resolver ─────

// permRuleFile is the on-disk shape core/toolloop.NewStaticResolverFromDataDir
// reads from <DataDir>/mcp_servers.json (core/toolloop/perms.go's
// unexported staticConfig/permRule, mirrored here by field name/JSON tag
// since the production types are unexported). Writing this file BEFORE
// core.New/rpc.New boot is what makes api.toolPermsResolver (stack.perms)
// carry a real, active rule rather than a hand-built fixedResolver.
type permRuleFile struct {
	Version int `json:"version"`
	Rules   []struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
		Policy string `json:"policy"`
		Reason string `json:"reason,omitempty"`
	} `json:"rules"`
}

// productionAPIWithPermRule boots a real Core + rpc.API exactly like
// cedarWiringAPI (api_cedar_gate_wiring_test.go), but seeds a static
// permission rule at <DataDir>/mcp_servers.json instead of (or in
// addition to) a Cedar policy — driving api.toolPermsResolver (the
// stack.perms merged resolver newLLMStack constructs) with an ACTIVE
// rule rather than a fake PermissionResolver.
func productionAPIWithPermRule(t *testing.T, server, tool, policy, reason string) *API {
	t.Helper()
	sandboxUserConfigDir(t)
	dataDir := t.TempDir()

	cfg := permRuleFile{Version: 1}
	cfg.Rules = append(cfg.Rules, struct {
		Server string `json:"server"`
		Tool   string `json:"tool"`
		Policy string `json:"policy"`
		Reason string `json:"reason,omitempty"`
	}{Server: server, Tool: tool, Policy: policy, Reason: reason})
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal mcp_servers.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "mcp_servers.json"), raw, 0o644); err != nil {
		t.Fatalf("write mcp_servers.json: %v", err)
	}

	c, err := core.New(core.Options{DataDir: dataDir})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	t.Cleanup(api.Shutdown)
	assertSettingsStoreIsSandboxed(t, api)
	return api
}

// TestWfSchedDispatcher_ProductionWire_ScheduledDeniesViaMergedResolver_EmptyParentSessionID
// is the reviewer's requested lock-down: a scheduled (cron-tick)
// dispatch's RunOptions carry no ParentSessionID
// (wfSchedDispatcher.Dispatch calls RunWithOptions with only an ID —
// see core/rpc/wf_sched_dispatcher.go), so the sessionID reaching
// api.toolPermsResolver.Resolve is the empty string. Every other
// wfToolGate test injects a fake PermissionResolver; this drives the
// REAL merged resolver New() actually wires, with a real static deny
// rule for the exact tool the workflow calls, and proves the empty
// session id does not fail open. Paired with a permit leg on a
// different (unruled) tool — CLAUDE.md's enforce()-NotApplicable trap —
// so the assertion discriminates "this specific rule denied" from "the
// resolver blanket-denies everything when sessionID is empty."
func TestWfSchedDispatcher_ProductionWire_ScheduledDeniesViaMergedResolver_EmptyParentSessionID(t *testing.T) {
	api := productionAPIWithPermRule(t, "ghost-server", "ghost-tool-denied", "deny", "blocked by static permission rule")
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil after New() — the production perms wire did not run")
	}
	ctx := context.Background()
	api.SetContext(ctx)

	t.Run("deny leg: rule matches, empty ParentSessionID does not fail open", func(t *testing.T) {
		const yaml = `
id: zz-wf-sched-perms-probe-deny
name: "wf scheduled perms probe (deny)"
version: 1
steps:
  - name: call_it
    kind: mcp_call
    server: ghost-server
    tool_name: ghost-tool-denied
`
		saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
		if serr != nil {
			t.Fatalf("Save: %v", serr)
		}
		d := &wfSchedDispatcher{api: api}
		_, err := d.Dispatch(ctx, saved.ID, true) // scheduled=true: cron tick, no session
		if err == nil {
			t.Fatal("scheduled dispatch against a REAL static deny rule succeeded — " +
				"the merged resolver failed open on an empty ParentSessionID")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Fatalf("err = %v, want it to name a denial", err)
		}
		if strings.Contains(err.Error(), "not installed") || strings.Contains(err.Error(), "unknown server") {
			t.Fatalf("err = %v names a pool-lookup failure, not a permission denial — "+
				"the gate did not run before the pool lookup", err)
		}
	})

	t.Run("permit leg: same scheduled dispatch, unruled tool reaches the pool", func(t *testing.T) {
		const yaml = `
id: zz-wf-sched-perms-probe-permit
name: "wf scheduled perms probe (permit)"
version: 1
steps:
  - name: call_it
    kind: mcp_call
    server: ghost-server
    tool_name: ghost-tool-unruled
`
		saved, serr := api.Workflows().Save(ctx, workflowsview.SaveInput{YAML: yaml})
		if serr != nil {
			t.Fatalf("Save: %v", serr)
		}
		d := &wfSchedDispatcher{api: api}
		_, err := d.Dispatch(ctx, saved.ID, true)
		if err == nil {
			t.Fatal("expected an error (ghost-server is not a real configured MCP server) — " +
				"but the point of this leg is WHICH error")
		}
		if strings.Contains(err.Error(), "denied") {
			t.Fatalf("err = %v: the unruled tool was denied — the merged resolver is blanket-denying "+
				"under an empty ParentSessionID, not discriminating by rule", err)
		}
		if !strings.Contains(err.Error(), "not installed") && !strings.Contains(err.Error(), "unknown server") {
			t.Fatalf("err = %v, want a pool-lookup failure proving the call reached past the gate", err)
		}
	})
}
