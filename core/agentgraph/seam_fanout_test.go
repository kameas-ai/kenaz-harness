package agentgraph_test

// Seam-fanout matrix tests (WP04 — integration-test-harness-01KQ8TD1).
//
// Layer 2 of the integration test harness. Every kernel kind must have:
//  1. A YAML fixture in testdata/seam_fanout/<kind>.yaml declaring which
//     seams the executor exercises (completeness gate, enforced by
//     TestSeamFanout_AllKindsHaveFixture).
//  2. An optional kernel-level test that drives the kind through the
//     recorder fakes and asserts the exact seam calls (the tests below).
//
// The 495fb13 bug class ("executor called history but dropped messages") is
// caught by TestSeamFanout_HistoryRead_HistoryReadCalledAndMessagesForwarded.
//
// Adding a new kind without a testdata/seam_fanout/<kind>.yaml file causes
// TestSeamFanout_AllKindsHaveFixture to fail naming the missing kind.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/internal/recorders"
	"gopkg.in/yaml.v3"
)

// ── Fixture loading ───────────────────────────────────────────────────────────

// seamFanoutFixture is the deserialized shape of testdata/seam_fanout/<kind>.yaml.
type seamFanoutFixture struct {
	Kind  string `yaml:"kind"`
	Seams []struct {
		Seam     string `yaml:"seam"`
		Required bool   `yaml:"required"`
	} `yaml:"seams"`
	Notes string `yaml:"notes"`
}

// loadSeamFanoutFixtures reads all YAML files from testdata/seam_fanout/
// and returns a map of kind → fixture.
func loadSeamFanoutFixtures(t *testing.T) map[string]seamFanoutFixture {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("seam_fanout: cannot determine caller location")
	}
	dir := filepath.Join(filepath.Dir(thisFile), "testdata", "seam_fanout")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("seam_fanout: read testdata/seam_fanout: %v", err)
	}
	fixtures := make(map[string]seamFanoutFixture)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("seam_fanout: read %s: %v", entry.Name(), err)
		}
		var f seamFanoutFixture
		if err := yaml.Unmarshal(data, &f); err != nil {
			t.Fatalf("seam_fanout: parse %s: %v", entry.Name(), err)
		}
		if f.Kind == "" {
			t.Fatalf("seam_fanout: fixture %s has no kind field", entry.Name())
		}
		fixtures[f.Kind] = f
	}
	return fixtures
}

// ── TestSeamFanout_AllKindsHaveFixture (completeness gate) ───────────────────

// TestSeamFanout_AllKindsHaveFixture is the gating completeness check.
// Every kind returned by agentgraph.AllNodeKinds() must have a corresponding
// YAML fixture in testdata/seam_fanout/<kind>.yaml. Adding a new kind without
// a fixture fails this test with a message naming the missing kind.
func TestSeamFanout_AllKindsHaveFixture(t *testing.T) {
	fixtures := loadSeamFanoutFixtures(t)
	var missing []string
	for _, kind := range agentgraph.AllNodeKinds() {
		kindStr := string(kind)
		if _, ok := fixtures[kindStr]; !ok {
			missing = append(missing, kindStr)
		}
	}
	sort.Strings(missing)
	for _, k := range missing {
		t.Errorf(
			"seam_fanout: missing fixture for kind %q — "+
				"create testdata/seam_fanout/%s.yaml with kind: %s and seams: []",
			k, k, k,
		)
	}
}

// TestSeamFanout_FixturesHaveValidKinds verifies that every fixture's
// kind field is actually a known NodeKind (catches typos in fixture files).
func TestSeamFanout_FixturesHaveValidKinds(t *testing.T) {
	fixtures := loadSeamFanoutFixtures(t)
	known := make(map[string]bool)
	for _, k := range agentgraph.AllNodeKinds() {
		known[string(k)] = true
	}
	for kindStr := range fixtures {
		if !known[kindStr] {
			t.Errorf("seam_fanout: fixture kind %q is not a known NodeKind", kindStr)
		}
	}
}

// ── Kernel-level seam-fanout tests ───────────────────────────────────────────

// buildOneNodeGraph builds a minimal graph with a single node of the given kind.
func buildOneNodeGraph(nodeID string, kind agentgraph.NodeKind, attrs agentgraph.NodeAttrs) *agentgraph.Graph {
	return &agentgraph.Graph{
		ID:          fmt.Sprintf("test-%s", string(kind)),
		SpecVersion: "1",
		Entrypoints: []string{nodeID},
		Nodes: []agentgraph.Node{
			{ID: nodeID, Kind: kind, Attrs: attrs},
		},
	}
}

// buildTwoNodeGraph builds a graph: src (transform "emit_text") → dst (the
// node under test). The "emit_text" transform must be registered on
// env.Transforms before calling Run; it emits the given port name with the
// given value so the edge routes real content into the dst node's input.
func buildTwoNodeGraph(srcID, dstID string, dstKind agentgraph.NodeKind, dstAttrs agentgraph.NodeAttrs, fromPort, toPort string) *agentgraph.Graph {
	return &agentgraph.Graph{
		ID:          fmt.Sprintf("test-%s-with-input", string(dstKind)),
		SpecVersion: "1",
		Entrypoints: []string{srcID},
		Nodes: []agentgraph.Node{
			{ID: srcID, Kind: agentgraph.NodeKindTransform, Attrs: agentgraph.TransformAttrs{Name: "emit_text"}},
			{ID: dstID, Kind: dstKind, Attrs: dstAttrs},
		},
		Edges: []agentgraph.Edge{
			{
				From: agentgraph.EndpointRef{Node: srcID, Port: fromPort},
				To:   agentgraph.EndpointRef{Node: dstID, Port: toPort},
			},
		},
	}
}

// recorderBundle holds all recorder fakes for a single-run Env.
type recorderBundle struct {
	LLM         *recorders.LLMProvider
	Tools       *recorders.ToolRegistry
	Memory      *recorders.MemoryStore
	History     *recorders.HistoryReader
	HWriter     *recorders.HistoryWriter
	Attachments *recorders.AttachmentResolver
	Policy      *recorders.PolicyGate
	Trace       *recorders.TraceSink
	Ask         *recorders.AskBus
	Compactor   *recorders.Compactor
	BashOutput  *recorders.BashOutputStore
	Corpus      *recorders.CorpusBackend
	Activities  *recorders.ActivityCatalog
}

// newRecorderEnv builds an Env with all seams wired to recording fakes.
// Returns the env and a bundle of all recorders. The caller must set
// env.Graph before passing env to k.Run.
func newRecorderEnv(g *agentgraph.Graph, sessionID string) (*agentgraph.Env, *recorderBundle) {
	rb := &recorderBundle{
		LLM:         &recorders.LLMProvider{Resp: agentgraph.LLMResponse{Content: "ok", FinishReason: "stop"}},
		Tools:       &recorders.ToolRegistry{},
		Memory:      &recorders.MemoryStore{},
		History:     &recorders.HistoryReader{},
		HWriter:     &recorders.HistoryWriter{},
		Attachments: &recorders.AttachmentResolver{},
		Policy:      &recorders.PolicyGate{},
		Trace:       &recorders.TraceSink{},
		Ask:         &recorders.AskBus{},
		Compactor:   &recorders.Compactor{},
		BashOutput:  &recorders.BashOutputStore{},
		Corpus:      &recorders.CorpusBackend{},
		Activities:  &recorders.ActivityCatalog{},
	}
	env := &agentgraph.Env{
		RunID:         "fanout-run",
		SessionID:     sessionID,
		Graph:         g,
		LLM:           rb.LLM,
		Tools:         rb.Tools,
		Memory:        rb.Memory,
		History:       rb.History,
		HistoryWriter: rb.HWriter,
		Attachments:   rb.Attachments,
		Policy:        rb.Policy,
		Trace:         rb.Trace,
		Ask:           rb.Ask,
		Compactor:     rb.Compactor,
		BashOutput:    rb.BashOutput,
		Corpus:        rb.Corpus,
		Activities:    rb.Activities,
	}
	return env, rb
}

// ---- model kind ----

// TestSeamFanout_Model_LLMGenerateCalled verifies that a model node calls
// LLMProvider.Generate exactly once and that the system prompt is forwarded.
func TestSeamFanout_Model_LLMGenerateCalled(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("m1", agentgraph.NodeKindModel, agentgraph.ModelAttrs{
		Provider:     "test",
		Model:        "m1",
		MaxTokens:    100,
		SystemPrompt: "you are helpful",
	})
	env, rb := newRecorderEnv(g, "sess-1")
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.LLM.RequireCalledOnce(t)
	rb.LLM.RequireSystemPrompt(t, "you are helpful")
}

// TestSeamFanout_Model_HistoryReadCalledBeforeLLM verifies the 495fb13 bug
// class is caught: when session_id is set, HistoryReader.History must be
// called before LLMProvider.Generate, and the history messages must appear
// in the LLM request.
func TestSeamFanout_Model_HistoryReadCalledBeforeLLM(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("m1", agentgraph.NodeKindModel, agentgraph.ModelAttrs{
		Provider:  "test",
		Model:     "m1",
		MaxTokens: 100,
	})
	env, rb := newRecorderEnv(g, "sess-hist")
	// Pre-load history so the executor finds messages to pull.
	rb.History.Result = []agentgraph.Message{
		{Role: "user", Content: "previous question"},
		{Role: "assistant", Content: "previous answer"},
	}
	k := agentgraph.NewKernel()
	// The model node has no incoming edges, so inputs.messages is empty.
	// The executor must pull from env.History when SessionID is set.
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// History must have been read.
	rb.History.RequireCalledWithSession(t, "sess-hist")
	// LLM must have been called with the history messages included.
	rb.LLM.RequireMessageCount(t, 2)
}

// ---- history_read kind ----

// TestSeamFanout_HistoryRead_HistoryReadCalledAndMessagesForwarded is the
// direct regression test for the 495fb13 bug: the executor called history but
// did not pass the messages forward to output ports.
func TestSeamFanout_HistoryRead_HistoryReadCalledAndMessagesForwarded(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("hr1", agentgraph.NodeKindHistoryRead, agentgraph.HistoryReadAttrs{N: 5})
	env, rb := newRecorderEnv(g, "sess-2")
	rb.History.Result = []agentgraph.Message{
		{Role: "user", Content: "msg1"},
		{Role: "assistant", Content: "msg2"},
	}
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// HistoryReader.History must have been called.
	rb.History.RequireCalledWithSession(t, "sess-2")
	// Messages must appear on the output port (the 495fb13 regression).
	outputs := env.State.Outputs("hr1")
	msgs, ok := outputs["messages"]
	if !ok {
		t.Fatal("history_read output 'messages' port is absent — 495fb13 regression")
	}
	msgSlice, ok := msgs.([]agentgraph.Message)
	if !ok {
		t.Fatalf("history_read output 'messages' is %T, want []agentgraph.Message", msgs)
	}
	if len(msgSlice) != 2 {
		t.Errorf("history_read output 'messages' len = %d, want 2", len(msgSlice))
	}
}

// ---- session_write kind ----

// TestSeamFanout_SessionWrite_AppendMessageCalled verifies that session_write
// calls HistoryWriter.AppendMessage with the correct role.
// Uses a two-node graph: an "emit_text" transform node supplies the text
// input via an edge, then session_write writes it to history.
func TestSeamFanout_SessionWrite_AppendMessageCalled(t *testing.T) {
	t.Parallel()
	// src1.text → sw1.text: emit_text emits port "text" = "response text".
	g := buildTwoNodeGraph("src1", "sw1", agentgraph.NodeKindSessionWrite,
		agentgraph.SessionWriteAttrs{Role: "assistant"},
		"text", "text",
	)
	env, rb := newRecorderEnv(g, "sess-3")
	// Register an "emit_text" transform that returns {text: "response text"}.
	env.Transforms = agentgraph.NewTransformRegistry()
	agentgraph.BuiltinTransforms(env.Transforms)
	env.Transforms.Register("emit_text", func(_ context.Context, _ agentgraph.PortValues, _ map[string]any) (agentgraph.PortValues, error) {
		return agentgraph.PortValues{"text": "response text"}, nil
	})
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.HWriter.RequireCalledWithRole(t, "assistant")
}

// ---- trace_write kind ----

// TestSeamFanout_TraceWrite_SpanCalled verifies that trace_write exercises
// the TraceSink.Span seam. The kernel wraps every node fire with a span;
// this test asserts that TraceSink.Span is called at least once during the run.
func TestSeamFanout_TraceWrite_SpanCalled(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("tw1", agentgraph.NodeKindTraceWrite, agentgraph.TraceWriteAttrs{
		Message:  "kernel checkpoint",
		Severity: "info",
	})
	env, rb := newRecorderEnv(g, "sess-4")
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// TraceSink.Span is called by the kernel for every node fire.
	if rb.Trace.CallCount() == 0 {
		t.Error("TraceSink.Span: not called — kernel did not wrap trace_write node")
	}
}

// ---- memory kind ----

// TestSeamFanout_Memory_WriteCalledWithContent verifies that a memory node
// calls MemoryStore.Write with content provided via attrs.
func TestSeamFanout_Memory_WriteCalledWithContent(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("mem1", agentgraph.NodeKindMemory, agentgraph.MemoryAttrs{
		Mode:    "write",
		Scope:   "session",
		Content: "important fact",
	})
	env, rb := newRecorderEnv(g, "sess-5")
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.Memory.RequireWriteWithScope(t, "session")
}

// ---- tool kind ----

// TestSeamFanout_Tool_RegistryCallCalled verifies that a tool node calls
// ToolRegistry.Call with the configured tool name.
func TestSeamFanout_Tool_RegistryCallCalled(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("tool1", agentgraph.NodeKindTool, agentgraph.ToolAttrs{
		Name: "read_file",
	})
	env, rb := newRecorderEnv(g, "sess-6")
	rb.Tools.Register("read_file", agentgraph.ToolResult{Content: "file contents"}, nil)
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.Tools.RequireCalledWithTool(t, "read_file")
}

// ---- read_file kind ----

// TestSeamFanout_ReadFile_PolicyGateChecked verifies that read_file consults
// PolicyGate.CheckFileRead before reading.
func TestSeamFanout_ReadFile_PolicyGateChecked(t *testing.T) {
	t.Parallel()
	const testFile = "/tmp/test-seam-fanout-testfile.txt"
	g := buildOneNodeGraph("rf1", agentgraph.NodeKindReadFile, agentgraph.ReadFileAttrs{
		Path: testFile,
	})
	// Write the file so the read succeeds.
	if err := os.WriteFile(testFile, []byte("hello"), 0o644); err != nil {
		t.Fatalf("setup: write test file: %v", err)
	}
	t.Cleanup(func() { os.Remove(testFile) })

	env, rb := newRecorderEnv(g, "sess-7")
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.Policy.RequireFileReadChecked(t, testFile)
}

// ---- read_bash_output kind ----

// TestSeamFanout_ReadBashOutput_StoreGetCalled verifies that read_bash_output
// calls BashOutputStore.Get with the configured bash_run_id.
func TestSeamFanout_ReadBashOutput_StoreGetCalled(t *testing.T) {
	t.Parallel()
	g := buildOneNodeGraph("rbo1", agentgraph.NodeKindReadBashOutput, agentgraph.ReadBashOutputAttrs{
		BashRunId: "run-xyz",
	})
	env, rb := newRecorderEnv(g, "sess-8")
	rb.BashOutput.RegisterOutput("run-xyz", agentgraph.BashOutput{RunID: "run-xyz", Stdout: "hello"})
	k := agentgraph.NewKernel()
	if err := k.Run(context.Background(), env); err != nil {
		t.Fatalf("Run: %v", err)
	}
	rb.BashOutput.RequireCalledWithRunID(t, "run-xyz")
}
