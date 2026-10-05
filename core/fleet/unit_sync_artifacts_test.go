package fleet

// unit_sync_artifacts_test.go — artifacts-as-units-01DOGF0C WP05 (spec
// FR-5, pin P-5). Artifacts became core/units rows (kind='artifact') in
// WP04, and units are what the UnitSyncer pushes to fleet. Before WP04
// artifacts could never leave the device; this pins that they still
// cannot unless explicitly shared — on REAL sqlite (the units SQL store
// the production syncer reads), not the in-memory units store the rest of
// this file's siblings use.
//
// Why it holds (decision record D3): push eligibility is classification
// ∈ {team, org} (PushDirty iterates exactly those; ListDirty returns nil
// for personal; MapUnitToNode refuses personal). The artifacts store
// writes every artifact unit 'personal' and never changes classification
// — not on capture, not on a new revision, not on promote to
// project/global scope.

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func TestUnitSyncer_PushDirty_NeverPushesArtifactUnits(t *testing.T) {
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{DataDir: t.TempDir(), EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })

	m := units.NewManager(units.NewSQLStore(db))
	reader := coreart.NewStaticSessionProjectReader(map[string]string{"s-art": "proj-1"})
	arts := coreart.NewUnitsStore(db, coreart.WithSessionProjectReader(reader))

	// Every artifact shape that could plausibly look "dirty" or "shared":
	// fresh captures of each scope, a revised one (Version bumped past any
	// baseline), and ones promoted to project and global scope.
	var artIDs []string
	for _, scope := range []string{coreart.ScopeKindSession, coreart.ScopeKindProject, coreart.ScopeKindGlobal} {
		a, err := arts.Insert(ctx, coreart.Artifact{SessionID: "s-art", MimeType: "text/plain", ContentHash: "h-" + scope, Source: coreart.SourceToolOutput, ScopeKind: scope})
		if err != nil {
			t.Fatalf("insert %s artifact: %v", scope, err)
		}
		artIDs = append(artIDs, a.ID)
	}
	revised, _ := arts.Insert(ctx, coreart.Artifact{SessionID: "s-art", MimeType: "text/plain", ContentHash: "h-rev", Source: coreart.SourceCodeBlock})
	if _, err := arts.WriteVersion(ctx, coreart.ArtifactVersion{ArtifactID: revised.ID, ContentHash: "h-rev2"}); err != nil {
		t.Fatalf("WriteVersion: %v", err)
	}
	promoted, _ := arts.Insert(ctx, coreart.Artifact{SessionID: "s-art", MimeType: "text/plain", ContentHash: "h-pro", Source: coreart.SourceUserPin})
	if _, err := arts.UpdateScope(ctx, promoted.ID, coreart.ScopeKindProject, ""); err != nil {
		t.Fatalf("promote to project: %v", err)
	}
	if _, err := arts.UpdateScope(ctx, promoted.ID, coreart.ScopeKindGlobal, ""); err != nil {
		t.Fatalf("promote to global: %v", err)
	}
	artIDs = append(artIDs, revised.ID, promoted.ID)

	for _, id := range artIDs {
		u, err := m.Get(ctx, id)
		if err != nil {
			t.Fatalf("artifact %s not a unit: %v", id, err)
		}
		if u.Classification != units.ClassPersonal {
			t.Errorf("artifact unit %s classification = %s, want personal (the only thing keeping it off the wire)", id, u.Classification)
		}
	}
	isArtifact := map[string]bool{}
	for _, id := range artIDs {
		isArtifact[id] = true
	}
	for _, class := range []units.Classification{units.ClassTeam, units.ClassOrg} {
		dirty, err := m.ListDirty(ctx, class)
		if err != nil {
			t.Fatalf("ListDirty(%s): %v", class, err)
		}
		for _, u := range dirty {
			if isArtifact[u.ID] || u.Kind == units.KindArtifact {
				t.Errorf("artifact unit %s is in the %s push worklist", u.ID, class)
			}
		}
	}

	// End to end through the syncer against a fake fleet server: one
	// team document beside the artifacts; exactly that document is pushed.
	teamDoc := seedTeamUnit(t, m, "team doc", "shared")
	fake := &contextFakeServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()
	stubTokens(t, TokenSet{AccessToken: "at", RefreshToken: "rt", ExpiresAt: time.Now().Add(time.Hour)})
	syncer := NewUnitSyncer(makeTestClient(t, srv.URL), m, NewUnitMapper("team-1"), makeCapPollerWithTeamCap(t), t.TempDir())
	n, err := syncer.PushDirty(ctx)
	if err != nil {
		t.Fatalf("PushDirty: %v", err)
	}
	if n != 1 {
		t.Fatalf("PushDirty accepted %d nodes, want 1 (the team doc only)", n)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, req := range fake.pushRequests {
		for _, node := range req.Nodes {
			if isArtifact[node.ID] || node.Kind == string(units.KindArtifact) {
				t.Errorf("artifact %s reached the fleet push request", node.ID)
			}
			if node.ID != teamDoc.ID {
				t.Errorf("unexpected pushed node %s", node.ID)
			}
		}
	}
	for _, id := range artIDs {
		if _, err := m.GetSyncState(ctx, id); err == nil {
			t.Errorf("artifact unit %s has a sync sidecar row — it was treated as synced", id)
		}
	}
}
