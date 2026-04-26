package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	corememory "github.com/sigil-tech/kaneaz-harness/core/memory"
)

type fakeReader struct {
	msgs map[string][]Message
	pid  map[string]string
}

func (f *fakeReader) ListMessages(_ context.Context, sessionID string) ([]Message, error) {
	return f.msgs[sessionID], nil
}

func (f *fakeReader) ProjectIDForSession(_ context.Context, sessionID string) (string, error) {
	if f.pid == nil {
		return "", nil
	}
	return f.pid[sessionID], nil
}

type fakeEmbedder struct{ dim int }

func (f *fakeEmbedder) Kind() string    { return "fake" }
func (f *fakeEmbedder) Dimensions() int { return f.dim }
func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0}
	}
	return out, nil
}

func newAPIWithStore(t *testing.T, reader MessageReader) (*API, corememory.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := corememory.NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	api := New(Config{
		Store:    store,
		Embedder: &fakeEmbedder{dim: 2},
		Reader:   reader,
	})
	return api, store
}

func TestRememberMessage_DefaultsToSessionScope(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {{ID: "m1", Role: "user", Content: "remember me"}},
	}}
	api, _ := newAPIWithStore(t, reader)
	id, err := api.RememberMessage(context.Background(), "sess-1", "m1", "")
	if err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}
	chunks, _ := api.ListChunks(context.Background(), ListFilter{})
	if len(chunks) != 1 {
		t.Fatalf("len = %d", len(chunks))
	}
	if chunks[0].ID != id {
		t.Fatalf("id mismatch: %q vs %q", chunks[0].ID, id)
	}
	if chunks[0].ScopeKind != corememory.ScopeKindSession {
		t.Errorf("ScopeKind = %q, want session", chunks[0].ScopeKind)
	}
	if chunks[0].ScopeID != "sess-1" {
		t.Errorf("ScopeID = %q, want sess-1", chunks[0].ScopeID)
	}
}

func TestRememberMessage_GlobalScope(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {{ID: "m1", Role: "assistant", Content: "global memory"}},
	}}
	api, _ := newAPIWithStore(t, reader)
	if _, err := api.RememberMessage(context.Background(), "sess-1", "m1", "global"); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}
	chunks, _ := api.ListChunks(context.Background(), ListFilter{ScopeKind: corememory.ScopeKindGlobal})
	if len(chunks) != 1 {
		t.Fatalf("len = %d", len(chunks))
	}
	if chunks[0].ScopeID != "" {
		t.Errorf("global scope id = %q, want empty", chunks[0].ScopeID)
	}
}

func TestRememberMessage_ProjectScope_FallsBackWhenNoProject(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {{ID: "m1", Role: "user", Content: "project memory"}},
	}}
	api, _ := newAPIWithStore(t, reader)
	if _, err := api.RememberMessage(context.Background(), "sess-1", "m1", "project"); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}
	chunks, _ := api.ListChunks(context.Background(), ListFilter{})
	if len(chunks) != 1 {
		t.Fatalf("len = %d", len(chunks))
	}
	if chunks[0].ScopeKind != corememory.ScopeKindSession {
		t.Errorf("ScopeKind = %q, want session (fallback)", chunks[0].ScopeKind)
	}
}

func TestRememberMessage_ProjectScope_UsesResolverWhenAvailable(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{
		msgs: map[string][]Message{
			"sess-1": {{ID: "m1", Role: "user", Content: "project memory"}},
		},
		pid: map[string]string{"sess-1": "proj-A"},
	}
	api, _ := newAPIWithStore(t, reader)
	if _, err := api.RememberMessage(context.Background(), "sess-1", "m1", "project"); err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}
	chunks, _ := api.ListChunks(context.Background(), ListFilter{})
	if len(chunks) != 1 {
		t.Fatalf("len = %d", len(chunks))
	}
	if chunks[0].ScopeKind != corememory.ScopeKindProject {
		t.Errorf("ScopeKind = %q, want project", chunks[0].ScopeKind)
	}
	if chunks[0].ScopeID != "proj-A" {
		t.Errorf("ScopeID = %q, want proj-A", chunks[0].ScopeID)
	}
	if chunks[0].ProjectID != "proj-A" {
		t.Errorf("ProjectID = %q, want proj-A", chunks[0].ProjectID)
	}
}

func TestRememberMessage_RejectsInvalidScope(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {{ID: "m1", Role: "user", Content: "x"}},
	}}
	api, _ := newAPIWithStore(t, reader)
	if _, err := api.RememberMessage(context.Background(), "sess-1", "m1", "world"); err == nil {
		t.Fatal("expected error for invalid scope")
	}
}

func TestPromoteScope_MoveSemantics(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {{ID: "m1", Role: "user", Content: "promote me"}},
	}}
	api, _ := newAPIWithStore(t, reader)
	ctx := context.Background()
	originalID, err := api.RememberMessage(ctx, "sess-1", "m1", "session")
	if err != nil {
		t.Fatalf("RememberMessage: %v", err)
	}
	newID, err := api.PromoteScope(ctx, originalID, "project", "proj-A")
	if err != nil {
		t.Fatalf("PromoteScope: %v", err)
	}
	if newID == originalID {
		t.Fatal("PromoteScope returned the same id; expected a fresh id")
	}
	chunks, _ := api.ListChunks(ctx, ListFilter{})
	if len(chunks) != 1 {
		t.Fatalf("len after promote = %d, want 1", len(chunks))
	}
	got := chunks[0]
	if got.ID != newID {
		t.Errorf("listed ID = %q, want %q", got.ID, newID)
	}
	if got.ScopeKind != corememory.ScopeKindProject || got.ScopeID != "proj-A" {
		t.Errorf("scope = (%s, %s)", got.ScopeKind, got.ScopeID)
	}
	// Verify the original is gone from the store.
	if err := api.Forget(ctx, originalID); err == nil {
		t.Error("original id still present after promote (should have been deleted)")
	}
}

func TestListChunks_FilterByScopeKind(t *testing.T) {
	t.Parallel()
	reader := &fakeReader{msgs: map[string][]Message{
		"sess-1": {
			{ID: "m1", Role: "user", Content: "session-only"},
			{ID: "m2", Role: "user", Content: "global-only"},
		},
	}}
	api, _ := newAPIWithStore(t, reader)
	ctx := context.Background()
	if _, err := api.RememberMessage(ctx, "sess-1", "m1", "session"); err != nil {
		t.Fatalf("session pin: %v", err)
	}
	// Wait one tick to dodge ContentHash dedup safety; different content
	// here means it won't trigger dedup, but be explicit anyway.
	time.Sleep(time.Millisecond)
	if _, err := api.RememberMessage(ctx, "sess-1", "m2", "global"); err != nil {
		t.Fatalf("global pin: %v", err)
	}
	all, _ := api.ListChunks(ctx, ListFilter{})
	if len(all) != 2 {
		t.Fatalf("all len = %d", len(all))
	}
	sessOnly, _ := api.ListChunks(ctx, ListFilter{ScopeKind: "session", ScopeID: "sess-1"})
	if len(sessOnly) != 1 || sessOnly[0].Content != "session-only" {
		t.Errorf("session filter wrong: %+v", sessOnly)
	}
	globalOnly, _ := api.ListChunks(ctx, ListFilter{ScopeKind: "global"})
	if len(globalOnly) != 1 || globalOnly[0].Content != "global-only" {
		t.Errorf("global filter wrong: %+v", globalOnly)
	}
}
