package slashcmd

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

// fakeMemory is a deterministic MemoryGateway for tests. Every call is
// recorded so tests can assert side effects without dragging in the
// real core/memory store.
type fakeMemory struct {
	mu sync.Mutex

	// MemorizeID is the id Memorize returns. When empty, Memorize uses
	// "mem-fake-1".
	MemorizeID string
	// MemorizeErr is returned by Memorize when non-nil.
	MemorizeErr error
	// MemorizeCalls records each (sessionID, text) pair.
	MemorizeCalls []struct{ SessionID, Text string }

	// RecallHits is returned by Recall on success.
	RecallHits []MemoryHit
	// RecallErr is returned by Recall when non-nil.
	RecallErr error
	// RecallCalls records each (sessionID, query, k) tuple.
	RecallCalls []struct {
		SessionID, Query string
		K                int
	}

	// ForgetErr is returned by Forget when non-nil.
	ForgetErr error
	// ForgotIDs records each id Forget was called with.
	ForgotIDs []string
}

func (f *fakeMemory) Memorize(_ context.Context, sessionID, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.MemorizeCalls = append(f.MemorizeCalls, struct{ SessionID, Text string }{sessionID, text})
	if f.MemorizeErr != nil {
		return "", f.MemorizeErr
	}
	id := f.MemorizeID
	if id == "" {
		id = "mem-fake-1"
	}
	return id, nil
}

func (f *fakeMemory) Recall(_ context.Context, sessionID, query string, k int) ([]MemoryHit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.RecallCalls = append(f.RecallCalls, struct {
		SessionID, Query string
		K                int
	}{sessionID, query, k})
	if f.RecallErr != nil {
		return nil, f.RecallErr
	}
	return append([]MemoryHit(nil), f.RecallHits...), nil
}

func (f *fakeMemory) Forget(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ForgotIDs = append(f.ForgotIDs, id)
	return f.ForgetErr
}

func TestMemorize_PersistsAndReportsID(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{MemorizeID: "mem-abc"}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, err := r.Execute(context.Background(), "sess-1", "/memorize prefer rg over grep")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "mem-abc") {
		t.Errorf("Text missing id: %q", res.Text)
	}
	if len(mem.MemorizeCalls) != 1 {
		t.Fatalf("Memorize calls = %d, want 1", len(mem.MemorizeCalls))
	}
	if mem.MemorizeCalls[0].SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want sess-1", mem.MemorizeCalls[0].SessionID)
	}
	if mem.MemorizeCalls[0].Text != "prefer rg over grep" {
		t.Errorf("Text = %q, want %q", mem.MemorizeCalls[0].Text, "prefer rg over grep")
	}
}

func TestMemorize_NoArgs(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, _ := r.Execute(context.Background(), "s", "/memorize")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "missing text") {
		t.Errorf("Text missing usage hint: %q", res.Text)
	}
	if len(mem.MemorizeCalls) != 0 {
		t.Errorf("Memorize should not be called: %v", mem.MemorizeCalls)
	}
}

func TestMemorize_GatewayUnwired(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{})
	res, _ := r.Execute(context.Background(), "s", "/memorize hello")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("Text missing not-wired hint: %q", res.Text)
	}
}

func TestRecall_RendersHits(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{
		RecallHits: []MemoryHit{
			{ID: "mem-1", Content: "always prefer rg over grep", Score: 0.91},
			{ID: "mem-2", Content: "use ripgrep with --hidden", Score: 0.83},
		},
	}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, err := r.Execute(context.Background(), "sess-1", "/recall ripgrep")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	for _, want := range []string{"mem-1", "mem-2", "rg over grep", "Recalled 2 chunks"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("Text missing %q:\n%s", want, res.Text)
		}
	}
	if len(mem.RecallCalls) != 1 {
		t.Fatalf("Recall calls = %d, want 1", len(mem.RecallCalls))
	}
	if mem.RecallCalls[0].K != recallDefaultK {
		t.Errorf("K = %d, want %d", mem.RecallCalls[0].K, recallDefaultK)
	}
}

func TestRecall_NoMatches(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, _ := r.Execute(context.Background(), "s", "/recall obscure-thing")
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "no matches") {
		t.Errorf("Text missing no-matches: %q", res.Text)
	}
}

func TestRecall_NoArgs(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Memory: &fakeMemory{}})
	res, _ := r.Execute(context.Background(), "s", "/recall")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "missing query") {
		t.Errorf("Text missing usage hint: %q", res.Text)
	}
}

func TestForget_DeletesByID(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, err := r.Execute(context.Background(), "s", "/forget mem-abc")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "mem-abc") {
		t.Errorf("Text missing id: %q", res.Text)
	}
	if len(mem.ForgotIDs) != 1 || mem.ForgotIDs[0] != "mem-abc" {
		t.Errorf("ForgotIDs = %v", mem.ForgotIDs)
	}
}

func TestForget_NotFound(t *testing.T) {
	t.Parallel()
	mem := &fakeMemory{ForgetErr: ErrMemoryChunkNotFound}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, err := r.Execute(context.Background(), "s", "/forget mem-missing")
	if err != nil {
		t.Fatalf("Execute should not return wrapped not-found: %v", err)
	}
	if res.Kind != ResultKindWarning {
		t.Errorf("Kind = %q, want warning", res.Kind)
	}
	if !strings.Contains(res.Text, "no chunk") {
		t.Errorf("Text missing not-found phrase: %q", res.Text)
	}
}

func TestForget_BackendError(t *testing.T) {
	t.Parallel()
	boom := errors.New("disk full")
	mem := &fakeMemory{ForgetErr: boom}
	r, _ := NewRegistry(Deps{Memory: mem})
	res, err := r.Execute(context.Background(), "s", "/forget mem-x")
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want %v", err, boom)
	}
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
}

func TestForget_NoArgs(t *testing.T) {
	t.Parallel()
	r, _ := NewRegistry(Deps{Memory: &fakeMemory{}})
	res, _ := r.Execute(context.Background(), "s", "/forget")
	if res.Kind != ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "missing id") {
		t.Errorf("Text missing usage hint: %q", res.Text)
	}
}

func TestSummariseLine_TrimsLong(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a ", 200)
	got := summariseLine(long)
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis suffix: %q", got)
	}
	if len([]rune(got)) > 121 {
		t.Errorf("line too long: %d runes", len([]rune(got)))
	}
}
