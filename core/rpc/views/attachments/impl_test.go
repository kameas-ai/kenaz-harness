package attachments_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	coreatt "github.com/sigil-tech/kaneaz-harness/core/attachments"
	attview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
)

func newAPI(t *testing.T, reader attview.SessionProjectReader) *attview.API {
	t.Helper()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore())
	return attview.New(mgr, reader)
}

func mustAdd(t *testing.T, api *attview.API, in attview.AddInput) attview.Attachment {
	t.Helper()
	a, err := api.Add(context.Background(), in)
	if err != nil {
		t.Fatalf("Add(%+v): %v", in, err)
	}
	return a
}

func TestAPI_AddListRemove(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	a := mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:abc",
		Content:       "snapshot",
	})
	if a.ID == "" {
		t.Errorf("Add returned empty id")
	}
	if a.CreatedAt == "" {
		t.Errorf("Add returned empty CreatedAt")
	}

	got, err := api.List(ctx, coreatt.ScopeKindSession, "s1")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].Content != "snapshot" {
		t.Errorf("List = %+v", got)
	}

	if err := api.Remove(ctx, a.ID); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	got, err = api.List(ctx, coreatt.ScopeKindSession, "s1")
	if err != nil {
		t.Fatalf("List after Remove: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("List after Remove = %+v", got)
	}
}

func TestAPI_AddRejectsInvalidScope(t *testing.T) {
	t.Parallel()
	api := newAPI(t, nil)
	_, err := api.Add(context.Background(), attview.AddInput{
		ScopeKind:     "garbage",
		ContentSource: "inline:x",
	})
	if !errors.Is(err, coreatt.ErrInvalidScope) {
		t.Errorf("got %v, want ErrInvalidScope", err)
	}
}

type stubReader struct {
	m map[string]string
}

func (s *stubReader) ProjectID(_ context.Context, sessionID string) (*string, error) {
	v, ok := s.m[sessionID]
	if !ok || v == "" {
		return nil, nil
	}
	return &v, nil
}

func TestAPI_ListResolved_GlobalProjectSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, &stubReader{m: map[string]string{"s1": "p1"}})

	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindGlobal,
		ContentSource: "inline:g",
		Content:       "global",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "p1",
		ContentSource: "inline:p",
		Content:       "project",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:s",
		Content:       "session",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "other",
		ContentSource: "inline:other",
		Content:       "other-project",
	})

	got, err := api.ListResolved(ctx, "s1")
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	want := []string{"global", "project", "session"}
	if len(got) != len(want) {
		t.Fatalf("ListResolved = %+v", got)
	}
	for i, w := range want {
		if got[i].Content != w {
			t.Errorf("got[%d].Content = %q, want %q", i, got[i].Content, w)
		}
	}
}

func TestAPI_ListResolved_NilReaderSkipsProject(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindGlobal,
		ContentSource: "inline:g",
		Content:       "global",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindProject,
		ScopeID:       "p1",
		ContentSource: "inline:p",
		Content:       "project",
	})
	mustAdd(t, api, attview.AddInput{
		ScopeKind:     coreatt.ScopeKindSession,
		ScopeID:       "s1",
		ContentSource: "inline:s",
		Content:       "session",
	})

	got, err := api.ListResolved(ctx, "s1")
	if err != nil {
		t.Fatalf("ListResolved: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListResolved (no reader) = %+v", got)
	}
	if got[0].Content != "global" || got[1].Content != "session" {
		t.Errorf("order: %+v", got)
	}
}

func TestAPI_Reorder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newAPI(t, nil)

	a := mustAdd(t, api, attview.AddInput{
		ScopeKind: coreatt.ScopeKindGlobal, ContentSource: "inline:a", Content: "a", Position: 0,
	})
	b := mustAdd(t, api, attview.AddInput{
		ScopeKind: coreatt.ScopeKindGlobal, ContentSource: "inline:b", Content: "b", Position: 1,
	})
	if err := api.Reorder(ctx, coreatt.ScopeKindGlobal, "", []string{b.ID, a.ID}); err != nil {
		t.Fatalf("Reorder: %v", err)
	}
	got, _ := api.List(ctx, coreatt.ScopeKindGlobal, "")
	if got[0].Content != "b" || got[1].Content != "a" {
		t.Errorf("after Reorder = %+v", got)
	}
}

type fakeLib struct {
	store map[string]string
}

func (f *fakeLib) Get(_ context.Context, p string) (string, error) {
	v, ok := f.store[p]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}

func TestAPI_Refresh_LibrarySource(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	lib := &fakeLib{store: map[string]string{"foo.md": "fresh"}}
	mgr := coreatt.NewManager(coreatt.NewMemoryStore(), coreatt.WithLibrary(lib))
	api := attview.New(mgr, nil)
	stored, err := api.Add(ctx, attview.AddInput{
		ScopeKind: coreatt.ScopeKindSession, ScopeID: "s1",
		ContentSource: "library:foo.md", Content: "stale",
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	out, err := api.Refresh(ctx, stored.ID)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if out.Content != "fresh" {
		t.Errorf("Refresh content = %q, want fresh", out.Content)
	}
}

func TestAPI_NilManagerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic")
		}
	}()
	_ = attview.New(nil, nil)
}

// fakeMediaStore is a minimal in-memory MediaStore satisfying the
// coreatt.MediaStore contract. Only the methods Manager.AddMedia and
// Manager.Remove invoke are exercised; the rest return safe defaults.
type fakeMediaStore struct {
	mu    sync.Mutex
	rows  map[string]coreatt.MediaArtifact
	bytes map[string][]byte
}

func newFakeMediaStore() *fakeMediaStore {
	return &fakeMediaStore{
		rows:  map[string]coreatt.MediaArtifact{},
		bytes: map[string][]byte{},
	}
}

func (m *fakeMediaStore) Put(_ context.Context, body []byte, mediaType, originalName string) (coreatt.MediaArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	hash := sha256.Sum256(body)
	hashHex := hex.EncodeToString(hash[:])
	id := hashHex[:26]
	art := coreatt.MediaArtifact{
		ID:           id,
		ContentHash:  hashHex,
		MediaType:    mediaType,
		ByteSize:     int64(len(body)),
		OriginalName: originalName,
		CreatedAt:    time.Now().UTC(),
	}
	m.rows[id] = art
	m.bytes[hashHex] = append([]byte(nil), body...)
	return art, nil
}

func (m *fakeMediaStore) Get(_ context.Context, id string) (coreatt.MediaArtifact, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	art, ok := m.rows[id]
	if !ok {
		return coreatt.MediaArtifact{}, nil, coreatt.ErrMediaNotFound
	}
	return art, m.bytes[art.ContentHash], nil
}

func (m *fakeMediaStore) GetByHash(_ context.Context, contentHash string) (coreatt.MediaArtifact, []byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, art := range m.rows {
		if art.ContentHash == contentHash {
			return art, m.bytes[contentHash], nil
		}
	}
	return coreatt.MediaArtifact{}, nil, coreatt.ErrMediaNotFound
}

func (m *fakeMediaStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id)
	return nil
}

func (m *fakeMediaStore) List(_ context.Context, _ coreatt.MediaFilter) ([]coreatt.MediaArtifact, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]coreatt.MediaArtifact, 0, len(m.rows))
	for _, a := range m.rows {
		out = append(out, a)
	}
	return out, nil
}

func (m *fakeMediaStore) RefcountFor(_ context.Context, _ string) (int, error) {
	return 0, nil
}

func (m *fakeMediaStore) PruneOrphans(_ context.Context) (int, error) {
	return 0, nil
}

func (m *fakeMediaStore) RegisterRefcountSource(_ coreatt.RefcountSource) {}

// TestAPI_AddMedia_RoundTrip pins the multimodal-io WP03 base64
// upload path: a valid base64 payload decodes, the manager mints a
// media-backed Attachment, and the rpc-wire shape carries MediaID.
func TestAPI_AddMedia_RoundTrip(t *testing.T) {
	t.Parallel()
	media := newFakeMediaStore()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore(), coreatt.WithMediaStore(media))
	api := attview.New(mgr, nil)

	body := []byte("png-bytes")
	in := attview.AddMediaInput{
		ScopeKind:        coreatt.ScopeKindSession,
		ScopeID:          "s1",
		MediaBytesBase64: base64.StdEncoding.EncodeToString(body),
		MediaType:        "image/png",
		OriginalName:     "shot.png",
	}
	att, err := api.AddMedia(context.Background(), in)
	if err != nil {
		t.Fatalf("AddMedia: %v", err)
	}
	if att.MediaID == "" {
		t.Errorf("MediaID empty on returned wire shape: %+v", att)
	}
	if !strings.HasPrefix(att.ContentSource, "media:") {
		t.Errorf("ContentSource = %q, want prefix media:", att.ContentSource)
	}
	if att.Kind != coreatt.KindUser {
		t.Errorf("Kind = %q, want %q", att.Kind, coreatt.KindUser)
	}
}

// TestAPI_AddMedia_OversizeReject ensures payloads beyond
// MaxMediaBytes fail closed before the manager is touched.
func TestAPI_AddMedia_OversizeReject(t *testing.T) {
	t.Parallel()
	media := newFakeMediaStore()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore(), coreatt.WithMediaStore(media))
	api := attview.New(mgr, nil)

	tooBig := make([]byte, attview.MaxMediaBytes+1)
	in := attview.AddMediaInput{
		ScopeKind:        coreatt.ScopeKindSession,
		ScopeID:          "s1",
		MediaBytesBase64: base64.StdEncoding.EncodeToString(tooBig),
		MediaType:        "image/png",
	}
	_, err := api.AddMedia(context.Background(), in)
	if err == nil {
		t.Fatal("expected oversize rejection")
	}
	if !errors.Is(err, coreatt.ErrOversize) {
		t.Errorf("got %v, want ErrOversize", err)
	}
	if len(media.rows) != 0 {
		t.Errorf("media store touched on oversize reject: %d rows", len(media.rows))
	}
}

// TestAPI_AddMedia_InvalidBase64Reject ensures malformed base64
// payloads fail closed.
func TestAPI_AddMedia_InvalidBase64Reject(t *testing.T) {
	t.Parallel()
	media := newFakeMediaStore()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore(), coreatt.WithMediaStore(media))
	api := attview.New(mgr, nil)

	in := attview.AddMediaInput{
		ScopeKind:        coreatt.ScopeKindSession,
		ScopeID:          "s1",
		MediaBytesBase64: "!!!not-base64!!!",
		MediaType:        "image/png",
	}
	_, err := api.AddMedia(context.Background(), in)
	if err == nil {
		t.Fatal("expected invalid-base64 rejection")
	}
	if !errors.Is(err, attview.ErrInvalidMediaBytes) {
		t.Errorf("got %v, want ErrInvalidMediaBytes", err)
	}
	if len(media.rows) != 0 {
		t.Errorf("media store touched on bad base64: %d rows", len(media.rows))
	}
}

// TestAPI_AddMedia_NoMediaStore verifies a manager built without
// WithMediaStore surfaces ErrMediaStoreUnavailable through AddMedia.
func TestAPI_AddMedia_NoMediaStore(t *testing.T) {
	t.Parallel()
	mgr := coreatt.NewManager(coreatt.NewMemoryStore())
	api := attview.New(mgr, nil)

	in := attview.AddMediaInput{
		ScopeKind:        coreatt.ScopeKindSession,
		ScopeID:          "s1",
		MediaBytesBase64: base64.StdEncoding.EncodeToString([]byte("x")),
		MediaType:        "image/png",
	}
	_, err := api.AddMedia(context.Background(), in)
	if !errors.Is(err, coreatt.ErrMediaStoreUnavailable) {
		t.Errorf("got %v, want ErrMediaStoreUnavailable", err)
	}
}
