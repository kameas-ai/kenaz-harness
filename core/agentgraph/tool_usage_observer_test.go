package agentgraph

import (
	"context"
	"sync"
	"testing"
	"time"
)

// tool_usage_observer_test.go — Env.ToolUsage, the usage-telemetry seam.
//
// These run the REAL executors (tool_dispatch fan-out and the builtin-tool
// node), not toolPostDispatch in isolation: the property worth pinning is
// that a tool call the product actually makes is reported exactly once, with
// the right outcome, whichever path made it.

type toolUsageRecord struct {
	sessionID string
	toolName  string
	latency   time.Duration
	success   bool
}

type fakeToolUsage struct {
	mu   sync.Mutex
	recs []toolUsageRecord
}

func (f *fakeToolUsage) ToolInvoked(_ context.Context, sessionID, toolName string, latency time.Duration, success bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, toolUsageRecord{sessionID, toolName, latency, success})
}

func (f *fakeToolUsage) snapshot() []toolUsageRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]toolUsageRecord, len(f.recs))
	copy(out, f.recs)
	return out
}

func TestToolUsage_DispatchReportsEachOutcomeOnce(t *testing.T) {
	t.Parallel()
	usage := &fakeToolUsage{}
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	tools.allow("kenaz__bash", "exit status 1", true) // tool-flagged error
	tools.failWith("mcp__acme__query", "connection refused")
	env := &Env{RunID: "r", SessionID: "sess-1", Tools: tools, ToolUsage: usage}
	applyEnvDefaults(env)

	node := &Node{ID: "dispatch", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{MaxConcurrent: 3}}
	_, err := toolDispatchExecutor{}.Execute(context.Background(), env, node, PortValues{
		"tool_calls": []ToolCallRequest{
			{ID: "c1", Name: "kenaz__glob", Arguments: `{"pattern":"*.go"}`},
			{ID: "c2", Name: "kenaz__bash", Arguments: `{"command":"false"}`},
			{ID: "c3", Name: "mcp__acme__query", Arguments: `{"q":"select 1"}`},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := map[string]toolUsageRecord{}
	for _, r := range usage.snapshot() {
		if _, dup := got[r.toolName]; dup {
			t.Errorf("%s reported more than once", r.toolName)
		}
		got[r.toolName] = r
	}
	want := map[string]bool{"kenaz__glob": true, "kenaz__bash": false, "mcp__acme__query": false}
	if len(got) != len(want) {
		t.Fatalf("reported %d invocation(s) %v, want %d", len(got), got, len(want))
	}
	for name, success := range want {
		r, ok := got[name]
		if !ok {
			t.Errorf("%s was invoked but not reported", name)
			continue
		}
		if r.success != success {
			t.Errorf("%s success = %v, want %v", name, r.success, success)
		}
		if r.sessionID != "sess-1" {
			t.Errorf("%s sessionID = %q", name, r.sessionID)
		}
		if r.latency < 0 || r.latency > time.Minute {
			t.Errorf("%s latency = %v, want a sane elapsed time", name, r.latency)
		}
	}
}

func TestToolUsage_BlockedCallIsNotAnInvocation(t *testing.T) {
	t.Parallel()
	usage := &fakeToolUsage{}
	hooks := &fakeLifecycleHookRunner{
		preResult: LifecycleMergedOutput{Blocked: true, BlockReason: "denied by policy"},
	}
	tools := newStubTools()
	tools.allow("kenaz__glob", `{"matches":[]}`, false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools, LifecycleHooks: hooks, ToolUsage: usage}
	applyEnvDefaults(env)

	dispatchOneCall(t, env, "kenaz__glob", `{"pattern":"*.go"}`)

	if n := len(usage.snapshot()); n != 0 {
		t.Errorf("a hook-blocked call was reported as %d tool invocation(s); it never ran", n)
	}
}

func TestToolUsage_NodePathReportsLikeDispatch(t *testing.T) {
	t.Parallel()
	usage := &fakeToolUsage{}
	tools := newStubTools()
	name := builtinToolNameFor(NodeKindSubagentDispatch)
	tools.allow(name, "ok", false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools, ToolUsage: usage}
	applyEnvDefaults(env)

	ex := builtinToolExecutor{kind: NodeKindSubagentDispatch, toolName: name}
	if _, err := ex.Execute(context.Background(), env,
		&Node{ID: "n", Kind: NodeKindSubagentDispatch,
			Attrs: SubagentDispatchAttrs{Profile: "explore", Prompt: "go"}}, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	recs := usage.snapshot()
	if len(recs) != 1 || recs[0].toolName != name || !recs[0].success {
		t.Errorf("node-path invocation reported as %+v, want exactly one successful %s", recs, name)
	}
}

func TestToolUsage_NilObserverIsInert(t *testing.T) {
	t.Parallel()
	tools := newStubTools()
	tools.allow("kenaz__glob", "ok", false)
	env := &Env{RunID: "r", SessionID: "s", Tools: tools}
	applyEnvDefaults(env)
	dispatchOneCall(t, env, "kenaz__glob", `{}`) // must not panic
}
