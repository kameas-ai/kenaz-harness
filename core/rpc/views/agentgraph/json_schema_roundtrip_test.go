package agentgraph

import (
	"encoding/json"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
)

// TestJsonSchemaAttr_SurvivesSaveLoadRoundTrip is
// structured-output-is-reachable-01PMZE14 WP-PI's AC-PI-2 "second
// bypass" obligation: saveGraph stores the author's YAML verbatim
// (manager.go's saveGraph writes spec.YAML unchanged to disk), so a
// fixture that constructs a coreag.Graph in Go and never round-trips
// it through manager.saveGraph -> file -> manager.loadGraph ->
// coreag.LoadYAML -> decodeAttrs has NOT proven that an authored
// json_schema: attr survives the trip. WP02's own tests
// (core/agentgraph/exec_compute_test.go) construct
// ModelAttrs{JsonSchema: ...} directly in Go and do not exercise this
// path at all — this test is the one that does, driving the REAL file
// persistence layer (not session.NewMemoryStore() or an in-memory
// fixture) with a temp DataDir.
func TestJsonSchemaAttr_SurvivesSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m, err := NewManager(WithDataDir(dir))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	const graphYAML = `spec_version: "1"
id: json_schema_roundtrip
entrypoints: [n]
nodes:
  - id: n
    kind: model
    attrs:
      model: "gpt-4o"
      json_schema:
        type: object
        properties:
          verdict:
            type: string
        required:
          - verdict
`
	if err := m.saveGraph(GraphSpec{ID: "json_schema_roundtrip", YAML: graphYAML}); err != nil {
		t.Fatalf("saveGraph: %v", err)
	}

	loaded, err := m.loadGraph("json_schema_roundtrip")
	if err != nil {
		t.Fatalf("loadGraph: %v", err)
	}

	// loadGraph returns the raw bytes it read back off disk — assert
	// that round trip is byte-faithful before going further, so a
	// failure downstream in LoadYAML/decodeAttrs isn't confused with a
	// file-write/read bug.
	if loaded.YAML != graphYAML {
		t.Fatalf("loaded YAML does not match saved YAML.\nsaved:  %q\nloaded: %q", graphYAML, loaded.YAML)
	}

	g, err := coreag.LoadYAML([]byte(loaded.YAML))
	if err != nil {
		t.Fatalf("LoadYAML(loaded): %v", err)
	}

	var node *coreag.Node
	for i := range g.Nodes {
		if g.Nodes[i].ID == "n" {
			node = &g.Nodes[i]
			break
		}
	}
	if node == nil {
		t.Fatal(`node "n" not found in reloaded graph`)
	}
	attrs, ok := node.Attrs.(coreag.ModelAttrs)
	if !ok {
		t.Fatalf("node attrs are %T, want coreag.ModelAttrs", node.Attrs)
	}
	if attrs.JsonSchema == nil {
		t.Fatal("JsonSchema is nil after the save/load/decode round trip — the file persistence layer dropped it")
	}

	// Assert the SHAPE survived, not just presence: decodeAttrs going
	// through map[string]any -> YAML -> file -> YAML -> map[string]any
	// again is exactly where a naive re-serialization could reorder,
	// coerce, or drop nested keys.
	gotJSON, err := json.Marshal(attrs.JsonSchema)
	if err != nil {
		t.Fatalf("marshal round-tripped JsonSchema: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	props, ok := got["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties missing or wrong shape after round trip: %#v", got)
	}
	if _, ok := props["verdict"]; !ok {
		t.Fatalf("properties.verdict missing after round trip: %#v", props)
	}
	required, ok := got["required"].([]any)
	if !ok || len(required) != 1 || required[0] != "verdict" {
		t.Fatalf("required array did not survive the round trip: %#v", got["required"])
	}
}
