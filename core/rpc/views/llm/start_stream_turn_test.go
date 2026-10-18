package llm

import (
	"context"
	"sync"
	"testing"
)

// recordingChatRunner captures the UserTurn StartStream hands the runner.
type recordingChatRunner struct {
	mu    sync.Mutex
	turns []UserTurn
}

func (r *recordingChatRunner) StartStream(_ context.Context, _, _, _ string, turn UserTurn) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.turns = append(r.turns, turn)
	return "sub-1", nil
}
func (r *recordingChatRunner) StopStream(context.Context, string) error { return nil }
func (r *recordingChatRunner) HasPausedSubFor(string) (string, bool)    { return "", false }
func (r *recordingChatRunner) RedriveLastTurn(context.Context, string) (string, error) {
	return "", nil
}
func (r *recordingChatRunner) snapshot() []UserTurn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]UserTurn(nil), r.turns...)
}

// TestStartStream_AnnounceFreshness pins the context-sync freshness rule
// (chat-single-writer-01DOGF0G review L3): a user turn is announced iff no
// row spans it yet — not "iff it is the session's last row".
func TestStartStream_AnnounceFreshness(t *testing.T) {
	cases := []struct {
		name         string
		stored       []SessionMessage
		wantID       string
		wantAnnounce bool
	}{
		{
			name: "fresh turn at the tail",
			stored: []SessionMessage{
				{ID: "u0", Role: "user", Content: "first"},
				{ID: "a0", Role: "assistant", Content: "reply", TurnSpanID: "u0"},
				{ID: "u1", Role: "user", Content: "second"},
			},
			wantID: "u1", wantAnnounce: true,
		},
		{
			// (a) the previous turn's deferred journal.Finish landed a move
			// AFTER the new user row: still fresh — it spans u0, not u1.
			name: "send racing the previous turn's deferred write",
			stored: []SessionMessage{
				{ID: "u0", Role: "user", Content: "first"},
				{ID: "u1", Role: "user", Content: "second"},
				{ID: "a0", Role: "assistant", Content: "late move", TurnSpanID: "u0"},
			},
			wantID: "u1", wantAnnounce: true,
		},
		{
			// (b) not fresh: a run of u1 already wrote moves (a failed
			// turn being re-run) — it was announced by that run.
			name: "re-run of a turn that already wrote moves",
			stored: []SessionMessage{
				{ID: "u1", Role: "user", Content: "second"},
				{ID: "m1", Role: "tool", Content: "kenaz__bash(command=<string>)", TurnSpanID: "u1"},
			},
			wantID: "u1", wantAnnounce: false,
		},
		{
			name: "reader without ids never claims a span",
			stored: []SessionMessage{
				{Role: "user", Content: "no id"},
			},
			wantID: "", wantAnnounce: true,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordingChatRunner{}
			api := New(Config{History: &fakeHistory{stored: tc.stored}, ChatRunner: runner})
			if _, err := api.StartStream(context.Background(), "p", "s", ""); err != nil {
				t.Fatalf("StartStream: %v", err)
			}
			got := runner.snapshot()
			if len(got) != 1 {
				t.Fatalf("runner StartStream calls = %d, want 1", len(got))
			}
			if got[0].MessageID != tc.wantID || got[0].Announce != tc.wantAnnounce {
				t.Errorf("UserTurn = %+v, want MessageID=%q Announce=%v", got[0], tc.wantID, tc.wantAnnounce)
			}
		})
	}
}
