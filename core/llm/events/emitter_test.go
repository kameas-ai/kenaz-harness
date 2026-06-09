package events

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	llm "github.com/kameas-ai/kenaz-harness/core/llm"
)

func TestEmitter_RequestSubmittedExcludesCredentials(t *testing.T) {
	sink := &MemorySink{}
	em := New(sink)

	req := llm.GenerationRequest{
		SessionID: "s1",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: []llm.ContentBlock{{Type: "text", Text: "hi"}}}},
		Tools:     []llm.ToolSpec{{Name: "t"}},
	}
	prof := llm.ProviderProfile{
		ID: "p1", Kind: "anthropic", Model: "claude",
		Cred: llm.CredentialReference{Kind: "env", Locator: "ANTHROPIC_API_KEY"},
	}
	if err := em.RequestSubmitted(context.Background(), req, prof, "snap-1"); err != nil {
		t.Fatal(err)
	}
	entries := sink.Entries()
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Kind != KindRequestSubmitted {
		t.Fatalf("kind: %s", e.Kind)
	}
	if e.ProfileID != "p1" || e.Provider != "anthropic" || e.Model != "claude" {
		t.Fatalf("identity fields: %+v", e)
	}
	// Crucial: request payload must not contain a "credential" / "auth"
	// field — only the requested-capabilities and counts.
	for k := range e.Payload {
		if strings.Contains(strings.ToLower(k), "credential") || strings.Contains(strings.ToLower(k), "auth") || strings.Contains(strings.ToLower(k), "secret") || strings.Contains(strings.ToLower(k), "key") {
			t.Fatalf("payload key looks credential-related: %q", k)
		}
	}
}

func TestEmitter_AllKinds_KnownAndUnique(t *testing.T) {
	seen := map[Kind]bool{}
	for _, k := range AllKinds() {
		if seen[k] {
			t.Fatalf("duplicate kind %s", k)
		}
		seen[k] = true
		if !strings.HasPrefix(string(k), "llm/") {
			t.Fatalf("kind not in llm/ namespace: %s", k)
		}
	}
}

func TestEmitter_OrderedSuccessChain(t *testing.T) {
	sink := &MemorySink{}
	em := New(sink)
	em.SetClock(func() time.Time { return time.Unix(1700000000, 0) })

	req := llm.GenerationRequest{SessionID: "s"}
	prof := llm.ProviderProfile{ID: "p", Kind: "openai", Model: "gpt"}

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(em.RequestSubmitted(context.Background(), req, prof, "snap"))
	must(em.StreamChunk(context.Background(), req, prof, llm.StreamEvent{Kind: llm.StreamText, Text: "hi"}))
	must(em.StreamChunk(context.Background(), req, prof, llm.StreamEvent{Kind: llm.StreamText, Text: " there"}))
	must(em.ResponseFinal(context.Background(), req, prof, llm.Response{FinishReason: "stop"}, 12))

	want := []Kind{KindRequestSubmitted, KindStreamChunk, KindStreamChunk, KindResponseFinal}
	got := sink.Kinds()
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: got %s want %s", i, got[i], want[i])
		}
	}
}

func TestEmitter_FailurePathConsistency(t *testing.T) {
	sink := &MemorySink{}
	em := New(sink)
	req := llm.GenerationRequest{SessionID: "s"}
	prof := llm.ProviderProfile{ID: "p", Kind: "openai", Model: "gpt"}

	if err := em.RequestSubmitted(context.Background(), req, prof, ""); err != nil {
		t.Fatal(err)
	}
	if err := em.StreamChunk(context.Background(), req, prof, llm.StreamEvent{Kind: llm.StreamText, Text: "partial"}); err != nil {
		t.Fatal(err)
	}
	if err := em.Error(context.Background(), req, prof, &llm.ErrTransient{Status: 503, Message: "drop"}); err != nil {
		t.Fatal(err)
	}
	kinds := sink.Kinds()
	if kinds[len(kinds)-1] != KindError {
		t.Fatalf("failure path didn't end in error: %v", kinds)
	}
}

func TestMemorySink_RaceSafe(t *testing.T) {
	sink := &MemorySink{}
	em := New(sink)
	prof := llm.ProviderProfile{ID: "p", Kind: "anthropic", Model: "m"}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = em.StreamChunk(context.Background(), llm.GenerationRequest{}, prof, llm.StreamEvent{Kind: llm.StreamText, Text: "x"})
		}()
	}
	wg.Wait()
	if got := len(sink.Entries()); got != 50 {
		t.Fatalf("got %d entries", got)
	}
}
