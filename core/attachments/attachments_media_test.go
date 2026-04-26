package attachments_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
)

// TestManager_AddMedia_RoundTrip verifies the WP02 binary-attachment
// path: AddMedia uploads through the MediaStore, mints a media-backed
// attachment with ContentSource="media:<id>", and the resulting row
// surfaces through List.
func TestManager_AddMedia_RoundTrip(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	mgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	ctx := context.Background()

	body := []byte("png-bytes-here")
	att, err := mgr.AddMedia(ctx, attachments.ScopeKindSession, "s1", body, "image/png", "shot.png")
	if err != nil {
		t.Fatalf("AddMedia: %v", err)
	}
	if att.MediaID == nil {
		t.Fatal("MediaID nil")
	}
	if !strings.HasPrefix(att.ContentSource, "media:") {
		t.Errorf("ContentSource = %q", att.ContentSource)
	}
	if att.Content != "" {
		t.Errorf("Content = %q, want empty for binary", att.Content)
	}
	got, err := mgr.List(ctx, attachments.ScopeFilter{
		ScopeKind: attachments.ScopeKindSession,
		ScopeID:   "s1",
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != att.ID {
		t.Errorf("List did not surface AddMedia row: %+v", got)
	}
	// File on disk exists at <DataDir>/media/<content_hash>.
	listed, err := media.List(ctx, attachments.MediaFilter{})
	if err != nil {
		t.Fatalf("media List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("media rows = %d", len(listed))
	}
	if _, err := os.Stat(filepath.Join(dir, "media", listed[0].ContentHash)); err != nil {
		t.Errorf("hash file missing: %v", err)
	}
}

// TestManager_AddMedia_WithoutStore returns ErrMediaStoreUnavailable.
func TestManager_AddMedia_WithoutStore(t *testing.T) {
	t.Parallel()
	mgr := attachments.NewManager(attachments.NewMemoryStore())
	_, err := mgr.AddMedia(context.Background(), attachments.ScopeKindSession,
		"s1", []byte("x"), "text/plain", "x.txt")
	if !errors.Is(err, attachments.ErrMediaStoreUnavailable) {
		t.Errorf("got %v, want ErrMediaStoreUnavailable", err)
	}
}

// TestManager_Remove_LastRefSweepsFile — refcount=1 attachment; removal
// drops the metadata row AND the on-disk file.
func TestManager_Remove_LastRefSweepsFile(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	mgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	ctx := context.Background()

	body := []byte("only ref")
	att, err := mgr.AddMedia(ctx, attachments.ScopeKindSession, "s1", body, "image/png", "")
	if err != nil {
		t.Fatalf("AddMedia: %v", err)
	}
	listed, _ := media.List(ctx, attachments.MediaFilter{})
	if len(listed) != 1 {
		t.Fatalf("setup: media rows = %d", len(listed))
	}
	hashFile := filepath.Join(dir, "media", listed[0].ContentHash)

	if err := mgr.Remove(ctx, att.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	// Metadata row gone.
	listed, _ = media.List(ctx, attachments.MediaFilter{})
	if len(listed) != 0 {
		t.Errorf("media rows after Remove = %d, want 0", len(listed))
	}
	// On-disk file gone.
	if _, err := os.Stat(hashFile); !os.IsNotExist(err) {
		t.Errorf("hash file still on disk: %v", err)
	}
}

// TestManager_Remove_WithSecondRefKeepsFile — two attachments share a
// content hash; removing one drops one media row but leaves the file
// because the other attachment + media row still reference it.
func TestManager_Remove_WithSecondRefKeepsFile(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	mgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	ctx := context.Background()

	body := []byte("two refs")
	a1, err := mgr.AddMedia(ctx, attachments.ScopeKindSession, "s1", body, "image/png", "a.png")
	if err != nil {
		t.Fatalf("AddMedia a: %v", err)
	}
	if _, err := mgr.AddMedia(ctx, attachments.ScopeKindSession, "s2", body, "image/png", "b.png"); err != nil {
		t.Fatalf("AddMedia b: %v", err)
	}
	listed, _ := media.List(ctx, attachments.MediaFilter{})
	if len(listed) != 2 {
		t.Fatalf("setup: media rows = %d", len(listed))
	}
	hashFile := filepath.Join(dir, "media", listed[0].ContentHash)

	if err := mgr.Remove(ctx, a1.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	// One media row left; file still on disk.
	listed, _ = media.List(ctx, attachments.MediaFilter{})
	if len(listed) != 1 {
		t.Errorf("media rows after Remove = %d, want 1", len(listed))
	}
	if _, err := os.Stat(hashFile); err != nil {
		t.Errorf("hash file gone too early: %v", err)
	}
}

// TestManager_Remove_TextOnlyUnaffected ensures the new refcount path
// is a no-op for the existing text-only attachments.
func TestManager_Remove_TextOnlyUnaffected(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	media := attachments.NewSQLMediaStore(db, dir)
	media.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	mgr := attachments.NewManager(
		attachments.NewSQLStore(db),
		attachments.WithMediaStore(media),
	)
	ctx := context.Background()

	att, err := mgr.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abc",
		Content:       "plain text",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := mgr.Remove(ctx, att.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
}
