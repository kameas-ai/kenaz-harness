package rpc

// slash_tool_dispatcher_adapter_test.go — automation-actually-runs-01PMZ404
// UNIT-4. Owner ruling G-2 (docs/escalation-register-2026-08-19.md Part 9,
// E-004): "namespaced tool names, JSON arguments, and the SAME confirm/Cedar
// path a chat tool call takes." These tests prove that property rather than
// asserting it:
//
//   - TestSlashToolDispatcher_CedarDenyAndPermitDiscriminate drives the
//     REAL production toolPermsResolver (real Cedar engine, real shipped
//     harness_write_forbid.cedar policy — never cedar.AllowAll{}) with the
//     SAME (server, tool) name in two session kinds, so the pairing proves
//     the verdict is discriminated by the policy, not by a blanket refusal
//     (CLAUDE.md's standing trap: hooks.go's enforce() maps both Allow and
//     NotApplicable to nil, so "no policy matched" must not be confused
//     with "permitted" — this test asserts an explicit Deny, paired with a
//     permit case that flips only the session_kind context attribute).
//   - TestSlash_UserRun_ProductionWireDiscriminatesByCedar repeats the
//     pairing through the ACTUAL RPC surface (rpc.New -> Slash().UserRun),
//     with zero test doubles, proving the wire — not just the adapter type
//     in isolation.
//   - TestSlashToolDispatcher_ConfirmEach* prove the confirm-each ladder:
//     a confirm_each verdict parks on the SAME *toolloop.ConfirmBus a chat
//     tool call would park on, denies by default with no channel attached
//     (matching kernelToolAdapter's headless-deny default), and dispatches
//     only once approved.
//   - TestSlashToolDispatcher_ArgsRoundTrip_FromModelEntryPoint drives the
//     []string -> json.RawMessage boundary conversion from the model's
//     actual entry point (kenaz__skill's Tool.Call -> Dispatch.
//     RunModelInvoked -> Dispatch.Run), not from a helper — the ruling
//     calls this conversion "the part most likely to be got wrong quietly".
//   - TestSlashToolDispatcher_ToolExecForbidDiscriminates is UNIT-4's fix
//     for the gap an independent review of PR #307 found: the tests above
//     drove the perms ladder's Cedar-backed arm, which only ever evaluates
//     Action::"use_tool" — the finer-grained Action::"tool_exec" sibling
//     the chat tool path also evaluates (PolicyGateAdapter.CheckTool,
//     core/rpc/views/agentgraph/env_deps_policy.go) was never consulted
//     here. This test installs the EXACT forbid shape
//     default_policy.cedar:38-46 invites a user to write
//     (`forbid (principal == User::"local", action ==
//     Action::"tool_exec", resource == Tool::"...")`) and proves
//     DispatchTool now denies it — paired with a permit leg (CLAUDE.md's
//     enforce()-NotApplicable trap) so the verdict is shown to
//     discriminate by resource, not blanket-refuse. perms is left nil so
//     the denial can only come from the new gate-backed cedar.CheckTool
//     call, not the pre-existing perms ladder the tests above already
//     cover.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	harnessmcp "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/session"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
	coreskill "github.com/kameas-ai/kenaz-harness/core/tools/skill"
)

// ─── shared fakes ─────────────────────────────────────────────────────────

// recordingPool is a race-safe fake toolloop.MCPPool that records every
// Call and returns a canned response. CLAUDE.md's race-safe-fakes pattern:
// writes go through mu, reads go through snapshot().
type recordingPool struct {
	mu     sync.Mutex
	calls  []recordedCall
	output json.RawMessage
	err    error
}

type recordedCall struct {
	Server string
	Tool   string
	Args   json.RawMessage
}

func (p *recordingPool) Tools(context.Context) ([]toolloop.Tool, error) { return nil, nil }

func (p *recordingPool) Call(_ context.Context, server, tool string, args json.RawMessage) (json.RawMessage, error) {
	p.mu.Lock()
	p.calls = append(p.calls, recordedCall{Server: server, Tool: tool, Args: append(json.RawMessage(nil), args...)})
	p.mu.Unlock()
	if p.err != nil {
		return nil, p.err
	}
	return p.output, nil
}

func (p *recordingPool) snapshot() []recordedCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]recordedCall, len(p.calls))
	copy(out, p.calls)
	return out
}

// fixedResolver is a toolloop.PermissionResolver that always returns the
// same Resolution, for tests that only care about one arm of the
// confirm-each ladder in isolation from Cedar.
type fixedResolver struct {
	policy toolloop.ToolPolicy
	reason string
}

func (r fixedResolver) Resolve(_ context.Context, _, server, tool string) (toolloop.Resolution, error) {
	return toolloop.Resolution{Server: server, Tool: tool, Policy: r.policy, Reason: r.reason}, nil
}

// ─── Cedar deny/permit discrimination (adapter-level) ──────────────────────

// TestSlashToolDispatcher_CedarDenyAndPermitDiscriminate is the deny leg
// paired with the permit leg required by CLAUDE.md's enforce()-NotApplicable
// trap: the SAME (server, tool) — harness-self__harness_write_set_setting —
// is dispatched twice against the REAL production toolPermsResolver
// (bootAPIWithCore boots a real Core + real Cedar engine + the shipped
// harness_write_forbid.cedar bundle), with only the calling session's Kind
// changed between the two calls. A chat session must deny (the forbid rule
// fires); an onboarding session must not (the forbid's own condition,
// session_kind != "onboarding", is false, so it does not apply). If the
// test only asserted the deny case, a resolver that blanket-denied
// everything would pass it — the permit leg is what proves the verdict is
// actually conditioned on the policy.
func TestSlashToolDispatcher_CedarDenyAndPermitDiscriminate(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")
	if api.toolPermsResolver == nil {
		t.Fatal("api.toolPermsResolver is nil after New() — the production Cedar wire did not run")
	}

	rec, err := api.Sessions().Create(context.Background(), "chat session")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	// harness_write_create_project, not harness_write_set_setting: ruling
	// G-4 removed the set_setting tool entirely (harness-self-attach-01PMHS01
	// UNIT-8) while this unit was in flight, so the symbol this test
	// originally used no longer exists.
	//
	// The substitution preserves what the test measures. harness_write_forbid
	// .cedar forbids by PATTERN — `context.tool_name like "harness_write_*"
	// && context.session_kind != "onboarding"` — so any surviving
	// harness_write_* tool exercises the same rule. The test is about the
	// dispatcher consulting Cedar at all, not about which tool it names.
	const toolName = "harness-self__" + harnessmcp.ToolCreateProject

	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	adapter := &slashToolDispatcherAdapter{
		pool:  pool,
		perms: api.toolPermsResolver, // the REAL merged resolver, not a stand-in.
	}
	ctx := toolloop.WithSessionID(context.Background(), rec.ID)

	// ── deny leg: chat session ──────────────────────────────────────────
	_, err = adapter.DispatchTool(ctx, toolName, []string{"--noop"})
	if err == nil {
		t.Fatal("chat session: DispatchTool returned nil error for a Cedar-forbidden tool")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("chat session: err = %v, want it to name a denial", err)
	}
	if calls := pool.snapshot(); len(calls) != 0 {
		t.Errorf("chat session: pool was called %d times, want 0 — a denied call must never reach the pool", len(calls))
	}

	// ── permit leg: SAME tool, SAME session, only Kind flipped ──────────
	if err := c.SessionManager().SetKind(context.Background(), rec.ID, session.SessionKindOnboarding); err != nil {
		t.Fatalf("SetKind: %v", err)
	}
	_, err = adapter.DispatchTool(ctx, toolName, []string{"--noop"})
	if err != nil && strings.Contains(err.Error(), "denied") {
		t.Fatalf("onboarding session: DispatchTool still denied the SAME tool: %v — the verdict is not discriminating on session_kind", err)
	}
	if calls := pool.snapshot(); len(calls) != 1 {
		t.Fatalf("onboarding session: pool was called %d times, want 1 — the permitted call must reach the pool", len(calls))
	}
	if calls := pool.snapshot(); calls[0].Server != "harness-self" || calls[0].Tool != harnessmcp.ToolCreateProject {
		t.Errorf("pool.Call got (server=%q, tool=%q), want (harness-self, %q)", calls[0].Server, calls[0].Tool, harnessmcp.ToolCreateProject)
	}
}

// TestSlash_UserRun_ProductionWireDiscriminatesByCedar repeats the same
// pairing through the ACTUAL RPC surface — rpc.New()'s wired
// slashDispatch, reached via Slash().UserSave + Slash().UserRun exactly as
// the frontend composer would call it — with zero test doubles anywhere in
// the path. This is the "production-path assertion" spec.md's rule 3 (§10)
// requires: a hand-built adapter proves the TYPE works; this proves New()
// actually reaches for it.
func TestSlash_UserRun_ProductionWireDiscriminatesByCedar(t *testing.T) {
	c, api := bootAPIWithCore(t, t.TempDir(), "")

	ctx := context.Background()
	rec, err := api.Sessions().Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	const toolName = "harness-self__" + harnessmcp.ToolCreateProject
	saveErr := api.Slash().UserSave(ctx, slashview.UserCommandWire{
		Name:             "danger-write",
		Scope:            "global",
		Kind:             "tool",
		Description:      "test: a write tool a chat session must not reach",
		ModelInvokable:   true,
		Tool:             toolName,
		ToolArgsTemplate: "--noop",
	})
	if saveErr != nil {
		t.Fatalf("UserSave: %v", saveErr)
	}

	// ── deny leg ─────────────────────────────────────────────────────
	_, err = api.Slash().UserRun(ctx, "danger-write", nil, rec.ID, "", "", "")
	if err == nil {
		t.Fatal("UserRun (chat session): expected an error for a Cedar-forbidden tool, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("UserRun (chat session): err = %v, want it to name a denial", err)
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Errorf("UserRun: err = %v — this is UNIT-3's nil-dispatcher sentinel; the production wire did not attach a tool dispatcher", err)
	}

	// ── permit leg: same command, same session, Kind flipped ───────────
	if err := c.SessionManager().SetKind(ctx, rec.ID, session.SessionKindOnboarding); err != nil {
		t.Fatalf("SetKind: %v", err)
	}
	_, err = api.Slash().UserRun(ctx, "danger-write", nil, rec.ID, "", "", "")
	if err != nil && strings.Contains(err.Error(), "denied") {
		t.Fatalf("UserRun (onboarding session): still denied the SAME command: %v", err)
	}
}

// TestSlashToolDispatcher_ProductionWire_DenyDiscipline_MutationNote
// documents the falsification this file is not allowed to run
// automatically (spec.md's mutation-test convention in this campaign is
// "RUN by hand and record the result", not a permanent CI mutation
// harness): removing the permission gate from DispatchTool (making it call
// a.pool.Call unconditionally, mirroring wfToolDispatcherAdapter's shape)
// must turn TestSlashToolDispatcher_CedarDenyAndPermitDiscriminate's deny
// leg red. Verified by hand for this commit; see the mission report.
func TestSlashToolDispatcher_ProductionWire_DenyDiscipline_MutationNote(t *testing.T) {
	t.Skip("documentation-only marker; see the function doc comment")
}

// ─── tool_exec forbid (the gap PR #307's review found) ─────────────────────

// TestSlashToolDispatcher_ToolExecForbidDiscriminates is UNIT-4's proof for
// the second Cedar action the chat tool path evaluates.
// PolicyGateAdapter.CheckTool (core/rpc/views/agentgraph/env_deps_policy.go)
// composes cedar.CheckUseTool (Action::"use_tool") and cedar.CheckTool
// (Action::"tool_exec") from the SAME dispatch boundary
// (core/agentgraph/exec_dispatch.go:217-219) a chat tool call takes.
// Before this commit, DispatchTool's perms ladder only ever reached the
// use_tool-equivalent leg (its Cedar-backed session-kind arm evaluates
// Action::"use_tool" — see cedarSessionKindResolver.Resolve); a forbid on
// Action::"tool_exec" — the EXACT rule default_policy.cedar:38-46 invites a
// user to write ("Per-tool deny rules can be added by the user to lock down
// individual tools") — reached the tool pool unevaluated.
//
// perms is left nil so the deny below can only come from the new
// gate-backed cedar.CheckTool call this test exists to pin, not from the
// perms ladder TestSlashToolDispatcher_CedarDenyAndPermitDiscriminate
// already covers.
//
// Paired with a permit leg (CLAUDE.md's enforce()-NotApplicable trap:
// hooks.go's enforce() maps both Allow and NotApplicable to nil, so a
// deny-only test cannot distinguish "the policy denied this" from "the
// adapter blanket-refuses everything"): the SAME forbid, scoped to one
// tool by resource, must not deny an unrelated tool.
func TestSlashToolDispatcher_ToolExecForbidDiscriminates(t *testing.T) {
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
		adapter := &slashToolDispatcherAdapter{pool: pool, gate: e} // perms nil: not this test's concern.
		_, err := adapter.DispatchTool(context.Background(), "kenaz__bash", []string{"echo", "hi"})
		if err == nil {
			t.Fatal("expected a denial for kenaz__bash (tool_exec forbid), got nil")
		}
		if !strings.Contains(err.Error(), "denied") {
			t.Errorf("err = %v, want it to name a denial", err)
		}
		if !cedar.IsPolicyDenied(err) {
			t.Errorf("err = %v, want it to wrap a *cedar.PolicyDeniedError", err)
		}
		if calls := pool.snapshot(); len(calls) != 0 {
			t.Errorf("pool was called %d times, want 0 — a tool_exec-denied call must never reach the pool", len(calls))
		}
	})

	t.Run("permit leg: same forbid, different tool", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		adapter := &slashToolDispatcherAdapter{pool: pool, gate: e} // perms nil: not this test's concern.
		_, err := adapter.DispatchTool(context.Background(), "kenaz__websearch", []string{"query"})
		if err != nil {
			t.Fatalf("kenaz__websearch: unexpected denial: %v", err)
		}
		if calls := pool.snapshot(); len(calls) != 1 {
			t.Fatalf("pool was called %d times, want 1 — an unrelated tool must still dispatch", len(calls))
		}
	})
}

// TestSlashToolDispatcher_ToolExecForbid_ProductionWireDiscriminatesByCedar
// repeats the tool_exec pairing through the production wire — rpc.New's
// wired slashDispatch reached via Slash().UserSave + Slash().UserRun, with
// zero test doubles — proving core/rpc/api.go actually assigns gate:
// a.cedarGate() at the slashToolDispatcherAdapter construction site, not
// just that the adapter type enforces tool_exec when hand-built with one.
// Uses the SAME shipped default_policy.cedar tool_exec permit-by-default
// rule (default_policy.cedar:38-46) plus an installed forbid, exactly as a
// user would add one from the policy editor.
func TestSlashToolDispatcher_ToolExecForbid_ProductionWireDiscriminatesByCedar(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")

	if api.cedarEngine == nil {
		t.Fatal("api.cedarEngine is nil after New() — the production Cedar wire did not run")
	}
	if err := api.cedarEngine.SetPolicyText("tool-exec-forbid.cedar", []byte(`
forbid (
    principal == User::"local",
    action == Action::"tool_exec",
    resource == Tool::"kenaz__bash"
);
`)); err != nil {
		t.Fatalf("SetPolicyText: %v", err)
	}

	ctx := context.Background()
	rec, err := api.Sessions().Create(ctx, "chat session")
	if err != nil {
		t.Fatalf("Sessions().Create: %v", err)
	}

	saveErr := api.Slash().UserSave(ctx, slashview.UserCommandWire{
		Name:             "tool-exec-forbidden",
		Scope:            "global",
		Kind:             "tool",
		Description:      "test: a tool forbidden by Action::tool_exec only",
		ModelInvokable:   true,
		Tool:             "kenaz__bash",
		ToolArgsTemplate: "echo hi",
	})
	if saveErr != nil {
		t.Fatalf("UserSave: %v", saveErr)
	}

	// ── deny leg ─────────────────────────────────────────────────────
	_, err = api.Slash().UserRun(ctx, "tool-exec-forbidden", nil, rec.ID, "", "", "")
	if err == nil {
		t.Fatal("UserRun: expected an error for a tool_exec-forbidden tool, got nil")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("UserRun: err = %v, want it to name a denial", err)
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Errorf("UserRun: err = %v — this is UNIT-3's nil-dispatcher sentinel; the production wire did not attach a tool dispatcher", err)
	}

	// ── permit leg: a different tool, same forbid still installed ──────
	saveErr = api.Slash().UserSave(ctx, slashview.UserCommandWire{
		Name:             "tool-exec-allowed",
		Scope:            "global",
		Kind:             "tool",
		Description:      "test: an unrelated tool the forbid does not name",
		ModelInvokable:   true,
		Tool:             "kenaz__websearch",
		ToolArgsTemplate: "query",
	})
	if saveErr != nil {
		t.Fatalf("UserSave (allowed): %v", saveErr)
	}
	_, err = api.Slash().UserRun(ctx, "tool-exec-allowed", nil, rec.ID, "", "", "")
	if err != nil && strings.Contains(err.Error(), "denied") {
		t.Fatalf("UserRun (kenaz__websearch): unexpected denial: %v — the tool_exec forbid must not blanket-deny every tool", err)
	}
}

// ─── confirm-each ladder ────────────────────────────────────────────────

// TestSlashToolDispatcher_ConfirmEach_NoChannel_DeniesByDefault proves the
// SAME headless-deny-by-default posture kernelToolAdapter.resolveConfirmEach
// applies: a confirm_each verdict with no ConfirmBus attached must NOT
// silently dispatch.
func TestSlashToolDispatcher_ConfirmEach_NoChannel_DeniesByDefault(t *testing.T) {
	pool := &recordingPool{output: json.RawMessage(`"ok"`)}
	adapter := &slashToolDispatcherAdapter{
		pool:  pool,
		perms: fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
		// confirm left nil: no channel.
	}
	_, err := adapter.DispatchTool(context.Background(), "kenaz__bash", []string{"echo", "hi"})
	if err == nil {
		t.Fatal("no-channel confirm_each: expected a denial, got nil error")
	}
	if !strings.Contains(err.Error(), "denied") {
		t.Errorf("err = %v, want it to name a denial", err)
	}
	if calls := pool.snapshot(); len(calls) != 0 {
		t.Errorf("pool was called %d times, want 0 — an unanswered confirm_each must never dispatch", len(calls))
	}
}

// TestSlashToolDispatcher_ConfirmEach_ParksOnSameBusAndHonoursDecision
// proves a confirm_each verdict parks on the SAME *toolloop.ConfirmBus a
// chat tool call would park on — the property "a tool that would prompt
// for confirmation in chat also prompts when reached via a slash command".
// ConfirmBus.Pending's own doc guarantees the publish callback fires
// synchronously and re-entrant Resolve calls are safe, so this test
// resolves the parked request from inside the publisher — no goroutine,
// no timing race — while still exercising the real park/resolve
// round-trip, not a shortcut.
func TestSlashToolDispatcher_ConfirmEach_ParksOnSameBusAndHonoursDecision(t *testing.T) {
	t.Run("approved", func(t *testing.T) {
		pool := &recordingPool{output: json.RawMessage(`"ok"`)}
		var gotReq toolloop.ConfirmRequest
		// Forward-declared so the publisher closure (which fires
		// re-entrantly, per ConfirmBus.Pending's own doc) can resolve the
		// SAME bus it was just handed a request by.
		var bus *toolloop.ConfirmBus
		bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
			gotReq = req
			if err := bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true}); err != nil {
				t.Fatalf("Resolve: %v", err)
			}
		})

		adapter := &slashToolDispatcherAdapter{
			pool:    pool,
			perms:   fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
			confirm: bus,
		}
		_, err := adapter.DispatchTool(toolloop.WithSessionID(context.Background(), "sess-approve"), "kenaz__bash", []string{"echo", "hi"})
		if err != nil {
			t.Fatalf("approved confirm_each: unexpected error: %v", err)
		}
		if calls := pool.snapshot(); len(calls) != 1 {
			t.Fatalf("pool was called %d times, want 1", len(calls))
		}
		if gotReq.Server != "kenaz" || gotReq.Tool != "bash" {
			t.Errorf("ConfirmRequest = %+v, want server=kenaz tool=bash", gotReq)
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

		adapter := &slashToolDispatcherAdapter{
			pool:    pool,
			perms:   fixedResolver{policy: toolloop.PolicyConfirmEach, reason: "confirm each use"},
			confirm: bus,
		}
		_, err := adapter.DispatchTool(toolloop.WithSessionID(context.Background(), "sess-deny"), "kenaz__bash", []string{"echo", "hi"})
		if err == nil {
			t.Fatal("denied confirm_each: expected an error, got nil")
		}
		if !strings.Contains(err.Error(), "user said no") {
			t.Errorf("err = %v, want it to carry the user's reason", err)
		}
		if calls := pool.snapshot(); len(calls) != 0 {
			t.Errorf("pool was called %d times, want 0 — a denied confirmation must never dispatch", len(calls))
		}
	})
}

// ─── args round-trip, driven from the model's entry point ────────────────

// TestSlashToolDispatcher_ArgsRoundTrip_FromModelEntryPoint drives the
// []string -> json.RawMessage conversion from the model's actual entry
// point: kenaz__skill's Tool.Call -> Dispatch.RunModelInvoked ->
// Dispatch.Run's KindTool branch -> slashToolDispatcherAdapter.DispatchTool.
// Owner ruling G-2 calls this boundary "the part most likely to be got
// wrong quietly, so drive it from the model's entry point, not a helper" —
// this test does not call DispatchTool or json.Marshal directly.
func TestSlashToolDispatcher_ArgsRoundTrip_FromModelEntryPoint(t *testing.T) {
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	store := coreslashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := coreslashcmd.UserCommand{
		Name:             "deploy",
		Scope:            coreslashcmd.ScopeGlobal,
		Kind:             coreslashcmd.KindTool,
		Description:      "Deploy to env",
		ModelInvokable:   true,
		Tool:             "kenaz__bash",
		ToolArgsTemplate: "./deploy.sh --env {{env}} --count {{count}}",
		Inputs: []coreslashcmd.UserCommandInput{
			{Name: "env", Kind: coreslashcmd.InputKindText, Required: true},
			{Name: "count", Kind: coreslashcmd.InputKindText, Required: true},
		},
	}
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	pool := &recordingPool{output: json.RawMessage(`"deployed"`)}
	adapter := &slashToolDispatcherAdapter{pool: pool} // perms nil: not this test's concern.
	dispatch := coreslashcmd.NewDispatch(store, nil).WithToolDispatcher(adapter)

	skillTool := coreskill.New(coreskill.Options{Dispatch: dispatch})
	input, err := json.Marshal(map[string]any{
		"name":       "deploy",
		"args":       map[string]string{"env": "staging", "count": "3"},
		"session_id": "sess-args",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	out, callErr := skillTool.Call(ctx, input)
	if callErr != nil {
		t.Fatalf("skillTool.Call: %v", callErr)
	}
	var res struct {
		Output  string `json:"output"`
		Kind    string `json:"kind"`
		IsError bool   `json:"isError"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal skill output: %v", err)
	}
	if res.IsError {
		t.Fatalf("skillTool.Call returned an error result: %s", res.Error)
	}
	if res.Output != "deployed" {
		t.Errorf("Output = %q, want %q", res.Output, "deployed")
	}

	calls := pool.snapshot()
	if len(calls) != 1 {
		t.Fatalf("pool was called %d times, want 1", len(calls))
	}
	if calls[0].Server != "kenaz" || calls[0].Tool != "bash" {
		t.Fatalf("pool.Call got (server=%q, tool=%q), want (kenaz, bash)", calls[0].Server, calls[0].Tool)
	}

	// The boundary conversion this test exists to pin: the rendered
	// []string args ("./deploy.sh --env staging --count 3", split) must
	// arrive at the pool as a JSON ARRAY, not a shell-joined string and
	// not silently dropped.
	var gotArgs []string
	if err := json.Unmarshal(calls[0].Args, &gotArgs); err != nil {
		t.Fatalf("pool received non-JSON-array args %s: %v", calls[0].Args, err)
	}
	wantArgs := []string{"./deploy.sh", "--env", "staging", "--count", "3"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, gotArgs[i], wantArgs[i])
		}
	}
}

// ─── namespaced-name defence in depth ─────────────────────────────────────

// TestSlashToolDispatcher_RejectsBareToolName is defence-in-depth for
// owner ruling G-2's "namespaced tool names": core/slashcmd.Validate is
// the primary gate (rejects a bare name at save time), but a malformed
// name must not silently resolve to an empty server either.
func TestSlashToolDispatcher_RejectsBareToolName(t *testing.T) {
	pool := &recordingPool{}
	adapter := &slashToolDispatcherAdapter{pool: pool}
	_, err := adapter.DispatchTool(context.Background(), "bash", []string{"echo"})
	if err == nil {
		t.Fatal("expected an error for a bare (non-namespaced) tool name")
	}
	if !strings.Contains(err.Error(), "namespaced") {
		t.Errorf("err = %v, want it to name the namespacing requirement", err)
	}
	if calls := pool.snapshot(); len(calls) != 0 {
		t.Errorf("pool was called %d times, want 0", len(calls))
	}
}
