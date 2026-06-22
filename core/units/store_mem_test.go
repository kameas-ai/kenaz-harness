package units_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/units"
)

// makeUnit is a helper that returns a minimal valid Unit.
func makeUnit(kind units.Kind, scope units.Scope) units.Unit {
	return units.Unit{
		Kind:           kind,
		Scope:          scope,
		ScopeID:        "",
		Classification: units.ClassPersonal,
		LoadPolicy:     units.LoadOnDemand,
		Title:          "test unit",
		Body:           "hello world",
	}
}

// ── Create ─────────────────────────────────────────────────────────────

func TestMemStore_Create_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	now := time.Now().UTC().Truncate(time.Millisecond)
	u := makeUnit(units.KindDoc, units.ScopeSession)
	u.ScopeID = "sess-1"
	u.Title = "My doc"
	u.Body = "# Hello"
	u.CreatedAt = now

	got, err := st.Create(ctx, u)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Errorf("expected auto-generated id, got empty")
	}
	if got.Version != 0 {
		t.Errorf("Version = %d, want 0", got.Version)
	}
	if got.Title != "My doc" || got.Body != "# Hello" {
		t.Errorf("Title/Body drift: %+v", got)
	}
	if got.Scope != units.ScopeSession || got.ScopeID != "sess-1" {
		t.Errorf("Scope/ScopeID drift: %+v", got)
	}
	if string(got.Metadata) != "{}" {
		t.Errorf("Metadata default = %s, want {}", got.Metadata)
	}

	round, err := st.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round.Title != got.Title {
		t.Errorf("Title mismatch after Get: %q", round.Title)
	}
}

func TestMemStore_Create_AutoID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	got, err := st.Create(ctx, makeUnit(units.KindSnippet, units.ScopeGlobal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Errorf("expected auto-generated id")
	}
}

func TestMemStore_Create_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	u := makeUnit(units.KindDoc, units.ScopeGlobal)
	u.Kind = "bogus"
	_, err := st.Create(ctx, u)
	if !errors.Is(err, units.ErrUnsupportedKind) {
		t.Errorf("got %v, want ErrUnsupportedKind", err)
	}
}

func TestMemStore_Create_RejectsInvalidScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	u := makeUnit(units.KindDoc, units.ScopeGlobal)
	u.Scope = "universe"
	_, err := st.Create(ctx, u)
	if !errors.Is(err, units.ErrUnsupportedScope) {
		t.Errorf("got %v, want ErrUnsupportedScope", err)
	}
}

func TestMemStore_Create_RejectsInvalidClassification(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	u := makeUnit(units.KindDoc, units.ScopeGlobal)
	u.Classification = "everyone"
	_, err := st.Create(ctx, u)
	if !errors.Is(err, units.ErrUnsupportedClassification) {
		t.Errorf("got %v, want ErrUnsupportedClassification", err)
	}
}

func TestMemStore_Create_RejectsInvalidLoadPolicy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	u := makeUnit(units.KindDoc, units.ScopeGlobal)
	u.LoadPolicy = "lazy"
	_, err := st.Create(ctx, u)
	if !errors.Is(err, units.ErrUnsupportedLoadPolicy) {
		t.Errorf("got %v, want ErrUnsupportedLoadPolicy", err)
	}
}

// ── Get ────────────────────────────────────────────────────────────────

func TestMemStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	_, err := st.Get(ctx, "no-such-id")
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

// ── List ───────────────────────────────────────────────────────────────

func TestMemStore_List_FilterByScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	for i, scope := range []units.Scope{units.ScopeGlobal, units.ScopeProject, units.ScopeSession} {
		u := makeUnit(units.KindDoc, scope)
		u.ScopeID = "id"
		u.Title = string(scope)
		u.CreatedAt = time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		if _, err := st.Create(ctx, u); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := st.List(ctx, units.UnitFilter{Scope: units.ScopeGlobal})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Scope != units.ScopeGlobal {
		t.Errorf("List(ScopeGlobal) = %+v, want 1 global unit", got)
	}
}

func TestMemStore_List_FilterByScopeID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	for _, sid := range []string{"proj-a", "proj-a", "proj-b"} {
		u := makeUnit(units.KindDoc, units.ScopeProject)
		u.ScopeID = sid
		if _, err := st.Create(ctx, u); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := st.List(ctx, units.UnitFilter{ScopeID: "proj-a"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("List(ScopeID=proj-a) len = %d, want 2", len(got))
	}
}

func TestMemStore_List_FilterByKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	kinds := []units.Kind{units.KindRoot, units.KindDoc, units.KindSnippet}
	for _, k := range kinds {
		u := makeUnit(k, units.ScopeGlobal)
		if _, err := st.Create(ctx, u); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := st.List(ctx, units.UnitFilter{Kind: units.KindDoc})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Kind != units.KindDoc {
		t.Errorf("List(Kind=doc) = %+v", got)
	}
}

func TestMemStore_List_Empty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	got, err := st.List(ctx, units.UnitFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List on empty store = %d rows, want 0", len(got))
	}
}

// ── Update / versioning ─────────────────────────────────────────────────

func TestMemStore_Update_BumpsVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	created, err := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Version != 0 {
		t.Fatalf("initial Version = %d, want 0", created.Version)
	}

	updated, err := st.Update(ctx, created.ID, "v1 body", nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Version != 1 {
		t.Errorf("after first update Version = %d, want 1", updated.Version)
	}
	if updated.Body != "v1 body" {
		t.Errorf("Body = %q, want %q", updated.Body, "v1 body")
	}

	updated2, err := st.Update(ctx, created.ID, "v2 body", nil)
	if err != nil {
		t.Fatalf("Update second: %v", err)
	}
	if updated2.Version != 2 {
		t.Errorf("after second update Version = %d, want 2", updated2.Version)
	}
}

func TestMemStore_Update_NotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	_, err := st.Update(ctx, "no-such-id", "body", nil)
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

func TestMemStore_ListVersions_History(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	created, err := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// No versions before first update.
	vs, err := st.ListVersions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("initial ListVersions len = %d, want 0", len(vs))
	}

	for i, body := range []string{"alpha", "beta", "gamma"} {
		if _, err := st.Update(ctx, created.ID, body, nil); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}

	vs, err = st.ListVersions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 3 {
		t.Errorf("ListVersions len = %d, want 3", len(vs))
	}
	for i, v := range vs {
		if v.Version != i+1 {
			t.Errorf("vs[%d].Version = %d, want %d", i, v.Version, i+1)
		}
	}
	if vs[2].Body != "gamma" {
		t.Errorf("vs[2].Body = %q, want gamma", vs[2].Body)
	}
}

// ── Edges ─────────────────────────────────────────────────────────────

func TestMemStore_AddEdge_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	a, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindSnippet, units.ScopeGlobal))

	e, err := st.AddEdge(ctx, units.Edge{
		FromID: a.ID,
		ToID:   b.ID,
		Kind:   units.EdgeReferences,
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if e.ID == "" {
		t.Errorf("expected auto-generated edge id")
	}
	if e.Kind != units.EdgeReferences {
		t.Errorf("Kind = %q, want references", e.Kind)
	}
	if e.Version != 1 {
		t.Errorf("Version = %d, want 1", e.Version)
	}
}

func TestMemStore_AddEdge_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	a, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))

	_, err := st.AddEdge(ctx, units.Edge{
		FromID: a.ID,
		ToID:   b.ID,
		Kind:   "bogus_kind",
	})
	if !errors.Is(err, units.ErrUnsupportedEdgeKind) {
		t.Errorf("got %v, want ErrUnsupportedEdgeKind", err)
	}
}

func TestMemStore_AddEdge_RejectsMissingUnit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	a, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))

	_, err := st.AddEdge(ctx, units.Edge{
		FromID: a.ID,
		ToID:   "no-such-id",
		Kind:   units.EdgeReferences,
	})
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

func TestMemStore_ListEdges_BothDirections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()

	a, _ := st.Create(ctx, makeUnit(units.KindRoot, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	c, _ := st.Create(ctx, makeUnit(units.KindSnippet, units.ScopeGlobal))

	_, _ = st.AddEdge(ctx, units.Edge{FromID: a.ID, ToID: b.ID, Kind: units.EdgeReferences})
	_, _ = st.AddEdge(ctx, units.Edge{FromID: c.ID, ToID: a.ID, Kind: units.EdgeDerivedFrom})

	edges, err := st.ListEdges(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	// a is from in edge 1 and to in edge 2 — both should appear.
	if len(edges) != 2 {
		t.Errorf("ListEdges len = %d, want 2", len(edges))
	}
}

// ── Manager ────────────────────────────────────────────────────────────

func TestManager_Promote_CreatesEdge(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()
	mgr := units.NewManager(st)

	src, err := mgr.Create(ctx, makeUnit(units.KindDoc, units.ScopeSession))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	promoted, edge, err := mgr.Promote(ctx, src.ID, units.ScopeProject, "proj-x", units.ClassTeam)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if promoted.Scope != units.ScopeProject {
		t.Errorf("promoted Scope = %q, want project", promoted.Scope)
	}
	if promoted.ScopeID != "proj-x" {
		t.Errorf("promoted ScopeID = %q, want proj-x", promoted.ScopeID)
	}
	if promoted.Classification != units.ClassTeam {
		t.Errorf("promoted Classification = %q, want team", promoted.Classification)
	}
	if promoted.Kind != src.Kind {
		t.Errorf("promoted Kind = %q, want %q", promoted.Kind, src.Kind)
	}
	if edge.Kind != units.EdgePromotedFrom {
		t.Errorf("edge Kind = %q, want promoted_from", edge.Kind)
	}
	if edge.FromID != promoted.ID || edge.ToID != src.ID {
		t.Errorf("edge from=%q to=%q, want from=%q to=%q",
			edge.FromID, edge.ToID, promoted.ID, src.ID)
	}
}

func TestManager_Promote_ToGlobal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()
	mgr := units.NewManager(st)

	src, _ := mgr.Create(ctx, makeUnit(units.KindArtifact, units.ScopeSession))

	promoted, edge, err := mgr.Promote(ctx, src.ID, units.ScopeGlobal, "", units.ClassOrg)
	if err != nil {
		t.Fatalf("Promote to global: %v", err)
	}
	if promoted.Scope != units.ScopeGlobal {
		t.Errorf("promoted Scope = %q, want global", promoted.Scope)
	}
	if edge.Kind != units.EdgePromotedFrom {
		t.Errorf("edge Kind = %q, want promoted_from", edge.Kind)
	}
}

func TestManager_Promote_InvalidScope(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()
	mgr := units.NewManager(st)

	src, _ := mgr.Create(ctx, makeUnit(units.KindDoc, units.ScopeSession))

	_, _, err := mgr.Promote(ctx, src.ID, "universe", "", units.ClassPersonal)
	if !errors.Is(err, units.ErrUnsupportedScope) {
		t.Errorf("got %v, want ErrUnsupportedScope", err)
	}
}

func TestManager_Update_MetadataRoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()
	mgr := units.NewManager(st)

	created, _ := mgr.Create(ctx, makeUnit(units.KindArtifact, units.ScopeGlobal))
	meta := []byte(`{"content_hash":"abc123","byte_size":42}`)

	updated, err := mgr.Update(ctx, created.ID, "new body", meta)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if string(updated.Metadata) != string(meta) {
		t.Errorf("Metadata = %s, want %s", updated.Metadata, meta)
	}
}

func TestManager_ListVersions_OrderedAsc(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := units.NewMemoryStore()
	mgr := units.NewManager(st)

	created, _ := mgr.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	for i := range 5 {
		if _, err := mgr.Update(ctx, created.ID, "body"+string(rune('0'+i)), nil); err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
	}

	vs, err := mgr.ListVersions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 5 {
		t.Fatalf("ListVersions len = %d, want 5", len(vs))
	}
	for i, v := range vs {
		if v.Version != i+1 {
			t.Errorf("vs[%d].Version = %d, want %d", i, v.Version, i+1)
		}
	}
}
