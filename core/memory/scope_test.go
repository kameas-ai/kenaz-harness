package memory

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fixedClock returns a now() func that always reports t. Used to drive
// the store's dedup window deterministically.
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// newScopedStore opens a fresh chromemStore at a tempdir path with a
// fixed clock so the dedup-window assertions stay deterministic.
func newScopedStore(t *testing.T, now time.Time) *chromemStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewChromemStore(filepath.Join(dir, "memory.gob"))
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	cs, ok := s.(*chromemStore)
	if !ok {
		t.Fatalf("store type assertion failed")
	}
	cs.now = fixedClock(now)
	return cs
}

func TestStore_DedupWithinWindow_RejectsDuplicate(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s := newScopedStore(t, now)
	ctx := context.Background()
	first := Chunk{
		ID: "a", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "user prefers vim", Embedding: []float32{1, 0},
		CreatedAt: now.Add(-10 * time.Second),
	}
	if err := s.Add(ctx, first); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	dup := Chunk{
		ID: "b", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "user prefers vim", Embedding: []float32{1, 0},
		CreatedAt: now,
	}
	err := s.Add(ctx, dup)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("Add dup err = %v, want ErrDuplicate", err)
	}
	listed, _ := s.List(ctx)
	if len(listed) != 1 {
		t.Fatalf("dup leaked into store: %+v", listed)
	}
}

func TestStore_DedupOutsideWindow_AllowsRepin(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s := newScopedStore(t, now)
	ctx := context.Background()
	old := Chunk{
		ID: "a", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "user prefers vim", Embedding: []float32{1, 0},
		CreatedAt: now.Add(-time.Hour),
	}
	if err := s.Add(ctx, old); err != nil {
		t.Fatalf("first Add: %v", err)
	}
	repin := Chunk{
		ID: "b", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "user prefers vim", Embedding: []float32{1, 0},
		CreatedAt: now,
	}
	if err := s.Add(ctx, repin); err != nil {
		t.Fatalf("repin Add: %v", err)
	}
	listed, _ := s.List(ctx)
	if len(listed) != 2 {
		t.Fatalf("repin not stored: len=%d", len(listed))
	}
}

func TestStore_DedupAcrossScopes_AllowedSameContent(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	s := newScopedStore(t, now)
	ctx := context.Background()
	sess := Chunk{
		ID: "a", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "shared content", Embedding: []float32{1, 0}, CreatedAt: now,
	}
	if err := s.Add(ctx, sess); err != nil {
		t.Fatalf("session Add: %v", err)
	}
	glob := Chunk{
		ID: "b", ScopeKind: ScopeKindGlobal, ScopeID: "",
		Content: "shared content", Embedding: []float32{1, 0}, CreatedAt: now,
	}
	if err := s.Add(ctx, glob); err != nil {
		t.Fatalf("global Add (different scope): %v", err)
	}
}

func TestStore_AddPopulatesContentHash(t *testing.T) {
	t.Parallel()
	s := newScopedStore(t, time.Now())
	ctx := context.Background()
	c := Chunk{
		ID: "a", SessionID: "s1", ScopeKind: ScopeKindSession, ScopeID: "s1",
		Content: "hash me", Embedding: []float32{1, 0}, CreatedAt: time.Now(),
	}
	if err := s.Add(ctx, c); err != nil {
		t.Fatalf("Add: %v", err)
	}
	stored, _ := s.List(ctx)
	if stored[0].ContentHash == "" {
		t.Fatal("ContentHash not populated")
	}
	if stored[0].ContentHash != HashContent("hash me") {
		t.Fatalf("ContentHash mismatch: %s", stored[0].ContentHash)
	}
}

// legacyChunk mirrors the pre-WP06 Chunk shape exactly. Encoded with
// the canonical gob name "Chunk" so it lines up with the current
// decoder; the missing fields exercise the backfill.
type legacyChunk struct {
	ID         string
	SessionID  string
	SourceTurn string
	Content    string
	Embedding  []float32
	CreatedAt  time.Time
}

func TestStore_Load_BackfillsLegacyGob(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.gob")

	gob.Register(legacyChunk{})
	legacy := []legacyChunk{
		{
			ID: "old1", SessionID: "sess-x", SourceTurn: "user",
			Content: "stored before WP06", Embedding: []float32{0.5, 0.5},
			CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		},
	}
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	// Encode the struct under the wire-name "Chunk" so the loader's
	// gob.NewDecoder().Decode(&[]Chunk{}) accepts it. We do this by
	// encoding into a typed slice that has the struct fields the new
	// Chunk lacks (so missing fields stay zero and trigger backfill).
	type wireChunk = legacyChunk
	if err := enc.Encode([]wireChunk(legacy)); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s, err := NewChromemStore(path)
	if err != nil {
		t.Fatalf("NewChromemStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	listed, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("len = %d, want 1", len(listed))
	}
	got := listed[0]
	if got.ScopeKind != ScopeKindSession {
		t.Fatalf("ScopeKind = %q, want session", got.ScopeKind)
	}
	if got.ScopeID != "sess-x" {
		t.Fatalf("ScopeID = %q, want sess-x", got.ScopeID)
	}
	if got.ContentHash == "" {
		t.Fatal("ContentHash not backfilled")
	}
	if got.ContentHash != HashContent("stored before WP06") {
		t.Fatalf("ContentHash mismatch: %s", got.ContentHash)
	}
}

func TestRetriever_ScopedUnion_GlobalProjectSession(t *testing.T) {
	t.Parallel()
	s := newScopedStore(t, time.Now())
	ctx := context.Background()
	now := time.Now().UTC()

	// Build chunks: each axis-aligned vector represents a different
	// scope, so the retriever's filtering — not the similarity score —
	// drives which ones survive.
	add := func(id, content string, vec []float32, kind, scopeID string) {
		t.Helper()
		if err := s.Add(ctx, Chunk{
			ID: id, SessionID: scopeID, ScopeKind: kind, ScopeID: scopeID,
			Content: content, Embedding: vec, CreatedAt: now,
		}); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	add("g1", "global pin", []float32{1, 0, 0, 0, 0}, ScopeKindGlobal, "")
	add("p1", "project pin", []float32{1, 0, 0, 0, 0}, ScopeKindProject, "proj-A")
	add("s1", "session pin", []float32{1, 0, 0, 0, 0}, ScopeKindSession, "sess-1")
	add("p2", "other project pin", []float32{1, 0, 0, 0, 0}, ScopeKindProject, "proj-B")
	add("s2", "sister session pin", []float32{1, 0, 0, 0, 0}, ScopeKindSession, "sess-2")

	emb := &fakeEmbedder{
		table: map[string][]float32{"q": {1, 0, 0, 0, 0}},
		dims:  5,
	}
	r := NewRetriever(s, emb, func() bool { return true }, 0.5)
	hits, err := r.RetrieveScoped(ctx, "q", "sess-1", "proj-A", 10)
	if err != nil {
		t.Fatalf("RetrieveScoped: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Content] = true
	}
	if !got["global pin"] {
		t.Error("global pin missing from union")
	}
	if !got["project pin"] {
		t.Error("project pin missing from union")
	}
	if !got["session pin"] {
		t.Error("session pin missing from union")
	}
	if got["other project pin"] {
		t.Error("cross-project pin leaked into result")
	}
	if got["sister session pin"] {
		t.Error("sister session pin leaked into result")
	}
}

func TestRetriever_ScopedUnion_NoProject_StillReturnsGlobalAndSession(t *testing.T) {
	t.Parallel()
	s := newScopedStore(t, time.Now())
	ctx := context.Background()
	now := time.Now().UTC()
	add := func(id, content string, vec []float32, kind, scopeID string) {
		t.Helper()
		if err := s.Add(ctx, Chunk{
			ID: id, SessionID: scopeID, ScopeKind: kind, ScopeID: scopeID,
			Content: content, Embedding: vec, CreatedAt: now,
		}); err != nil {
			t.Fatalf("Add %s: %v", id, err)
		}
	}
	add("g1", "global pin", []float32{1, 0}, ScopeKindGlobal, "")
	add("s1", "session pin", []float32{1, 0}, ScopeKindSession, "sess-1")
	add("p1", "project pin (irrelevant)", []float32{1, 0}, ScopeKindProject, "proj-A")

	emb := &fakeEmbedder{table: map[string][]float32{"q": {1, 0}}, dims: 2}
	r := NewRetriever(s, emb, func() bool { return true }, 0.5)
	hits, err := r.RetrieveScoped(ctx, "q", "sess-1", "", 10)
	if err != nil {
		t.Fatalf("RetrieveScoped: %v", err)
	}
	got := map[string]bool{}
	for _, h := range hits {
		got[h.Content] = true
	}
	if !got["global pin"] || !got["session pin"] {
		t.Errorf("expected global+session, got %v", got)
	}
	if got["project pin (irrelevant)"] {
		t.Error("project pin leaked when projectID is empty")
	}
}

func TestStore_PromoteScope_MoveSemantics(t *testing.T) {
	t.Parallel()
	s := newScopedStore(t, time.Now())
	ctx := context.Background()
	now := time.Now().UTC()
	original := Chunk{
		ID: "orig", SessionID: "sess-1", ScopeKind: ScopeKindSession, ScopeID: "sess-1",
		Content: "promote me", Embedding: []float32{1, 0}, CreatedAt: now,
	}
	if err := s.Add(ctx, original); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := s.PromoteScope(ctx, "orig", "promoted", ScopeKindProject, "proj-A"); err != nil {
		t.Fatalf("PromoteScope: %v", err)
	}
	listed, _ := s.List(ctx)
	if len(listed) != 1 {
		t.Fatalf("len after promote = %d, want 1", len(listed))
	}
	got := listed[0]
	if got.ID != "promoted" {
		t.Errorf("ID after promote = %q, want promoted", got.ID)
	}
	if got.ScopeKind != ScopeKindProject || got.ScopeID != "proj-A" {
		t.Errorf("scope = (%s, %s), want (project, proj-A)", got.ScopeKind, got.ScopeID)
	}
	if got.ProjectID != "proj-A" {
		t.Errorf("ProjectID not synced: %q", got.ProjectID)
	}
	if got.Content != "promote me" {
		t.Errorf("content lost: %q", got.Content)
	}
	// Original ID must be gone.
	if err := s.Delete(ctx, "orig"); err == nil {
		t.Error("original ID still resolvable after promote")
	}
}

func TestStore_PromoteScope_MissingChunk(t *testing.T) {
	t.Parallel()
	s := newScopedStore(t, time.Now())
	err := s.PromoteScope(context.Background(), "missing", "new", ScopeKindGlobal, "")
	if err == nil {
		t.Fatal("expected error for missing chunk")
	}
}
