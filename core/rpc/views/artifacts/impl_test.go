package artifacts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	coreart "github.com/kameas-ai/kenaz-harness/core/artifacts"
	"github.com/kameas-ai/kenaz-harness/core/attachments"
)

// newTestAPI constructs an API wired against in-memory stores rooted
// at a tempdir. The session-project map drives the scope-promote
// validation: sess-with-proj → "proj-1", every other session has no
// project.
func newTestAPI(t *testing.T) (*API, string, *fakeMessageReader) {
	t.Helper()
	dir := t.TempDir()
	media := attachments.NewMemoryMediaStore(dir)
	store := coreart.NewMemoryStore(coreart.WithMemSessionProjectReader(
		coreart.NewStaticSessionProjectReader(map[string]string{
			"sess-with-proj":  "proj-1",
			"sister-with-proj": "proj-1",
		}),
	))
	media.RegisterRefcountSource(coreart.ArtifactsRefcountSource{Store: store})
	mgr := coreart.NewManager(store, &mediaPutShim{inner: media},
		coreart.WithSessionReader(coreart.NewStaticSessionProjectReader(map[string]string{
			"sess-with-proj":  "proj-1",
			"sister-with-proj": "proj-1",
		})),
	)
	msgs := &fakeMessageReader{
		store: map[string]Message{
			"sess-1/m-1": {ID: "m-1", SessionID: "sess-1", Role: "assistant",
				Content: "Here is some text the user may want to pin."},
		},
	}
	api := New(Config{
		Store:    store,
		Manager:  mgr,
		Media:    media,
		Messages: msgs,
		DataDir:  dir,
	})
	return api, dir, msgs
}

// mediaPutShim narrows attachments.MediaStore onto coreart.MediaStorer.
type mediaPutShim struct {
	inner attachments.MediaStore
}

func (a *mediaPutShim) Put(ctx context.Context, b []byte, mediaType, originalName string) (attachments.MediaArtifact, error) {
	return a.inner.Put(ctx, b, mediaType, originalName)
}

type fakeMessageReader struct {
	store map[string]Message
}

func (r *fakeMessageReader) GetMessage(_ context.Context, sessionID, messageID string) (Message, error) {
	if m, ok := r.store[sessionID+"/"+messageID]; ok {
		return m, nil
	}
	return Message{}, errors.New("not found")
}

// TestAPI_GetRoundTrip — capture an artifact, then resolve via Get
// and verify metadata + bytes match.
func TestAPI_GetRoundTrip(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestAPI(t)
	ctx := context.Background()
	row, err := api.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title:    "x.txt",
		MimeType: "text/plain",
		Bytes:    []byte("hello world"),
		Source:   coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, "sess-1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if len(row) != 1 {
		t.Fatalf("Capture rows = %d", len(row))
	}
	got, err := api.Get(ctx, row[0].ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Artifact.ID != row[0].ID {
		t.Errorf("Get id mismatch: %q vs %q", got.Artifact.ID, row[0].ID)
	}
	if string(got.Bytes) != "hello world" {
		t.Errorf("bytes = %q", got.Bytes)
	}
}

// TestAPI_DeleteSweepsCASOnZeroRefcount — Delete drops the row AND
// reclaims the file when no other reference remains.
func TestAPI_DeleteSweepsCASOnZeroRefcount(t *testing.T) {
	t.Parallel()
	api, dir, _ := newTestAPI(t)
	ctx := context.Background()
	captured, err := api.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "alone.txt", MimeType: "text/plain",
		Bytes: []byte("solitary"), Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, "sess-1")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	contentHash := captured[0].ContentHash
	path := filepath.Join(dir, "media", contentHash)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("CAS file missing pre-delete: %v", err)
	}
	if err := api.Delete(ctx, captured[0].ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("CAS file should be reclaimed, err=%v", err)
	}
}

// TestAPI_DeleteSkipsCASWhenSharedHash — when another row references
// the same content hash, the file survives.
func TestAPI_DeleteSkipsCASWhenSharedHash(t *testing.T) {
	t.Parallel()
	api, dir, _ := newTestAPI(t)
	ctx := context.Background()
	body := []byte("dup-payload")
	cap1, err := api.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "a.txt", MimeType: "text/plain", Bytes: body,
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, "sess-1")
	if err != nil {
		t.Fatalf("Capture 1: %v", err)
	}
	cap2, err := api.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "b.txt", MimeType: "text/plain", Bytes: body,
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, "sess-1")
	if err != nil {
		t.Fatalf("Capture 2: %v", err)
	}
	if cap1[0].ContentHash != cap2[0].ContentHash {
		t.Fatalf("hashes diverged: %q vs %q", cap1[0].ContentHash, cap2[0].ContentHash)
	}
	path := filepath.Join(dir, "media", cap1[0].ContentHash)
	if err := api.Delete(ctx, cap1[0].ID); err != nil {
		t.Fatalf("Delete 1: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("CAS file removed despite refcount > 0: %v", err)
	}
}

// TestAPI_PromoteMovesScope — Promote a session-scope artifact in a
// project-bearing session lifts it to project scope; sister sessions
// in the same project see it via a project filter.
func TestAPI_PromoteMovesScope(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestAPI(t)
	ctx := context.Background()
	captured, err := api.mgr.Capture(ctx, []coreart.CaptureCandidate{{
		Title: "p.txt", MimeType: "text/plain", Bytes: []byte("promote me"),
		Source: coreart.SourceUserPin,
		SourceRef: coreart.ArtifactSourceRef{MessageID: "m"},
	}}, "sess-with-proj")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	updated, err := api.Promote(ctx, captured[0].ID, "project", "proj-1")
	if err != nil {
		t.Fatalf("Promote: %v", err)
	}
	if updated.ScopeKind != "project" || updated.ProjectID != "proj-1" {
		t.Errorf("post-promote = %+v", updated)
	}
	// Sister-session listing via project filter sees the artifact.
	listed, err := api.List(ctx, ArtifactFilter{ProjectID: "proj-1"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, a := range listed {
		if a.ID == captured[0].ID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("promoted artifact not in project list: %+v", listed)
	}
}

// TestAPI_SaveFromMessage — manual user-pin produces a user_pin
// artifact carrying the message text.
func TestAPI_SaveFromMessage(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestAPI(t)
	ctx := context.Background()
	got, err := api.SaveFromMessage(ctx, "sess-1", "m-1", "my pin", 0, 0)
	if err != nil {
		t.Fatalf("SaveFromMessage: %v", err)
	}
	if got.Source != "user_pin" {
		t.Errorf("Source = %q, want user_pin", got.Source)
	}
	if got.Title != "my pin" {
		t.Errorf("Title = %q", got.Title)
	}
	if got.MimeType != "text/markdown" {
		t.Errorf("MimeType = %q", got.MimeType)
	}
}

// TestAPI_SaveFromMessage_Range — non-zero range pulls the substring.
func TestAPI_SaveFromMessage_Range(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestAPI(t)
	ctx := context.Background()
	// "Here is some text the user may want to pin."
	//  012345678901234567890
	got, err := api.SaveFromMessage(ctx, "sess-1", "m-1", "slice", 8, 17)
	if err != nil {
		t.Fatalf("SaveFromMessage: %v", err)
	}
	// Read it back to verify the captured slice.
	full, err := api.Get(ctx, got.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(full.Bytes) != "some text" {
		t.Errorf("Bytes = %q, want \"some text\"", full.Bytes)
	}
}

// TestAPI_SaveFromMessage_InvalidRange — out-of-bounds range yields
// ErrInvalidRange.
func TestAPI_SaveFromMessage_InvalidRange(t *testing.T) {
	t.Parallel()
	api, _, _ := newTestAPI(t)
	ctx := context.Background()
	_, err := api.SaveFromMessage(ctx, "sess-1", "m-1", "x", 5, 1)
	if !errors.Is(err, ErrInvalidRange) {
		t.Errorf("err = %v, want ErrInvalidRange", err)
	}
}
