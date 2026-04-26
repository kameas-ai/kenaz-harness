package hooks

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/memory"
)

type fakeRetriever struct {
	calls    int
	snippets []memory.Snippet
	err      error
	lastQ    string
	lastK    int
}

func (f *fakeRetriever) Retrieve(_ context.Context, q string, k int) ([]memory.Snippet, error) {
	f.calls++
	f.lastQ = q
	f.lastK = k
	return f.snippets, f.err
}

type fakeStore struct {
	mu     sync.Mutex
	chunks []memory.Chunk
	err    error
}

func (f *fakeStore) Add(_ context.Context, c memory.Chunk) error {
	if f.err != nil {
		return f.err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.chunks = append(f.chunks, c)
	return nil
}

type fakeEmbedder struct {
	called int
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.called++
	out := make([][]float32, len(texts))
	for i := range texts {
		out[i] = []float32{1, 0, 0}
	}
	return out, nil
}

func TestMemoryRetrieve_PrependsSystemMessage(t *testing.T) {
	t.Parallel()
	retr := &fakeRetriever{snippets: []memory.Snippet{
		{Content: "user prefers tabs", Similarity: 0.92},
		{Content: "user lives in NYC", Similarity: 0.81},
	}}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Retriever: retr})

	ev := PreSendEvent{
		SessionID: "s",
		UserText:  "what do I like?",
		Messages:  []HookMessage{{Role: "user", Content: "what do I like?"}},
	}
	fn := reg.preSend[BuiltinMemoryRetrieve]
	out, err := fn(context.Background(), ev, map[string]any{
		"top_k":         5,
		"system_prefix": "Memories:",
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if retr.calls != 1 || retr.lastK != 5 || retr.lastQ != "what do I like?" {
		t.Fatalf("retriever called wrong: %+v", retr)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %+v", out.Messages)
	}
	if out.Messages[0].Role != "system" {
		t.Fatalf("first message role = %q", out.Messages[0].Role)
	}
	if out.Messages[0].Content == "" {
		t.Fatalf("system message empty")
	}
}

func TestMemoryRetrieve_PreservesMissionASystemPrompt(t *testing.T) {
	t.Parallel()
	retr := &fakeRetriever{snippets: []memory.Snippet{
		{Content: "user prefers vim"},
	}}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Retriever: retr})

	ev := PreSendEvent{
		UserText: "q",
		Messages: []HookMessage{
			{Role: "system", Content: "you are helpful"}, // mission A's system prompt
			{Role: "user", Content: "q"},
		},
	}
	fn := reg.preSend[BuiltinMemoryRetrieve]
	out, _ := fn(context.Background(), ev, nil)
	if len(out.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %+v", out.Messages)
	}
	// Mission A's system prompt MUST stay first.
	if out.Messages[0].Content != "you are helpful" {
		t.Fatalf("mission A's system prompt got reordered: %+v", out.Messages)
	}
	if out.Messages[1].Role != "system" {
		t.Fatalf("memory inject not in slot 1: %+v", out.Messages)
	}
}

func TestMemoryRetrieve_NoSnippetsLeavesEventUntouched(t *testing.T) {
	t.Parallel()
	retr := &fakeRetriever{}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Retriever: retr})
	ev := PreSendEvent{
		UserText: "q",
		Messages: []HookMessage{{Role: "user", Content: "q"}},
	}
	fn := reg.preSend[BuiltinMemoryRetrieve]
	out, _ := fn(context.Background(), ev, nil)
	if len(out.Messages) != 1 {
		t.Fatalf("messages mutated despite no snippets: %+v", out.Messages)
	}
}

func TestMemoryPersist_EmbedsAndStores(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	emb := &fakeEmbedder{}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Store: store, Embedder: emb})

	fn := reg.postSend[BuiltinMemoryPersist]
	ev := PostSendEvent{
		SessionID:     "sess",
		UserTurn:      "Tell me about my preferences? I want to know more about myself.",
		AssistantTurn: "Based on past chats, you prefer vim, tabs, and onions in moderation. " +
			"Your typical workday starts at 9am and you tend to reach for python before bash. " +
			"You enjoy long-form essays and prefer unicode-safe terminals.",
		FinishReason: "completed",
	}
	if err := fn(context.Background(), ev, map[string]any{
		"min_user_chars":      10,
		"min_assistant_chars": 50,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if emb.called != 1 {
		t.Fatalf("embedder called %d times, want 1", emb.called)
	}
	if len(store.chunks) != 1 {
		t.Fatalf("store len = %d, want 1", len(store.chunks))
	}
	chunk := store.chunks[0]
	if chunk.SessionID != "sess" {
		t.Errorf("SessionID = %q", chunk.SessionID)
	}
	if chunk.SourceTurn != "post_send" {
		t.Errorf("SourceTurn = %q", chunk.SourceTurn)
	}
	if len(chunk.Embedding) == 0 {
		t.Errorf("embedding missing")
	}
}

func TestMemoryPersist_SkipsOnFinishReasonError(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	emb := &fakeEmbedder{}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Store: store, Embedder: emb})

	fn := reg.postSend[BuiltinMemoryPersist]
	ev := PostSendEvent{
		SessionID: "sess",
		UserTurn:  "user turn long enough to clear the threshold",
		AssistantTurn: "assistant turn way longer than the default 200 char minimum so " +
			"only the finish-reason check should keep it from being persisted.",
		FinishReason: "stop-called",
	}
	if err := fn(context.Background(), ev, map[string]any{
		"min_user_chars":      10,
		"min_assistant_chars": 10,
	}); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if len(store.chunks) != 0 || emb.called != 0 {
		t.Fatalf("expected skip, got chunks=%d emb=%d", len(store.chunks), emb.called)
	}
}

func TestMemoryPersist_SkipsBelowThreshold(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	emb := &fakeEmbedder{}
	reg := NewBuiltinRegistry()
	RegisterMemoryBuiltins(reg, MemoryDeps{Store: store, Embedder: emb})

	fn := reg.postSend[BuiltinMemoryPersist]
	ev := PostSendEvent{
		SessionID:     "sess",
		UserTurn:      "short",
		AssistantTurn: "also short",
		FinishReason:  "completed",
	}
	if err := fn(context.Background(), ev, nil); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if len(store.chunks) != 0 {
		t.Fatalf("short turn was persisted: %+v", store.chunks)
	}
}

func TestMemoryPersist_SurfacesEmbedderError(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	reg := NewBuiltinRegistry()
	emb := &erroringEmbedder{err: errors.New("rate limited")}
	RegisterMemoryBuiltins(reg, MemoryDeps{Store: store, Embedder: emb})
	fn := reg.postSend[BuiltinMemoryPersist]
	err := fn(context.Background(), PostSendEvent{
		UserTurn:      mustStringOfLen(100),
		AssistantTurn: mustStringOfLen(250),
		FinishReason:  "completed",
	}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStarterMemoryHooks_ShapeIsValid(t *testing.T) {
	t.Parallel()
	for _, h := range StarterMemoryHooks() {
		if err := h.Validate(); err != nil {
			t.Errorf("starter hook %s invalid: %v", h.ID, err)
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────

type erroringEmbedder struct {
	err error
}

func (e *erroringEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	return nil, e.err
}

func mustStringOfLen(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'a'
	}
	return string(out)
}
