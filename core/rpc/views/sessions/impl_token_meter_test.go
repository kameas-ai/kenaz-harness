package sessions

// impl_token_meter_test.go — per-message token meter integration tests (PR #83, v0.5.2).
//
// The per-message token meter (per-message-token-meter-01KR3PQR) depends on
// two wiring steps:
//
//   1. usage.Manager.Add writes prompt_tokens / completion_tokens /
//      cost_usd / cost_source columns onto the session_messages row.
//
//   2. messageToView projects those columns into the wire-shape Message
//      that the frontend reads to populate TokenMeterChip props.
//
// This file tests step 2 (the view projection) against the in-memory store,
// which lets us inject pre-populated Message structs with pointer fields set
// directly — simulating what step 1 produces after a real LLM turn.
//
// Together with the existing TokenMeterChip.test.ts (frontend) these tests
// give the full happy-path stack:
//   store (pointer fields set) → messageToView → wire Message → frontend props.

import (
	"context"
	"testing"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/session"
)

// helper returns a pointer to a float64 value.
func fptr(v float64) *float64 { return &v }

// helper returns a pointer to an int value.
func iptr(v int) *int { return &v }

// TestMessageToView_TokenMeter_PopulatesPointers verifies that messageToView
// surfaces PromptTokens, CompletionTokens, CostUSD, and MessageCostSource
// from a session.Message that has its usage pointer fields set.
func TestMessageToView_TokenMeter_PopulatesPointers(t *testing.T) {
	t.Parallel()

	prompt := 1234
	completion := 56
	costUSD := 0.012
	src := "provider"

	m := session.Message{
		ID:               "msg-1",
		SessionID:        "sess-1",
		Role:             session.RoleAssistant,
		Content:          "Hello, world!",
		CreatedAt:        time.Now().UTC(),
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		CostUSD:          &costUSD,
		MessageCostSource: src,
	}

	view := messageToView(m)

	if view.PromptTokens == nil {
		t.Fatal("PromptTokens is nil; want non-nil pointer")
	}
	if *view.PromptTokens != prompt {
		t.Errorf("PromptTokens = %d, want %d", *view.PromptTokens, prompt)
	}

	if view.CompletionTokens == nil {
		t.Fatal("CompletionTokens is nil; want non-nil pointer")
	}
	if *view.CompletionTokens != completion {
		t.Errorf("CompletionTokens = %d, want %d", *view.CompletionTokens, completion)
	}

	if view.CostUSD == nil {
		t.Fatal("CostUSD is nil; want non-nil pointer")
	}
	if *view.CostUSD != costUSD {
		t.Errorf("CostUSD = %v, want %v", *view.CostUSD, costUSD)
	}

	if view.MessageCostSource != src {
		t.Errorf("MessageCostSource = %q, want %q", view.MessageCostSource, src)
	}
}

// TestMessageToView_TokenMeter_NilOnMissingUsage verifies that messageToView
// produces nil pointers (omitempty serialisation) for messages with no usage
// data — i.e., the typical user-turn row.
func TestMessageToView_TokenMeter_NilOnMissingUsage(t *testing.T) {
	t.Parallel()

	m := session.Message{
		ID:        "msg-user",
		SessionID: "sess-1",
		Role:      session.RoleUser,
		Content:   "What is 2+2?",
		CreatedAt: time.Now().UTC(),
		// PromptTokens / CompletionTokens / CostUSD / MessageCostSource all zero-value.
	}

	view := messageToView(m)

	if view.PromptTokens != nil {
		t.Errorf("PromptTokens = %v, want nil for user row", view.PromptTokens)
	}
	if view.CompletionTokens != nil {
		t.Errorf("CompletionTokens = %v, want nil for user row", view.CompletionTokens)
	}
	if view.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil for user row", view.CostUSD)
	}
	if view.MessageCostSource != "" {
		t.Errorf("MessageCostSource = %q, want empty for user row", view.MessageCostSource)
	}
}

// TestMessageToView_TokenMeter_DerivedSource verifies that a "derived" cost
// (from the pricing table) round-trips correctly through the view projection.
func TestMessageToView_TokenMeter_DerivedSource(t *testing.T) {
	t.Parallel()

	prompt := 4096
	completion := 512
	cost := 0.0019

	m := session.Message{
		ID:               "msg-derived",
		SessionID:        "sess-2",
		Role:             session.RoleAssistant,
		Content:          "Some response",
		CreatedAt:        time.Now().UTC(),
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		CostUSD:          &cost,
		MessageCostSource: "derived",
	}

	view := messageToView(m)

	if view.MessageCostSource != "derived" {
		t.Errorf("MessageCostSource = %q, want derived", view.MessageCostSource)
	}
	if view.PromptTokens == nil || *view.PromptTokens != 4096 {
		t.Errorf("PromptTokens = %v, want 4096", view.PromptTokens)
	}
}

// TestListMessages_TokenMeter_RoundTrip exercises the full read path:
// append a message with token fields set directly into the in-memory store,
// then retrieve it through ListMessages and verify the view projection is correct.
func TestListMessages_TokenMeter_RoundTrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	store := session.NewMemoryStore()
	mgr := session.NewManager(store)
	api := NewManagerAPI(mgr)

	// Create session.
	s, err := api.Create(ctx, "token-session")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Inject assistant message with token usage directly via store.
	prompt := 800
	completion := 120
	cost := 0.0055
	if _, err := store.AppendMessage(ctx, session.Message{
		ID:               "msg-assist",
		SessionID:        s.ID,
		Role:             session.RoleAssistant,
		Content:          "I will help you.",
		CreatedAt:        time.Now().UTC(),
		PromptTokens:     &prompt,
		CompletionTokens: &completion,
		CostUSD:          &cost,
		MessageCostSource: "provider",
	}); err != nil {
		t.Fatalf("AppendMessage: %v", err)
	}

	msgs, err := api.ListMessages(ctx, s.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("len = %d, want 1", len(msgs))
	}
	view := msgs[0]

	if view.PromptTokens == nil {
		t.Fatal("PromptTokens is nil after ListMessages round-trip")
	}
	if *view.PromptTokens != prompt {
		t.Errorf("PromptTokens = %d, want %d", *view.PromptTokens, prompt)
	}
	if view.CompletionTokens == nil {
		t.Fatal("CompletionTokens is nil after ListMessages round-trip")
	}
	if *view.CompletionTokens != completion {
		t.Errorf("CompletionTokens = %d, want %d", *view.CompletionTokens, completion)
	}
	if view.CostUSD == nil {
		t.Fatal("CostUSD is nil after ListMessages round-trip")
	}
	if *view.CostUSD != cost {
		t.Errorf("CostUSD = %v, want %v", *view.CostUSD, cost)
	}
	if view.MessageCostSource != "provider" {
		t.Errorf("MessageCostSource = %q, want provider", view.MessageCostSource)
	}
}
