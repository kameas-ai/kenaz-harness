package artifacts_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/artifacts"
	"github.com/sigil-tech/kaneaz-harness/core/attachments"
)

// TestManager_Capture_Single materializes one candidate and verifies
// that an artifact row, a media metadata row, and a CAS file all
// exist after the call.
func TestManager_Capture_Single(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	got, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{{
		Title:    "tictactoe.html",
		MimeType: "text/html",
		Bytes:    []byte("<html>hello</html>"),
		Source:   artifacts.SourceCodeBlock,
		SourceRef: artifacts.ArtifactSourceRef{
			MessageID:      "m1",
			Filename:       "tictactoe.html",
			CodeBlockIndex: 0,
		},
	}}, "s1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Capture len = %d, want 1", len(got))
	}
	a := got[0]
	if a.ID == "" {
		t.Errorf("expected fresh id, got empty")
	}
	if a.SessionID != "s1" {
		t.Errorf("SessionID = %q", a.SessionID)
	}
	if a.MimeType != "text/html" {
		t.Errorf("MimeType = %q", a.MimeType)
	}
	if a.ByteSize != int64(len("<html>hello</html>")) {
		t.Errorf("ByteSize = %d", a.ByteSize)
	}

	// CAS file at <dir>/media/<content_hash> exists.
	if _, err := os.Stat(filepath.Join(dir, "media", a.ContentHash)); err != nil {
		t.Errorf("hash file missing: %v", err)
	}
}

// TestManager_Capture_Multiple_FreshIDs verifies each candidate gets
// its own artifact id even when content dedupes.
func TestManager_Capture_Multiple_FreshIDs(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	got, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{
		{
			Title: "foo.go", MimeType: "text/x-go",
			Bytes: []byte("package main"), Source: artifacts.SourceCodeBlock,
			SourceRef: artifacts.ArtifactSourceRef{MessageID: "m1"},
		},
		{
			Title: "bar.go", MimeType: "text/x-go",
			Bytes: []byte("package other"), Source: artifacts.SourceCodeBlock,
			SourceRef: artifacts.ArtifactSourceRef{MessageID: "m1"},
		},
	}, "s1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID == got[1].ID {
		t.Errorf("expected distinct ids, both = %q", got[0].ID)
	}
}

// TestManager_Capture_SharedHash_Dedups (A11) — two captures of
// identical content produce two artifact rows but only one CAS file.
func TestManager_Capture_SharedHash_Dedups(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	body := []byte("identical")
	for i := 0; i < 2; i++ {
		_, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{{
			Title:     "x.txt",
			MimeType:  "text/plain",
			Bytes:     body,
			Source:    artifacts.SourceCodeBlock,
			SourceRef: artifacts.ArtifactSourceRef{MessageID: "m1"},
		}}, "s1")
		if err != nil {
			t.Fatalf("Capture[%d]: %v", i, err)
		}
	}
	listed, err := store.List(context.Background(), artifacts.ArtifactFilter{SessionID: "s1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 2 {
		t.Errorf("artifact rows = %d, want 2", len(listed))
	}
	// Both rows share the same content_hash → one CAS file.
	mediaDir := filepath.Join(dir, "media")
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	files := 0
	for _, e := range entries {
		if !e.IsDir() {
			files++
		}
	}
	if files != 1 {
		t.Errorf("CAS files on disk = %d, want 1", files)
	}
}

// failingMedia panics on the second Put — exercises the partial-success
// semantics in Capture.
type failingMedia struct {
	inner  artifacts.MediaStorer
	calls  int
	failOn int
}

func (f *failingMedia) Put(ctx context.Context, b []byte, mt, name string) (attachments.MediaArtifact, error) {
	f.calls++
	if f.calls == f.failOn {
		return attachments.MediaArtifact{}, errors.New("synthetic media failure")
	}
	return f.inner.Put(ctx, b, mt, name)
}

func TestManager_Capture_PartialSuccessOnMidBatchFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := &failingMedia{inner: attachments.NewMemoryMediaStore(dir), failOn: 2}
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	got, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{
		{
			Title: "ok.txt", MimeType: "text/plain",
			Bytes: []byte("first"), Source: artifacts.SourceCodeBlock,
			SourceRef: artifacts.ArtifactSourceRef{MessageID: "m"},
		},
		{
			Title: "bad.txt", MimeType: "text/plain",
			Bytes: []byte("second"), Source: artifacts.SourceCodeBlock,
			SourceRef: artifacts.ArtifactSourceRef{MessageID: "m"},
		},
	}, "s1")
	if err == nil {
		t.Fatalf("expected error on second candidate")
	}
	if len(got) != 1 {
		t.Errorf("partial success len = %d, want 1", len(got))
	}
	if got[0].Title != "ok.txt" {
		t.Errorf("partial success title = %q", got[0].Title)
	}
}

// TestManager_Capture_DefaultsProjectFromSession verifies the
// SessionProjectReader hookup: a session that has a project produces
// artifacts whose ProjectID is pre-populated.
func TestManager_Capture_DefaultsProjectFromSession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media,
		artifacts.WithSessionReader(
			artifacts.NewStaticSessionProjectReader(map[string]string{"s1": "p1"}),
		),
	)

	got, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{{
		Title:     "x.txt",
		MimeType:  "text/plain",
		Bytes:     []byte("y"),
		Source:    artifacts.SourceCodeBlock,
		SourceRef: artifacts.ArtifactSourceRef{MessageID: "m"},
	}}, "s1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(got) != 1 || got[0].ProjectID == nil || *got[0].ProjectID != "p1" {
		t.Errorf("ProjectID not defaulted: %+v", got[0].ProjectID)
	}
}

// TestManager_Capture_RejectsEmptySession ensures we don't insert
// orphan rows.
func TestManager_Capture_RejectsEmptySession(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)
	_, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{{
		Title:     "x",
		MimeType:  "text/plain",
		Bytes:     []byte("y"),
		Source:    artifacts.SourceCodeBlock,
		SourceRef: artifacts.ArtifactSourceRef{MessageID: "m"},
	}}, "")
	if err == nil {
		t.Fatalf("expected error on empty sessionID")
	}
}

// TestManager_Capture_NoCandidates_ShortCircuits.
func TestManager_Capture_NoCandidates_ShortCircuits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)
	got, err := mgr.Capture(context.Background(), nil, "s1")
	if err != nil {
		t.Errorf("err = %v, want nil", err)
	}
	if got != nil {
		t.Errorf("got = %+v, want nil", got)
	}
}

// TestManager_WriteVersion_RoundTrip verifies the Manager.WriteVersion
// path: bytes go into the MediaStore CAS and a version row comes back
// with the correct content_hash + byte_size.
func TestManager_WriteVersion_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	// Capture a parent artifact first.
	captured, err := mgr.Capture(context.Background(), []artifacts.CaptureCandidate{{
		Title:     "notes.md",
		MimeType:  "text/markdown",
		Bytes:     []byte("# original"),
		Source:    artifacts.SourceToolOutput,
		SourceRef: artifacts.ArtifactSourceRef{MessageID: "m1"},
	}}, "sess-1")
	if err != nil || len(captured) != 1 {
		t.Fatalf("Capture: %v (len=%d)", err, len(captured))
	}
	parent := captured[0]

	summary := "revised introduction"
	ver, err := mgr.WriteVersion(context.Background(), parent.ID,
		[]byte("# revised"), "", &summary, nil)
	if err != nil {
		t.Fatalf("WriteVersion: %v", err)
	}
	if ver.Version != 1 {
		t.Errorf("Version = %d, want 1", ver.Version)
	}
	if ver.ArtifactID != parent.ID {
		t.Errorf("ArtifactID = %q, want %q", ver.ArtifactID, parent.ID)
	}
	if ver.ContentHash == "" {
		t.Errorf("ContentHash empty")
	}
	if ver.ByteSize != int64(len("# revised")) {
		t.Errorf("ByteSize = %d, want %d", ver.ByteSize, len("# revised"))
	}
	if ver.MimeType != "text/markdown" {
		t.Errorf("MimeType = %q, want text/markdown (inherited from parent)", ver.MimeType)
	}
	if ver.Summary == nil || *ver.Summary != summary {
		t.Errorf("Summary = %v, want %q", ver.Summary, summary)
	}
}

// TestManager_WriteVersion_NotFound verifies that updating a
// non-existent artifact returns ErrArtifactNotFound.
func TestManager_WriteVersion_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)

	_, err := mgr.WriteVersion(context.Background(), "ghost", []byte("hi"), "", nil, nil)
	if !errors.Is(err, artifacts.ErrArtifactNotFound) {
		t.Errorf("got %v, want ErrArtifactNotFound", err)
	}
}

// TestManager_SetCaptureConfig roundtrips the live config.
func TestManager_SetCaptureConfig(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := artifacts.NewMemoryStore()
	mgr := artifacts.NewManager(store, media)
	def := mgr.Config()
	if def.CodeBlockMinLines != 10 || def.CodeBlockMinBytes != 200 {
		t.Errorf("default cfg = %+v", def)
	}
	mgr.SetCaptureConfig(artifacts.CaptureConfig{CodeBlockMinLines: 5})
	if got := mgr.Config().CodeBlockMinLines; got != 5 {
		t.Errorf("after Set: MinLines = %d, want 5", got)
	}
}
