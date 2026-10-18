package session

import (
	"context"
	"testing"
)

// The memory store must mirror the SQL store's ON DELETE CASCADE on
// session_turn_runs (agentgraph-settings-linkage-01DOGF0D review L4):
// a fixture built on NewMemoryStore must not keep a deleted session's
// turn -> run links when production would not. The SQL half is pinned
// in chat/turn_runs_upgrade_test.go.
func TestMemStore_DeleteCascadesTurnRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	m := NewManager(NewMemoryStore())
	keep, err := m.Create(ctx, "keep")
	if err != nil {
		t.Fatal(err)
	}
	gone, err := m.Create(ctx, "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RecordTurnRun(ctx, keep.ID, "s1", "chat-keep", "g", "d"); err != nil {
		t.Fatal(err)
	}
	if err := m.RecordTurnRun(ctx, gone.ID, "s2", "chat-gone", "g", "d"); err != nil {
		t.Fatal(err)
	}
	if err := m.Delete(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}
	if runs, _ := m.ListTurnRuns(ctx, gone.ID); len(runs) != 0 {
		t.Errorf("deleted session kept %d turn runs", len(runs))
	}
	if runs, _ := m.ListTurnRuns(ctx, keep.ID); len(runs) != 1 {
		t.Errorf("unrelated session lost its turn run: %+v", runs)
	}
}
