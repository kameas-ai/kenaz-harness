package nodes_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// TestCatalogGetListArchetypes loads the shipped set and exercises every
// public Catalog accessor.
func TestCatalogGetListArchetypes(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}

	// IDs sorted.
	ids := cat.IDs()
	if !sort.StringsAreSorted(ids) {
		t.Errorf("IDs not sorted: %v", ids)
	}
	// `tool` joined the archetype layer in
	// agentgraph-total-convergence-01PMGX01 WP04: the old generic `tool`
	// KIND became _archetype.tool.yaml, the shared contract every
	// builtin tool node extends.
	wantArchetypes := []string{"compute", "control", "state", "read", "write", "marker", "tool"}
	for _, id := range wantArchetypes {
		if _, err := cat.Get(id); err != nil {
			t.Errorf("Get(%q): %v", id, err)
		}
	}

	// Archetypes() returns exactly the archetype layer (WP04 lands kind
	// manifests; archetypes themselves remain non-callable per FR-007).
	archs := cat.Archetypes()
	if len(archs) != len(wantArchetypes) {
		t.Errorf("Archetypes len: got %d, want %d", len(archs), len(wantArchetypes))
	}
	for _, a := range archs {
		if !a.Manifest.IsArchetype() {
			t.Errorf("Archetypes returned non-archetype: %q", a.Manifest.ID)
		}
	}

	// Kinds() returns the 34 callable kinds shipped after
	// agentgraph-total-convergence-01PMGX01 WP04 retired the generic
	// `tool` kind in favour of the `tool` archetype (was 34 before).
	kinds := cat.Kinds()
	const wantCallable = 34
	if len(kinds) != wantCallable {
		t.Errorf("expected %d kinds (WP04+WP05+session_write+tool_dispatch+sleep+subagent_dispatch+escalation_ladder+router, minus the retired generic `tool` kind), got %d", wantCallable, len(kinds))
	}

	// IsCallable returns false for archetypes and unknown IDs.
	for _, id := range wantArchetypes {
		if cat.IsCallable(id) {
			t.Errorf("IsCallable(%q): archetype should be non-callable", id)
		}
	}
	if cat.IsCallable("never_existed") {
		t.Error("IsCallable(unknown) should be false")
	}

	// ListByCategory returns archetypes + every kind whose category
	// matches. 2 archetypes (compute + tool, which extends it) + 13
	// callable compute kinds = 15. The count was unchanged across
	// agentgraph-total-convergence-01PMGX01 WP04 because the generic
	// `tool` kind became the `tool` archetype (one moved layer, not one
	// added or removed entry); WP11a adds `router`, a genuinely new
	// compute kind, taking 12 -> 13.
	if got := cat.ListByCategory(nodes.CategoryCompute); len(got) != 15 {
		t.Errorf("ListByCategory(compute): got %d, want 15 (2 archetypes + 13 kinds)", len(got))
	}
	// state archetype + read/write/marker archetypes + 12 callable state
	// kinds (8 from WP04 + read_file/read_bash_output/write_file from
	// WP05 + session_write from chat-migration) = 16.
	stateLayer := cat.ListByCategory(nodes.CategoryState)
	if len(stateLayer) != 16 {
		t.Errorf("ListByCategory(state): got %d, want 16 (4 archetypes + 12 kinds)", len(stateLayer))
	}
}

// TestCatalogListByArchetype: read, write, and marker each chain through
// state, so listing by archetype `state` returns the three children.
func TestCatalogListByArchetype(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	children := cat.ListByArchetype("state")
	// 3 archetype-children (read/write/marker) + 12 callable kinds
	// (WP04's 8 + WP05's read_file/read_bash_output/write_file +
	// chat-migration's session_write) = 15 entries under state.
	if got, want := len(children), 15; got != want {
		t.Fatalf("ListByArchetype(state): got %d, want %d", got, want)
	}
	gotIDs := map[string]bool{}
	for _, rm := range children {
		gotIDs[rm.Manifest.ID] = true
	}
	for _, want := range []string{"read", "write", "marker"} {
		if !gotIDs[want] {
			t.Errorf("missing child %q under archetype state", want)
		}
	}
	// Spot-check that callable kinds also surface.
	for _, want := range []string{"memory", "history_read", "checkpoint"} {
		if !gotIDs[want] {
			t.Errorf("missing kind %q under archetype state", want)
		}
	}
}

// TestCatalogConcurrentReads asserts the read paths are race-free. We
// drive 16 goroutines each performing a fan of Get/List/IsCallable
// calls; the test relies on `go test -race` to surface any data-race
// in the underlying RWMutex.
func TestCatalogConcurrentReads(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	const goroutines = 16
	const ops = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < ops; i++ {
				_, _ = cat.Get("compute")
				_ = cat.List()
				_ = cat.IDs()
				_ = cat.Archetypes()
				_ = cat.Kinds()
				_ = cat.IsCallable("compute")
				_ = cat.ListByCategory(nodes.CategoryState)
				_ = cat.ListByArchetype("state")
			}
		}()
	}
	wg.Wait()
}

// TestNewEmptyCatalog: the test-only empty constructor returns a
// usable but empty catalog.
func TestNewEmptyCatalog(t *testing.T) {
	t.Parallel()
	c := nodes.NewEmptyCatalog()
	if c == nil {
		t.Fatal("nil catalog")
	}
	if c.Len() != 0 {
		t.Errorf("expected 0, got %d", c.Len())
	}
	_, err := c.Get("anything")
	if err == nil {
		t.Error("expected ErrUnknownKind on empty catalog")
	}
}
