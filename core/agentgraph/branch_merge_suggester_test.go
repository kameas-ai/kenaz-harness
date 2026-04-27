package agentgraph

import (
	"testing"
	"time"
)

func TestMergeSuggester_TerminalStateToken(t *testing.T) {
	t.Parallel()
	s := NewMergeSuggester()
	got := s.Inspect(ChildRunStatus{
		BranchID:         "b1",
		LastAssistantMsg: "Latest version is 1.2.3. Done.",
	})
	if got.Reason != ReasonTerminalState {
		t.Errorf("reason = %q, want terminal_state_token", got.Reason)
	}
	if got.BranchID != "b1" {
		t.Errorf("branch id = %q", got.BranchID)
	}
}

func TestMergeSuggester_RunComplete_Wins(t *testing.T) {
	t.Parallel()
	s := NewMergeSuggester()
	got := s.Inspect(ChildRunStatus{
		BranchID:    "b1",
		RunComplete: true,
	})
	if got.Reason != ReasonRunComplete {
		t.Errorf("reason = %q, want child_run_complete", got.Reason)
	}
}

func TestMergeSuggester_IdleTimeout(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := NewMergeSuggester()
	s.SetClock(func() time.Time { return now })
	s.SetIdleTimeout(time.Minute)

	// Idle 2 min — should fire.
	got := s.Inspect(ChildRunStatus{
		BranchID:         "b1",
		LastActivityAt:   now.Add(-2 * time.Minute),
		HasInflightNodes: false,
	})
	if got.Reason != ReasonIdleTimeout {
		t.Errorf("reason = %q, want idle_timeout", got.Reason)
	}

	// In-flight — should not fire.
	got2 := s.Inspect(ChildRunStatus{
		BranchID:         "b1",
		LastActivityAt:   now.Add(-2 * time.Minute),
		HasInflightNodes: true,
	})
	if got2.Reason != "" {
		t.Errorf("reason = %q, want empty (in-flight)", got2.Reason)
	}

	// Just-active — should not fire.
	got3 := s.Inspect(ChildRunStatus{
		BranchID:         "b1",
		LastActivityAt:   now.Add(-30 * time.Second),
		HasInflightNodes: false,
	})
	if got3.Reason != "" {
		t.Errorf("reason = %q, want empty (recent activity)", got3.Reason)
	}
}

func TestMergeSuggester_NoBranchID_ReturnsZero(t *testing.T) {
	t.Parallel()
	s := NewMergeSuggester()
	got := s.Inspect(ChildRunStatus{LastAssistantMsg: "Done."})
	if got.Reason != "" {
		t.Errorf("reason = %q, want empty", got.Reason)
	}
}

func TestMergeSuggester_CustomTokens(t *testing.T) {
	t.Parallel()
	s := NewMergeSuggester()
	s.SetTerminalTokens([]string{"shipped"})
	got := s.Inspect(ChildRunStatus{
		BranchID:         "b1",
		LastAssistantMsg: "We shipped.",
	})
	if got.Reason != ReasonTerminalState {
		t.Errorf("reason = %q, want terminal_state_token", got.Reason)
	}
}
