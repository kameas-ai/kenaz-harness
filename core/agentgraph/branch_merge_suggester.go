package agentgraph

import (
	"strings"
	"time"
)

// MergeSuggestionReason explains why the suggester fired. Stable
// strings so the frontend can localize / surface differently.
type MergeSuggestionReason string

const (
	// ReasonTerminalState — the child's last assistant turn matched a
	// configured terminal-state token (Done, Resolved, Final answer, etc.).
	ReasonTerminalState MergeSuggestionReason = "terminal_state_token"
	// ReasonRunComplete — the child kernel run reached a terminal
	// state (run_complete event).
	ReasonRunComplete MergeSuggestionReason = "child_run_complete"
	// ReasonIdleTimeout — the child has been idle for at least the
	// configured idle window with no in-flight nodes.
	ReasonIdleTimeout MergeSuggestionReason = "idle_timeout"
)

// MergeSuggestion is what the suggester returns when it fires. Empty
// reason means "don't suggest yet".
type MergeSuggestion struct {
	BranchID string
	Reason   MergeSuggestionReason
	// Detail is a free-text user-facing explanation.
	Detail string
}

// MergeSuggester is the v1 heuristic for surfacing "merge?" prompts on
// the frontend. Configurable via the dial table per spec FR-037; for
// v1 the table is small and dial-tunable through Set* methods.
type MergeSuggester struct {
	terminalTokens []string
	idleTimeout    time.Duration
	now            func() time.Time
}

// NewMergeSuggester returns a suggester with the v1 default dials:
// terminal tokens = {"Done", "Resolved", "Final answer", "Concluded"},
// idle timeout = 5 minutes (spec default).
func NewMergeSuggester() *MergeSuggester {
	return &MergeSuggester{
		terminalTokens: []string{
			"done.", "resolved.", "final answer", "concluded.",
			"that's the answer", "that completes",
		},
		idleTimeout: 5 * time.Minute,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

// SetTerminalTokens overrides the configured terminal-state token list.
// Tokens are matched case-insensitive against the child's last
// assistant turn.
func (s *MergeSuggester) SetTerminalTokens(tokens []string) {
	cp := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" {
			cp = append(cp, t)
		}
	}
	s.terminalTokens = cp
}

// SetIdleTimeout overrides the idle-timeout dial.
func (s *MergeSuggester) SetIdleTimeout(d time.Duration) {
	if d > 0 {
		s.idleTimeout = d
	}
}

// SetClock overrides the wall-clock source. Tests pin this for
// deterministic timing.
func (s *MergeSuggester) SetClock(now func() time.Time) {
	if now != nil {
		s.now = now
	}
}

// ChildRunStatus is the minimal projection of a child kernel run the
// suggester needs. The kernel passes one in via Inspect().
type ChildRunStatus struct {
	BranchID         string
	LastAssistantMsg string
	LastActivityAt   time.Time
	RunComplete      bool
	HasInflightNodes bool
}

// Inspect returns a non-empty MergeSuggestion when any of the
// heuristics fires, or zero-value when none do.
func (s *MergeSuggester) Inspect(st ChildRunStatus) MergeSuggestion {
	if st.BranchID == "" {
		return MergeSuggestion{}
	}
	if st.RunComplete {
		return MergeSuggestion{
			BranchID: st.BranchID,
			Reason:   ReasonRunComplete,
			Detail:   "Child run reached a terminal state.",
		}
	}
	if hit, token := containsTerminalToken(st.LastAssistantMsg, s.terminalTokens); hit {
		return MergeSuggestion{
			BranchID: st.BranchID,
			Reason:   ReasonTerminalState,
			Detail:   "Child branch's last reply contains a terminal-state marker (" + token + ").",
		}
	}
	if !st.HasInflightNodes && !st.LastActivityAt.IsZero() {
		idle := s.now().Sub(st.LastActivityAt)
		if idle >= s.idleTimeout {
			return MergeSuggestion{
				BranchID: st.BranchID,
				Reason:   ReasonIdleTimeout,
				Detail:   "Child branch has been idle for " + idle.Round(time.Second).String() + ".",
			}
		}
	}
	return MergeSuggestion{}
}

func containsTerminalToken(msg string, tokens []string) (bool, string) {
	if msg == "" {
		return false, ""
	}
	lower := strings.ToLower(msg)
	for _, t := range tokens {
		if strings.Contains(lower, t) {
			return true, t
		}
	}
	return false, ""
}
