package rpc

import (
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
)

// TestAPI_Shutdown_StopsSchedulersBeforeChatDrain pins Shutdown's order:
// the workflow + chat-cron schedulers stop BEFORE the chat runner drains,
// so a firing cannot start a run mid-drain. Not parallel: swaps a
// package-level hook. Mutation: move the drain back above the scheduler
// stops and the order assertion fails.
func TestAPI_Shutdown_StopsSchedulersBeforeChatDrain(t *testing.T) {
	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c, WithSettingsStore(newTestStore(t)))
	if api.chatRunner == nil {
		t.Fatal("chatRunner not held on the API — the drain under test would be vacuous")
	}
	var steps []string
	shutdownStepHook = func(s string) { steps = append(steps, s) }
	defer func() { shutdownStepHook = nil }()
	api.Shutdown()
	want := []string{"run_schedulers_stopped", "chat_runs_drained"}
	if len(steps) != 2 || steps[0] != want[0] || steps[1] != want[1] {
		t.Fatalf("Shutdown steps = %v, want %v", steps, want)
	}
}
