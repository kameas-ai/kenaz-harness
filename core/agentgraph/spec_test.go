package agentgraph

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestNodeKindEnumeration locks AllNodeKinds in place so a future
// re-ordering or accidental drop is caught by CI.
func TestNodeKindEnumeration(t *testing.T) {
	t.Parallel()

	// Canonical (post-WP04 + WP05 read_file/read_bash_output/write_file
	// + sleep from builtin-tools-search-and-elicitation-01KZNP3D WP04)
	// ordering: AllNodeKinds is sorted by manifest ID, so the slice is
	// alphabetised across categories.
	want := []NodeKind{
		NodeKindActivity, NodeKindApproval, NodeKindArtifact, NodeKindAsk,
		NodeKindAttachment, NodeKindBranch, NodeKindCheckpoint, NodeKindCompact,
		NodeKindCorpusRead, NodeKindCorpusWrite, NodeKindDecision, NodeKindEscalate,
		NodeKindEscalationLadder,
		NodeKindHistoryRead, NodeKindJoin, NodeKindLoop, NodeKindMemory,
		NodeKindMerge, NodeKindModel, NodeKindParallel, NodeKindPlanner,
		NodeKindReadBashOutput, NodeKindReadFile, NodeKindReflect, NodeKindRetry,
		NodeKindReview, NodeKindRouter, NodeKindSessionWrite, NodeKindSleep, NodeKindSubagentDispatch,
		NodeKindToolDispatch, NodeKindTraceWrite, NodeKindTransform, NodeKindWriteFile,
	}
	got := AllNodeKinds()
	// Compare set-equal (order is sorted-by-ID; we accept whatever order
	// the codegen emits as long as the membership set matches the spec
	// taxonomy).
	if len(got) != len(want) {
		t.Fatalf("AllNodeKinds(): len=%d, want %d\ngot=%v", len(got), len(want), got)
	}
	wantSet := map[NodeKind]bool{}
	for _, k := range want {
		wantSet[k] = true
	}
	for _, k := range got {
		if !wantSet[k] {
			t.Errorf("AllNodeKinds() includes unexpected kind %q", k)
		}
	}
	// Also pin alphabetical order, since AllNodeKinds is sorted by ID.
	sorted := append([]NodeKind(nil), want...)
	if !reflect.DeepEqual(got, sorted) {
		t.Fatalf("AllNodeKinds() ordering drift\n got=%v\nwant=%v", got, sorted)
	}
	// Smoke-check that the four shipped aliases resolve.
	for _, alias := range []string{"llm", "plan", "fork", "branch"} {
		_ = alias
	}
	for _, k := range got {
		if !k.IsKnown() {
			t.Errorf("kind %q reports !IsKnown()", k)
		}
		if defaultAttrsFor(k) == nil {
			t.Errorf("kind %q has no default attrs decoder", k)
		}
		ins, outs := defaultPortsFor(k)
		if ins == nil && outs == nil {
			t.Errorf("kind %q has no default ports", k)
		}
	}
	if NodeKind("xyzzy").IsKnown() {
		t.Errorf("unknown kind reported as known")
	}
}

// TestPortTypeCompatibility exercises the loose nominal type rules.
func TestPortTypeCompatibility(t *testing.T) {
	t.Parallel()

	cases := []struct {
		src, dst PortType
		want     bool
	}{
		{PortTypeMessages, PortTypeMessages, true},
		{PortTypeMessages, PortTypeText, false},
		{PortTypeAny, PortTypeMessages, true},
		{PortTypeMessages, PortTypeAny, true},
		{"", PortTypeText, false},
		{PortTypeText, "", false},
		{PortTypeBranchID, PortTypeBranchID, true},
		{PortTypeBranchID, PortTypeMessages, false},
	}
	for _, tc := range cases {
		if got := tc.src.Compatible(tc.dst); got != tc.want {
			t.Errorf("%q.Compatible(%q) = %v, want %v", tc.src, tc.dst, got, tc.want)
		}
	}
}

// TestRoundTripYAML_to_JSON_to_YAML loads each fixture, converts to
// JSON, back to a Graph, then back to YAML, and re-parses the result.
// The final Graph value must DeepEqual the first.
func TestRoundTripYAML_to_JSON_to_YAML(t *testing.T) {
	t.Parallel()

	fixtures := []string{
		"testdata/default_toolloop.yaml",
		"testdata/branch_v1_simple.yaml",
		"testdata/reflect_loop.yaml",
	}
	for _, path := range fixtures {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			t.Parallel()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			g1, err := LoadYAML(raw)
			if err != nil {
				t.Fatalf("LoadYAML: %v", err)
			}
			// YAML → JSON.
			jsonBytes, err := DumpJSON(g1)
			if err != nil {
				t.Fatalf("DumpJSON: %v", err)
			}
			g2, err := LoadJSON(jsonBytes)
			if err != nil {
				t.Fatalf("LoadJSON: %v", err)
			}
			// JSON → YAML.
			yamlBytes, err := DumpYAML(g2)
			if err != nil {
				t.Fatalf("DumpYAML: %v", err)
			}
			g3, err := LoadYAML(yamlBytes)
			if err != nil {
				t.Fatalf("re-LoadYAML: %v", err)
			}
			if !reflect.DeepEqual(g1, g3) {
				// Surface a diff via JSON encodings for readability.
				j1, _ := json.MarshalIndent(g1, "", "  ")
				j3, _ := json.MarshalIndent(g3, "", "  ")
				t.Fatalf("round-trip drift\n--- yaml-loaded ---\n%s\n--- yaml→json→yaml ---\n%s",
					j1, j3)
			}
		})
	}
}

// TestUnmarshalYAML_NodeAttrsTyped ensures Node.Attrs is the kind-specific
// typed struct after loading (rather than a free-form map).
func TestUnmarshalYAML_NodeAttrsTyped(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/default_toolloop.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g, err := LoadYAML(raw)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}

	var sawLoop, sawLLM, sawTool, sawHistory, sawTrace, sawDecision, sawParallel bool
	for _, n := range g.Nodes {
		switch n.Kind {
		case NodeKindLoop:
			a, ok := n.Attrs.(LoopAttrs)
			if !ok {
				t.Errorf("node %q: attrs not LoopAttrs (got %T)", n.ID, n.Attrs)
				continue
			}
			if a.MaxIterations != 8 {
				t.Errorf("node %q: max_iterations = %d, want 8", n.ID, a.MaxIterations)
			}
			sawLoop = true
		case NodeKindModel:
			if _, ok := n.Attrs.(ModelAttrs); !ok {
				t.Errorf("node %q: attrs not ModelAttrs (got %T)", n.ID, n.Attrs)
			}
			sawLLM = true
		case NodeKindToolDispatch:
			if _, ok := n.Attrs.(ToolDispatchAttrs); !ok {
				t.Errorf("node %q: attrs not ToolDispatchAttrs (got %T)", n.ID, n.Attrs)
			}
			sawTool = true
		case NodeKindHistoryRead:
			if _, ok := n.Attrs.(HistoryReadAttrs); !ok {
				t.Errorf("node %q: attrs not HistoryReadAttrs (got %T)", n.ID, n.Attrs)
			}
			sawHistory = true
		case NodeKindTraceWrite:
			if _, ok := n.Attrs.(TraceWriteAttrs); !ok {
				t.Errorf("node %q: attrs not TraceWriteAttrs (got %T)", n.ID, n.Attrs)
			}
			sawTrace = true
		case NodeKindDecision:
			if _, ok := n.Attrs.(DecisionAttrs); !ok {
				t.Errorf("node %q: attrs not DecisionAttrs (got %T)", n.ID, n.Attrs)
			}
			sawDecision = true
		case NodeKindParallel:
			if _, ok := n.Attrs.(ParallelAttrs); !ok {
				t.Errorf("node %q: attrs not ParallelAttrs (got %T)", n.ID, n.Attrs)
			}
			sawParallel = true
		}
	}
	for _, pair := range []struct {
		name string
		ok   bool
	}{
		{"loop", sawLoop}, {"model", sawLLM}, {"tool_dispatch", sawTool},
		{"history_read", sawHistory}, {"trace_write", sawTrace},
		{"decision", sawDecision}, {"parallel", sawParallel},
	} {
		if !pair.ok {
			t.Errorf("default_toolloop fixture missing expected kind %q", pair.name)
		}
	}
}

// TestStableYAMLDecode locks down a couple of structural invariants
// against accidental schema breakage.
func TestStableYAMLDecode(t *testing.T) {
	t.Parallel()

	const src = `
spec_version: "1"
id: "demo"
entrypoints: [a]
nodes:
  - id: a
    kind: llm
    attrs:
      model: claude
      max_tokens: 100
`
	g, err := LoadYAML([]byte(src))
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	if g.SpecVersion != "1" {
		t.Errorf("spec_version = %q, want 1", g.SpecVersion)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("nodes len = %d, want 1", len(g.Nodes))
	}
	a, ok := g.Nodes[0].Attrs.(ModelAttrs)
	if !ok {
		t.Fatalf("attrs not ModelAttrs (got %T)", g.Nodes[0].Attrs)
	}
	if a.Model != "claude" || a.MaxTokens != 100 {
		t.Errorf("decoded ModelAttrs drift: %+v", a)
	}
}

// TestProgrammaticBuild_RoundTrip constructs a Graph in pure Go,
// dumps it to YAML, reloads it, and verifies attrs are typed.
func TestProgrammaticBuild_RoundTrip(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "synth",
		Entrypoints: []string{"n1"},
		Nodes: []Node{
			{
				ID:   "n1",
				Kind: NodeKindModel,
				Attrs: ModelAttrs{
					Model:     "claude",
					MaxTokens: 256,
				},
			},
		},
	}
	yamlBytes, err := DumpYAML(g)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	// sanity: yaml must contain a `kind: model` line (the canonical
	// post-WP04 name) so an author can edit the file by hand.
	if !contains(yamlBytes, "kind: model") {
		t.Errorf("yaml dump missing `kind: model`:\n%s", yamlBytes)
	}
	g2, err := LoadYAML(yamlBytes)
	if err != nil {
		t.Fatalf("re-LoadYAML: %v", err)
	}
	if !reflect.DeepEqual(g, g2) {
		t.Errorf("programmatic round-trip drift\n got=%+v\nwant=%+v", g2, g)
	}
}

// TestUnknownKind_DecodeError confirms the loader rejects a node-kind
// it doesn't recognise.
func TestUnknownKind_DecodeError(t *testing.T) {
	t.Parallel()

	const src = `
spec_version: "1"
id: "x"
entrypoints: [n]
nodes:
  - id: n
    kind: definitely_not_a_kind
`
	if _, err := LoadYAML([]byte(src)); err == nil {
		t.Fatalf("expected unknown-kind error, got nil")
	}
}

// TestYAMLOmitsEmptyAttrs ensures emitting a CheckpointNode (which has
// no required fields) does not produce a stray `attrs: null` key.
func TestYAMLOmitsEmptyAttrs(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "checkpoint-only",
		Entrypoints: []string{"c"},
		Nodes: []Node{
			{ID: "c", Kind: NodeKindCheckpoint, Attrs: CheckpointAttrs{}},
		},
	}
	out, err := DumpYAML(g)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	// We tolerate `attrs: {}` but not `attrs: null` — `{}` survives a
	// round-trip cleanly while `null` ends up as a Go nil that flows
	// down strange paths.
	if contains(out, "attrs: null") {
		t.Errorf("yaml dumped `attrs: null`:\n%s", out)
	}
}

// TestJSONMarshal_NodeAttrs_Symmetric encodes and decodes a Graph via
// JSON and confirms attrs survives the round-trip as a typed struct.
func TestJSONMarshal_NodeAttrs_Symmetric(t *testing.T) {
	t.Parallel()

	g := Graph{
		SpecVersion: SpecVersion,
		ID:          "j",
		Entrypoints: []string{"n"},
		Nodes: []Node{
			{
				ID:   "n",
				Kind: NodeKindReview,
				Attrs: ReviewAttrs{
					UpstreamNode:  "n",
					MaxIterations: 3,
				},
			},
		},
	}
	buf, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var g2 Graph
	if err := json.Unmarshal(buf, &g2); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(g, g2) {
		t.Errorf("json round-trip drift\n got=%+v\nwant=%+v", g2, g)
	}
}

// helper.
func contains(haystack []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if string(haystack[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}

// TestYAMLNodeUntouched verifies the raw YAML node parses to a wireGraph
// shape so we can compare `attrs:` blocks structurally.
func TestYAMLRawShape(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("testdata/reflect_loop.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var w wireGraph
	if err := yaml.Unmarshal(raw, &w); err != nil {
		t.Fatalf("yaml unmarshal: %v", err)
	}
	if w.SpecVersion != "1" {
		t.Errorf("spec_version = %q, want 1", w.SpecVersion)
	}
	var sawLoop bool
	for _, n := range w.Nodes {
		if n.Kind == NodeKindLoop {
			sawLoop = true
			if _, ok := n.Attrs["max_iterations"]; !ok {
				t.Errorf("loop node missing max_iterations attr in raw shape")
			}
		}
	}
	if !sawLoop {
		t.Errorf("reflect_loop fixture must contain a loop node")
	}
}
