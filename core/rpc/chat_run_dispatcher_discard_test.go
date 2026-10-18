package rpc

// discardEmptyFailedSession keeps any session holding more than the
// dispatcher's own prompt — in particular a failed run's tool activity,
// which is the only record of what the agent did before failing.
// Real sqlite via buildDispatcherTestStack.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

func TestDiscardEmptyFailedSession_KeepsSessionWithToolActivity(t *testing.T) {
	stack := buildDispatcherTestStack(t, "unused")
	d := outcomeDispatcher(stack)
	ctx := context.Background()
	const prompt = "send the weekly status mail"

	for _, tc := range []struct {
		name      string
		extraRole string
		wantKept  bool
	}{
		{"prompt only is discarded", "", false},
		{"a tool row keeps it", "tool", true},
		{"an assistant row keeps it", "assistant", true},
	} {
		sess, err := stack.sessionsAPI.Create(ctx, "Scheduled: "+tc.name)
		if err != nil {
			t.Fatalf("%s: Create: %v", tc.name, err)
		}
		if _, err := stack.sessionsAPI.AppendMessage(ctx, sess.ID, "user", prompt); err != nil {
			t.Fatalf("%s: append prompt: %v", tc.name, err)
		}
		if tc.extraRole != "" {
			if _, err := stack.sessionsAPI.AppendMessage(ctx, sess.ID, tc.extraRole, "outlook__send_mail -> sent"); err != nil {
				t.Fatalf("%s: append %s row: %v", tc.name, tc.extraRole, err)
			}
		}
		rec := scheduler.ChatRunHistoryRecord{Status: "failed", SessionID: sess.ID, Error: "boom"}
		d.discardEmptyFailedSession(ctx, &rec, prompt)

		_, getErr := stack.sessionsAPI.Get(ctx, sess.ID)
		kept := getErr == nil
		if kept != tc.wantKept {
			t.Errorf("%s: session kept = %v, want %v", tc.name, kept, tc.wantKept)
		}
		if kept && rec.SessionID != sess.ID {
			t.Errorf("%s: kept session but cleared the record's SessionID", tc.name)
		}
		if !kept && rec.SessionID != "" {
			t.Errorf("%s: deleted session but the record still links to it", tc.name)
		}
	}
}
