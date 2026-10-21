package wiring_test

// Two compactions racing on one session (scheduled-chat-context-budget
// review): the maximal dial rolling on a user's turn while a scheduled
// budget pass rolls the same conversation. Both read the live history,
// both summarise; without a compare-and-swap in ApplyCompaction the loser
// writes a SECOND live summary and re-points the winner's originals
// (compacted_into_id overwritten). Real sqlite: the CAS lives in the SQL.
//
// The race is made deterministic: the loser's summariser call runs the
// winner's whole roll before it returns, so the loser's read is stale by
// the time it writes.
//
// Fail-on-revert: drop `AND archived_at IS NULL` / the RowsAffected check
// from sqlStore.ApplyCompaction -> two live summaries.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction"
	"github.com/kameas-ai/kenaz-harness/core/agentgraph/compaction/wiring"
	"github.com/kameas-ai/kenaz-harness/core/session"
)

type raceLLM struct {
	n      int
	during func()
}

func (l *raceLLM) CallForSummary(context.Context, compaction.ProviderProfileRef, string, string) (string, int, int, error) {
	l.n++
	n := l.n
	if n == 1 && l.during != nil {
		l.during()
	}
	return fmt.Sprintf("S%d", n), 1, 1, nil
}

type bigWindow struct{}

func (bigWindow) MaxContextTokens(compaction.ProviderProfileRef) (int, bool) { return 1_000_000, true }

func TestApplyCompaction_ConcurrentRollsLeaveOneLiveSummary(t *testing.T) {
	ctx := context.Background()
	mgr, store := pairingStore(t)
	sess, err := mgr.Create(ctx, "race")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var prompts []string
	for i := 0; i < 6; i++ {
		u, err := mgr.AppendMessage(ctx, sess.ID, session.Message{Role: session.RoleUser, Content: fmt.Sprintf("u%d", i)})
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		prompts = append(prompts, u.ID)
		if _, err := mgr.AppendMessage(ctx, sess.ID, session.Message{Role: session.RoleAssistant, Content: fmt.Sprintf("a%d", i)}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}

	llm := &raceLLM{}
	eng, err := compaction.NewSessionEngine(compaction.SessionEngineConfig{
		Store: wiring.NewMessageStore(store), LLM: llm, Capabilities: bigWindow{},
	})
	if err != nil {
		t.Fatalf("NewSessionEngine: %v", err)
	}
	var winnerErr error
	// The loser is the scheduled budget pass (keep 1 run); while its
	// summariser runs, the maximal dial (recent window 2) rolls first.
	llm.during = func() { _, winnerErr = eng.RollingSummarize(ctx, sess.ID, compaction.ProviderProfileRef{}, 2) }
	_, loserErr := eng.RollingSummarizeWith(ctx, sess.ID, compaction.ProviderProfileRef{}, compaction.RollingOptions{
		RunWindow: &compaction.RunWindow{StartIDs: prompts, Keep: 1},
		Tier:      "scheduled_budget",
	})
	if winnerErr != nil {
		t.Fatalf("winner roll: %v", winnerErr)
	}
	if !errors.Is(loserErr, session.ErrCompactionConflict) {
		t.Fatalf("loser roll = %v, want ErrCompactionConflict", loserErr)
	}

	active, err := store.ListMessagesActive(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessagesActive: %v", err)
	}
	var summaries []string
	for _, m := range active {
		if strings.HasPrefix(m.Content, "[Rolling summary:") {
			summaries = append(summaries, m.Content)
		}
	}
	if len(summaries) != 1 || summaries[0] != "[Rolling summary: S2]" {
		t.Fatalf("live summaries = %v, want only the winner's", summaries)
	}
	// The winner's originals still point at the winner's summary.
	all, err := store.ListMessages(ctx, sess.ID)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	winnerID := ""
	for _, m := range active {
		if strings.HasPrefix(m.Content, "[Rolling summary:") {
			winnerID = m.ID
		}
	}
	archived := 0
	for _, m := range all {
		if m.ArchivedAt == nil {
			continue
		}
		archived++
		if m.CompactedIntoID == nil || *m.CompactedIntoID != winnerID {
			t.Fatalf("archived row %s points at %v, want the winner %s", m.ID, m.CompactedIntoID, winnerID)
		}
	}
	if archived != 8 {
		t.Fatalf("%d archived rows, want the winner's 8 (4 pairs)", archived)
	}
}
