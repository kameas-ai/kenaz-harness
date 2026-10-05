package rpc

// chat-single-writer-01DOGF0G review L4: buildResumeStarter is the one
// StartStream caller with no frontend append in front of it, so after the
// runner stopped writing user turns it became that turn's single writer.
// Pin it on the production chain (real sqlite, real runner, real
// llmHistoryWriter announce): the continuation prompt is stored ONCE, the
// resumed turn's moves anchor on it, and context-sync hears of it ONCE.

import (
	"context"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

func TestResumeStarter_WritesContinuationPromptOnceAndAnnouncesOnce(t *testing.T) {
	ctx := context.Background()
	h := newSingleWriterHarness(t)
	rec, err := h.sessMgr.Create(ctx, "resume probe")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	sid := rec.ID
	if _, err := h.sessMgr.AppendMessage(ctx, sid, session.Message{Role: session.RoleUser, Content: "explain quicksort"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	partial, err := h.sessMgr.AppendMessage(ctx, sid, session.Message{Role: session.RoleAssistant, Content: "Sure! Quicksort picks a pivot and"})
	if err != nil {
		t.Fatalf("append partial: %v", err)
	}
	if err := h.sessMgr.MarkStreamingFailure(ctx, sid, partial.ID, "transient", true); err != nil {
		t.Fatalf("mark partial failed: %v", err)
	}
	before, err := h.sessMgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}

	starter := buildResumeStarter(h.runner, h.sessMgr, "test-profile", "")
	if starter == nil {
		t.Fatal("buildResumeStarter returned nil")
	}
	if _, err := starter.StartResume(ctx, sid, partial.ID, "", ""); err != nil {
		t.Fatalf("StartResume: %v", err)
	}
	waitForClosedWithin(t, h.broker, 10*time.Second)

	after, err := h.sessMgr.ListMessages(ctx, sid)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	wantPrompt := buildContinuationPrompt(partial.Content)
	var prompts []session.Message
	for _, m := range after[len(before):] {
		if m.Role == session.RoleUser {
			prompts = append(prompts, m)
		}
	}
	if len(prompts) != 1 || prompts[0].Content != wantPrompt {
		t.Fatalf("new user rows after resume = %d (%+v), want exactly one holding the continuation prompt", len(prompts), prompts)
	}
	promptID := prompts[0].ID
	spanned := 0
	for _, m := range after[len(before):] {
		if span := m.TurnSpanID(); span != "" {
			spanned++
			if span != promptID {
				t.Errorf("row %s spans %q, want the continuation prompt %q", m.ID, span, promptID)
			}
		}
	}
	if spanned == 0 {
		t.Error("the resumed turn's moves are not anchored on the continuation prompt")
	}
	var userEvents []map[string]string
	for _, ev := range h.sync.snapshot() {
		if ev["role"] == "user" {
			userEvents = append(userEvents, ev)
		}
	}
	if len(userEvents) != 1 || userEvents[0]["id"] != promptID {
		t.Errorf("context-sync user events = %v, want exactly one naming %s", userEvents, promptID)
	}
}
