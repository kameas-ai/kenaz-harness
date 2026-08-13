package agentgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
)

// materializeFakeTools is a race-safe ToolRegistry: the tool_dispatch
// executor fans calls out across goroutines, so every field the test
// body reads goes through snapshot().
type materializeFakeTools struct {
	mu    sync.Mutex
	calls []string
	// failWith, when set, makes every dispatch fail with the returned
	// error. Used by the redaction probes to get real error text — the
	// kind producers build by interpolating the failing arguments —
	// into the EventLog.
	failWith func(ToolCall) error
}

func (f *materializeFakeTools) Has(string) bool { return true }

func (f *materializeFakeTools) Call(_ context.Context, c ToolCall) (ToolResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c.Name)
	fail := f.failWith
	f.mu.Unlock()
	if fail != nil {
		return ToolResult{}, fail(c)
	}
	return ToolResult{Content: "ok:" + c.Name}, nil
}

func (f *materializeFakeTools) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// materializeFakeLLM emits a scripted sequence of turns: the first turns
// carry tool calls, the last carries none, which is what ends the loop.
type materializeFakeLLM struct {
	mu    sync.Mutex
	turn  int
	turns []LLMResponse
}

func (f *materializeFakeLLM) Generate(_ context.Context, _ LLMRequest) (LLMResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.turn >= len(f.turns) {
		return LLMResponse{Content: "done", FinishReason: "stop"}, nil
	}
	r := f.turns[f.turn]
	f.turn++
	return r, nil
}

// materializeChatShapedGraph mirrors chat_default's executed shape:
// a linear preamble, a loop whose body is model → tool_dispatch, and a
// terminal write. It is built in Go rather than loaded from the library
// so the test pins the projection, not the library file.
func materializeChatShapedGraph() Graph {
	return Graph{
		SpecVersion: SpecVersion,
		ID:          "chat_shaped",
		Name:        "Chat shaped",
		Entrypoints: []string{"ask_user"},
		Nodes: []Node{
			{
				ID:    "ask_user",
				Kind:  NodeKindTransform,
				Title: "User turn",
				Attrs: TransformAttrs{Name: "concat"},
			},
			{
				ID:    "assistant_turn",
				Kind:  NodeKindModel,
				Title: "Assistant turn",
				Attrs: ModelAttrs{Model: "test-model", MaxTokens: 64},
				Outputs: []Port{
					{Name: "response", Type: PortTypeMessages},
					{Name: "assistant", Type: PortTypeAny},
					{Name: "tool_calls", Type: PortTypeAny},
					{Name: "finish_reason", Type: PortTypeText},
				},
			},
			{
				ID:    "tool_dispatch",
				Kind:  NodeKindToolDispatch,
				Title: "Dispatch tool calls",
				Attrs: ToolDispatchAttrs{ParallelDispatch: true, MaxConcurrent: 4},
			},
			{
				ID:    "agent_loop",
				Kind:  NodeKindLoop,
				Title: "Agent loop",
				Outputs: []Port{
					{Name: "out", Type: PortTypeAny},
					{Name: "assistant_text", Type: PortTypeText},
					{Name: "tool_call_count", Type: PortTypeNumber},
				},
				Attrs: LoopAttrs{
					MaxIterations: 5,
					Condition:     "tool_call_count > 0",
					Body:          []string{"assistant_turn", "tool_dispatch"},
				},
			},
			{
				ID:    "verdict",
				Kind:  NodeKindDecision,
				Title: "Anything to recover?",
				Inputs: []Port{
					{Name: "in", Type: PortTypeAny},
					{Name: "tool_call_count", Type: PortTypeNumber},
				},
				Attrs: DecisionAttrs{Condition: "tool_call_count > 99", NextTrue: "recover", NextFalse: "assistant_write"},
			},
			{
				ID:    "recover",
				Kind:  NodeKindTransform,
				Title: "Recovery path (not taken)",
				Attrs: TransformAttrs{Name: "concat"},
			},
			{
				ID:    "assistant_write",
				Kind:  NodeKindTransform,
				Title: "Persist assistant turn",
				Attrs: TransformAttrs{Name: "concat"},
			},
		},
		Edges: []Edge{
			{From: EndpointRef{Node: "ask_user", Port: "out"}, To: EndpointRef{Node: "agent_loop", Port: "in"}},
			{From: EndpointRef{Node: "agent_loop", Port: "out"}, To: EndpointRef{Node: "verdict", Port: "in"}},
			{From: EndpointRef{Node: "agent_loop", Port: "tool_call_count"}, To: EndpointRef{Node: "verdict", Port: "tool_call_count"}},
			{From: EndpointRef{Node: "verdict", Port: "true"}, To: EndpointRef{Node: "recover", Port: "in"}},
			{From: EndpointRef{Node: "verdict", Port: "false"}, To: EndpointRef{Node: "assistant_write", Port: "in"}},
			// Validator-only body wiring, exactly as chat_default does it.
			{From: EndpointRef{Node: "ask_user", Port: "out"}, To: EndpointRef{Node: "assistant_turn", Port: "messages"}},
			{From: EndpointRef{Node: "assistant_turn", Port: "tool_calls"}, To: EndpointRef{Node: "tool_dispatch", Port: "tool_calls"}},
		},
	}
}

func toolCallJSON(t *testing.T, args map[string]any) string {
	t.Helper()
	buf, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return string(buf)
}

// runMaterializeFixture runs a two-iteration turn: turn 1 emits two tool
// calls, turn 2 emits one, turn 3 emits none and the loop exits.
func runMaterializeFixture(t *testing.T) (Graph, string, EventLog, *materializeFakeTools) {
	t.Helper()
	g := materializeChatShapedGraph()
	if err := Validate(g); err != nil {
		t.Fatalf("fixture graph does not validate: %v", err)
	}
	llm := &materializeFakeLLM{turns: []LLMResponse{
		{Content: "calling tools", TokensUsed: 11, CostUSD: 0.01, FinishReason: "tool_use", ToolCalls: []ToolCallRequest{
			{ID: "toolu_01aa", Name: "kenaz__read_file", Arguments: toolCallJSON(t, map[string]any{"path": "/etc/shadow", "limit": 10})},
			{ID: "toolu_01bb", Name: "fs__list_dir", Arguments: toolCallJSON(t, map[string]any{"dir": "/tmp"})},
		}},
		{Content: "one more", TokensUsed: 7, FinishReason: "tool_use", ToolCalls: []ToolCallRequest{
			{ID: "toolu_01cc", Name: "kenaz__bash", Arguments: toolCallJSON(t, map[string]any{"command": "ls -la /secret"})},
		}},
		{Content: "final answer", TokensUsed: 5, FinishReason: "stop"},
	}}
	tools := &materializeFakeTools{}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{
		RunID: "run-materialize-1",
		Graph: &g,
		LLM:   llm,
		Tools: tools,
	}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	return g, env.RunID, log, tools
}

// TestMaterializeRun_PerIterationExpansion is the core of contract
// clauses 1 + 2: every action taken is a node, and tool_dispatch's
// fan-out is N nodes rather than one opaque one.
func TestMaterializeRun_PerIterationExpansion(t *testing.T) {
	src, runID, log, tools := runMaterializeFixture(t)
	if got := len(tools.snapshot()); got != 3 {
		t.Fatalf("fixture ran %d tool calls, want 3", got)
	}

	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}

	byID := map[string]Node{}
	for _, n := range mg.Nodes {
		byID[n.ID] = n
	}

	// Three model turns fired, so three distinct model instances.
	for _, want := range []string{"assistant_turn@1", "assistant_turn@2", "assistant_turn@3"} {
		n, ok := byID[want]
		if !ok {
			t.Fatalf("missing materialized node %q; got %v", want, nodeIDsOf(mg))
		}
		if n.Kind != NodeKindModel {
			t.Errorf("node %s kind = %s, want model", want, n.Kind)
		}
		if n.Materialized == nil || n.Materialized.SourceNode != "assistant_turn" {
			t.Errorf("node %s missing provenance back to assistant_turn", want)
		}
	}
	if got := byID["assistant_turn@1"].Materialized.Iteration; got != 1 {
		t.Errorf("assistant_turn@1 iteration = %d, want 1", got)
	}
	if got := byID["assistant_turn@2"].Materialized.Iteration; got != 2 {
		t.Errorf("assistant_turn@2 iteration = %d, want 2", got)
	}
	if got := byID["assistant_turn@1"].Materialized.Tokens; got != 11 {
		t.Errorf("assistant_turn@1 tokens = %d, want 11", got)
	}

	// Clause 2: the fan-out is node materialization. Two calls in
	// iteration 1, one in iteration 2 — each its own node instance.
	wantCalls := map[string]string{
		"tool_dispatch@1[toolu_01aa]": "kenaz__read_file",
		"tool_dispatch@1[toolu_01bb]": "fs__list_dir",
		"tool_dispatch@2[toolu_01cc]": "kenaz__bash",
	}
	for id, tool := range wantCalls {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("missing per-call node %q; got %v", id, nodeIDsOf(mg))
		}
		if n.Materialized.Tool != tool {
			t.Errorf("%s tool = %q, want %q", id, n.Materialized.Tool, tool)
		}
		if n.Materialized.CallID == "" {
			t.Errorf("%s has no call id", id)
		}
	}
	if got := byID["tool_dispatch@1[toolu_01aa]"].Materialized.Server; got != "kenaz" {
		t.Errorf("server = %q, want kenaz", got)
	}
	if got := byID["tool_dispatch@1[toolu_01bb]"].Materialized.Server; got != "fs" {
		t.Errorf("server = %q, want fs", got)
	}
	if got := byID["tool_dispatch@1"].Materialized.ToolCalls; got != 2 {
		t.Errorf("tool_dispatch@1 fan-out count = %d, want 2", got)
	}

	// The not-taken branch is visible as a skip rather than absent.
	rec, ok := byID["recover@1"]
	if !ok {
		t.Fatalf("skipped branch not materialized; got %v", nodeIDsOf(mg))
	}
	if rec.Materialized.Status != MaterializedStatusSkipped {
		t.Errorf("recover@1 status = %q, want %q", rec.Materialized.Status, MaterializedStatusSkipped)
	}

	// The loop unrolled: its body lists the real instances, in order.
	loop, ok := byID["agent_loop@1"]
	if !ok {
		t.Fatal("agent_loop@1 missing")
	}
	la, ok := loop.Attrs.(LoopAttrs)
	if !ok {
		t.Fatalf("agent_loop@1 attrs type %T", loop.Attrs)
	}
	if len(la.Body) < 6 {
		t.Errorf("unrolled body = %v, want every fired instance", la.Body)
	}
	if la.Body[0] != "assistant_turn@1" {
		t.Errorf("unrolled body starts at %q, want assistant_turn@1", la.Body[0])
	}
}

// TestMaterializeRun_RedactsToolArguments is the redaction contract:
// the EventLog holds raw args, the materialized graph must not.
func TestMaterializeRun_RedactsToolArguments(t *testing.T) {
	src, runID, log, _ := runMaterializeFixture(t)

	// Precondition: the raw value really is in the log, so this test
	// fails loudly if the projection ever starts copying it.
	var sawRaw bool
	if err := log.Replay(runID, func(e Event) error {
		if e.Kind == EventToolCall && strings.Contains(string(e.Payload), "/etc/shadow") {
			sawRaw = true
		}
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !sawRaw {
		t.Fatal("fixture precondition: expected raw args in the EventLog")
	}

	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	yaml, err := DumpYAML(mg)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	for _, secret := range []string{"/etc/shadow", "ls -la /secret", "/tmp"} {
		if strings.Contains(string(yaml), secret) {
			t.Errorf("materialized graph leaked raw argument value %q", secret)
		}
	}
	var summary string
	for _, n := range mg.Nodes {
		if n.ID == "tool_dispatch@1[toolu_01aa]" && n.Materialized != nil {
			summary = n.Materialized.ArgsSummary
		}
	}
	if want := "2 arguments: limit (number), path (string)"; summary != want {
		t.Errorf("args summary = %q, want %q", summary, want)
	}
}

// TestMaterializeRun_RoundTripsAsGraphSpec is contract clause 3: a
// completed turn round-trips as a graph spec — it serialises, reloads,
// validates, and re-projects identically.
func TestMaterializeRun_RoundTripsAsGraphSpec(t *testing.T) {
	src, runID, log, _ := runMaterializeFixture(t)

	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	if err := Validate(mg); err != nil {
		t.Fatalf("materialized graph does not validate: %v", err)
	}

	yamlBytes, err := DumpYAML(mg)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	reloaded, err := LoadYAML(yamlBytes)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if err := Validate(reloaded); err != nil {
		t.Fatalf("reloaded materialized graph does not validate: %v", err)
	}
	roundTripped, err := DumpYAML(reloaded)
	if err != nil {
		t.Fatalf("DumpYAML(reloaded): %v", err)
	}
	if string(roundTripped) != string(yamlBytes) {
		t.Errorf("YAML round-trip is not stable:\n--- first ---\n%s\n--- second ---\n%s", yamlBytes, roundTripped)
	}

	// The JSON wire (what the RPC returns) round-trips too.
	jsonBytes, err := DumpJSON(mg)
	if err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	fromJSON, err := LoadJSON(jsonBytes)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if err := Validate(fromJSON); err != nil {
		t.Fatalf("JSON-reloaded materialized graph does not validate: %v", err)
	}
	if len(fromJSON.Nodes) != len(mg.Nodes) {
		t.Errorf("JSON round-trip node count = %d, want %d", len(fromJSON.Nodes), len(mg.Nodes))
	}
	// Provenance survives the wire — otherwise the editor could not
	// show what each node did.
	var withProv int
	for _, n := range fromJSON.Nodes {
		if n.Materialized != nil && n.Materialized.SourceNode != "" {
			withProv++
		}
	}
	if withProv != len(mg.Nodes) {
		t.Errorf("%d/%d reloaded nodes kept provenance", withProv, len(mg.Nodes))
	}

	// Re-projecting the same run yields the same spec: the projection
	// is a pure function of (spec, log).
	again, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun (2nd): %v", err)
	}
	againYAML, err := DumpYAML(again)
	if err != nil {
		t.Fatalf("DumpYAML(2nd): %v", err)
	}
	if string(againYAML) != string(yamlBytes) {
		t.Error("re-projecting the same run produced a different spec")
	}
}

// TestMaterializeRun_AuthoredGraphsAreByteIdentical pins that adding the
// materialization block to Node changed no authored graph's bytes.
func TestMaterializeRun_AuthoredGraphsAreByteIdentical(t *testing.T) {
	g := materializeChatShapedGraph()
	out, err := DumpYAML(g)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	if strings.Contains(string(out), "materialized:") {
		t.Errorf("authored graph emitted a materialization block:\n%s", out)
	}
	if strings.Contains(string(out), "provenance:") {
		t.Errorf("authored graph emitted a provenance block:\n%s", out)
	}
}

// TestMaterializeRun_Errors covers the guard rails.
func TestMaterializeRun_Errors(t *testing.T) {
	g := materializeChatShapedGraph()
	log := NewMemoryEventLog()
	if _, err := MaterializeRun(g, "", log); err == nil {
		t.Error("empty run id should error")
	}
	if _, err := MaterializeRun(g, "r", nil); err == nil {
		t.Error("nil log should error")
	}
	if _, err := MaterializeRun(g, "no-such-run", log); err == nil {
		t.Error("a run with no recorded fires should error")
	}
}

// TestLoopBodyFiresEmitLifecycleEvents pins the additive kernel change
// the projection depends on: a Loop body fire now gets the same
// node_start / node_complete pair every other node gets, plus the
// iteration + container keys.
func TestLoopBodyFiresEmitLifecycleEvents(t *testing.T) {
	src, runID, log, _ := runMaterializeFixture(t)
	_ = src

	type row struct {
		kind      EventKind
		iteration int
		container string
	}
	var starts []row
	if err := log.Replay(runID, func(e Event) error {
		if e.NodeID != "assistant_turn" || e.Kind != EventNodeStart {
			return nil
		}
		p := decodeEventPayload(e.Payload)
		starts = append(starts, row{e.Kind, payloadInt(p, "iteration"), payloadString(p, "container")})
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(starts) != 3 {
		t.Fatalf("assistant_turn node_start count = %d, want 3", len(starts))
	}
	for i, r := range starts {
		if r.iteration != i+1 {
			t.Errorf("start #%d iteration = %d, want %d", i, r.iteration, i+1)
		}
		if r.container != "agent_loop" {
			t.Errorf("start #%d container = %q, want agent_loop", i, r.container)
		}
	}
}

func nodeIDsOf(g Graph) []string {
	out := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

// ── review finding F1: error text must never reach the artifact ──────
//
// Producers interpolate raw arguments into error text: exec_dispatch
// quotes the failing call, and os errors quote the path
// (`open /etc/shadow: permission denied`). The projection therefore
// classifies rather than copies, and these two probes are what holds
// that line — one for an incidental leak (a file path an OS error
// happened to include) and one for a deliberately planted secret.

// runLeakProbeFixture runs a turn whose every tool call fails with an
// error containing `secret`.
func runLeakProbeFixture(t *testing.T, secret string) (Graph, string, EventLog) {
	t.Helper()
	g := materializeChatShapedGraph()
	llm := &materializeFakeLLM{turns: []LLMResponse{
		{Content: "calling", FinishReason: "tool_use", ToolCalls: []ToolCallRequest{
			{ID: "toolu_leak1", Name: "kenaz__read_file", Arguments: toolCallJSON(t, map[string]any{"path": secret})},
			{ID: "toolu_leak2", Name: "kenaz__bash", Arguments: toolCallJSON(t, map[string]any{"command": "cat " + secret})},
		}},
		{Content: "giving up", FinishReason: "stop"},
	}}
	tools := &materializeFakeTools{
		failWith: func(c ToolCall) error {
			// The exact shape a real producer emits: the failing
			// argument, verbatim, inside the message.
			return fmt.Errorf("open %v: permission denied", c.Args["path"])
		},
	}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{RunID: "run-leak-probe", Graph: &g, LLM: llm, Tools: tools}
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("kernel run: %v", err)
	}
	return g, env.RunID, log
}

func TestMaterializeRun_ErrorTextNeverReachesArtifact(t *testing.T) {
	probes := []struct {
		name   string
		secret string
	}{
		{"file path in error", "/etc/shadow"},
		{"planted credential in error", "sk-live-PLANTED-9f3a2b7c"},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			src, runID, log := runLeakProbeFixture(t, probe.secret)

			// Precondition: the secret really is in the raw log, so
			// this probe fails loudly if the projection ever starts
			// forwarding error text again.
			var inLog bool
			if err := log.Replay(runID, func(e Event) error {
				if e.Kind == EventNodeError && strings.Contains(string(e.Payload), probe.secret) {
					inLog = true
				}
				return nil
			}); err != nil {
				t.Fatalf("replay: %v", err)
			}
			if !inLog {
				t.Fatalf("probe precondition: expected %q in a node_error payload", probe.secret)
			}

			mg, err := MaterializeRun(src, runID, log)
			if err != nil {
				t.Fatalf("MaterializeRun: %v", err)
			}

			// The YAML artifact.
			yamlBytes, err := DumpYAML(mg)
			if err != nil {
				t.Fatalf("DumpYAML: %v", err)
			}
			if strings.Contains(string(yamlBytes), probe.secret) {
				t.Errorf("materialized YAML leaked %q", probe.secret)
			}
			// The JSON the RPC hands the frontend.
			jsonBytes, err := DumpJSON(mg)
			if err != nil {
				t.Fatalf("DumpJSON: %v", err)
			}
			if strings.Contains(string(jsonBytes), probe.secret) {
				t.Errorf("materialized JSON leaked %q", probe.secret)
			}

			// The failure is still legible as structure.
			var classed int
			vocabulary := map[string]bool{
				MaterializedErrorNotFound: true, MaterializedErrorPermissionDenied: true,
				MaterializedErrorTimeout: true, MaterializedErrorCancelled: true,
				MaterializedErrorBudgetExceeded: true, MaterializedErrorAuthFailed: true,
				MaterializedErrorRateLimited: true, MaterializedErrorInvalidInput: true,
				MaterializedErrorUnavailable: true, MaterializedErrorNotImplemented: true,
				MaterializedErrorOther: true,
			}
			for _, n := range mg.Nodes {
				if n.Materialized == nil || n.Materialized.ErrorClass == "" {
					continue
				}
				classed++
				for _, cls := range strings.Split(n.Materialized.ErrorClass, ",") {
					if !vocabulary[cls] {
						t.Errorf("node %s error_class %q is not in the closed vocabulary", n.ID, cls)
					}
				}
				if n.Materialized.ErrorBytes <= 0 {
					t.Errorf("node %s classified an error but recorded no byte count", n.ID)
				}
			}
			if classed == 0 {
				t.Error("no node carried an error classification — the failure vanished from the artifact")
			}
		})
	}
}

// TestMaterializeRun_PerCallErrorsAllRetained is review finding N4: a
// parallel fan-out that fails several calls records EVERY failure, on
// the per-call node that failed, not just the first one on the parent.
func TestMaterializeRun_PerCallErrorsAllRetained(t *testing.T) {
	src, runID, log := runLeakProbeFixture(t, "/etc/shadow")
	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	byID := map[string]Node{}
	for _, n := range mg.Nodes {
		byID[n.ID] = n
	}
	failed := 0
	for _, id := range []string{"tool_dispatch@1[toolu_leak1]", "tool_dispatch@1[toolu_leak2]"} {
		n, ok := byID[id]
		if !ok {
			t.Fatalf("missing per-call node %q; got %v", id, nodeIDsOf(mg))
		}
		if n.Materialized.ErrorClass == "" {
			t.Errorf("%s: per-call failure was not recorded on the call node", id)
			continue
		}
		failed++
		if n.Materialized.Status != MaterializedStatusError {
			t.Errorf("%s status = %q, want error", id, n.Materialized.Status)
		}
	}
	if failed != 2 {
		t.Errorf("recorded %d per-call failures, want 2 — a fan-out must not keep only the first", failed)
	}
	// The dispatcher counted them without being failed itself.
	if got := byID["tool_dispatch@1"].Materialized.ErrorCount; got != 2 {
		t.Errorf("dispatcher error_count = %d, want 2", got)
	}
}

// TestMaterializeRun_ArgSummaryKeysAreBounded is review finding N5.
func TestMaterializeRun_ArgSummaryKeysAreBounded(t *testing.T) {
	huge := strings.Repeat("k", 5000)
	got := summarizeArgsForArtifact(map[string]any{huge: "v", huge + "x": 1})
	if len([]rune(got)) > maxMaterializedArgsSummaryRunes+len("...[truncated]") {
		t.Errorf("summary is %d runes, want <= %d", len([]rune(got)), maxMaterializedArgsSummaryRunes)
	}
	if strings.Contains(got, strings.Repeat("k", maxMaterializedArgKeyRunes+1)) {
		t.Error("an argument key was rendered past the cap")
	}
	// Two keys that collapse onto the same capped form both survive.
	if !strings.Contains(got, "2 arguments") {
		t.Errorf("colliding capped keys lost an argument: %q", got)
	}
}

// ── review finding F4: every body-fire start closes ──────────────────

// TestLoopBodyFires_NoDanglingStartOnBudgetCap trips a budget cap
// inside the loop body and asserts the log has no node_start without a
// matching node_complete or node_error. Before the fix, the fire that
// KILLED the run was the one the trace showed as still in flight.
func TestLoopBodyFires_NoDanglingStartOnBudgetCap(t *testing.T) {
	g := materializeChatShapedGraph()
	llm := &materializeFakeLLM{turns: []LLMResponse{
		{Content: "one", FinishReason: "tool_use", ToolCalls: []ToolCallRequest{
			{ID: "toolu_x", Name: "kenaz__noop", Arguments: "{}"},
		}},
		{Content: "two", FinishReason: "tool_use", ToolCalls: []ToolCallRequest{
			{ID: "toolu_y", Name: "kenaz__noop", Arguments: "{}"},
		}},
		{Content: "three", FinishReason: "stop"},
	}}
	log := NewMemoryEventLog()
	k := NewKernel(WithEventLog(log))
	env := &Env{
		RunID:  "run-budget-cap",
		Graph:  &g,
		LLM:    llm,
		Tools:  &materializeFakeTools{},
		Budget: Budget{MaxLLMCallsPerRun: 1},
	}
	// The cap is expected to fail the run; what is under test is the log.
	runErr := k.Run(context.Background(), env)
	if runErr == nil {
		t.Fatal("expected the budget cap to fail the run")
	}

	openFires := map[string]int{}
	var dangling []string
	if err := log.Replay(env.RunID, func(e Event) error {
		switch e.Kind {
		case EventNodeStart:
			openFires[e.NodeID]++
		case EventNodeComplete, EventNodeError:
			p := decodeEventPayload(e.Payload)
			if retried, _ := p["retried"].(bool); retried {
				return nil // a retried error is not a close
			}
			if openFires[e.NodeID] > 0 {
				openFires[e.NodeID]--
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}
	for node, n := range openFires {
		if n > 0 {
			dangling = append(dangling, fmt.Sprintf("%s x%d", node, n))
		}
	}
	if len(dangling) > 0 {
		sort.Strings(dangling)
		t.Errorf("node_start events with no matching close: %v", dangling)
	}

	// And the projection shows the failure rather than an open fire.
	mg, err := MaterializeRun(g, env.RunID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	var sawError bool
	for _, n := range mg.Nodes {
		if n.Materialized == nil {
			continue
		}
		if n.Materialized.Status == MaterializedStatusIncomplete {
			t.Errorf("node %s materialized as incomplete; every fire should have closed", n.ID)
		}
		if n.Materialized.Status == MaterializedStatusError {
			sawError = true
			if n.Materialized.ErrorClass == "" {
				t.Errorf("node %s failed with no classification", n.ID)
			}
		}
	}
	if !sawError {
		t.Error("the budget cap did not surface as a failed node in the projection")
	}
}

// ── review finding F3: the edge set is the claim, so pin it exactly ──
//
// "Every action is a node" is only half the contract; the other half is
// that the edges say what flowed where. Counting edges does not test
// that: three separate mutations (dropping every edge, disabling the
// per-iteration wrap-around, disabling the tool fan-out) all leave a
// graph that still validates and still round-trips. So this asserts the
// exact set, as a set — order is an implementation detail, membership
// is the contract.
func TestMaterializeRun_EdgeSetIsExact(t *testing.T) {
	src, runID, log, _ := runMaterializeFixture(t)
	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}

	type edge struct{ fromNode, fromPort, toNode, toPort string }
	want := map[edge]string{
		// The run's spine, outside the loop.
		{"ask_user@1", "out", "agent_loop@1", "in"}:                         "entry into the loop",
		{"agent_loop@1", "out", "verdict@1", "in"}:                          "loop payload to the decision",
		{"agent_loop@1", "tool_call_count", "verdict@1", "tool_call_count"}: "loop counter to the decision",
		{"verdict@1", "true", "recover@1", "in"}:                            "the branch that was skipped",
		{"verdict@1", "false", "assistant_write@1", "in"}:                   "the branch that was taken",

		// Into the body: the user's turn reached the first model call.
		{"ask_user@1", "out", "assistant_turn@1", "messages"}: "entry into the body",

		// Per-iteration: model to dispatcher, once per iteration.
		{"assistant_turn@1", "tool_calls", "tool_dispatch@1", "tool_calls"}: "iteration 1 dispatch",
		{"assistant_turn@2", "tool_calls", "tool_dispatch@2", "tool_calls"}: "iteration 2 dispatch",
		{"assistant_turn@3", "tool_calls", "tool_dispatch@3", "tool_calls"}: "iteration 3 dispatch",

		// The wrap-around: what "and then it went round again" looks
		// like once the loop is unrolled (connectIterations).
		{"tool_dispatch@1", "messages", "assistant_turn@2", "messages"}: "iteration 1 -> 2",
		{"tool_dispatch@2", "messages", "assistant_turn@3", "messages"}: "iteration 2 -> 3",

		// The fan-out: each materialized call is fed the tool_calls the
		// model emitted (connectFanOut).
		{"assistant_turn@1", "tool_calls", "tool_dispatch@1[toolu_01aa]", "tool_calls"}: "fan-out call 1",
		{"assistant_turn@1", "tool_calls", "tool_dispatch@1[toolu_01bb]", "tool_calls"}: "fan-out call 2",
		{"assistant_turn@2", "tool_calls", "tool_dispatch@2[toolu_01cc]", "tool_calls"}: "fan-out call 3",
	}

	got := map[edge]bool{}
	for _, e := range mg.Edges {
		key := edge{e.From.Node, e.From.Port, e.To.Node, e.To.Port}
		if got[key] {
			t.Errorf("duplicate edge %v", key)
		}
		got[key] = true
	}
	for w, why := range want {
		if !got[w] {
			t.Errorf("missing edge %s.%s -> %s.%s (%s)", w.fromNode, w.fromPort, w.toNode, w.toPort, why)
		}
	}
	for g := range got {
		if _, ok := want[g]; !ok {
			t.Errorf("unexpected edge %s.%s -> %s.%s", g.fromNode, g.fromPort, g.toNode, g.toPort)
		}
	}
	if len(got) != len(want) {
		t.Errorf("edge count = %d, want %d", len(got), len(want))
	}
}
