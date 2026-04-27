package activities_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	agentgraph "github.com/sigil-tech/kaneaz-harness/core/agentgraph"
	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/activities"
)

// TestLoadBundled loads the embedded YAML files and asserts every
// shipped activity ID is present + its graph validates green.
func TestLoadBundled(t *testing.T) {
	t.Parallel()
	cat, err := activities.LoadCatalog(activities.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	want := []string{"ask", "decompose", "plan", "retrieve", "summarize", "validate"}
	for _, id := range want {
		if !cat.HasActivity(id) {
			t.Errorf("missing bundled activity %q", id)
		}
	}
	for _, id := range want {
		ent, err := cat.Get(id, "")
		if err != nil {
			t.Fatalf("Get(%q): %v", id, err)
		}
		if ent.Hash == "" {
			t.Errorf("activity %q has empty hash", id)
		}
		if ent.Graph.ID != id {
			t.Errorf("activity %q yaml id mismatch: got %q", id, ent.Graph.ID)
		}
		// Validate using the kernel's validator. We allow the activity
		// to declare unknown sibling activity references (the loader
		// does not gate on transitive references) but the topology
		// must be consistent.
		if err := agentgraph.Validate(ent.Graph); err != nil {
			t.Errorf("activity %q failed validate: %v", id, err)
		}
	}
}

// TestUserOverrideWins drops a user YAML on top of a bundled activity
// and asserts the catalog returns the user's bytes (different hash +
// user source tag).
func TestUserOverrideWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	overrideID := "plan"
	override := []byte(`spec_version: "1"
id: ` + overrideID + `
name: User-supplied plan
entrypoints: [user_planner]
nodes:
  - id: user_planner
    kind: plan
    attrs:
      verbosity: terse
`)
	if err := os.WriteFile(filepath.Join(dir, "plan.yaml"), override, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cat, err := activities.LoadCatalog(activities.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	ent, err := cat.Get(overrideID, "")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ent.Graph.Name != "User-supplied plan" {
		t.Errorf("wanted user override; got name=%q source=%q", ent.Graph.Name, ent.Source)
	}
}

// TestVersionTag asserts a `version:` sibling round-trips through Entry.Version
// and that explicit-version Resolve picks the right entry.
func TestVersionTag(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	v1 := []byte(`spec_version: "1"
id: tagged
version: "v1"
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: terse
`)
	v2 := []byte(`spec_version: "1"
id: tagged
version: "v2"
entrypoints: [a]
nodes:
  - id: a
    kind: plan
    attrs:
      verbosity: standard
`)
	if err := os.WriteFile(filepath.Join(dir, "tagged-v1.yaml"), v1, 0o644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tagged-v2.yaml"), v2, 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	cat, err := activities.LoadCatalog(activities.LoadOptions{UserDir: dir, SkipBundled: true})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	versions := cat.Versions("tagged")
	if len(versions) != 2 {
		t.Fatalf("versions = %v", versions)
	}
	e1, err := cat.Get("tagged", "v1")
	if err != nil {
		t.Fatalf("Get v1: %v", err)
	}
	e2, err := cat.Get("tagged", "v2")
	if err != nil {
		t.Fatalf("Get v2: %v", err)
	}
	if e1.Version != "v1" || e2.Version != "v2" {
		t.Errorf("versions wrong: %q / %q", e1.Version, e2.Version)
	}
	// Empty version asks for canonical; with no unversioned default
	// the highest-sort version wins.
	def, err := cat.Get("tagged", "")
	if err != nil {
		t.Fatalf("Get default: %v", err)
	}
	if def.Version != "v2" {
		t.Errorf("default = %q want v2", def.Version)
	}
}

// TestResolveSatisfiesSeam asserts the catalog satisfies the kernel-side
// ActivityCatalog seam and produces a runnable sub-graph (lookup +
// validator green) for the bundled `summarize` activity, which is the
// graph Bundle D's compaction strategy will recursively invoke.
func TestResolveSatisfiesSeam(t *testing.T) {
	t.Parallel()
	cat, err := activities.LoadCatalog(activities.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	var seam agentgraph.ActivityCatalog = cat
	g, err := seam.Resolve("summarize", "")
	if err != nil {
		t.Fatalf("Resolve summarize: %v", err)
	}
	if g == nil || g.ID != "summarize" {
		t.Fatalf("resolved graph = %+v", g)
	}
	if err := agentgraph.Validate(*g); err != nil {
		t.Errorf("summarize validate: %v", err)
	}
}

// TestNotFoundError covers the lookup miss path.
func TestNotFoundError(t *testing.T) {
	t.Parallel()
	cat, err := activities.LoadCatalog(activities.LoadOptions{SkipBundled: true})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	_, err = cat.Get("nope", "")
	if !errors.Is(err, activities.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// TestRecursiveExpansion exercises an activity that references another
// activity. The catalog's Resolve must succeed for both the outer and
// inner activity; the kernel itself drives the splicing per ActivityNode
// executor semantics.
func TestRecursiveExpansion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	outer := []byte(`spec_version: "1"
id: outer
entrypoints: [bridge]
nodes:
  - id: bridge
    kind: activity
    attrs:
      activity_id: inner
`)
	inner := []byte(`spec_version: "1"
id: inner
entrypoints: [leaf]
nodes:
  - id: leaf
    kind: plan
    attrs:
      verbosity: terse
`)
	if err := os.WriteFile(filepath.Join(dir, "outer.yaml"), outer, 0o644); err != nil {
		t.Fatalf("write outer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "inner.yaml"), inner, 0o644); err != nil {
		t.Fatalf("write inner: %v", err)
	}
	cat, err := activities.LoadCatalog(activities.LoadOptions{UserDir: dir, SkipBundled: true})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if !cat.HasActivity("outer") || !cat.HasActivity("inner") {
		t.Fatalf("loaded ids = %v", cat.List())
	}
	og, err := cat.Resolve("outer", "")
	if err != nil {
		t.Fatalf("resolve outer: %v", err)
	}
	if len(og.Nodes) != 1 || og.Nodes[0].Kind != agentgraph.NodeKindActivity {
		t.Fatalf("outer graph not as expected: %+v", og)
	}
	// The catalog's HasActivity is the validator hook — wire it and
	// confirm both graphs validate cleanly when we register the
	// activity registry.
	if err := agentgraph.Validate(*og, agentgraph.WithActivityRegistry(cat)); err != nil {
		t.Errorf("outer validate: %v", err)
	}
	ig, err := cat.Resolve("inner", "")
	if err != nil {
		t.Fatalf("resolve inner: %v", err)
	}
	if err := agentgraph.Validate(*ig, agentgraph.WithActivityRegistry(cat)); err != nil {
		t.Errorf("inner validate: %v", err)
	}
}

// TestAddGraphProgrammatic asserts the catalog can be built without
// touching disk — useful for tests + RPC-level activity persistence.
func TestAddGraphProgrammatic(t *testing.T) {
	t.Parallel()
	cat := activities.NewEmptyCatalog()
	g := agentgraph.Graph{
		SpecVersion: "1",
		ID:          "synthetic",
		Entrypoints: []string{"a"},
		Nodes: []agentgraph.Node{
			{
				ID:    "a",
				Kind:  agentgraph.NodeKindPlanner,
				Attrs: agentgraph.PlannerAttrs{Verbosity: "terse"},
			},
		},
	}
	if err := cat.AddGraph(g, "v1"); err != nil {
		t.Fatalf("AddGraph: %v", err)
	}
	got, err := cat.Get("synthetic", "v1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Graph.ID != "synthetic" {
		t.Errorf("graph round-trip failed: %+v", got.Graph)
	}
}
