package artifacts_test

// store_contract_test.go — one behavioural contract for every
// artifacts.Store implementation (artifacts-as-units-01DOGF0C WP02).
//
// The units-backed store must be a drop-in for the legacy artifacts-table
// store: the 11 consumers in spec §2.3 keep their call shapes, so any
// behavioural difference here is a regression for them. Every SQL
// implementation runs against a REAL sqlite database opened through the
// production storagesqlite.Open (CLAUDE.md blind spot #2 — no in-memory
// fixture stands in for SQL encode/decode). The memory store rides along
// so the fixture most consumer tests use is held to the same contract.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

type storeImpl struct {
	name string
	// build returns the store under test plus the DB it lives in (nil for
	// the memory store).
	build func(t *testing.T, reader artifacts.SessionProjectReader) (artifacts.Store, storage.DB)
}

func contractImpls() []storeImpl {
	return []storeImpl{
		{name: "legacy-sql", build: func(t *testing.T, r artifacts.SessionProjectReader) (artifacts.Store, storage.DB) {
			db := openTestDB(t)
			// The legacy artifacts table carries FKs to sessions and
			// projects; seed every id the contract cases use.
			for _, p := range []string{"p1", "proj-1", "p9"} {
				seedProject(t, db, p)
			}
			for _, s := range []string{"s", "s1", "s2", "s9", "sess-1", "s-proj", "s-none"} {
				seedSession(t, db, s, nil)
			}
			return artifacts.NewSQLStore(db, artifacts.WithSessionProjectReader(r)), db
		}},
		{name: "units", build: func(t *testing.T, r artifacts.SessionProjectReader) (artifacts.Store, storage.DB) {
			db := openTestDB(t)
			return artifacts.NewUnitsStore(db, artifacts.WithSessionProjectReader(r)), db
		}},
		{name: "memory", build: func(t *testing.T, r artifacts.SessionProjectReader) (artifacts.Store, storage.DB) {
			return artifacts.NewMemoryStore(artifacts.WithMemSessionProjectReader(r)), nil
		}},
	}
}

func forEachStore(t *testing.T, reader artifacts.SessionProjectReader, fn func(t *testing.T, st artifacts.Store, db storage.DB)) {
	t.Helper()
	for _, impl := range contractImpls() {
		impl := impl
		t.Run(impl.name, func(t *testing.T) {
			t.Parallel()
			st, db := impl.build(t, reader)
			fn(t, st, db)
		})
	}
}

func mustInsert(t *testing.T, st artifacts.Store, a artifacts.Artifact) artifacts.Artifact {
	t.Helper()
	got, err := st.Insert(context.Background(), a)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	return got
}

func TestStoreContract_RoundTripEverySourceAndScope(t *testing.T) {
	t.Parallel()
	pid := "proj-1"
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		created := time.Date(2026, 10, 4, 12, 0, 0, 123456789, time.UTC)
		for _, src := range []string{artifacts.SourceCodeBlock, artifacts.SourceToolOutput, artifacts.SourceUserPin, artifacts.SourceModelOutput} {
			for _, scope := range []string{artifacts.ScopeKindSession, artifacts.ScopeKindProject, artifacts.ScopeKindGlobal} {
				in := artifacts.Artifact{
					SessionID:   "sess-1",
					ProjectID:   &pid,
					Title:       src + "/" + scope,
					MimeType:    "text/x-go",
					ContentHash: "hash-" + src + "-" + scope,
					ByteSize:    77,
					Source:      src,
					SourceRef: artifacts.ArtifactSourceRef{
						MessageID: "m1", ToolCallID: "tc1", CodeBlockIndex: 2, Filename: "main.go",
						AbsolutePath: "/tmp/main.go", ImageIndex: 1, RevisedPrompt: "rp",
					},
					ScopeKind: scope,
					CreatedAt: created,
				}
				ins := mustInsert(t, st, in)
				if ins.ID == "" {
					t.Fatal("Insert minted no id")
				}
				got, err := st.Get(ctx, ins.ID)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				if got.SessionID != in.SessionID || got.Title != in.Title || got.MimeType != in.MimeType ||
					got.ContentHash != in.ContentHash || got.ByteSize != in.ByteSize || got.Source != in.Source ||
					got.ScopeKind != in.ScopeKind || got.SourceRef != in.SourceRef || !got.CreatedAt.Equal(created) {
					t.Errorf("round trip mismatch:\n got  %+v\n want %+v", got, in)
				}
				if got.ProjectID == nil || *got.ProjectID != pid {
					t.Errorf("ProjectID = %v, want %q", got.ProjectID, pid)
				}
			}
		}
	})
}

func TestStoreContract_PreservesCallerID(t *testing.T) {
	t.Parallel()
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		got := mustInsert(t, st, artifacts.Artifact{
			ID: "01JCALLERSUPPLIEDID0000000", SessionID: "s", MimeType: "text/plain",
			ContentHash: "h", Source: artifacts.SourceUserPin,
		})
		if got.ID != "01JCALLERSUPPLIEDID0000000" {
			t.Errorf("ID = %q, want the caller's", got.ID)
		}
		if _, err := st.Get(context.Background(), got.ID); err != nil {
			t.Errorf("Get(caller id): %v", err)
		}
	})
}

func TestStoreContract_RejectsUnknownSourceAndScope(t *testing.T) {
	t.Parallel()
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		if _, err := st.Insert(ctx, artifacts.Artifact{Source: "nope", MimeType: "x", ContentHash: "h"}); !errors.Is(err, artifacts.ErrUnsupportedSource) {
			t.Errorf("unknown source err = %v", err)
		}
		if _, err := st.Insert(ctx, artifacts.Artifact{Source: artifacts.SourceUserPin, ScopeKind: "team", MimeType: "x", ContentHash: "h"}); !errors.Is(err, artifacts.ErrUnsupportedScope) {
			t.Errorf("unknown scope err = %v", err)
		}
		if _, err := st.Get(ctx, "ghost"); !errors.Is(err, artifacts.ErrArtifactNotFound) {
			t.Errorf("Get ghost err = %v", err)
		}
		if _, err := st.Delete(ctx, "ghost"); !errors.Is(err, artifacts.ErrArtifactNotFound) {
			t.Errorf("Delete ghost err = %v", err)
		}
		if _, err := st.WriteVersion(ctx, artifacts.ArtifactVersion{ArtifactID: "ghost", ContentHash: "h"}); !errors.Is(err, artifacts.ErrArtifactNotFound) {
			t.Errorf("WriteVersion ghost err = %v", err)
		}
	})
}

func TestStoreContract_ListFiltersAndOrder(t *testing.T) {
	t.Parallel()
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		p1 := "p1"
		base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		a1 := mustInsert(t, st, artifacts.Artifact{SessionID: "s1", MimeType: "text/plain", ContentHash: "h1", Source: artifacts.SourceCodeBlock, CreatedAt: base})
		a2 := mustInsert(t, st, artifacts.Artifact{SessionID: "s1", ProjectID: &p1, MimeType: "image/png", ContentHash: "h2", Source: artifacts.SourceToolOutput, ScopeKind: artifacts.ScopeKindProject, CreatedAt: base.Add(time.Second)})
		a3 := mustInsert(t, st, artifacts.Artifact{SessionID: "s2", MimeType: "image/jpeg", ContentHash: "h3", Source: artifacts.SourceUserPin, ScopeKind: artifacts.ScopeKindGlobal, CreatedAt: base.Add(2 * time.Second)})

		ids := func(f artifacts.ArtifactFilter) []string {
			t.Helper()
			got, err := st.List(ctx, f)
			if err != nil {
				t.Fatalf("List(%+v): %v", f, err)
			}
			out := make([]string, 0, len(got))
			for _, a := range got {
				out = append(out, a.ID)
			}
			return out
		}
		eq := func(label string, got, want []string) {
			t.Helper()
			if len(got) != len(want) {
				t.Errorf("%s = %v, want %v", label, got, want)
				return
			}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("%s = %v, want %v", label, got, want)
					return
				}
			}
		}
		eq("all (newest first)", ids(artifacts.ArtifactFilter{}), []string{a3.ID, a2.ID, a1.ID})
		eq("session s1", ids(artifacts.ArtifactFilter{SessionID: "s1"}), []string{a2.ID, a1.ID})
		eq("project p1", ids(artifacts.ArtifactFilter{ProjectID: "p1"}), []string{a2.ID})
		eq("mime image/", ids(artifacts.ArtifactFilter{MimeTypePrefix: "image/"}), []string{a3.ID, a2.ID})
		eq("source user_pin", ids(artifacts.ArtifactFilter{Source: artifacts.SourceUserPin}), []string{a3.ID})
		eq("scope global", ids(artifacts.ArtifactFilter{ScopeKind: artifacts.ScopeKindGlobal}), []string{a3.ID})
		eq("scope session", ids(artifacts.ArtifactFilter{ScopeKind: artifacts.ScopeKindSession}), []string{a1.ID})
	})
}

func TestStoreContract_UpdateScope(t *testing.T) {
	t.Parallel()
	reader := artifacts.NewStaticSessionProjectReader(map[string]string{"s-proj": "p1"})
	forEachStore(t, reader, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		a := mustInsert(t, st, artifacts.Artifact{SessionID: "s-proj", MimeType: "text/plain", ContentHash: "h", Source: artifacts.SourceCodeBlock})
		orphan := mustInsert(t, st, artifacts.Artifact{SessionID: "s-none", MimeType: "text/plain", ContentHash: "h", Source: artifacts.SourceCodeBlock})

		got, err := st.UpdateScope(ctx, a.ID, artifacts.ScopeKindProject, "")
		if err != nil {
			t.Fatalf("promote to project: %v", err)
		}
		if got.ID != a.ID || got.ScopeKind != artifacts.ScopeKindProject || got.ProjectID == nil || *got.ProjectID != "p1" {
			t.Errorf("after promote = %+v, want same id, project scope, project p1", got)
		}
		if list, _ := st.List(ctx, artifacts.ArtifactFilter{ProjectID: "p1"}); len(list) != 1 {
			t.Errorf("List(project p1) after promote = %d, want 1", len(list))
		}
		if _, err := st.UpdateScope(ctx, a.ID, artifacts.ScopeKindProject, "other"); !errors.Is(err, artifacts.ErrUnsupportedScope) {
			t.Errorf("promote to mismatched project err = %v", err)
		}
		if _, err := st.UpdateScope(ctx, orphan.ID, artifacts.ScopeKindProject, ""); !errors.Is(err, artifacts.ErrUnsupportedScope) {
			t.Errorf("promote session-without-project err = %v", err)
		}
		got, err = st.UpdateScope(ctx, a.ID, artifacts.ScopeKindGlobal, "")
		if err != nil {
			t.Fatalf("promote to global: %v", err)
		}
		if got.ScopeKind != artifacts.ScopeKindGlobal || got.ProjectID != nil {
			t.Errorf("after global = %+v, want global scope, nil project", got)
		}
		got, err = st.UpdateScope(ctx, a.ID, artifacts.ScopeKindSession, "")
		if err != nil {
			t.Fatalf("demote to session: %v", err)
		}
		if got.ScopeKind != artifacts.ScopeKindSession || got.ProjectID != nil || got.SessionID != "s-proj" {
			t.Errorf("after demote = %+v, want session scope, nil project, origin session kept", got)
		}
		if _, err := st.UpdateScope(ctx, a.ID, "team", ""); !errors.Is(err, artifacts.ErrUnsupportedScope) {
			t.Errorf("unknown scope err = %v", err)
		}
		if _, err := st.UpdateScope(ctx, "ghost", artifacts.ScopeKindGlobal, ""); !errors.Is(err, artifacts.ErrArtifactNotFound) {
			t.Errorf("UpdateScope ghost err = %v", err)
		}
	})
}

func TestStoreContract_DeleteAndRefcount(t *testing.T) {
	t.Parallel()
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		a := mustInsert(t, st, artifacts.Artifact{SessionID: "s", MimeType: "text/plain", ContentHash: "shared", Source: artifacts.SourceCodeBlock})
		b := mustInsert(t, st, artifacts.Artifact{SessionID: "s", MimeType: "text/plain", ContentHash: "shared", Source: artifacts.SourceCodeBlock})
		if n, err := st.RefcountFor(ctx, "shared"); err != nil || n < 2 {
			t.Fatalf("RefcountFor(shared) = %d, %v; want >= 2", n, err)
		}
		if n, _ := (artifacts.ArtifactsRefcountSource{Store: st}).Refcount(ctx, "shared"); n < 2 {
			t.Errorf("ArtifactsRefcountSource = %d, want >= 2", n)
		}
		hash, err := st.Delete(ctx, a.ID)
		if err != nil || hash != "shared" {
			t.Fatalf("Delete = %q, %v; want shared", hash, err)
		}
		if _, err := st.Get(ctx, a.ID); !errors.Is(err, artifacts.ErrArtifactNotFound) {
			t.Errorf("Get after delete err = %v", err)
		}
		if n, _ := st.RefcountFor(ctx, "shared"); n < 1 {
			t.Errorf("RefcountFor after one delete = %d, want >= 1 (b still holds it)", n)
		}
		if _, err := st.Delete(ctx, b.ID); err != nil {
			t.Fatalf("Delete b: %v", err)
		}
		if n, _ := st.RefcountFor(ctx, "shared"); n != 0 {
			t.Errorf("RefcountFor after both deleted = %d, want 0", n)
		}
	})
}

func TestStoreContract_Versions(t *testing.T) {
	t.Parallel()
	forEachStore(t, nil, func(t *testing.T, st artifacts.Store, _ storage.DB) {
		ctx := context.Background()
		parent := mustInsert(t, st, artifacts.Artifact{SessionID: "s", Title: "t", MimeType: "text/plain", ContentHash: "h0", ByteSize: 1, Source: artifacts.SourceCodeBlock})
		if vs, err := st.ListVersions(ctx, parent.ID); err != nil || len(vs) != 0 {
			t.Fatalf("fresh ListVersions = %v, %v; want empty", vs, err)
		}
		summary, path := "fix", "/p/v1"
		v1, err := st.WriteVersion(ctx, artifacts.ArtifactVersion{ArtifactID: parent.ID, ContentHash: "h1", ByteSize: 11, MimeType: "text/plain", Summary: &summary, Path: &path})
		if err != nil {
			t.Fatalf("WriteVersion 1: %v", err)
		}
		v2, err := st.WriteVersion(ctx, artifacts.ArtifactVersion{ArtifactID: parent.ID, ContentHash: "h2", ByteSize: 12, MimeType: "text/markdown"})
		if err != nil {
			t.Fatalf("WriteVersion 2: %v", err)
		}
		if v1.Version != 1 || v2.Version != 2 {
			t.Errorf("versions = %d, %d; want 1, 2", v1.Version, v2.Version)
		}
		vs, err := st.ListVersions(ctx, parent.ID)
		if err != nil || len(vs) != 2 {
			t.Fatalf("ListVersions = %v, %v; want 2 rows", vs, err)
		}
		if vs[0].ContentHash != "h1" || vs[0].ByteSize != 11 || vs[0].Summary == nil || *vs[0].Summary != "fix" || vs[0].Path == nil || *vs[0].Path != "/p/v1" {
			t.Errorf("v1 = %+v", vs[0])
		}
		if vs[1].ContentHash != "h2" || vs[1].MimeType != "text/markdown" || vs[1].Summary != nil || vs[1].Path != nil {
			t.Errorf("v2 = %+v (nil summary/path must stay nil)", vs[1])
		}
		// The parent (capture) is never rewritten by a revision.
		got, _ := st.Get(ctx, parent.ID)
		if got.ContentHash != "h0" || got.MimeType != "text/plain" {
			t.Errorf("parent after versions = %+v, want the original capture", got)
		}
		if _, err := st.Delete(ctx, parent.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if vs, _ := st.ListVersions(ctx, parent.ID); len(vs) != 0 {
			t.Errorf("ListVersions after parent delete = %d, want 0", len(vs))
		}
	})
}

// TestUnitsStore_KindIsolation pins that the units-backed store can only
// see and touch kind='artifact' rows: a document unit sharing the table is
// invisible to Get/List/Delete/UpdateScope and survives byte-identical.
func TestUnitsStore_KindIsolation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	st := artifacts.NewUnitsStore(db)
	um := units.NewManager(units.NewSQLStore(db))
	doc, err := um.Create(ctx, units.Unit{
		Kind: units.KindDoc, Scope: units.ScopeSession, ScopeID: "s", Classification: units.ClassPersonal,
		LoadPolicy: units.LoadOnDemand, Title: "a doc", Body: "body", Metadata: []byte(`{"content_hash":"dochash"}`),
	})
	if err != nil {
		t.Fatalf("create doc unit: %v", err)
	}
	mustInsert(t, st, artifacts.Artifact{SessionID: "s", MimeType: "text/plain", ContentHash: "h", Source: artifacts.SourceUserPin})

	if _, err := st.Get(ctx, doc.ID); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("Get(doc id) err = %v, want not found", err)
	}
	if _, err := st.Delete(ctx, doc.ID); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("Delete(doc id) err = %v, want not found", err)
	}
	if _, err := st.UpdateScope(ctx, doc.ID, artifacts.ScopeKindGlobal, ""); !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("UpdateScope(doc id) err = %v, want not found", err)
	}
	list, _ := st.List(ctx, artifacts.ArtifactFilter{})
	if len(list) != 1 {
		t.Errorf("List = %d rows, want only the 1 artifact", len(list))
	}
	after, err := um.Get(ctx, doc.ID)
	if err != nil || after.Title != doc.Title || after.Body != doc.Body || string(after.Metadata) != string(doc.Metadata) || after.Scope != doc.Scope {
		t.Errorf("doc unit after artifact ops = %+v, %v; want untouched", after, err)
	}
	// Refcount is deliberately kind-agnostic (over-count keeps bytes).
	if n, _ := st.RefcountFor(ctx, "dochash"); n != 1 {
		t.Errorf("RefcountFor(dochash) = %d, want 1 — refcount must count every unit kind", n)
	}
}

// TestUnitsStore_RowShape pins the persisted unit row a units-backed
// Insert writes: kind=artifact, personal, on_demand, version 0, empty
// body — the classification and load policy are what keep an artifact
// out of fleet push (FR-5) and out of eager context injection.
func TestUnitsStore_RowShape(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openTestDB(t)
	st := artifacts.NewUnitsStore(db)
	pid := "p9"
	a := mustInsert(t, st, artifacts.Artifact{SessionID: "s9", ProjectID: &pid, Title: "x", MimeType: "image/png", ContentHash: "h9", ByteSize: 9, Source: artifacts.SourceModelOutput, ScopeKind: artifacts.ScopeKindProject})
	u, err := units.NewManager(units.NewSQLStore(db)).Get(ctx, a.ID)
	if err != nil {
		t.Fatalf("units Get: %v", err)
	}
	if u.Kind != units.KindArtifact || u.Classification != units.ClassPersonal || u.LoadPolicy != units.LoadOnDemand ||
		u.Version != 0 || u.Body != "" || u.Scope != units.ScopeProject || u.ScopeID != "p9" {
		t.Errorf("unit row = %+v", u)
	}
}
