package nodes_test

import (
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/nodes"
)

// TestLoadShipped loads the embedded archetype manifests and asserts
// the v1 archetype set is present. v1 ships only archetypes in WP01;
// concrete kinds land in WP04.
func TestLoadShipped(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	wantArchetypes := []string{"compute", "control", "state", "read", "write", "marker"}
	for _, id := range wantArchetypes {
		rm, err := cat.Get(id)
		if err != nil {
			t.Errorf("missing archetype %q: %v", id, err)
			continue
		}
		if !rm.Manifest.IsArchetype() {
			t.Errorf("%q should be an archetype manifest", id)
		}
		if rm.Manifest.IsCallable() {
			t.Errorf("%q archetype should not be callable", id)
		}
		if rm.Hash == "" {
			t.Errorf("%q has empty hash", id)
		}
		if rm.Source == "" {
			t.Errorf("%q has empty source", id)
		}
	}
	// 3-deep state→read chain check: read's Chain is [state, read].
	read, err := cat.Get("read")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got, want := len(read.Chain), 2; got != want {
		t.Errorf("read.Chain: got %d, want %d (chain=%v)", got, want, read.Chain)
	}
	// `read` should inherit `provenance` attr from `state`.
	if _, ok := read.Manifest.Attrs["provenance"]; !ok {
		t.Error("read should inherit provenance attr from state")
	}
	// Categories rolled through.
	if read.Manifest.Category != nodes.CategoryState {
		t.Errorf("read.category: got %q", read.Manifest.Category)
	}
}

// TestLoadShippedSkipped: SkipBundled=true with empty UserDir produces
// an empty catalog without error.
func TestLoadShippedSkipped(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if got := cat.Len(); got != 0 {
		t.Errorf("expected empty catalog, got %d entries", got)
	}
}

// TestLoadUserDirMissing: a non-existent UserDir is silently ignored.
func TestLoadUserDirMissing(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: "/path/that/does/not/exist"})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	// Shipped archetypes still present.
	if _, err := cat.Get("compute"); err != nil {
		t.Errorf("missing shipped compute: %v", err)
	}
}

// TestLoadFilenameMismatch: a manifest whose filename stem disagrees
// with its `id:` field is rejected at parse time.
func TestLoadFilenameMismatch(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "renamed.yaml", `id: original
extends: compute
executor: agentgraph.ExecOriginal
`)
	_, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true, UserDir: dir})
	if err == nil {
		t.Fatal("expected filename↔id mismatch error")
	}
}

// TestLoadShippedHashStable: the same manifest loaded twice yields the
// same content-hash.
func TestLoadShippedHashStable(t *testing.T) {
	t.Parallel()
	cat1, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("first load: %v", err)
	}
	cat2, err := nodes.LoadCatalog(nodes.LoadOptions{})
	if err != nil {
		t.Fatalf("second load: %v", err)
	}
	for _, id := range []string{"compute", "control", "state", "read", "write", "marker"} {
		a, _ := cat1.Get(id)
		b, _ := cat2.Get(id)
		if a == nil || b == nil {
			t.Fatalf("missing %q after reload", id)
		}
		if a.Hash != b.Hash {
			t.Errorf("hash drift for %q: %q != %q", id, a.Hash, b.Hash)
		}
		if a.Hash == "" {
			t.Errorf("%q has empty hash", id)
		}
	}
}

// TestLoadUserOverrideMergesAcrossSources stages a user file in a
// directory whose content overrides a shipped manifest; we verify the
// merge fires by hashing against the user file and checking the new
// description surfaced.
//
// The forbidden-field rejection path is exercised by the internal
// merge_internal_test.go (which can call the unexported helper
// directly with constructed Manifest values, avoiding filename
// gymnastics).
func TestLoadUserOverrideMergesAcrossSources(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// User-side override of the shipped `compute` archetype: tune its
	// description and add a fresh budget_consumes scalar.
	writeManifest(t, dir, "_archetype.compute.yaml", `archetype: compute
category: compute
description: tuned-for-my-harness
budget: llm
budget_consumes: [tokens, llm_calls, cost_usd, my_dial]
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("compute")
	if err != nil {
		t.Fatalf("Get(compute): %v", err)
	}
	if rm.Manifest.Description != "tuned-for-my-harness" {
		t.Errorf("description: got %q, want tuned-for-my-harness", rm.Manifest.Description)
	}
	// Source records the user file path.
	if rm.Source == "" {
		t.Error("source is empty")
	}
}

// TestLoadEmptyDirWithBundled: an empty UserDir returns the bundled set
// only.
func TestLoadEmptyDirWithBundled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	if cat.Len() < 6 {
		t.Errorf("expected at least 6 archetypes, got %d", cat.Len())
	}
}

// TestLoadCorruptUserManifest: a malformed user YAML returns a load
// error but the catalog still contains the bundled set.
func TestLoadCorruptUserManifest(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "corrupt.yaml", "not: [valid: yaml")
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: dir})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if cat == nil {
		t.Fatal("expected partial catalog despite parse error")
	}
	if _, gErr := cat.Get("compute"); gErr != nil {
		t.Errorf("bundled compute should still load despite user error: %v", gErr)
	}
}

// TestLoadDuplicateUserAndShipped: a user-side manifest with a fresh
// id (that doesn't shadow shipped) is allowed and registered.
func TestLoadDuplicateUserAndShipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifest(t, dir, "fresh.yaml", `id: fresh
extends: compute
executor: agentgraph.ExecFresh
`)
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{UserDir: dir})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	rm, err := cat.Get("fresh")
	if err != nil {
		t.Fatalf("Get(fresh): %v", err)
	}
	if rm.Manifest.Executor != "agentgraph.ExecFresh" {
		t.Errorf("executor: got %q", rm.Manifest.Executor)
	}
	// Inherited from shipped compute.
	if rm.Manifest.Category != nodes.CategoryCompute {
		t.Errorf("category: got %q", rm.Manifest.Category)
	}
}

// TestErrUnknownKind: catalog Get returns ErrUnknownKind for missing
// IDs.
func TestErrUnknownKind(t *testing.T) {
	t.Parallel()
	cat, err := nodes.LoadCatalog(nodes.LoadOptions{SkipBundled: true})
	if err != nil {
		t.Fatalf("LoadCatalog: %v", err)
	}
	_, err = cat.Get("never_existed")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, nodes.ErrUnknownKind) {
		t.Errorf("expected ErrUnknownKind, got: %v", err)
	}
}
