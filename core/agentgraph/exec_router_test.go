package agentgraph

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// exec_router_test.go — behaviour tests for the `router` node kind
// (agentgraph-total-convergence-01PMGX01 WP11a; design in
// agentic-turn-routing-01PMAG01 §3.1, tasks WP03/WP04).

// countingLLM is the race-safe fake provider these tests assert call
// counts against. The router's headline property is "fused mode costs
// ZERO additional Generate calls", which is only provable by counting.
// The kernel fires nodes from bare goroutines, so every field is
// mutex-guarded and test-side reads go through snapshot helpers
// (CLAUDE.md "Race-safe test fakes").
type countingLLM struct {
	mu       sync.Mutex
	calls    int
	prompts  []string
	requests []LLMRequest
	reply    string
}

func (c *countingLLM) Generate(_ context.Context, req LLMRequest) (LLMResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.requests = append(c.requests, req)
	if len(req.Messages) > 0 {
		c.prompts = append(c.prompts, req.Messages[len(req.Messages)-1].Content)
	}
	return LLMResponse{Content: c.reply}, nil
}

func (c *countingLLM) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *countingLLM) snapshotRequests() []LLMRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]LLMRequest, len(c.requests))
	copy(out, c.requests)
	return out
}

func (c *countingLLM) snapshotPrompts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, len(c.prompts))
	copy(out, c.prompts)
	return out
}

// routerNode builds a router node carrying the three-choice menu the
// spec's §3.1 example uses.
func routerNode(id string, attrs RouterAttrs) *Node {
	return &Node{
		ID:   id,
		Kind: NodeKindRouter,
		Outputs: []Port{
			{Name: "next", Type: PortType("text")},
			{Name: "out", Type: PortType("any")},
			{Name: "research", Type: PortType("any")},
			{Name: "draft", Type: PortType("any")},
			{Name: "done", Type: PortType("any")},
		},
		Attrs: attrs,
	}
}

func routerMenu() map[string]any {
	return map[string]any{
		"research": map[string]any{"target": "tool_dispatch", "description": "Gather more information with tools"},
		"draft":    map[string]any{"target": "assistant_turn", "description": "Write or revise the answer"},
		"done":     map[string]any{"target": "exit_gate", "description": "The work is complete and verified"},
	}
}

func routerEnv(t *testing.T, llm LLMProvider) *Env {
	t.Helper()
	env := &Env{RunID: "run-router", SessionID: "sess", LLM: llm}
	applyEnvDefaults(env)
	if llm != nil {
		env.LLM = llm
	}
	return env
}

// TestRouter_FusedModeIssuesZeroExtraGenerateCalls is the FR-003/FR-004
// pin and the single most important test in this file: routing on the
// common path must be free. It is what catches an "innocent" refactor
// that gives the router its own model call.
func TestRouter_FusedModeIssuesZeroExtraGenerateCalls(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{reply: "draft"}
	env := routerEnv(t, llm)
	// The upstream model turn's own recorded output carries the choice.
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `Here you go. {"content": "hi", "next_choice": "research"}`},
	})

	node := routerNode("route", RouterAttrs{
		Mode:          routerModeFused,
		SourceNode:    "assistant_turn",
		Choices:       routerMenu(),
		DefaultChoice: "done",
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "payload"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := llm.callCount(); got != 0 {
		t.Errorf("Generate called %d times in fused mode, want 0 — routing must be free on the common path (FR-003)", got)
	}
	if got, _ := res.Outputs.GetString("next"); got != "research" {
		t.Errorf("next = %q, want %q", got, "research")
	}
	if _, ok := res.Outputs["research"]; !ok {
		t.Error("winning choice port `research` absent")
	}
	for _, dead := range []string{"draft", "done"} {
		if _, ok := res.Outputs[dead]; ok {
			t.Errorf("losing choice port %q must be absent", dead)
		}
	}
}

// TestRouter_FusedFallsBackToDefaultOnGarbledChoice covers the WP04
// acceptance bullet: a missing or unparseable next_choice resolves to
// default_choice rather than erroring or guessing.
func TestRouter_FusedFallsBackToDefaultOnGarbledChoice(t *testing.T) {
	t.Parallel()
	for name, upstream := range map[string]PortValues{
		"no source outputs at all": {},
		"text with no choice":      {"assistant": Message{Role: "assistant", Content: "I have finished."}},
		"unknown choice id":        {"assistant": Message{Role: "assistant", Content: `{"next_choice": "teleport"}`}},
		"truncated json":           {"assistant": Message{Role: "assistant", Content: `{"next_choice": "resea`}},
	} {
		upstream := upstream
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			llm := &countingLLM{reply: "research"}
			env := routerEnv(t, llm)
			env.State.SetOutputs("assistant_turn", upstream)
			node := routerNode("route", RouterAttrs{
				SourceNode: "assistant_turn", Choices: routerMenu(), DefaultChoice: "done",
			})
			res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got, _ := res.Outputs.GetString("next"); got != "done" {
				t.Errorf("next = %q, want the default_choice %q", got, "done")
			}
			if got := llm.callCount(); got != 0 {
				t.Errorf("Generate called %d times, want 0 — the default path must not spend a call", got)
			}
		})
	}
}

// TestRouter_FusedDegradesToStandalone covers the capability fallback:
// a provider that returns no parsable structured field degrades to an
// explicit routing call when the author opted in.
func TestRouter_FusedDegradesToStandalone(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{reply: "research"}
	env := routerEnv(t, llm)
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: "plain prose, no structured output support here"},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenu(),
		DefaultChoice: "done", FallbackToStandalone: true,
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := llm.callCount(); got != 1 {
		t.Errorf("Generate called %d times, want 1 (degraded to standalone)", got)
	}
	if got, _ := res.Outputs.GetString("next"); got != "research" {
		t.Errorf("next = %q, want %q", got, "research")
	}
}

// TestRouter_StandaloneRoutesEachChoice walks the whole menu through
// standalone mode and also pins that an answer wrapped in prose is
// still honored — the "Sure! PASS" defect class this mission is
// explicitly not re-living.
func TestRouter_StandaloneRoutesEachChoice(t *testing.T) {
	t.Parallel()
	for reply, want := range map[string]string{
		"research":                     "research",
		"  draft  ":                    "draft",
		"Sure! I'll pick done.":        "done",
		`{"next_choice": "research"}`:  "research",
		"nothing recognisable at all!": "done", // -> default_choice
	} {
		reply, want := reply, want
		t.Run(strings.TrimSpace(reply), func(t *testing.T) {
			t.Parallel()
			llm := &countingLLM{reply: reply}
			env := routerEnv(t, llm)
			node := routerNode("route", RouterAttrs{
				Mode: routerModeStandalone, Choices: routerMenu(), DefaultChoice: "done",
			})
			res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got, _ := res.Outputs.GetString("next"); got != want {
				t.Errorf("reply %q -> next %q, want %q", reply, got, want)
			}
		})
	}
}

// TestRouter_StandalonePromptCarriesGraphBaseAndMenu pins the
// missing-prompt anti-pattern the escalation ladder's rungs shipped
// with: the routing call must compose the graph base (which carries
// TaskState + rejected approaches) and must list the menu.
func TestRouter_StandalonePromptCarriesGraphBaseAndMenu(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{reply: "done"}
	env := routerEnv(t, llm)
	env.Graph = &Graph{SystemPrompt: "GRAPH-BASE-MARKER"}
	env.TaskState.SetGoal("ship the report")

	node := routerNode("route", RouterAttrs{
		Mode: routerModeStandalone, Choices: routerMenu(), DefaultChoice: "done",
		SystemPrompt: "NODE-ROLE-MARKER",
	})
	if _, err := (routerExecutor{}).Execute(context.Background(), env, node, PortValues{"in": "p"}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	reqs := llm.snapshotRequests()
	if len(reqs) != 1 {
		t.Fatalf("Generate calls = %d, want 1", len(reqs))
	}
	for _, want := range []string{"GRAPH-BASE-MARKER", "NODE-ROLE-MARKER", "ship the report"} {
		if !strings.Contains(reqs[0].SystemPrompt, want) {
			t.Errorf("system prompt missing %q:\n%s", want, reqs[0].SystemPrompt)
		}
	}
	prompts := llm.snapshotPrompts()
	if len(prompts) != 1 {
		t.Fatalf("user prompts = %d, want 1", len(prompts))
	}
	// Menu rendered in sorted-id order, descriptions included.
	for _, want := range []string{"done", "draft", "research", "Gather more information with tools"} {
		if !strings.Contains(prompts[0], want) {
			t.Errorf("menu prompt missing %q:\n%s", want, prompts[0])
		}
	}
	doneIdx := strings.Index(prompts[0], "- done")
	researchIdx := strings.Index(prompts[0], "- research")
	if doneIdx > researchIdx {
		t.Errorf("menu is not in sorted-id order:\n%s", prompts[0])
	}
}

// TestRouter_ThrashGuardForcesDefault covers max_consecutive_same: a
// router that keeps landing on the same branch is broken out of it.
func TestRouter_ThrashGuardForcesDefault(t *testing.T) {
	t.Parallel()
	llm := &countingLLM{}
	env := routerEnv(t, llm)
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `{"next_choice": "research"}`},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenu(),
		DefaultChoice: "done", MaxConsecutiveSame: 3,
	})

	var lastRes Result
	for i := 1; i <= 4; i++ {
		res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
		if err != nil {
			t.Fatalf("Execute #%d: %v", i, err)
		}
		got, _ := res.Outputs.GetString("next")
		want := "research"
		if i == 4 {
			want = "done" // the 4th consecutive identical pick trips the cap of 3
		}
		if got != want {
			t.Errorf("fire %d: next = %q, want %q", i, got, want)
		}
		lastRes = res
	}

	var sawOverride bool
	for _, e := range lastRes.Events.Events {
		if e.Kind == EventRouterOverride {
			sawOverride = true
			if !strings.Contains(string(e.Payload), "thrash") {
				t.Errorf("override payload does not name the thrash reason: %s", e.Payload)
			}
		}
	}
	if !sawOverride {
		t.Error("no EventRouterOverride emitted when the thrash guard tripped")
	}
}

// TestRouter_EmitsRouterChoiceEvent covers FR-005: every routing
// decision is an EventLog record.
func TestRouter_EmitsRouterChoiceEvent(t *testing.T) {
	t.Parallel()
	env := routerEnv(t, &countingLLM{})
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `{"next_choice": "draft"}`},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenu(), DefaultChoice: "done",
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{"in": "p"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var found bool
	for _, e := range res.Events.Events {
		if e.Kind != EventRouterChoice {
			continue
		}
		found = true
		for _, want := range []string{`"choice":"draft"`, `"mode":"fused"`, `"target":"assistant_turn"`} {
			if !strings.Contains(string(e.Payload), want) {
				t.Errorf("router_choice payload missing %s: %s", want, e.Payload)
			}
		}
	}
	if !found {
		t.Error("no EventRouterChoice emitted")
	}
}

// TestRouter_PassesInboundPayloadThrough pins the property that lets a
// router sit at the end of a Loop body: the loop flattens the LAST body
// node's outputs, so anything the router drops stops reaching nodes
// outside the loop (in chat_default that is assistant_write's text).
func TestRouter_PassesInboundPayloadThrough(t *testing.T) {
	t.Parallel()
	env := routerEnv(t, &countingLLM{})
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `{"next_choice": "done"}`},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenu(), DefaultChoice: "done",
	})
	inputs := PortValues{
		"in":              "payload",
		"assistant_text":  "the reply",
		"tool_call_count": 0,
	}
	res, err := routerExecutor{}.Execute(context.Background(), env, node, inputs)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := res.Outputs.GetString("assistant_text"); got != "the reply" {
		t.Errorf("assistant_text = %q, want it passed through", got)
	}
	if _, ok := res.Outputs["tool_call_count"]; !ok {
		t.Error("tool_call_count not passed through")
	}
}

// TestRouter_RejectsUnusableMenu covers the two configuration errors
// the executor refuses to guess past.
func TestRouter_RejectsUnusableMenu(t *testing.T) {
	t.Parallel()
	env := routerEnv(t, &countingLLM{})
	for name, attrs := range map[string]RouterAttrs{
		"empty menu":          {SourceNode: "assistant_turn", DefaultChoice: "done"},
		"default not in menu": {SourceNode: "assistant_turn", Choices: routerMenu(), DefaultChoice: "nope"},
	} {
		attrs := attrs
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := routerExecutor{}.Execute(context.Background(), env, routerNode("route", attrs), PortValues{})
			if err == nil {
				t.Fatal("expected an error, got nil")
			}
		})
	}
}

// TestParseChoiceField_RuneSafety pins the recurring truncation defect
// class: multi-byte content anywhere in the model's reply must never be
// split, and must never change the parsed choice.
func TestParseChoiceField_RuneSafety(t *testing.T) {
	t.Parallel()
	valid := map[string]bool{"done": true, "research": true}
	// A slice, not a map with aligned values: gofmt's column alignment
	// for multi-byte keys differs between Go releases, and a test that
	// re-formats itself depending on the local toolchain is noise in
	// every future diff.
	cases := []struct{ in, want string }{
		{`{"content": "café ☕ — naïve", "next_choice": "done"}`, "done"},
		{`prose 日本語テキスト {"next_choice":"research"} trailing`, "research"},
		{`{"next_choice": "done", "note": "brace } inside a string"}`, "done"},
		{`next_choice = research 🚀`, "research"},
	}
	for _, tc := range cases {
		got, ok := parseChoiceField(tc.in, defaultRouterChoiceField, valid)
		if !ok || got != tc.want {
			t.Errorf("parseChoiceField(%q) = (%q, %v), want (%q, true)", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := parseChoiceField("\u5b8c\u5168\u306b\u7121\u95a2\u4fc2\u306a\u30c6\u30ad\u30b9\u30c8", defaultRouterChoiceField, valid); ok {
		t.Error("parseChoiceField found a choice in unrelated multi-byte text")
	}
}

// ─────────────────────────────────────────────────────────────────────
// WP11c — the doom-loop signal reaches a consumer (01PMAG01 §3.7 / G4).
//
// tool_dispatch has set `should_replan` + `doom_loop_hits` since
// autonomy-recovery-runtime-01PMDL03 WP02 and NOTHING READ EITHER.
// events.go called the intended reader "a future ladder controller"; it
// was never built, and escalationLadderExecutor sat fully implemented
// and wired into zero graphs. These pin the reader.
// ─────────────────────────────────────────────────────────────────────

func routerMenuWithReplan() map[string]any {
	m := routerMenu()
	m["replan"] = map[string]any{"target": "recover", "description": "Step back and try a different approach"}
	return m
}

// TestRouter_DoomLoopOverridesTheModelsChoice is the headline: a
// tripped guard forces the replan route REGARDLESS of what the model
// asked for. That the override wins is the design — a thrashing model's
// own opinion about the next step is precisely the unreliable input.
func TestRouter_DoomLoopOverridesTheModelsChoice(t *testing.T) {
	t.Parallel()
	env := routerEnv(t, &countingLLM{})
	// The model is confidently asking to keep going.
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `{"next_choice": "research"}`},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenuWithReplan(),
		DefaultChoice: "done", ReplanChoice: "replan",
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{
		"in":            "payload",
		"should_replan": true,
		"doom_loop_hits": []map[string]any{
			{"tool": "read_file", "repeats": 3},
			{"tool": "read_file", "repeats": 4},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := res.Outputs.GetString("next"); got != "replan" {
		t.Errorf("next = %q, want %q — a tripped doom-loop guard must override the model", got, "replan")
	}
	if _, ok := res.Outputs["replan"]; !ok {
		t.Error("replan choice port absent")
	}
	if _, ok := res.Outputs["research"]; ok {
		t.Error("the model's stated choice port was still written")
	}

	var override, choice bool
	for _, e := range res.Events.Events {
		switch e.Kind {
		case EventRouterOverride:
			override = true
			for _, want := range []string{`"reason":"doom_loop"`, `"overridden":"research"`, `"doom_loop_hits":2`} {
				if !strings.Contains(string(e.Payload), want) {
					t.Errorf("override payload missing %s: %s", want, e.Payload)
				}
			}
		case EventRouterChoice:
			choice = true
		}
	}
	if !override {
		t.Error("no EventRouterOverride emitted for the doom-loop route")
	}
	if !choice {
		t.Error("no EventRouterChoice emitted; the override must still be an auditable routing decision")
	}
}

// TestRouter_DoomLoopIgnoredWithoutAReplanChoice: a graph that declares
// no replan route is not silently re-pointed at some other choice. The
// signal is consumed where a consumer was authored, and nowhere else.
func TestRouter_DoomLoopIgnoredWithoutAReplanChoice(t *testing.T) {
	t.Parallel()
	env := routerEnv(t, &countingLLM{})
	env.State.SetOutputs("assistant_turn", PortValues{
		"assistant": Message{Role: "assistant", Content: `{"next_choice": "research"}`},
	})
	node := routerNode("route", RouterAttrs{
		SourceNode: "assistant_turn", Choices: routerMenu(), DefaultChoice: "done",
	})
	res, err := routerExecutor{}.Execute(context.Background(), env, node, PortValues{
		"in": "p", "should_replan": true,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := res.Outputs.GetString("next"); got != "research" {
		t.Errorf("next = %q, want the model's choice honored when no replan route is authored", got)
	}
}

// TestRouter_NoDoomLoopLeavesTheModelInCharge is the negative half: the
// override fires on the signal, not on the mere presence of the attr.
func TestRouter_NoDoomLoopLeavesTheModelInCharge(t *testing.T) {
	t.Parallel()
	for name, inputs := range map[string]PortValues{
		"absent":        {"in": "p"},
		"explicitfalse": {"in": "p", "should_replan": false},
	} {
		inputs := inputs
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := routerEnv(t, &countingLLM{})
			env.State.SetOutputs("assistant_turn", PortValues{
				"assistant": Message{Role: "assistant", Content: `{"next_choice": "research"}`},
			})
			node := routerNode("route", RouterAttrs{
				SourceNode: "assistant_turn", Choices: routerMenuWithReplan(),
				DefaultChoice: "done", ReplanChoice: "replan",
			})
			res, err := routerExecutor{}.Execute(context.Background(), env, node, inputs)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			if got, _ := res.Outputs.GetString("next"); got != "research" {
				t.Errorf("next = %q, want the model's choice with no doom loop detected", got)
			}
		})
	}
}
