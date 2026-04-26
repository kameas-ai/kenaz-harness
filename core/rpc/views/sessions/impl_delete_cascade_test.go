package sessions

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/session"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// TestManagerAPI_DeleteSession_PrunesAttachmentsAndOrphanMedia is the
// A8 acceptance test from the multimodal-io spec: deleting a session
// drops session-scope attachments AND any media artifacts no longer
// referenced (refcount-driven).
func TestManagerAPI_DeleteSession_PrunesAttachmentsAndOrphanMedia(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close(context.Background())
	ctx := context.Background()

	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	attMgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	sessMgr := session.NewManager(session.NewSQLStore(session.NewStorageDB(db)))
	api := NewManagerAPIWithAttachments(sessMgr, attMgr)

	target, err := api.Create(ctx, "delete-me")
	if err != nil {
		t.Fatalf("Create target: %v", err)
	}
	keep, err := api.Create(ctx, "keep-me")
	if err != nil {
		t.Fatalf("Create keep: %v", err)
	}

	// Two media-backed attachments under target session: an exclusive
	// hash + a shared hash that another session also references.
	exclusive := []byte("exclusive-bytes-target-only")
	shared := []byte("shared-bytes-two-sessions")
	if _, err := attMgr.AddMedia(ctx, attachments.ScopeKindSession, target.ID,
		exclusive, "image/png", "exclusive.png"); err != nil {
		t.Fatalf("AddMedia exclusive: %v", err)
	}
	if _, err := attMgr.AddMedia(ctx, attachments.ScopeKindSession, target.ID,
		shared, "image/png", "shared-a.png"); err != nil {
		t.Fatalf("AddMedia shared on target: %v", err)
	}
	if _, err := attMgr.AddMedia(ctx, attachments.ScopeKindSession, keep.ID,
		shared, "image/png", "shared-b.png"); err != nil {
		t.Fatalf("AddMedia shared on keep: %v", err)
	}

	listed, err := media.List(ctx, attachments.MediaFilter{})
	if err != nil {
		t.Fatalf("media List: %v", err)
	}
	if len(listed) != 3 {
		t.Fatalf("setup: media rows = %d, want 3", len(listed))
	}
	hashes := map[string]string{}
	for _, m := range listed {
		hashes[m.OriginalName] = m.ContentHash
	}
	exclusiveHash := hashes["exclusive.png"]
	sharedHash := hashes["shared-a.png"]
	if exclusiveHash == "" || sharedHash == "" {
		t.Fatalf("hashes not captured: %+v", hashes)
	}
	exclusivePath := filepath.Join(dir, "media", exclusiveHash)
	sharedPath := filepath.Join(dir, "media", sharedHash)
	if _, err := os.Stat(exclusivePath); err != nil {
		t.Fatalf("exclusive file missing pre-delete: %v", err)
	}
	if _, err := os.Stat(sharedPath); err != nil {
		t.Fatalf("shared file missing pre-delete: %v", err)
	}

	// Delete the target session via the rpc surface.
	if err := api.Delete(ctx, target.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Target's session-scope attachments are gone.
	rest, err := attMgr.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   target.ID,
	})
	if err != nil {
		t.Fatalf("List target after delete: %v", err)
	}
	if len(rest) != 0 {
		t.Errorf("target session still has %d attachments", len(rest))
	}

	// keep session's attachment + its media row survive.
	keepAtts, err := attMgr.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   keep.ID,
	})
	if err != nil {
		t.Fatalf("List keep: %v", err)
	}
	if len(keepAtts) != 1 {
		t.Errorf("keep attachments = %d, want 1", len(keepAtts))
	}

	// Exclusive media: file pruned (no row references it).
	if _, err := os.Stat(exclusivePath); !os.IsNotExist(err) {
		t.Errorf("exclusive file should be gone, err=%v", err)
	}

	// Shared media: file lives because keep's attachment + its media
	// row still reference the hash.
	if _, err := os.Stat(sharedPath); err != nil {
		t.Errorf("shared file should survive, err=%v", err)
	}
	count, err := media.RefcountFor(ctx, sharedHash)
	if err != nil {
		t.Fatalf("RefcountFor shared: %v", err)
	}
	if count == 0 {
		t.Errorf("shared refcount = 0, want > 0")
	}
}
