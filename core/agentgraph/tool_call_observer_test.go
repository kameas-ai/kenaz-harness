package agentgraph

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// tool_call_observer_test.go — Env.ToolCalls (ml-producer-01MLPRD01 WP02):
// fired on EVERY exit of dispatchOne with a typed outcome. Drives the real
// tool_dispatch executor.

type fakeToolCalls struct {
	mu   sync.Mutex
	recs []ToolCallRecord
}

func (f *fakeToolCalls) ToolCallCompleted(rec ToolCallRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recs = append(f.recs, rec)
}

func (f *fakeToolCalls) snapshot() []ToolCallRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ToolCallRecord, len(f.recs))
	copy(out, f.recs)
	return out
}

func (f *fakeToolCalls) only(t *testing.T) ToolCallRecord {
	t.Helper()
	recs := f.snapshot()
	if len(recs) != 1 {
		t.Fatalf("observer fired %d times (%+v), want exactly once", len(recs), recs)
	}
	return recs[0]
}

// funcTools is a ToolRegistry whose Call is a closure.
type funcTools func(ctx context.Context, c ToolCall) (ToolResult, error)

func (funcTools) Has(string) bool { return true }
func (f funcTools) Call(ctx context.Context, c ToolCall) (ToolResult, error) {
	return f(ctx, c)
}

func TestToolCallObserver_EveryDispatchExitHasTheRightOutcome(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		setup func(env *Env)
		tool  string
		args  string
		want  ToolOutcome
		// wantResult is the expected ResultContent ("" = reached no tool).
		wantResult string
	}{
		{
			name: "ok",
			setup: func(env *Env) {
				st := newStubTools()
				st.allow("kenaz__glob", `{"matches":[]}`, false)
				env.Tools = st
			},
			tool: "kenaz__glob", args: `{"pattern":"*.go"}`, want: ToolOutcomeOK,
			wantResult: `{"matches":[]}`,
		},
		{
			name: "tool flags its own error",
			setup: func(env *Env) {
				st := newStubTools()
				st.allow("kenaz__bash", "exit status 1", true)
				env.Tools = st
			},
			tool: "kenaz__bash", args: `{"command":"false"}`, want: ToolOutcomeError,
			wantResult: "exit status 1",
		},
		{
			name: "dispatch error",
			setup: func(env *Env) {
				st := newStubTools()
				st.failWith("acme__query", "connection refused")
				env.Tools = st
			},
			tool: "acme__query", args: `{}`, want: ToolOutcomeError,
			wantResult: "connection refused",
		},
		{
			name: "registry states denied explicitly",
			setup: func(env *Env) {
				env.Tools = funcTools(func(context.Context, ToolCall) (ToolResult, error) {
					return ToolResult{Content: "tool denied", IsError: true, Outcome: ToolOutcomeDenied}, nil
				})
			},
			tool: "kenaz__write_file", args: `{"path":"x"}`, want: ToolOutcomeDenied,
			wantResult: "tool denied",
		},
		{
			name: "non-JSON arguments (_raw) rejected",
			setup: func(env *Env) {
				env.Tools = newStubTools()
			},
			tool: "kenaz__glob", args: `not json`, want: ToolOutcomeError,
		},
		{
			name: "schema rejection",
			setup: func(env *Env) {
				env.Tools = newStubTools()
				env.ToolSchemas = map[string][]byte{
					"kenaz__glob": []byte(`{"type":"object","properties":{"pattern":{"type":"string"}},"required":["pattern"]}`),
				}
			},
			tool: "kenaz__glob", args: `{"other":1}`, want: ToolOutcomeError,
		},
		{
			name: "cedar deny",
			setup: func(env *Env) {
				env.Tools = newStubTools()
				env.Policy = denyNamedToolPolicy{denied: "kenaz__bash", err: errors.New("forbidden")}
			},
			tool: "kenaz__bash", args: `{"command":"ls"}`, want: ToolOutcomeDenied,
		},
		{
			name: "hook deny",
			setup: func(env *Env) {
				st := newStubTools()
				st.allow("kenaz__glob", "ok", false)
				env.Tools = st
				env.LifecycleHooks = &fakeLifecycleHookRunner{
					preResult: LifecycleMergedOutput{Blocked: true, BlockReason: "no"},
				}
			},
			tool: "kenaz__glob", args: `{"pattern":"*"}`, want: ToolOutcomeDenied,
		},
		{
			name: "tool panics",
			setup: func(env *Env) {
				env.Tools = funcTools(func(context.Context, ToolCall) (ToolResult, error) {
					panic("boom")
				})
			},
			tool: "kenaz__glob", args: `{}`, want: ToolOutcomeError,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			obs := &fakeToolCalls{}
			env := &Env{RunID: "r", SessionID: "sess-1", ToolCalls: obs}
			tc.setup(env)
			applyEnvDefaults(env)
			dispatchOneCall(t, env, tc.tool, tc.args)
			got := obs.only(t)
			if got.Outcome != tc.want {
				t.Errorf("outcome = %q, want %q", got.Outcome, tc.want)
			}
			if got.SessionID != "sess-1" || got.ToolName != tc.tool {
				t.Errorf("record identity = %q/%q", got.SessionID, got.ToolName)
			}
			if got.RawArgs != tc.args {
				t.Errorf("RawArgs = %q, want %q", got.RawArgs, tc.args)
			}
			if got.ResultContent != tc.wantResult {
				t.Errorf("ResultContent = %q, want %q", got.ResultContent, tc.wantResult)
			}
			if got.Ctx == nil || got.Duration < 0 || got.Duration > time.Minute {
				t.Errorf("ctx/duration not populated: %+v", got)
			}
		})
	}
}

func TestToolCallObserver_CancelledRun(t *testing.T) {
	t.Parallel()
	obs := &fakeToolCalls{}
	ctx, cancel := context.WithCancel(context.Background())
	env := &Env{RunID: "r", SessionID: "s", ToolCalls: obs,
		Tools: funcTools(func(ctx context.Context, _ ToolCall) (ToolResult, error) {
			cancel()
			<-ctx.Done()
			return ToolResult{}, ctx.Err()
		})}
	applyEnvDefaults(env)
	node := &Node{ID: "dispatch", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{MaxConcurrent: 1}}
	if _, err := (toolDispatchExecutor{}).Execute(ctx, env, node, PortValues{
		"tool_calls": []ToolCallRequest{{ID: "c1", Name: "kenaz__bash", Arguments: `{"command":"sleep 9"}`}},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := obs.only(t); got.Outcome != ToolOutcomeCancelled {
		t.Fatalf("outcome = %q, want cancelled", got.Outcome)
	}
}

// The whole fan-out: one record per call, in any order.
func TestToolCallObserver_FanOutReportsEachCallOnce(t *testing.T) {
	t.Parallel()
	obs := &fakeToolCalls{}
	usage := &fakeToolUsage{}
	st := newStubTools()
	st.allow("kenaz__glob", "ok", false)
	st.allow("kenaz__grep", "ok", false)
	env := &Env{RunID: "r", SessionID: "s", Tools: st, ToolCalls: obs, ToolUsage: usage,
		Policy: denyNamedToolPolicy{denied: "kenaz__bash", err: errors.New("no")}}
	applyEnvDefaults(env)
	node := &Node{ID: "dispatch", Kind: NodeKindToolDispatch, Attrs: ToolDispatchAttrs{MaxConcurrent: 3}}
	if _, err := (toolDispatchExecutor{}).Execute(context.Background(), env, node, PortValues{
		"tool_calls": []ToolCallRequest{
			{ID: "c1", Name: "kenaz__glob", Arguments: `{}`},
			{ID: "c2", Name: "kenaz__grep", Arguments: `{}`},
			{ID: "c3", Name: "kenaz__bash", Arguments: `{}`},
		},
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	got := map[string]ToolOutcome{}
	for _, r := range obs.snapshot() {
		if _, dup := got[r.ToolName]; dup {
			t.Errorf("%s reported twice", r.ToolName)
		}
		got[r.ToolName] = r.Outcome
	}
	want := map[string]ToolOutcome{"kenaz__glob": ToolOutcomeOK, "kenaz__grep": ToolOutcomeOK, "kenaz__bash": ToolOutcomeDenied}
	if len(got) != len(want) {
		t.Fatalf("records = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	// ToolUsage is unchanged: the Cedar-denied call is not an invocation.
	if n := len(usage.snapshot()); n != 2 {
		t.Errorf("ToolUsage saw %d invocations, want 2 (denied call excluded)", n)
	}
}

type panickingToolCalls struct{}

func (panickingToolCalls) ToolCallCompleted(ToolCallRecord) { panic("observer bug") }

func TestToolCallObserver_PanickingObserverNeverFailsDispatch(t *testing.T) {
	t.Parallel()
	st := newStubTools()
	st.allow("kenaz__glob", "fine", false)
	env := &Env{RunID: "r", SessionID: "s", Tools: st, ToolCalls: panickingToolCalls{}}
	applyEnvDefaults(env)
	tr := firstToolResult(t, dispatchOneCall(t, env, "kenaz__glob", `{}`))
	if tr.IsError || tr.Content != "fine" {
		t.Fatalf("result = %+v, want the tool's own ok result", tr)
	}
}

func TestDeriveToolOutcome(t *testing.T) {
	t.Parallel()
	live := context.Background()
	done, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		tr   ToolResult
		err  error
		want ToolOutcome
	}{
		{"ok", live, ToolResult{Content: "x"}, nil, ToolOutcomeOK},
		{"is_error", live, ToolResult{IsError: true}, nil, ToolOutcomeError},
		{"explicit wins", live, ToolResult{IsError: true, Outcome: ToolOutcomeDenied}, nil, ToolOutcomeDenied},
		{"err", live, ToolResult{}, errors.New("x"), ToolOutcomeError},
		{"per-call timeout is an error", live, ToolResult{}, context.DeadlineExceeded, ToolOutcomeError},
		{"canceled err", live, ToolResult{}, context.Canceled, ToolOutcomeCancelled},
		{"run ctx done", done, ToolResult{}, errors.New("x"), ToolOutcomeCancelled},
		{"is_error on done ctx", done, ToolResult{IsError: true}, nil, ToolOutcomeCancelled},
	}
	for _, c := range cases {
		if got := deriveToolOutcome(c.ctx, c.tr, c.err); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
