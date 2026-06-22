package units_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

// openTestDB opens a fresh on-disk SQLite for unit store tests. All
// migrations (including 1100-units-init) are applied by the time
// storagesqlite.Open returns.
func openTestDB(t *testing.T) storage.DB {
	t.Helper()
	cfg := storage.Config{
		DataDir:          t.TempDir(),
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db
}

func TestSQLStore_Create_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	now := time.Now().UTC().Truncate(time.Microsecond)
	u := units.Unit{
		Kind:           units.KindDoc,
		Scope:          units.ScopeSession,
		ScopeID:        "sess-1",
		Classification: units.ClassPersonal,
		LoadPolicy:     units.LoadAlways,
		Title:          "My doc",
		Body:           "# Hello",
		CreatedAt:      now,
	}

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
	if got.Title != "My doc" {
		t.Errorf("Title = %q, want My doc", got.Title)
	}
	if string(got.Metadata) != "{}" {
		t.Errorf("Metadata = %q, want {}", got.Metadata)
	}

	round, err := st.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round.Title != got.Title {
		t.Errorf("Title drift: got %q, want %q", round.Title, got.Title)
	}
	if round.Kind != units.KindDoc {
		t.Errorf("Kind = %q, want doc", round.Kind)
	}
	if round.Scope != units.ScopeSession {
		t.Errorf("Scope = %q, want session", round.Scope)
	}
	if round.LoadPolicy != units.LoadAlways {
		t.Errorf("LoadPolicy = %q, want always", round.LoadPolicy)
	}
}

func TestSQLStore_Create_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	u := makeUnit(units.KindDoc, units.ScopeGlobal)
	u.Kind = "bad_kind"
	_, err := st.Create(ctx, u)
	if !errors.Is(err, units.ErrUnsupportedKind) {
		t.Errorf("got %v, want ErrUnsupportedKind", err)
	}
}

func TestSQLStore_Create_GlobalScope(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	got, err := st.Create(ctx, makeUnit(units.KindRoot, units.ScopeGlobal))
	if err != nil {
		t.Fatalf("Create global: %v", err)
	}
	if got.Scope != units.ScopeGlobal {
		t.Errorf("Scope = %q, want global", got.Scope)
	}

	round, err := st.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round.Scope != units.ScopeGlobal {
		t.Errorf("round-trip Scope = %q", round.Scope)
	}
}

func TestSQLStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)

	_, err := st.Get(context.Background(), "no-such-id")
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

func TestSQLStore_List_FilterByScope(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	for _, scope := range []units.Scope{units.ScopeGlobal, units.ScopeProject, units.ScopeSession} {
		u := makeUnit(units.KindDoc, scope)
		if _, err := st.Create(ctx, u); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	got, err := st.List(ctx, units.UnitFilter{Scope: units.ScopeGlobal})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("List(global) len = %d, want 1", len(got))
	}
}

func TestSQLStore_List_FilterByScopeID(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

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
		t.Errorf("List(proj-a) len = %d, want 2", len(got))
	}
}

func TestSQLStore_Update_BumpsVersionAndHistory(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	created, err := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for i, body := range []string{"v1", "v2", "v3"} {
		updated, err := st.Update(ctx, created.ID, body, nil)
		if err != nil {
			t.Fatalf("Update %d: %v", i, err)
		}
		if updated.Version != i+1 {
			t.Errorf("update %d: Version = %d, want %d", i, updated.Version, i+1)
		}
		if updated.Body != body {
			t.Errorf("update %d: Body = %q, want %q", i, updated.Body, body)
		}
	}

	vs, err := st.ListVersions(ctx, created.ID)
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
	if vs[2].Body != "v3" {
		t.Errorf("vs[2].Body = %q, want v3", vs[2].Body)
	}
}

func TestSQLStore_Update_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)

	_, err := st.Update(context.Background(), "no-such-id", "body", nil)
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

func TestSQLStore_AddEdge_RoundTrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	a, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindSnippet, units.ScopeGlobal))

	e, err := st.AddEdge(ctx, units.Edge{
		FromID: a.ID,
		ToID:   b.ID,
		Kind:   units.EdgeDerivedFrom,
	})
	if err != nil {
		t.Fatalf("AddEdge: %v", err)
	}
	if e.ID == "" {
		t.Errorf("expected auto-generated edge id")
	}
	if e.Kind != units.EdgeDerivedFrom {
		t.Errorf("Kind = %q, want derived_from", e.Kind)
	}
}

func TestSQLStore_AddEdge_RejectsInvalidKind(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	a, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))

	_, err := st.AddEdge(ctx, units.Edge{
		FromID: a.ID,
		ToID:   b.ID,
		Kind:   "bad_kind",
	})
	if !errors.Is(err, units.ErrUnsupportedEdgeKind) {
		t.Errorf("got %v, want ErrUnsupportedEdgeKind", err)
	}
}

func TestSQLStore_AddEdge_RejectsMissingFromUnit(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	b, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))

	_, err := st.AddEdge(ctx, units.Edge{
		FromID: "no-such",
		ToID:   b.ID,
		Kind:   units.EdgeReferences,
	})
	if !errors.Is(err, units.ErrUnitNotFound) {
		t.Errorf("got %v, want ErrUnitNotFound", err)
	}
}

func TestSQLStore_ListEdges_BothDirections(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	a, _ := st.Create(ctx, makeUnit(units.KindRoot, units.ScopeGlobal))
	b, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	c, _ := st.Create(ctx, makeUnit(units.KindSnippet, units.ScopeGlobal))

	_, _ = st.AddEdge(ctx, units.Edge{FromID: a.ID, ToID: b.ID, Kind: units.EdgeReferences})
	_, _ = st.AddEdge(ctx, units.Edge{FromID: c.ID, ToID: a.ID, Kind: units.EdgeDerivedFrom})

	edges, err := st.ListEdges(ctx, a.ID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	if len(edges) != 2 {
		t.Errorf("ListEdges len = %d, want 2", len(edges))
	}
}

func TestSQLStore_ListVersions_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	created, _ := st.Create(ctx, makeUnit(units.KindDoc, units.ScopeGlobal))
	vs, err := st.ListVersions(ctx, created.ID)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("ListVersions on fresh unit = %d rows, want 0", len(vs))
	}
}

func TestSQLStore_Update_MetadataJSON(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	created, _ := st.Create(ctx, makeUnit(units.KindArtifact, units.ScopeProject))
	meta := []byte(`{"content_hash":"deadbeef","byte_size":1024}`)

	updated, err := st.Update(ctx, created.ID, "body", meta)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if string(updated.Metadata) != string(meta) {
		t.Errorf("Metadata = %s, want %s", updated.Metadata, meta)
	}

	// Verify the history row also carries the metadata.
	vs, _ := st.ListVersions(ctx, created.ID)
	if len(vs) != 1 {
		t.Fatalf("ListVersions len = %d, want 1", len(vs))
	}
	if string(vs[0].Metadata) != string(meta) {
		t.Errorf("version Metadata = %s, want %s", vs[0].Metadata, meta)
	}
}

func TestSQLStore_Promote_EdgeLineage(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	mgr := units.NewManager(st)
	ctx := context.Background()

	src, _ := mgr.Create(ctx, makeUnit(units.KindArtifact, units.ScopeSession))
	promoted, edge, err := mgr.Promote(ctx, src.ID, units.ScopeGlobal, "", units.ClassOrg)
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}

	// The promoted unit must exist in the store.
	got, err := st.Get(ctx, promoted.ID)
	if err != nil {
		t.Fatalf("Get promoted: %v", err)
	}
	if got.Scope != units.ScopeGlobal {
		t.Errorf("Scope = %q, want global", got.Scope)
	}

	// Edge must be retrievable via ListEdges on either unit.
	edges, err := st.ListEdges(ctx, src.ID)
	if err != nil {
		t.Fatalf("ListEdges src: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.ID == edge.ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("promoted_from edge not found in ListEdges(src)")
	}
}

// TestSQLStore_ConflictsWithEdge_Enshrine verifies migration 1103 widened the
// unit_edges.kind CHECK so the Phase-3 conflicts_with marker edge is accepted by
// the SQL store, and that ResolveEnshrine round-trips against real SQLite
// (coexisting unit + marker edge + enshrine marker metadata) — WP17b/WP18.
func TestSQLStore_ConflictsWithEdge_Enshrine(t *testing.T) {
	db := openTestDB(t)
	m := units.NewManager(units.NewSQLStore(db))
	ctx := context.Background()

	src, err := m.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p1",
		Classification: units.ClassTeam, LoadPolicy: units.LoadAlways,
		Title: "policy", Body: "ours",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newUnit, edge, err := m.ResolveEnshrine(ctx, src.ID, "policy (theirs)", "theirs", "diverge")
	if err != nil {
		t.Fatalf("ResolveEnshrine (SQL): %v", err)
	}
	if edge.Kind != units.EdgeConflictsWith {
		t.Fatalf("edge kind = %q, want conflicts_with", edge.Kind)
	}

	// Round-trip: the conflicts_with edge survives in SQLite (CHECK accepted it).
	edges, err := m.ListEdges(ctx, src.ID)
	if err != nil {
		t.Fatalf("ListEdges: %v", err)
	}
	found := false
	for _, e := range edges {
		if e.Kind == units.EdgeConflictsWith && e.FromID == newUnit.ID && e.ToID == src.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("conflicts_with edge not persisted by SQL store")
	}

	// The enshrined unit is flagged at resolution time.
	loaded, err := m.Get(ctx, newUnit.ID)
	if err != nil {
		t.Fatalf("Get enshrined: %v", err)
	}
	if _, flagged := units.IsEnshrined(loaded); !flagged {
		t.Fatal("enshrined unit not flagged after SQL round-trip")
	}

	resolved, err := m.ResolveLoadable(ctx, units.UnitFilter{Scope: units.ScopeProject, ScopeID: "p1"})
	if err != nil {
		t.Fatalf("ResolveLoadable: %v", err)
	}
	var flaggedSeen bool
	for _, r := range resolved {
		if r.Flagged && r.Unit.ID == newUnit.ID {
			flaggedSeen = true
		}
	}
	if !flaggedSeen {
		t.Fatal("enshrined unit not surfaced flagged by ResolveLoadable (SQL)")
	}
}

// TestSQLStore_AddEdge_RejectsUnknownKind confirms the widened CHECK still
// rejects genuinely-unknown edge kinds (the validation boundary held).
func TestSQLStore_AddEdge_RejectsUnknownKind(t *testing.T) {
	db := openTestDB(t)
	st := units.NewSQLStore(db)
	ctx := context.Background()

	a, _ := st.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeGlobal,
		Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "a",
	})
	b, _ := st.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeGlobal,
		Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "b",
	})
	if _, err := st.AddEdge(ctx, units.Edge{FromID: a.ID, ToID: b.ID, Kind: units.EdgeKind("bogus")}); err == nil {
		t.Fatal("AddEdge accepted unknown kind")
	}
}
