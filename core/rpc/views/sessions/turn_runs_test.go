package sessions

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/session"
)

// TestManagerAPI_TurnRuns pins the Sessions_TurnRuns wire projection
// (agentgraph-settings-linkage-01DOGF0D WP03). In-memory store on
// purpose: this asserts the view's field mapping only — the SQL round
// trip and the upgrade path are pinned against real sqlite in
// chat/turn_runs_upgrade_test.go.
func TestManagerAPI_TurnRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mgr := session.NewManager(session.NewMemoryStore())
	api := NewManagerAPI(mgr)
	s, err := api.Create(ctx, "linked")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got, err := api.TurnRuns(ctx, s.ID); err != nil || len(got) != 0 {
		t.Fatalf("fresh session TurnRuns = %+v, %v; want empty, nil", got, err)
	}
	if err := mgr.RecordTurnRun(ctx, s.ID, "span-1", "chat-01J0000000000000000000RUN1", "chat_default", "sha256:ab"); err != nil {
		t.Fatalf("RecordTurnRun: %v", err)
	}
	got, err := api.TurnRuns(ctx, s.ID)
	if err != nil {
		t.Fatalf("TurnRuns: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("TurnRuns = %+v, want 1 row", got)
	}
	r := got[0]
	if r.RunID != "chat-01J0000000000000000000RUN1" || r.TurnSpanID != "span-1" || r.GraphID != "chat_default" || r.SpecDigest != "sha256:ab" || r.CreatedAt == "" {
		t.Errorf("TurnRun = %+v", r)
	}
	if _, err := api.TurnRuns(ctx, ""); err == nil {
		t.Error("TurnRuns(\"\") should reject an empty session id")
	}
	if err := mgr.RecordTurnRun(ctx, "no-such-session", "s", "r", "g", "d"); err == nil {
		t.Error("RecordTurnRun on a missing session should fail")
	}
}
