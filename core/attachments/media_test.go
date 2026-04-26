package attachments_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// openMediaTest opens a fresh on-disk SQLite + tempdir for media tests.
func openMediaTest(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close(context.Background())
	})
	return db, dir
}

func TestMediaStore_Put_WritesFileAndRow(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := []byte("hello world")
	want := sha256.Sum256(body)
	wantHex := hex.EncodeToString(want[:])

	art, err := store.Put(ctx, body, "text/plain", "hello.txt")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if art.ContentHash != wantHex {
		t.Errorf("ContentHash = %q, want %q", art.ContentHash, wantHex)
	}
	if art.ByteSize != int64(len(body)) {
		t.Errorf("ByteSize = %d", art.ByteSize)
	}
	if art.OriginalName != "hello.txt" {
		t.Errorf("OriginalName = %q", art.OriginalName)
	}

	// File exists on disk at the expected name.
	dest := filepath.Join(dir, "media", wantHex)
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("on-disk bytes mismatch")
	}
}

func TestMediaStore_Put_DedupsIdenticalBytes(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := []byte("identical bytes")
	first, err := store.Put(ctx, body, "text/plain", "a.txt")
	if err != nil {
		t.Fatalf("Put first: %v", err)
	}
	second, err := store.Put(ctx, body, "text/plain", "b.txt")
	if err != nil {
		t.Fatalf("Put second: %v", err)
	}
	if first.ContentHash != second.ContentHash {
		t.Errorf("expected matching hashes, got %q vs %q", first.ContentHash, second.ContentHash)
	}
	if first.ID == second.ID {
		t.Errorf("expected distinct ids, got %q", first.ID)
	}

	// Exactly one file on disk for the hash.
	entries, err := os.ReadDir(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	hashFiles := 0
	for _, ent := range entries {
		if !ent.IsDir() && len(ent.Name()) == 64 {
			hashFiles++
		}
	}
	if hashFiles != 1 {
		t.Errorf("hashFiles = %d, want 1", hashFiles)
	}
}

func TestMediaStore_Get_RoundTrip(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := []byte("payload")
	art, err := store.Put(ctx, body, "image/png", "pic.png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, gotBytes, err := store.Get(ctx, art.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != art.ID {
		t.Errorf("ID mismatch")
	}
	if string(gotBytes) != string(body) {
		t.Errorf("bytes mismatch")
	}
	if _, _, err := store.Get(ctx, "ghost"); !errors.Is(err, attachments.ErrMediaNotFound) {
		t.Errorf("ghost Get = %v, want ErrMediaNotFound", err)
	}
}

func TestMediaStore_GetByHash_LatestRow(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := []byte("dedupe me")
	first, err := store.Put(ctx, body, "text/plain", "early.txt")
	if err != nil {
		t.Fatalf("Put first: %v", err)
	}
	second, err := store.Put(ctx, body, "text/plain", "late.txt")
	if err != nil {
		t.Fatalf("Put second: %v", err)
	}
	got, _, err := store.GetByHash(ctx, first.ContentHash)
	if err != nil {
		t.Fatalf("GetByHash: %v", err)
	}
	if got.ID != second.ID {
		t.Errorf("GetByHash returned %q, want latest %q", got.ID, second.ID)
	}
	if _, _, err := store.GetByHash(ctx, "missing"); !errors.Is(err, attachments.ErrMediaNotFound) {
		t.Errorf("missing GetByHash = %v, want ErrMediaNotFound", err)
	}
}

func TestMediaStore_Delete_LeavesFile(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := []byte("two refs")
	a, err := store.Put(ctx, body, "text/plain", "")
	if err != nil {
		t.Fatalf("Put a: %v", err)
	}
	if _, err := store.Put(ctx, body, "text/plain", ""); err != nil {
		t.Fatalf("Put b: %v", err)
	}
	if err := store.Delete(ctx, a.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// File still exists — the second row keeps the on-disk file alive.
	if _, err := os.Stat(filepath.Join(dir, "media", a.ContentHash)); err != nil {
		t.Errorf("file unexpectedly removed: %v", err)
	}
	// Delete is single-responsibility — second Delete on the same id is
	// ErrMediaNotFound.
	if err := store.Delete(ctx, a.ID); !errors.Is(err, attachments.ErrMediaNotFound) {
		t.Errorf("double Delete = %v", err)
	}
}

func TestMediaStore_RefcountFor_PendingAttachments(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	store.RegisterRefcountSource(attachments.AttachmentsRefcountSource{DB: db})
	ctx := context.Background()

	body := []byte("with attachments")
	art, err := store.Put(ctx, body, "image/png", "x.png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	// 1 row in media_artifacts only (no attachments yet).
	count, err := store.RefcountFor(ctx, art.ContentHash)
	if err != nil {
		t.Fatalf("RefcountFor: %v", err)
	}
	if count != 1 {
		t.Errorf("RefcountFor = %d, want 1 (1 media row)", count)
	}

	// Add an attachment with media_id pointing at the artifact.
	attMgr := attachments.NewManager(attachments.NewSQLStore(db))
	mediaID := art.ID
	if _, err := attMgr.Add(ctx, attachments.Attachment{
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "media:" + art.ID,
		Kind:          attachments.KindUser,
		MediaID:       &mediaID,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	count, err = store.RefcountFor(ctx, art.ContentHash)
	if err != nil {
		t.Fatalf("RefcountFor 2: %v", err)
	}
	if count != 2 {
		t.Errorf("RefcountFor = %d, want 2 (1 media row + 1 attachment)", count)
	}
}

func TestMediaStore_PruneOrphans(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	// Plant an orphan file: live on disk under the media dir but no
	// metadata row references it.
	mediaDir := filepath.Join(dir, "media")
	if err := os.MkdirAll(mediaDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	orphanHash := sha256.Sum256([]byte("orphan"))
	orphanPath := filepath.Join(mediaDir, hex.EncodeToString(orphanHash[:]))
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Add a real Put for a different hash to confirm the live file is
	// not removed.
	live, err := store.Put(ctx, []byte("alive"), "text/plain", "")
	if err != nil {
		t.Fatalf("Put live: %v", err)
	}

	removed, err := store.PruneOrphans(ctx)
	if err != nil {
		t.Fatalf("PruneOrphans: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("orphan still on disk: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(mediaDir, live.ContentHash)); err != nil {
		t.Errorf("live file removed: %v", err)
	}

	// Idempotent: second call removes nothing.
	removed, err = store.PruneOrphans(ctx)
	if err != nil {
		t.Fatalf("PruneOrphans 2: %v", err)
	}
	if removed != 0 {
		t.Errorf("second PruneOrphans removed = %d, want 0", removed)
	}
}

func TestMediaStore_ConcurrentPut_SameBytes(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	body := make([]byte, 1024)
	for i := range body {
		body[i] = byte(i % 251)
	}

	const N = 8
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := store.Put(ctx, body, "application/octet-stream", "")
			errs[i] = err
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("put[%d]: %v", i, err)
		}
	}

	// Exactly one file on disk; N rows in media_artifacts.
	entries, err := os.ReadDir(filepath.Join(dir, "media"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	hashFiles := 0
	for _, ent := range entries {
		if !ent.IsDir() && len(ent.Name()) == 64 {
			hashFiles++
		}
	}
	if hashFiles != 1 {
		t.Errorf("hashFiles = %d, want 1", hashFiles)
	}
	rows, err := store.List(ctx, attachments.MediaFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != N {
		t.Errorf("rows = %d, want %d", len(rows), N)
	}
}

func TestMediaStore_Put_Oversize(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	// Cap at 1 KiB so a 30 KiB attempt overflows.
	store := attachments.NewSQLMediaStore(db, dir, attachments.WithMaxBytes(1024))
	ctx := context.Background()
	body := make([]byte, 30*1024)
	if _, err := store.Put(ctx, body, "application/octet-stream", ""); !errors.Is(err, attachments.ErrOversize) {
		t.Errorf("Put = %v, want ErrOversize", err)
	}
}

// TestMediaStore_OnDeleteSetNull_FromArtifacts confirms the FK
// REFERENCES with ON DELETE SET NULL works: deleting the
// media_artifacts row directly leaves the attachment with NULL media_id.
func TestMediaStore_OnDeleteSetNull_FromArtifacts(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	art, err := store.Put(ctx, []byte("set null target"), "image/png", "img.png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	attStore := attachments.NewSQLStore(db)
	mediaID := art.ID
	if err := attStore.Add(ctx, attachments.Attachment{
		ID:            "att-1",
		ScopeKind:     attachments.ScopeKindSession,
		ScopeID:       "s-x",
		ContentSource: "media:" + art.ID,
		Kind:          attachments.KindUser,
		MediaID:       &mediaID,
	}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := store.Delete(ctx, art.ID); err != nil {
		t.Fatalf("media Delete: %v", err)
	}

	got, err := attStore.Get(ctx, "att-1")
	if err != nil {
		t.Fatalf("Get attachment: %v", err)
	}
	if got.MediaID != nil {
		t.Errorf("MediaID = %v, want nil after media row deletion", got.MediaID)
	}
}
