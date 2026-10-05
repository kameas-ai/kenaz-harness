package artifacts_test

// units_cascade_test.go — artifacts-as-units-01DOGF0C WP03 (spec FR-4,
// FR-6). Real sqlite, real media store, real session/project managers.
//
// The two hazards these pin:
//
//   - §2.4 media refcount: media GC (PruneOrphans) must see every byte
//     blob an artifact unit — head row OR version row — references, or it
//     deletes the bytes out from under a live artifact.
//   - §2.6 FK cascade: units.scope_id has no FK, so deleting a session or
//     project removes nothing unless code does it. These drive the delete
//     through session.Manager / projects.Manager exactly as production
//     does, with the purge registered as a delete observer.

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
	"github.com/kameas-ai/kenaz-harness/core/projects"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

type cascadeRig struct {
	db       storage.DB
	dataDir  string
	media    attachments.MediaStore
	store    artifacts.Store
	sessions *session.Manager
	projects *projects.Manager
}

func newCascadeRig(t *testing.T) *cascadeRig {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{DataDir: dir, EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	st := artifacts.NewUnitsStore(db)
	media.RegisterRefcountSource(artifacts.ArtifactsRefcountSource{Store: st})
	rig := &cascadeRig{
		db: db, dataDir: dir, media: media, store: st,
		sessions: session.NewManager(session.NewSQLStore(session.NewStorageDB(db))),
		projects: projects.NewManager(projects.NewSQLStore(db)),
	}
	purger := st.(artifacts.ScopePurger)
	rig.sessions.AddDeleteObserver(func(ctx context.Context, id string) error {
		hashes, err := purger.PurgeSession(ctx, id)
		if err != nil {
			return err
		}
		_, err = attachments.ReleaseUnreferenced(ctx, media, hashes)
		return err
	})
	rig.projects.AddDeleteObserver(func(ctx context.Context, id string) error {
		hashes, err := purger.PurgeProject(ctx, id)
		if err != nil {
			return err
		}
		_, err = attachments.ReleaseUnreferenced(ctx, media, hashes)
		return err
	})
	return rig
}

func (r *cascadeRig) put(t *testing.T, body string) string {
	t.Helper()
	m, err := r.media.Put(context.Background(), []byte(body), "text/plain", "f.txt")
	if err != nil {
		t.Fatalf("media Put: %v", err)
	}
	return m.ContentHash
}

func (r *cascadeRig) onDisk(hash string) bool {
	_, err := os.Stat(filepath.Join(r.dataDir, "media", hash))
	return err == nil
}

// TestUnitsStore_MediaGCKeepsArtifactBytes pins FR-4: with the units
// refcount source registered, a GC pass over a media dir whose only
// remaining reference is an artifact unit (head row or version row)
// collects nothing — even once the media_artifacts metadata rows are gone.
func TestUnitsStore_MediaGCKeepsArtifactBytes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCascadeRig(t)
	head := r.put(t, "head bytes")
	rev := r.put(t, "revision bytes")
	a := mustInsert(t, r.store, artifacts.Artifact{SessionID: "s", MimeType: "text/plain", ContentHash: head, Source: artifacts.SourceUserPin})
	if _, err := r.store.WriteVersion(ctx, artifacts.ArtifactVersion{ArtifactID: a.ID, ContentHash: rev, MimeType: "text/plain"}); err != nil {
		t.Fatalf("WriteVersion: %v", err)
	}
	// Remove the media metadata rows so the ONLY thing keeping the files
	// alive is the artifacts refcount source.
	for _, h := range []string{head, rev} {
		rows, _ := r.media.List(ctx, attachments.MediaFilter{ContentHash: h})
		for _, m := range rows {
			if err := r.media.Delete(ctx, m.ID); err != nil {
				t.Fatalf("media Delete: %v", err)
			}
		}
	}
	removed, err := r.media.PruneOrphans(ctx)
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if removed != 0 || !r.onDisk(head) || !r.onDisk(rev) {
		t.Fatalf("GC removed %d file(s); head on disk=%v rev on disk=%v — a live artifact's bytes were collected",
			removed, r.onDisk(head), r.onDisk(rev))
	}
}

// TestUnitsStore_SessionDelete_PurgesArtifactUnits pins FR-6 / P-6 for
// sessions: deleting the session through session.Manager removes its
// session-scoped artifact units with their versions, unlinks (does not
// delete) an artifact promoted out of it, leaves other sessions alone,
// and lets media GC reclaim exactly the purged bytes.
func TestUnitsStore_SessionDelete_PurgesArtifactUnits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCascadeRig(t)
	s1, err := r.sessions.Create(ctx, "doomed")
	if err != nil {
		t.Fatalf("create s1: %v", err)
	}
	s2, err := r.sessions.Create(ctx, "survivor")
	if err != nil {
		t.Fatalf("create s2: %v", err)
	}
	gone := r.put(t, "s1 capture")
	goneRev := r.put(t, "s1 revision")
	promotedHash := r.put(t, "promoted out of s1")
	keptHash := r.put(t, "s2 capture")

	doomed := mustInsert(t, r.store, artifacts.Artifact{SessionID: s1.ID, MimeType: "text/plain", ContentHash: gone, Source: artifacts.SourceCodeBlock})
	if _, err := r.store.WriteVersion(ctx, artifacts.ArtifactVersion{ArtifactID: doomed.ID, ContentHash: goneRev}); err != nil {
		t.Fatalf("WriteVersion: %v", err)
	}
	promoted := mustInsert(t, r.store, artifacts.Artifact{SessionID: s1.ID, MimeType: "text/plain", ContentHash: promotedHash, Source: artifacts.SourceUserPin, ScopeKind: artifacts.ScopeKindGlobal})
	kept := mustInsert(t, r.store, artifacts.Artifact{SessionID: s2.ID, MimeType: "text/plain", ContentHash: keptHash, Source: artifacts.SourceCodeBlock})

	if err := r.sessions.Delete(ctx, s1.ID); err != nil {
		t.Fatalf("session Delete: %v", err)
	}

	if _, err := r.store.Get(ctx, doomed.ID); err == nil {
		t.Error("session-scoped artifact unit survived its session's delete")
	}
	var versions int
	if err := r.db.Reader().QueryRow(ctx, "SELECT COUNT(*) FROM unit_versions WHERE unit_id = ?", doomed.ID).Scan(&versions); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if versions != 0 {
		t.Errorf("unit_versions for purged artifact = %d, want 0", versions)
	}
	p, err := r.store.Get(ctx, promoted.ID)
	if err != nil {
		t.Fatalf("promoted (global) artifact deleted with its origin session: %v", err)
	}
	if p.SessionID != "" {
		t.Errorf("promoted artifact SessionID = %q, want \"\" (legacy ON DELETE SET NULL parity)", p.SessionID)
	}
	if _, err := r.store.Get(ctx, kept.ID); err != nil {
		t.Errorf("other session's artifact touched: %v", err)
	}
	if r.onDisk(gone) || r.onDisk(goneRev) {
		t.Errorf("purged artifact bytes still on disk (head=%v rev=%v) — media GC did not reclaim", r.onDisk(gone), r.onDisk(goneRev))
	}
	if !r.onDisk(promotedHash) || !r.onDisk(keptHash) {
		t.Errorf("live artifact bytes reclaimed (promoted=%v kept=%v)", r.onDisk(promotedHash), r.onDisk(keptHash))
	}
}

// TestUnitsStore_ProjectDelete_PurgesArtifactUnits pins FR-6 for
// projects: project-scoped artifact units go with the project; a
// session-scoped artifact that merely carried the project id keeps
// existing with the link nulled.
func TestUnitsStore_ProjectDelete_PurgesArtifactUnits(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCascadeRig(t)
	proj, err := r.projects.Create(ctx, "doomed project", "")
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	pid := proj.ID
	scopedHash := r.put(t, "project scoped")
	linkedHash := r.put(t, "session scoped, project linked")
	scoped := mustInsert(t, r.store, artifacts.Artifact{SessionID: "s", ProjectID: &pid, MimeType: "text/plain", ContentHash: scopedHash, Source: artifacts.SourceUserPin, ScopeKind: artifacts.ScopeKindProject})
	linked := mustInsert(t, r.store, artifacts.Artifact{SessionID: "s", ProjectID: &pid, MimeType: "text/plain", ContentHash: linkedHash, Source: artifacts.SourceCodeBlock})

	if err := r.projects.Delete(ctx, proj.ID); err != nil {
		t.Fatalf("project Delete: %v", err)
	}
	if _, err := r.store.Get(ctx, scoped.ID); err == nil {
		t.Error("project-scoped artifact unit survived its project's delete")
	}
	l, err := r.store.Get(ctx, linked.ID)
	if err != nil {
		t.Fatalf("session-scoped artifact deleted by a project delete: %v", err)
	}
	if l.ProjectID != nil {
		t.Errorf("linked artifact ProjectID = %v, want nil", *l.ProjectID)
	}
	if r.onDisk(scopedHash) {
		t.Error("purged project artifact's bytes still on disk")
	}
	if !r.onDisk(linkedHash) {
		t.Error("surviving artifact's bytes reclaimed")
	}
}

// TestUnitsStore_PurgeEmptyIDIsNoOp guards the one input that would turn
// a scoped purge into "every unscoped row": an empty id.
func TestUnitsStore_PurgeEmptyIDIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := newCascadeRig(t)
	g := mustInsert(t, r.store, artifacts.Artifact{MimeType: "text/plain", ContentHash: "h", Source: artifacts.SourceUserPin, ScopeKind: artifacts.ScopeKindGlobal})
	purger := r.store.(artifacts.ScopePurger)
	if _, err := purger.PurgeSession(ctx, ""); err != nil {
		t.Fatalf("PurgeSession(\"\"): %v", err)
	}
	if _, err := purger.PurgeProject(ctx, ""); err != nil {
		t.Fatalf("PurgeProject(\"\"): %v", err)
	}
	if _, err := r.store.Get(ctx, g.ID); err != nil {
		t.Errorf("global artifact (scope_id '') purged by an empty-id purge: %v", err)
	}
}
