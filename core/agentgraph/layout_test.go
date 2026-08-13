package agentgraph

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// layout_test.go pins visual-graph-authoring-01PMUX01 WP01: the
// additive `layout` metadata block on Graph. Five contracts, one test
// each:
//
//  1. Every bundled library graph + activity YAML (none of which carry
//     a layout block) round-trips load->dump stably, and gains no
//     layout content.
//  2. A fixture that DOES carry a layout block survives
//     load->dump->load with its integer coordinates intact.
//  3. The same graph, with and without a layout block, produces an
//     identical kernel promotion trace — layout is chassis metadata,
//     invisible to execution.
//  4. Materializing a run never emits a layout block on the projected
//     Graph, even when the source graph carried one.
//  5. An unknown node id in layout does not fail LoadYAML or
//     Validate() — a renamed/deleted node must not brick its graph.

// TestRoundTrip_BundledLibraryAndActivityGraphsStayLayoutFree is
// contract 1. It extends the existing round-trip pattern
// (TestRoundTripYAML_to_JSON_to_YAML) to every graph the product ships
// under core/rpc/views/agentgraph/library and core/agentgraph/
// activities/yaml: load, dump, reload, dump again. The two dumps must
// be byte-identical (the field is additive and inert for content that
// never declared it) and neither dump may contain a `layout:` block,
// since none of these fixtures authors one.
func TestRoundTrip_BundledLibraryAndActivityGraphsStayLayoutFree(t *testing.T) {
	t.Parallel()
	dirs := []string{
		"../rpc/views/agentgraph/library",
		"activities/yaml",
	}
	for _, dir := range dirs {
		dir := dir
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir %s: %v", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
				continue
			}
			path := filepath.Join(dir, e.Name())
			t.Run(path, func(t *testing.T) {
				t.Parallel()
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("read %s: %v", path, err)
				}
				g1, err := LoadYAML(raw)
				if err != nil {
					t.Fatalf("LoadYAML: %v", err)
				}
				if g1.Layout != nil {
					t.Fatalf("%s: LoadYAML populated Layout from a fixture with no layout block: %+v", path, g1.Layout)
				}
				out1, err := DumpYAML(g1)
				if err != nil {
					t.Fatalf("DumpYAML: %v", err)
				}
				if strings.Contains(string(out1), "layout:") {
					t.Fatalf("%s: dump emitted a layout block though the source has none:\n%s", path, out1)
				}
				g2, err := LoadYAML(out1)
				if err != nil {
					t.Fatalf("re-LoadYAML: %v", err)
				}
				out2, err := DumpYAML(g2)
				if err != nil {
					t.Fatalf("re-DumpYAML: %v", err)
				}
				if string(out1) != string(out2) {
					t.Fatalf("%s: dump is not stable across a second load/dump cycle\n--- first ---\n%s\n--- second ---\n%s",
						path, out1, out2)
				}
				if !reflect.DeepEqual(g1, g2) {
					t.Fatalf("%s: round-trip drift after adding the Layout field", path)
				}
			})
		}
	}
}

// TestLayoutRoundTrip is contract 2: a fixture that DOES carry a
// layout block keeps its exact integer coordinates through
// load->dump->load.
func TestLayoutRoundTrip(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/layout/layout_present.yaml")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	g1, err := LoadYAML(raw)
	if err != nil {
		t.Fatalf("LoadYAML: %v", err)
	}
	want := map[string]NodeLayout{
		"draft":        {X: 0, Y: 0},
		"critique":     {X: 200, Y: 0},
		"revise":       {X: 400, Y: 0},
		"refine_loop":  {X: 300, Y: 150},
		"final_review": {X: 600, Y: 0},
	}
	if !reflect.DeepEqual(g1.Layout, want) {
		t.Fatalf("Layout after LoadYAML = %+v, want %+v", g1.Layout, want)
	}

	out, err := DumpYAML(g1)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	if !strings.Contains(string(out), "layout:") {
		t.Fatalf("dump of a layout-carrying graph has no layout block:\n%s", out)
	}

	g2, err := LoadYAML(out)
	if err != nil {
		t.Fatalf("re-LoadYAML: %v", err)
	}
	if !reflect.DeepEqual(g2.Layout, want) {
		t.Fatalf("Layout after dump->reload = %+v, want %+v", g2.Layout, want)
	}
	if !reflect.DeepEqual(g1, g2) {
		t.Fatalf("full graph drift across dump/reload for a layout-carrying fixture")
	}

	// JSON wire round-trips the same values (the editor's RPC surface
	// is JSON, not YAML).
	jsonBytes, err := DumpJSON(g1)
	if err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	g3, err := LoadJSON(jsonBytes)
	if err != nil {
		t.Fatalf("LoadJSON: %v", err)
	}
	if !reflect.DeepEqual(g3.Layout, want) {
		t.Fatalf("Layout after JSON round-trip = %+v, want %+v", g3.Layout, want)
	}
}

// runPromotionTraceForGraph mirrors runPromotionTrace
// (kernel_promotion_golden_test.go) but runs an in-memory Graph value
// instead of reading a file, so the same graph can be run twice — once
// plain, once with a Layout block added — and the traces compared.
func runPromotionTraceForGraph(t *testing.T, label string, g Graph) promotionTrace {
	t.Helper()
	rec := newPromotionRecorder()
	log := NewMemoryEventLog()
	k := goldenKernel(rec, log)
	env := &Env{
		RunID:     "golden-layout-" + label,
		SessionID: "golden",
		Graph:     &g,
	}
	applyEnvDefaults(env)
	runErr := k.Run(context.Background(), env)

	outcome := "ok"
	switch {
	case runErr == nil:
	case errors.Is(runErr, ErrPaused):
		outcome = "paused"
	case errors.Is(runErr, ErrBudgetExceeded):
		outcome = "budget_exceeded"
	default:
		outcome = "error"
	}
	return promotionTrace{
		Fired:   rec.snapshotFired(),
		Skipped: skippedNodeIDs(t, log, env.RunID),
		Outcome: outcome,
	}
}

// TestLayoutMetadata_DoesNotAffectPromotionTrace is contract 3
// (spec.md §4: "A test pins that a graph with and without layout
// produces identical kernel traces"). It reuses the golden harness
// from kernel_promotion_golden_test.go on chat_default.yaml — the
// graph every chat turn runs — loaded independently twice so the two
// runs share no backing arrays: once as shipped (no layout), once with
// an in-memory Layout block added over a subset of its nodes.
func TestLayoutMetadata_DoesNotAffectPromotionTrace(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("../rpc/views/agentgraph/library/chat_default.yaml")
	if err != nil {
		t.Fatalf("read chat_default.yaml: %v", err)
	}

	plain, err := LoadYAML(raw)
	if err != nil {
		t.Fatalf("LoadYAML (plain): %v", err)
	}
	plain = GateAgenticTurnRouting(plain, false)

	withLayout, err := LoadYAML(raw)
	if err != nil {
		t.Fatalf("LoadYAML (withLayout): %v", err)
	}
	withLayout = GateAgenticTurnRouting(withLayout, false)
	withLayout.Layout = map[string]NodeLayout{
		"history_in":      {X: 0, Y: 0},
		"ask_user":        {X: 200, Y: 0},
		"assistant_write": {X: 400, Y: 0},
	}
	if withLayout.Layout == nil || plain.Layout != nil {
		t.Fatalf("test setup broken: plain.Layout=%v withLayout.Layout=%v", plain.Layout, withLayout.Layout)
	}

	plainTrace := runPromotionTraceForGraph(t, "plain", plain)
	layoutTrace := runPromotionTraceForGraph(t, "with-layout", withLayout)

	if plainTrace.String() != layoutTrace.String() {
		t.Fatalf("layout metadata changed the kernel promotion trace\n without layout: %s\n with layout:    %s",
			plainTrace, layoutTrace)
	}
}

// TestMaterializeRun_NeverEmitsLayout is contract 4. Projecting a run
// of a layout-carrying source graph must produce a Graph whose Layout
// is nil/empty — a materialized run is a record of what happened, not
// an authored canvas arrangement, and the projection's `finish()`
// literal (materialize.go) never copies the source's Layout field.
func TestMaterializeRun_NeverEmitsLayout(t *testing.T) {
	t.Parallel()
	src, runID, log, _ := runMaterializeFixture(t)
	src.Layout = map[string]NodeLayout{
		"ask_user":       {X: 10, Y: 20},
		"assistant_turn": {X: 220, Y: 20},
	}

	mg, err := MaterializeRun(src, runID, log)
	if err != nil {
		t.Fatalf("MaterializeRun: %v", err)
	}
	if len(mg.Layout) != 0 {
		t.Fatalf("MaterializeRun emitted a non-empty Layout: %+v", mg.Layout)
	}

	out, err := DumpYAML(mg)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	if strings.Contains(string(out), "layout:") {
		t.Fatalf("materialized graph's dump contains a layout block:\n%s", out)
	}
}

// TestLoadYAML_UnknownNodeIDInLayoutTolerated is contract 5. A layout
// entry whose key does not match any node.ID (a renamed or deleted
// node left a stale canvas-position entry behind) must NOT fail
// LoadYAML or Validate() — see the "Unknown-node-id tolerance" section
// of the Graph.Layout doc comment in spec.go for the documented
// decision: the entry round-trips inert rather than being dropped or
// rejected.
func TestLoadYAML_UnknownNodeIDInLayoutTolerated(t *testing.T) {
	t.Parallel()
	raw := []byte(`
spec_version: "1"
id: "layout_unknown_id"
entrypoints:
  - only
nodes:
  - id: only
    kind: trace_write
    attrs:
      severity: info
      message: "hi"
layout:
  only: { x: 10, y: 20 }
  ghost_node_that_was_renamed: { x: 999, y: 999 }
`)
	g, err := LoadYAML(raw)
	if err != nil {
		t.Fatalf("LoadYAML with an unknown layout node id returned an error: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Fatalf("Validate rejected a graph over an unknown layout node id: %v", err)
	}
	if len(g.Layout) != 2 {
		t.Fatalf("Layout entries = %d, want 2 (the decision is to ignore, not drop, stale entries)", len(g.Layout))
	}
	if _, ok := g.Layout["ghost_node_that_was_renamed"]; !ok {
		t.Fatalf("unknown-id layout entry was dropped on load; the documented behavior is to tolerate it inertly")
	}

	// The stale entry round-trips too: it is inert, not corrective.
	out, err := DumpYAML(g)
	if err != nil {
		t.Fatalf("DumpYAML: %v", err)
	}
	g2, err := LoadYAML(out)
	if err != nil {
		t.Fatalf("re-LoadYAML: %v", err)
	}
	if !reflect.DeepEqual(g.Layout, g2.Layout) {
		t.Fatalf("stale layout entry did not survive dump/reload: got %+v, want %+v", g2.Layout, g.Layout)
	}
}

// Integrator addition (review nit): pin the cloneLayoutMap non-aliasing
// contract directly at the wire-conversion seam. A round-trip test cannot
// see this (marshaled bytes are produced before any later mutation, and
// two parses never share maps — which is why the reviewer's `return m`
// mutant survived 904 tests: today's call graph makes the wire structs
// transient, so the clone is future-proofing). This test makes the
// contract falsifiable anyway by calling the conversions directly.
func TestLayoutMap_NotAliasedThroughWireConversion(t *testing.T) {
	src := Graph{
		SpecVersion: "1",
		ID:          "alias-pin",
		Nodes:       []Node{{ID: "n1", Kind: NodeKindTransform, Attrs: TransformAttrs{}}},
		Entrypoints: []string{"n1"},
		Layout:      map[string]NodeLayout{"n1": {X: 10, Y: 20}},
	}
	w, err := graphToWire(src)
	if err != nil {
		t.Fatalf("graphToWire: %v", err)
	}
	src.Layout["n1"] = NodeLayout{X: 999, Y: 999}
	if got := w.Layout["n1"]; got.X != 10 || got.Y != 20 {
		t.Fatalf("graphToWire aliased the layout map: got %+v, want {10 20}", got)
	}
	g2, err2 := wireToGraph(w)
	if err2 != nil {
		t.Fatalf("wireToGraph: %v", err2)
	}
	w.Layout["n1"] = NodeLayout{X: -5, Y: -5}
	if got := g2.Layout["n1"]; got.X != 10 || got.Y != 20 {
		t.Fatalf("wireToGraph aliased the layout map: got %+v, want {10 20}", got)
	}
}
