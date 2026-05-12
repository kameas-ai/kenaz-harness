package attachments_test

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	llm "github.com/sigil-tech/kaneaz-harness/core/llm"

	"github.com/sigil-tech/kaneaz-harness/core/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/storage"
	storagesqlite "github.com/sigil-tech/kaneaz-harness/core/storage/sqlite"
)

// ── minimal PNG fixture ───────────────────────────────────────────────────

// minimalPNG builds a minimal valid PNG with the supplied dimensions.
// The image is 1×W×H px of black pixels (RGBA 0,0,0,255).
// Reference: https://www.w3.org/TR/png/#5PNG-file-signature
func minimalPNG(width, height int) []byte {
	var buf bytes.Buffer

	// PNG signature
	buf.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})

	writeChunk := func(typ string, data []byte) {
		var chunk bytes.Buffer
		chunk.Write(data)
		chunkBytes := chunk.Bytes()

		lenBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(lenBuf, uint32(len(chunkBytes)))
		buf.Write(lenBuf)
		buf.WriteString(typ)
		buf.Write(chunkBytes)

		// CRC over type + data
		crcVal := crc32PNG([]byte(typ), chunkBytes)
		crcBuf := make([]byte, 4)
		binary.BigEndian.PutUint32(crcBuf, crcVal)
		buf.Write(crcBuf)
	}

	// IHDR
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], uint32(width))
	binary.BigEndian.PutUint32(ihdr[4:8], uint32(height))
	ihdr[8] = 8  // bit depth
	ihdr[9] = 2  // color type: RGB
	ihdr[10] = 0 // compression
	ihdr[11] = 0 // filter
	ihdr[12] = 0 // interlace
	writeChunk("IHDR", ihdr)

	// IDAT — one scanline of zeros per row
	var idatRaw bytes.Buffer
	for y := 0; y < height; y++ {
		idatRaw.WriteByte(0) // filter type: None
		for x := 0; x < width; x++ {
			idatRaw.Write([]byte{0, 0, 0}) // R, G, B
		}
	}
	var idatCompressed bytes.Buffer
	zw := zlib.NewWriter(&idatCompressed)
	_, _ = zw.Write(idatRaw.Bytes())
	_ = zw.Close()
	writeChunk("IDAT", idatCompressed.Bytes())

	// IEND
	writeChunk("IEND", nil)
	return buf.Bytes()
}

// crc32PNG computes a CRC-32 checksum following the PNG spec (polynomial 0xEDB88320).
func crc32PNG(typ, data []byte) uint32 {
	crc := uint32(0xFFFFFFFF)
	update := func(b byte) {
		crc ^= uint32(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0xEDB88320
			} else {
				crc >>= 1
			}
		}
	}
	for _, b := range typ {
		update(b)
	}
	for _, b := range data {
		update(b)
	}
	return ^crc
}

// minimalPDF builds the smallest syntactically valid PDF with one empty page.
// All xref offsets are computed from the actual byte positions so that
// ledongthuc/pdf can parse the file without panicking.
func minimalPDF() []byte {
	// Objects written sequentially; record byte offsets as we go.
	var buf bytes.Buffer

	header := "%PDF-1.4\n"
	buf.WriteString(header)

	obj1Start := buf.Len()
	obj1 := "1 0 obj\n<</Type /Catalog /Pages 2 0 R>>\nendobj\n"
	buf.WriteString(obj1)

	obj2Start := buf.Len()
	obj2 := "2 0 obj\n<</Type /Pages /Kids [3 0 R] /Count 1>>\nendobj\n"
	buf.WriteString(obj2)

	obj3Start := buf.Len()
	obj3 := "3 0 obj\n<</Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]>>\nendobj\n"
	buf.WriteString(obj3)

	xrefStart := buf.Len()

	// Cross-reference table.
	fmt.Fprintf(&buf, "xref\n0 4\n")
	fmt.Fprintf(&buf, "0000000000 65535 f \n")
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj1Start)
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj2Start)
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj3Start)

	fmt.Fprintf(&buf, "trailer\n<</Size 4 /Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", xrefStart)
	return buf.Bytes()
}

// encryptedPDF builds a minimal PDF with a Standard-encryption /Encrypt
// dictionary that contains deliberately wrong /U (user password hash) bytes.
// ledongthuc/pdf calls initEncrypt("") which computes the expected hash,
// finds it doesn't match /U, and returns ErrInvalidPassword — which contains
// the word "encrypted", so extractPDFPageCount maps it to ErrAttachmentEncrypted.
//
// The /O and /U values are each 32 bytes of 0xAA / 0xBB. Any real RC4 decrypt
// of the empty password will produce a different 32-byte sequence, so the
// HasPrefix check in initEncrypt fails deterministically.
func encryptedPDF() []byte {
	// 32-byte owner-password hash (arbitrary) — 64 hex chars.
	const oHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	// 32-byte user-password hash (wrong value) — 64 hex chars.
	const uHex = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	// 16-byte file ID — 32 hex chars.
	const idHex = "cccccccccccccccccccccccccccccccc"

	var buf bytes.Buffer

	buf.WriteString("%PDF-1.4\n")

	obj1Start := buf.Len()
	buf.WriteString("1 0 obj\n<</Type /Catalog /Pages 2 0 R>>\nendobj\n")

	obj2Start := buf.Len()
	buf.WriteString("2 0 obj\n<</Type /Pages /Kids [3 0 R] /Count 1>>\nendobj\n")

	obj3Start := buf.Len()
	buf.WriteString("3 0 obj\n<</Type /Page /Parent 2 0 R /MediaBox [0 0 612 792]>>\nendobj\n")

	// Encrypt dictionary: Filter=Standard, V=2, R=3, Length=128 (key size),
	// O and U are each 32 bytes, P=-3904 (typical permissions int).
	obj4Start := buf.Len()
	fmt.Fprintf(&buf,
		"4 0 obj\n<</Filter /Standard /V 2 /R 3 /Length 128"+
			" /O <%s>"+
			" /U <%s>"+
			" /P -3904>>\nendobj\n",
		oHex, uHex)

	xrefStart := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 5\n")
	fmt.Fprintf(&buf, "0000000000 65535 f \n")
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj1Start)
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj2Start)
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj3Start)
	fmt.Fprintf(&buf, "%010d 00000 n \n", obj4Start)
	fmt.Fprintf(&buf,
		"trailer\n<</Size 5 /Root 1 0 R /Encrypt 4 0 R /ID [<%s> <%s>]>>\nstartxref\n%d\n%%%%EOF\n",
		idHex, idHex, xrefStart)

	return buf.Bytes()
}

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

// ── WP06: image dimension + PDF page-count extraction ────────────────────

// TestMediaStore_Put_ImageDimensions_PNG verifies that Put extracts the correct
// pixel dimensions from a synthesised PNG and sets them on the returned artifact.
func TestMediaStore_Put_ImageDimensions_PNG(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	png := minimalPNG(120, 80)
	art, err := store.Put(ctx, png, "image/png", "test.png")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if art.ImageWidth != 120 {
		t.Errorf("ImageWidth = %d, want 120", art.ImageWidth)
	}
	if art.ImageHeight != 80 {
		t.Errorf("ImageHeight = %d, want 80", art.ImageHeight)
	}
}

// TestMediaStore_Put_ImageDimensions_NonImage verifies that Put leaves dimensions
// at zero when the MIME type is not an image.
func TestMediaStore_Put_ImageDimensions_NonImage(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	art, err := store.Put(ctx, []byte("not an image"), "text/plain", "note.txt")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if art.ImageWidth != 0 || art.ImageHeight != 0 {
		t.Errorf("expected zero dims for non-image, got (%d, %d)", art.ImageWidth, art.ImageHeight)
	}
}

// TestMediaStore_Put_ImageDimensions_Corrupt verifies that Put succeeds even
// when the image header is unreadable (best-effort extraction; zeros on failure).
func TestMediaStore_Put_ImageDimensions_Corrupt(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	// Corrupt PNG: valid signature prefix but garbage body.
	corrupt := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte("garbage")...)
	art, err := store.Put(ctx, corrupt, "image/png", "corrupt.png")
	if err != nil {
		t.Fatalf("Put should succeed even on corrupt image, got: %v", err)
	}
	// Dimensions should be zero (extraction failed non-fatally).
	if art.ImageWidth != 0 || art.ImageHeight != 0 {
		t.Errorf("corrupt image dims = (%d, %d), want (0, 0)", art.ImageWidth, art.ImageHeight)
	}
}

// TestMediaStore_Put_PDFPageCount verifies that Put extracts the correct page
// count from a minimal well-formed PDF.
func TestMediaStore_Put_PDFPageCount(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	pdfBytes := minimalPDF()
	art, err := store.Put(ctx, pdfBytes, "application/pdf", "doc.pdf")
	if err != nil {
		t.Fatalf("Put PDF: %v", err)
	}
	if art.PageCount != 1 {
		t.Errorf("PageCount = %d, want 1", art.PageCount)
	}
	// Image dimensions should be zero for a PDF.
	if art.ImageWidth != 0 || art.ImageHeight != 0 {
		t.Errorf("PDF image dims = (%d, %d), want (0, 0)", art.ImageWidth, art.ImageHeight)
	}
}

// TestMediaStore_Put_PDFEncrypted verifies that Put returns ErrAttachmentEncrypted
// when the PDF bytes indicate an encrypted document (password-protected).
func TestMediaStore_Put_PDFEncrypted(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	pdfBytes := encryptedPDF()
	_, err := store.Put(ctx, pdfBytes, "application/pdf", "enc.pdf")
	if err == nil {
		t.Fatal("Put encrypted PDF: want error, got nil")
	}
	var encErr *llm.ErrAttachmentEncrypted
	if !errors.As(err, &encErr) {
		t.Errorf("Put encrypted PDF = %v, want *llm.ErrAttachmentEncrypted", err)
	}
}

// TestMediaStore_Put_NonPDFDocument verifies that Put leaves PageCount at zero
// for a non-PDF application/* MIME type (no extraction attempted).
func TestMediaStore_Put_NonPDFDocument(t *testing.T) {
	t.Parallel()
	db, dir := openMediaTest(t)
	store := attachments.NewSQLMediaStore(db, dir)
	ctx := context.Background()

	art, err := store.Put(ctx, []byte("%DOCX binary"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "file.docx")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if art.PageCount != 0 {
		t.Errorf("PageCount = %d, want 0 for non-PDF", art.PageCount)
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
