package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	coreart "github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/projects"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// newCascadeFixture wires the full session + artifact cascade stack
// against an in-memory sqlite DB rooted at t.TempDir(). Returns the
// SessionsAPI under test plus the underlying handles tests use to
// assert state.
func newCascadeFixture(t *testing.T) (SessionsAPI, *coreart.Manager, coreart.Store, attachments.MediaStore, *session.Manager, *projects.Manager, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("storage open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	attMgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	projMgr := projects.NewManager(projects.NewSQLStore(db))
	sessReader := &cascadeSessionReader{mgr: sessMgr}
	artStore := coreart.NewSQLStore(db,
		coreart.WithSessionProjectReader(sessReader),
	)
	media.RegisterRefcountSource(coreart.ArtifactsRefcountSource{Store: artStore})
	artMgr := coreart.NewManager(artStore, &mediaPutAdapter{inner: media},
		coreart.WithSessionReader(sessReader),
	)
	api := NewManagerAPIWithAttachmentsAndArtifacts(sessMgr, attMgr, artStore, media, dir)
	return api, artMgr, artStore, media, sessMgr, projMgr, dir
}

type cascadeSessionReader struct {
	mgr *session.Manager
}

func (r *cascadeSessionReader) SessionProject(ctx context.Context, sessionID string) (string, error) {
	rec, err := r.mgr.Get(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if rec.ProjectID == nil {
		return "", nil
	}
	return *rec.ProjectID, nil
}

type mediaPutAdapter struct {
	inner attachments.MediaStore
}

func (a *mediaPutAdapter) Put(ctx context.Context, b []byte, mediaType, originalName string) (attachments.MediaArtifact, error) {
	return a.inner.Put(ctx, b, mediaType, originalName)
}

// TestDelete_CascadesArtifactsAndSweepsCAS — default options drop the
// session, its artifacts, and the orphan CAS files.
func TestDelete_CascadesArtifactsAndSweepsCAS(t *testing.T) {
	t.Parallel()
	api, artMgr, artStore, _, _, _, dir := newCascadeFixture(t)
	ctx := context.Background()
	target, err := api.Create(ctx, "delete-me")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	captured, err := artMgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "a.txt", MimeType: "text/plain", Bytes: []byte("solo"),
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, target.ID)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	hash := captured[0].ContentHash
	path := filepath.Join(dir, "media", hash)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CAS file missing pre-delete: %v", err)
	}

	if err := api.Delete(ctx, target.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	listed, err := artStore.List(ctx, coreart.ArtifactFilter{SessionID: target.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 0 {
		t.Errorf("artifacts after cascade = %d, want 0", len(listed))
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("CAS file should be reclaimed: %v", err)
	}
}

// TestDeleteWithOptions_PromoteArtifactsToProject — preserve flag
// + promote flag moves artifacts to project scope before the session
// row goes away. The CAS file must survive because the artifact row
// still references it.
func TestDeleteWithOptions_PromoteArtifactsToProject(t *testing.T) {
	t.Parallel()
	api, artMgr, artStore, _, sessMgr, projMgr, dir := newCascadeFixture(t)
	ctx := context.Background()

	proj, err := projMgr.Create(ctx, "p1", "")
	if err != nil {
		t.Fatalf("Create project: %v", err)
	}
	target, err := api.Create(ctx, "in-proj")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := sessMgr.MoveToProject(ctx, target.ID, &proj.ID); err != nil {
		t.Fatalf("MoveToProject: %v", err)
	}

	captured, err := artMgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "promote.txt", MimeType: "text/plain", Bytes: []byte("promote me"),
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, target.ID)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	hash := captured[0].ContentHash
	path := filepath.Join(dir, "media", hash)

	if err := api.DeleteWithOptions(ctx, target.ID, DeleteOptions{
		PreserveArtifacts:         true,
		PromoteArtifactsToProject: true,
	}); err != nil {
		t.Fatalf("DeleteWithOptions: %v", err)
	}

	// Project list still sees the artifact.
	listed, err := artStore.List(ctx, coreart.ArtifactFilter{ProjectID: proj.ID})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ScopeKind != "project" {
		t.Errorf("post-promote project list = %+v", listed)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("CAS file should survive promote: %v", err)
	}
}

// TestDeleteWithOptions_ErrorsWhenNoProjectAndPreserve — preserve
// without a project to absorb returns the orphan-protect sentinel.
func TestDeleteWithOptions_ErrorsWhenNoProjectAndPreserve(t *testing.T) {
	t.Parallel()
	api, artMgr, _, _, _, _, _ := newCascadeFixture(t)
	ctx := context.Background()
	target, err := api.Create(ctx, "loose")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := artMgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "x.txt", MimeType: "text/plain", Bytes: []byte("x"),
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, target.ID); err != nil {
		t.Fatalf("Capture: %v", err)
	}
	err = api.DeleteWithOptions(ctx, target.ID, DeleteOptions{PreserveArtifacts: true})
	if !errors.Is(err, ErrSessionHasArtifacts) {
		t.Errorf("err = %v, want ErrSessionHasArtifacts", err)
	}
}
