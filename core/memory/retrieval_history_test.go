package memory

import (
	"sync"
	"testing"
	"time"
)

func TestRetrievalHistory_PushAndLast(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}

	// Empty history → Last returns false.
	_, ok := h.Last("sess-1")
	if ok {
		t.Fatal("expected no record for empty history")
	}

	now := time.Now().UTC()
	h.Push(RetrievalRecord{
		SessionID: "sess-1",
		Query:     "what is the weather",
		At:        now,
		Results: []RetrievalResult{
			{ChunkID: "c1", Similarity: 0.9, Injected: true},
		},
	})

	rec, ok := h.Last("sess-1")
	if !ok {
		t.Fatal("expected a record after Push")
	}
	if rec.Query != "what is the weather" {
		t.Errorf("query = %q, want %q", rec.Query, "what is the weather")
	}
	if len(rec.Results) != 1 || rec.Results[0].ChunkID != "c1" {
		t.Errorf("results = %v", rec.Results)
	}
}

func TestRetrievalHistory_LastReturnsNewest(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		h.Push(RetrievalRecord{
			SessionID: "sess-A",
			Query:     "query-" + string(rune('a'+i)),
			At:        base.Add(time.Duration(i) * time.Second),
		})
	}
	rec, ok := h.Last("sess-A")
	if !ok {
		t.Fatal("expected record")
	}
	if rec.Query != "query-e" {
		t.Errorf("want last query 'query-e', got %q", rec.Query)
	}
}

func TestRetrievalHistory_PerSessionCap(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	base := time.Now().UTC()
	for i := 0; i < HistoryPerSessionCap+10; i++ {
		h.Push(RetrievalRecord{
			SessionID: "sess-cap",
			Query:     "q",
			At:        base.Add(time.Duration(i) * time.Millisecond),
		})
	}
	h.mu.RLock()
	n := len(h.bySess["sess-cap"].records)
	h.mu.RUnlock()
	if n > HistoryPerSessionCap {
		t.Errorf("per-session cap exceeded: %d > %d", n, HistoryPerSessionCap)
	}
}

func TestRetrievalHistory_GlobalCap(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	base := time.Now().UTC()
	// Use many distinct sessions so per-session cap doesn't trigger.
	for i := 0; i < HistoryGlobalCap+50; i++ {
		sess := "sess-" + string(rune('A'+i%26)) + string(rune('a'+i/26%26))
		h.Push(RetrievalRecord{
			SessionID: sess,
			Query:     "q",
			At:        base.Add(time.Duration(i) * time.Microsecond),
		})
	}
	if h.Total() > HistoryGlobalCap {
		t.Errorf("global cap exceeded: total = %d", h.Total())
	}
}

func TestRetrievalHistory_UnknownSession(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	_, ok := h.Last("no-such-session")
	if ok {
		t.Error("expected false for unknown session")
	}
}

func TestRetrievalHistory_ConcurrentPushAndLast(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	base := time.Now().UTC()

	var wg sync.WaitGroup
	// Writers.
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h.Push(RetrievalRecord{
				SessionID: "concurrent-sess",
				Query:     "q",
				At:        base.Add(time.Duration(i) * time.Millisecond),
			})
		}(i)
	}
	// Readers.
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Last("concurrent-sess")
		}()
	}
	wg.Wait()
	// Should not panic or race — the race detector confirms correctness.
}

func TestRetrievalHistory_EmptySessionIDDropped(t *testing.T) {
	t.Parallel()
	h := &RetrievalHistory{bySess: make(map[string]*sessionEntry)}
	h.Push(RetrievalRecord{SessionID: "", Query: "q", At: time.Now()})
	if h.Total() != 0 {
		t.Error("empty session-ID record should not be stored")
	}
}
