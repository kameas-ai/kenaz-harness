package rpc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph/chat"
	"github.com/kameas-ai/kenaz-harness/core/scheduler"
)

// stopBlockingCompactor parks the budget pass until ctx is cancelled —
// the shape of a quit that lands during the summariser's LLM call.
type stopBlockingCompactor struct{ entered chan struct{} }

func (c stopBlockingCompactor) CompactForSchedule(ctx context.Context, _, _, _ string, _ chat.ScheduledBudget) (chat.ScheduledBudgetOutcome, error) {
	select {
	case c.entered <- struct{}{}:
	default:
	}
	<-ctx.Done()
	return chat.ScheduledBudgetOutcome{Attempted: true}, ctx.Err()
}

// A quit (engine Stop) during the schedule's budget pass, before the
// prompt is written, must not use up a one-shot: the live dispatcher
// returns without marking the run started, the engine records "skipped",
// and the row stays enabled. Fail-on-revert: drop the ctx check before
// MarkRunDispatched -> the row is disabled although no prompt was written.
func TestLiveChatRunDispatcher_StopDuringCompactionKeepsOneShot(t *testing.T) {
	s := buildConversationStack(t)
	seedChatRunRow(t, s.store, "cr-quit", "status check at {{time}}")
	d := s.dispatcher(5 * time.Second)
	ctx := context.Background()

	// A first fire creates the conversation, so the next one compacts.
	first, err := s.api(d).RunNow(ctx, "cr-quit")
	if err != nil || first.Status != "completed" {
		t.Fatalf("RunNow: rec=%+v err=%v", first, err)
	}
	s.waitIdle(t, first.SessionID)

	rec, err := s.store.Get(ctx, "cr-quit")
	if err != nil {
		t.Fatal(err)
	}
	runAt := time.Now()
	rec.TriggerKind = scheduler.TriggerKindOnce
	rec.RunAt = &runAt
	rec.Cron = ""
	if err := s.store.Update(ctx, rec); err != nil {
		t.Fatal(err)
	}

	comp := stopBlockingCompactor{entered: make(chan struct{}, 1)}
	d.deps.Compactor = comp
	engine, err := scheduler.NewChatCronEngine(ctx, scheduler.ChatCronEngineConfig{Store: s.store, Dispatcher: d})
	if err != nil {
		t.Fatalf("NewChatCronEngine: %v", err)
	}
	t.Cleanup(engine.Stop)
	engine.Start()
	select {
	case <-comp.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the one-shot never reached the budget pass")
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := engine.StopContext(sctx); err != nil {
		t.Fatalf("StopContext: %v", err)
	}

	after, err := s.store.Get(ctx, "cr-quit")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Enabled {
		t.Fatal("one-shot disabled although the quit came before its prompt was written")
	}
	if !engine.Registered("cr-quit") {
		t.Fatal("one-shot dropped although it never ran")
	}
	hist, err := s.store.History(ctx, "cr-quit", 10)
	if err != nil {
		t.Fatal(err)
	}
	// Rows can share a timestamp second, so find it rather than index it.
	skipped := 0
	for _, h := range hist {
		if h.Status == scheduler.ChatRunStatusSkipped && strings.Contains(h.Error, "next time the app starts") {
			skipped++
		}
	}
	if len(hist) != 2 || skipped != 1 {
		t.Fatalf("history = %+v, want the first run plus one skipped row saying it runs at the next start", hist)
	}
	if u := s.roleCount(t, first.SessionID, "user"); u != 1 {
		t.Fatalf("conversation has %d user rows, want 1 (no prompt for the stopped fire)", u)
	}
}
