package rpc

// dogfood 2026-10-08 round 2 — scheduled chat outcomes.
//
// Real sqlite throughout (core.New over a temp DataDir via
// buildDispatcherTestStack): the session the run creates, the history
// row the scheduledchat API persists, and the List read that surfaces it
// as LastRun all go through the production SQL paths (CLAUDE.md blind
// spot #2).

import (
	"context"
	"strings"
	"testing"
	"time"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

func scheduledSessionCount(t *testing.T, stack *dispatcherTestStack) int {
	t.Helper()
	sessions, err := stack.sessionsAPI.List(context.Background())
	if err != nil {
		t.Fatalf("List sessions: %v", err)
	}
	n := 0
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, "Scheduled: ") {
			n++
		}
	}
	return n
}

func outcomeDispatcher(stack *dispatcherTestStack) *LiveChatRunDispatcher {
	return NewChatRunDispatcher(ChatRunDispatcherDeps{
		Store:          stack.store,
		Sessions:       stack.sessionsAPI,
		LLM:            stack.llmAPI,
		Bus:            stack.bus,
		DefaultProfile: func() string { return "test-profile" },
		DefaultModel: func(profileID string) string {
			if profileID != "test-profile" {
				return ""
			}
			return "aion-labs/aion-2.0"
		},
		Timeout: 5 * time.Second,
	})
}

// (e) A run that fails before any assistant message must not leave an
// empty "Scheduled: …" session behind; the history row keeps the error
// and model, with no dangling session link.
func TestLiveChatRunDispatcher_FailedRunWithoutOutputDiscardsItsSession(t *testing.T) {
	stack := buildDispatcherTestStack(t, "unused")
	stack.model.fail = &corellm.ErrInvalidRequest{Status: 400, Message: "This endpoint's maximum context length is 131072 tokens"}
	seedChatRunRow(t, stack.store, "cr-fail", "ping")

	rec, err := outcomeDispatcher(stack).DispatchChatRun(context.Background(),
		scheduler.Job{ID: "cr-fail", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-fail"}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DispatchChatRun: %v", err)
	}
	if rec.Status != "failed" || rec.Error == "" {
		t.Fatalf("record = %+v, want a failed record carrying the error", rec)
	}
	if rec.SessionID != "" {
		t.Errorf("SessionID = %q, want empty — the session was discarded, the record must not link to it", rec.SessionID)
	}
	if rec.Model != "aion-labs/aion-2.0" {
		t.Errorf("Model = %q, want the resolved active default", rec.Model)
	}
	if n := scheduledSessionCount(t, stack); n != 0 {
		t.Errorf("%d \"Scheduled:\" sessions remain after a failed run with no output, want 0", n)
	}
}

// A completed run keeps its session (it is the run's output) and records
// the model the run used.
func TestLiveChatRunDispatcher_CompletedRunKeepsSessionAndRecordsModel(t *testing.T) {
	stack := buildDispatcherTestStack(t, "pong")
	seedChatRunRow(t, stack.store, "cr-ok", "ping")

	rec, err := outcomeDispatcher(stack).DispatchChatRun(context.Background(),
		scheduler.Job{ID: "cr-ok", Kind: scheduler.JobKindChatRun, ChatRun: &scheduler.ChatRunSpec{ID: "cr-ok"}}, time.Now().UTC())
	if err != nil {
		t.Fatalf("DispatchChatRun: %v", err)
	}
	if rec.Status != "completed" || rec.SessionID == "" {
		t.Fatalf("record = %+v, want completed with a session", rec)
	}
	if rec.Model != "aion-labs/aion-2.0" {
		t.Errorf("Model = %q, want aion-labs/aion-2.0", rec.Model)
	}
	if n := scheduledSessionCount(t, stack); n != 1 {
		t.Errorf("%d \"Scheduled:\" sessions, want 1 (the completed run's)", n)
	}
}

// (c) The last run's outcome is persisted and comes back on the
// schedule's own List/Get entry — no separate history call, no toast
// required.
func TestScheduledChat_ListCarriesPersistedLastRun(t *testing.T) {
	stack := buildDispatcherTestStack(t, "unused")
	stack.model.fail = &corellm.ErrInvalidRequest{Status: 400, Message: "maximum context length is 131072 tokens"}
	seedChatRunRow(t, stack.store, "cr-last", "ping")

	api := scheduledchatview.New(scheduledchatview.Config{
		Store:      stack.store,
		Dispatcher: outcomeDispatcher(stack),
		DefaultModel: func() scheduledchatview.DefaultModel {
			return scheduledchatview.DefaultModel{ProfileID: "test-profile", Model: "aion-labs/aion-2.0"}
		},
	})

	entries, err := api.List(context.Background())
	if err != nil || len(entries) != 1 {
		t.Fatalf("List = %+v (err %v)", entries, err)
	}
	if entries[0].LastRun != nil {
		t.Fatalf("LastRun = %+v before any run, want nil", entries[0].LastRun)
	}

	if _, err := api.RunNow(context.Background(), "cr-last"); err != nil {
		t.Fatalf("RunNow: %v", err)
	}

	entry, err := api.Get(context.Background(), "cr-last")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	lr := entry.LastRun
	if lr == nil {
		t.Fatal("LastRun is nil after a run — the outcome was not persisted / surfaced")
	}
	if lr.Status != "failed" || lr.Error == "" || lr.Model != "aion-labs/aion-2.0" || lr.StartedAt.IsZero() {
		t.Errorf("LastRun = %+v, want failed with error, model and start time", lr)
	}
	entries, _ = api.List(context.Background())
	if entries[0].LastRun == nil || entries[0].LastRun.ID != lr.ID {
		t.Errorf("List LastRun = %+v, want the same row as Get", entries[0].LastRun)
	}

	dm, err := api.DefaultModel(context.Background())
	if err != nil || dm.Model != "aion-labs/aion-2.0" || dm.ProfileID != "test-profile" {
		t.Errorf("DefaultModel = %+v (err %v)", dm, err)
	}
}
