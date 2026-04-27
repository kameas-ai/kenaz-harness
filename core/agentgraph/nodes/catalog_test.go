package nodes_test

import (
	"sort"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/agentgraph/nodes"
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
	wantArchetypes := []string{"compute", "control", "state", "read", "write", "marker"}
	for _, id := range wantArchetypes {
		if _, err := cat.Get(id); err != nil {
			t.Errorf("Get(%q): %v", id, err)
		}
	}

	// Archetypes() returns exactly the archetype layer; no kinds yet
	// (they land in WP04).
	archs := cat.Archetypes()
	if len(archs) != len(wantArchetypes) {
		t.Errorf("Archetypes len: got %d, want %d", len(archs), len(wantArchetypes))
	}
	for _, a := range archs {
		if !a.Manifest.IsArchetype() {
			t.Errorf("Archetypes returned non-archetype: %q", a.Manifest.ID)
		}
	}

	// Kinds() returns 0 for now (WP01 ships archetypes only).
	kinds := cat.Kinds()
	if len(kinds) != 0 {
		t.Errorf("expected 0 kinds in WP01, got %d", len(kinds))
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

	// ListByCategory returns the archetypes whose category matches.
	if got := cat.ListByCategory(nodes.CategoryCompute); len(got) != 1 {
		t.Errorf("ListByCategory(compute): got %d, want 1", len(got))
	}
	stateLayer := cat.ListByCategory(nodes.CategoryState)
	// state, read (extends state), write (extends state), marker
	// (extends state) — 4 archetypes share the state category.
	if len(stateLayer) != 4 {
		t.Errorf("ListByCategory(state): got %d, want 4 (state/read/write/marker)", len(stateLayer))
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
	if got, want := len(children), 3; got != want {
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
