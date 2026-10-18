package fleet

// artifacts-as-units-01DOGF0C review F5: the Unit_* collaboration RPCs refuse
// kind='artifact' units. Real sqlite; the artifact is written by the
// production artifacts store, so the row is exactly what a user's capture
// produces.

import (
	"context"
	"errors"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
	"github.com/kameas-ai/kenaz-harness/core/units"
)

func TestImpl_UnitRPCs_RefuseArtifactUnits(t *testing.T) {
	ctx := context.Background()
	db, err := storagesqlite.Open(storage.Config{DataDir: t.TempDir(), EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	m := units.NewManager(units.NewSQLStore(db))
	impl := &Impl{Units: m} // nil Syncer: the guard must fire before the syncer check

	art, err := coreart.NewSQLStore(db).Insert(ctx, coreart.Artifact{
		SessionID: "s", Title: "captured tool output", MimeType: "text/plain", ContentHash: "h",
		Source: coreart.SourceToolOutput, SourceRef: coreart.ArtifactSourceRef{MessageID: "msg-secret"},
	})
	if err != nil {
		t.Fatalf("insert artifact: %v", err)
	}
	before, _ := m.Get(ctx, art.ID)
	var unitsBefore int
	_ = db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM units").Scan(&unitsBefore)

	if _, err := impl.Unit_PromoteAsMergeRequest(ctx, art.ID, "team", "t", "b"); !errors.Is(err, ErrArtifactUnitNotShareable) {
		t.Errorf("Unit_PromoteAsMergeRequest(artifact) err = %v, want ErrArtifactUnitNotShareable", err)
	}
	if err := impl.Unit_ResolveMerge(ctx, art.ID, "rewritten"); !errors.Is(err, ErrArtifactUnitNotShareable) {
		t.Errorf("Unit_ResolveMerge(artifact) err = %v, want ErrArtifactUnitNotShareable", err)
	}
	if _, err := impl.Unit_ResolveEnshrine(ctx, art.ID, "fork", "fork body", "why"); !errors.Is(err, ErrArtifactUnitNotShareable) {
		t.Errorf("Unit_ResolveEnshrine(artifact) err = %v, want ErrArtifactUnitNotShareable", err)
	}

	after, err := m.Get(ctx, art.ID)
	if err != nil || after.Version != before.Version || after.Body != before.Body || string(after.Metadata) != string(before.Metadata) {
		t.Errorf("artifact unit changed by refused RPCs: before %+v after %+v (%v)", before, after, err)
	}
	var unitsAfter int
	_ = db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM units").Scan(&unitsAfter)
	if unitsAfter != unitsBefore {
		t.Errorf("units count %d -> %d: a refused enshrine still forked the artifact", unitsBefore, unitsAfter)
	}

	// Documents are unaffected by the guard.
	doc, _ := m.Create(ctx, units.Unit{Kind: units.KindDoc, Scope: units.ScopeProject, ScopeID: "p", Classification: units.ClassTeam, LoadPolicy: units.LoadAlways, Title: "d", Body: "x"})
	if err := impl.Unit_ResolveMerge(ctx, doc.ID, "merged"); err != nil {
		t.Errorf("Unit_ResolveMerge(doc) = %v, want nil", err)
	}
}
