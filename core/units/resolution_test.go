package units

// resolution_test.go — WP17 (merge / enshrine) + WP18 (enshrined surfacing).

import (
	"context"
	"encoding/json"
	"testing"
)

func newResTestManager() *Manager {
	return NewManager(NewMemoryStore())
}

func mustCreate(t *testing.T, m *Manager, u Unit) Unit {
	t.Helper()
	out, err := m.Create(context.Background(), u)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return out
}

// TestResolveMerge_BumpsVersionWithResolvedBody verifies the whole-body MERGE
// resolution writes the resolved body as a new version (so the unit re-syncs).
func TestResolveMerge_BumpsVersionWithResolvedBody(t *testing.T) {
	m := newResTestManager()
	ctx := context.Background()

	u := mustCreate(t, m, Unit{
		Kind: KindDoc, Scope: ScopeProject, ScopeID: "p1",
		Classification: ClassTeam, LoadPolicy: LoadAlways,
		Title: "conflicted", Body: "local body",
	})

	resolved, err := m.ResolveMerge(ctx, u.ID, "merged body", nil)
	if err != nil {
		t.Fatalf("ResolveMerge: %v", err)
	}
	if resolved.Body != "merged body" {
		t.Errorf("body = %q, want merged body", resolved.Body)
	}
	if resolved.Version != u.Version+1 {
		t.Errorf("version = %d, want %d (bumped)", resolved.Version, u.Version+1)
	}

	// History retains the resolution.
	vers, err := m.ListVersions(ctx, u.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vers) == 0 || vers[len(vers)-1].Body != "merged body" {
		t.Errorf("history did not record the merge resolution: %+v", vers)
	}
}

func TestResolveMerge_MissingUnit(t *testing.T) {
	m := newResTestManager()
	if _, err := m.ResolveMerge(context.Background(), "nope", "x", nil); err == nil {
		t.Fatal("ResolveMerge on missing unit: want error, got nil")
	}
}

// TestResolveMerge_PreservesMetadataWhenNil asserts that calling ResolveMerge
// with nil metadata carries forward the unit's existing metadata rather than
// replacing it with "{}". Artifact provenance fields (content_hash, byte_size)
// must survive a whole-body merge-resolve.
func TestResolveMerge_PreservesMetadataWhenNil(t *testing.T) {
	m := newResTestManager()
	ctx := context.Background()

	// Create a unit with artifact-style metadata (provenance fields).
	existing := json.RawMessage(`{"content_hash":"deadbeef","byte_size":1024}`)
	u := mustCreate(t, m, Unit{
		Kind: KindArtifact, Scope: ScopeProject, ScopeID: "p1",
		Classification: ClassTeam, LoadPolicy: LoadAlways,
		Title: "artifact", Body: "original body",
		Metadata: existing,
	})

	// ResolveMerge with nil metadata: provenance must be preserved.
	resolved, err := m.ResolveMerge(ctx, u.ID, "resolved body", nil)
	if err != nil {
		t.Fatalf("ResolveMerge: %v", err)
	}
	if resolved.Body != "resolved body" {
		t.Errorf("body = %q, want resolved body", resolved.Body)
	}
	if resolved.Version != u.Version+1 {
		t.Errorf("version = %d, want %d", resolved.Version, u.Version+1)
	}
	// Metadata must not have been wiped to "{}".
	if string(resolved.Metadata) == "{}" {
		t.Error("ResolveMerge with nil metadata wiped existing metadata to {}; want provenance preserved")
	}
	// The exact provenance fields must survive.
	if string(resolved.Metadata) != string(existing) {
		t.Errorf("metadata = %s, want %s", resolved.Metadata, existing)
	}
}

// TestResolveEnshrine_CreatesCoexistingUnitAndMarkerEdge verifies enshrine
// creates a NEW coexisting unit (same scope/classification) carrying the other
// side's body, linked to the source by a conflicts_with marker edge, with the
// enshrine marker in metadata — BOTH coexist (WP17b).
func TestResolveEnshrine_CreatesCoexistingUnitAndMarkerEdge(t *testing.T) {
	m := newResTestManager()
	ctx := context.Background()

	src := mustCreate(t, m, Unit{
		Kind: KindDoc, Scope: ScopeProject, ScopeID: "p1",
		Classification: ClassTeam, LoadPolicy: LoadAlways,
		Title: "policy", Body: "our side",
	})

	newUnit, edge, err := m.ResolveEnshrine(ctx, src.ID, "policy (their side)", "their side", "two teams diverge")
	if err != nil {
		t.Fatalf("ResolveEnshrine: %v", err)
	}

	// Coexisting unit: new id, same scope/classification, the other body.
	if newUnit.ID == src.ID {
		t.Fatal("enshrined unit reused the source id; must be a distinct coexisting unit")
	}
	if newUnit.Scope != src.Scope || newUnit.ScopeID != src.ScopeID || newUnit.Classification != src.Classification {
		t.Errorf("enshrined unit scope/class mismatch: %+v vs src %+v", newUnit, src)
	}
	if newUnit.Body != "their side" {
		t.Errorf("enshrined body = %q, want their side", newUnit.Body)
	}

	// Marker edge: conflicts_with from new → src.
	if edge.Kind != EdgeConflictsWith {
		t.Errorf("edge kind = %q, want conflicts_with", edge.Kind)
	}
	if edge.FromID != newUnit.ID || edge.ToID != src.ID {
		t.Errorf("edge endpoints = %s→%s, want %s→%s", edge.FromID, edge.ToID, newUnit.ID, src.ID)
	}

	// Both units still load (source untouched).
	if _, err := m.Get(ctx, src.ID); err != nil {
		t.Errorf("source unit gone after enshrine: %v", err)
	}

	// The new unit carries the enshrine marker.
	marker, flagged := IsEnshrined(newUnit)
	if !flagged {
		t.Fatal("enshrined unit is not flagged")
	}
	if marker.PeerUnitID != src.ID {
		t.Errorf("marker peer = %q, want %q", marker.PeerUnitID, src.ID)
	}
	if marker.Reason != "two teams diverge" {
		t.Errorf("marker reason = %q", marker.Reason)
	}

	// The marker edge is discoverable on the source's edge list.
	edges, err := m.ListEdges(ctx, src.ID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == EdgeConflictsWith && e.FromID == newUnit.ID && e.ToID == src.ID {
			found = true
		}
	}
	if !found {
		t.Error("conflicts_with marker edge not found on source")
	}
}

// TestResolveLoadable_SurfacesEnshrinedFlaggedAndOrdered verifies WP18: at
// resolution time, enshrined-conflict units surface FLAGGED and precedence-
// ordered — never silently dropped. On-demand units are excluded.
func TestResolveLoadable_SurfacesEnshrinedFlaggedAndOrdered(t *testing.T) {
	m := newResTestManager()
	ctx := context.Background()

	// An always-load org global, an always-load team project, and the enshrined
	// pair on the team project. Plus an on-demand unit that must NOT surface.
	mustCreate(t, m, Unit{
		Kind: KindRoot, Scope: ScopeGlobal,
		Classification: ClassOrg, LoadPolicy: LoadAlways,
		Title: "org global", Body: "org",
	})
	src := mustCreate(t, m, Unit{
		Kind: KindDoc, Scope: ScopeProject, ScopeID: "p1",
		Classification: ClassTeam, LoadPolicy: LoadAlways,
		Title: "team policy", Body: "ours",
	})
	mustCreate(t, m, Unit{
		Kind: KindSnippet, Scope: ScopeProject, ScopeID: "p1",
		Classification: ClassTeam, LoadPolicy: LoadOnDemand,
		Title: "lazy", Body: "on demand",
	})
	if _, _, err := m.ResolveEnshrine(ctx, src.ID, "team policy (theirs)", "theirs", "divergence"); err != nil {
		t.Fatalf("ResolveEnshrine: %v", err)
	}

	resolved, err := m.ResolveLoadable(ctx, UnitFilter{})
	if err != nil {
		t.Fatalf("ResolveLoadable: %v", err)
	}

	// On-demand unit excluded.
	for _, r := range resolved {
		if r.Unit.Title == "lazy" {
			t.Fatal("on-demand unit leaked into the loadable set")
		}
	}

	// Exactly one flagged (enshrined) unit, and it is surfaced (not dropped).
	var flaggedCount int
	var flaggedIdx = -1
	for i, r := range resolved {
		if r.Flagged {
			flaggedCount++
			flaggedIdx = i
			if r.Marker.PeerUnitID != src.ID {
				t.Errorf("flagged marker peer = %q, want %q", r.Marker.PeerUnitID, src.ID)
			}
		}
	}
	if flaggedCount != 1 {
		t.Fatalf("flagged count = %d, want 1", flaggedCount)
	}

	// Precedence ordering: org/global loads first (rank 0); the enshrined team
	// pair sorts after its non-flagged peer.
	if resolved[0].Unit.Classification != ClassOrg {
		t.Errorf("first resolved = %s, want org base layer", resolved[0].Unit.Classification)
	}
	// The flagged unit immediately follows its non-flagged team peer.
	if flaggedIdx == 0 || resolved[flaggedIdx-1].Flagged {
		t.Errorf("enshrined unit at idx %d not ordered right after its peer", flaggedIdx)
	}
	// Precedence ranks are dense + ascending.
	for i, r := range resolved {
		if r.Precedence != i {
			t.Errorf("precedence[%d] = %d, want %d", i, r.Precedence, i)
		}
	}
}

// TestIsEnshrined_PlainUnit verifies a non-enshrined unit is not flagged and
// that non-object metadata does not panic.
func TestIsEnshrined_PlainUnit(t *testing.T) {
	if _, ok := IsEnshrined(Unit{}); ok {
		t.Error("empty unit reported enshrined")
	}
	if _, ok := IsEnshrined(Unit{Metadata: json.RawMessage(`{"foo":"bar"}`)}); ok {
		t.Error("plain-metadata unit reported enshrined")
	}
	if _, ok := IsEnshrined(Unit{Metadata: json.RawMessage(`"not an object"`)}); ok {
		t.Error("non-object metadata reported enshrined")
	}
}
