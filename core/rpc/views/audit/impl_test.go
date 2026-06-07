package audit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/event"
)

// recordingSubscriber captures every Subscribe / Unsubscribe call so the
// stream-broker contract can be asserted without pulling in core/rpc.
type recordingSubscriber struct {
	mu      sync.Mutex
	subs    map[string]<-chan any
	nextID  int
	stopped []string
}

func newRecordingSubscriber() *recordingSubscriber {
	return &recordingSubscriber{subs: map[string]<-chan any{}}
}

func (r *recordingSubscriber) Subscribe(_ context.Context, view, _ string, source <-chan any) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	id := view + "-" + time.Now().Format("150405.000000")
	r.subs[id] = source
	return id, nil
}

func (r *recordingSubscriber) Unsubscribe(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.subs, id)
	r.stopped = append(r.stopped, id)
	return nil
}

func TestPushAndList_RingBuffer(t *testing.T) {
	api := NewAPI(WithMaxBuffer(3))

	for i := 0; i < 5; i++ {
		api.Push(Entry{
			ID:        string(rune('a' + i)),
			Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
			Category:  "LLM",
			Subject:   "llm.request.started",
		})
	}

	got, err := api.ListEntries(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("ring buffer cap=3 should yield 3 entries, got %d", len(got))
	}
	// Newest-first order: last pushed (rune 'a'+4 = 'e') first.
	if got[0].ID != "e" {
		t.Errorf("newest-first violated: head id=%q want %q", got[0].ID, "e")
	}
}

func TestListEntries_FilterCategory(t *testing.T) {
	api := NewAPI()
	api.Push(Entry{ID: "1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.request.started"})
	api.Push(Entry{ID: "2", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "MCP", Subject: "mcp.tool.invoked"})
	api.Push(Entry{ID: "3", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.response.completed"})

	got, err := api.ListEntries(context.Background(), Filter{Categories: []string{"LLM"}})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("category filter LLM should yield 2, got %d", len(got))
	}
	for _, e := range got {
		if e.Category != "LLM" {
			t.Errorf("category filter leaked: %q", e.Category)
		}
	}
}

func TestVerifyEntry_Membership(t *testing.T) {
	api := NewAPI()
	api.Push(Entry{ID: "01HK", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.request.started"})

	ok, err := api.VerifyEntry(context.Background(), "01HK")
	if err != nil {
		t.Fatalf("VerifyEntry: %v", err)
	}
	if !ok {
		t.Errorf("VerifyEntry should return true for known id")
	}

	ok, _ = api.VerifyEntry(context.Background(), "missing-id")
	if ok {
		t.Errorf("VerifyEntry should return false for missing id")
	}
}

func TestStartStream_FansEntriesToBroker(t *testing.T) {
	rec := newRecordingSubscriber()
	api := NewAPI(WithSubscriber(rec))

	id, err := api.StartStream(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("StartStream: %v", err)
	}
	if id == "" {
		t.Fatalf("StartStream returned empty id with broker present")
	}

	// Push and read the source channel directly (via recording broker).
	api.Push(Entry{ID: "evt-1", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Category: "LLM", Subject: "llm.request.started"})

	rec.mu.Lock()
	src := rec.subs[id]
	rec.mu.Unlock()
	if src == nil {
		t.Fatalf("subscriber did not record source channel for id %q", id)
	}
	select {
	case ev := <-src:
		entry, ok := ev.(Entry)
		if !ok {
			t.Fatalf("expected Entry, got %T", ev)
		}
		if entry.ID != "evt-1" {
			t.Errorf("entry id = %q, want %q", entry.ID, "evt-1")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("did not receive Entry within deadline")
	}

	if err := api.StopStream(context.Background(), id); err != nil {
		t.Fatalf("StopStream: %v", err)
	}
	if len(rec.stopped) != 1 || rec.stopped[0] != id {
		t.Errorf("expected unsubscribe of %q, got %v", id, rec.stopped)
	}
}

func TestObserveEvent_KindToCategory(t *testing.T) {
	api := NewAPI()

	cases := []struct {
		kind     event.Kind
		category string
	}{
		{"llm.request.started", "LLM"},
		{"mcp.tool.invoked", "MCP"},
		{"a2a.card.received", "A2A"},
		{"policy.decision.denied", "POLICY"},
		{"bundle.installed", "BUNDLE"},
		{"context.entry.added", "CONTEXT"},
		{"scheduler.tick", "SCHEDULER"},
		{"unknown.kind.foo", "STORAGE"},
	}

	for _, tc := range cases {
		ev := event.Event{
			EventID:   event.ULID("01HK0000000000000000000000"),
			EmitterID: event.EmitterID("llm/anthropic"),
			Kind:      tc.kind,
			EmittedAt: time.Now().UTC(),
		}
		api.ObserveEvent(ev)
	}

	got, _ := api.ListEntries(context.Background(), Filter{})
	if len(got) != len(cases) {
		t.Fatalf("expected %d entries, got %d", len(cases), len(got))
	}
	// got is newest-first; iterate matching cases in reverse.
	for i, e := range got {
		want := cases[len(cases)-1-i]
		if e.Category != want.category {
			t.Errorf("kind %q -> category %q, want %q", want.kind, e.Category, want.category)
		}
	}
}

func TestStartStream_NoBroker_NoOp(t *testing.T) {
	api := NewAPI()
	id, err := api.StartStream(context.Background(), Filter{})
	if err != nil {
		t.Fatalf("StartStream without broker should not error, got %v", err)
	}
	if id != "" {
		t.Errorf("StartStream without broker should return empty id, got %q", id)
	}
	if err := api.StopStream(context.Background(), "any-id"); err != nil {
		t.Errorf("StopStream without broker should not error, got %v", err)
	}
}
