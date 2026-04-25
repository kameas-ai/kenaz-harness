package acp

import (
	"errors"
	"testing"
)

func TestTransportKindValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		k     TransportKind
		valid bool
	}{
		{TransportUDS, true},
		{TransportLoopback, true},
		{TransportLAN, true},
		{TransportPublic, true},
		{TransportKind(""), false},
		{TransportKind("ws"), false},
	}
	for _, c := range cases {
		if got := c.k.Valid(); got != c.valid {
			t.Errorf("TransportKind(%q).Valid() = %v, want %v", c.k, got, c.valid)
		}
	}
}

func TestCardSourceValid(t *testing.T) {
	t.Parallel()
	cases := []struct {
		s     CardSource
		valid bool
	}{
		{CardSourceInline, true},
		{CardSourceWellKnown, true},
		{CardSourceManualURL, true},
		{CardSource(""), false},
		{CardSource("foo"), false},
	}
	for _, c := range cases {
		if got := c.s.Valid(); got != c.valid {
			t.Errorf("CardSource(%q).Valid() = %v, want %v", c.s, got, c.valid)
		}
	}
}

func TestTaskStateTerminal(t *testing.T) {
	t.Parallel()
	cases := map[TaskState]bool{
		StatePending:       false,
		StateRunning:       false,
		StateAwaitingInput: false,
		StateCompleted:     true,
		StateFailed:        true,
		StateCancelled:     true,
	}
	for s, want := range cases {
		if got := s.Terminal(); got != want {
			t.Errorf("%s.Terminal() = %v, want %v", s, got, want)
		}
	}
}

// TestCanTransition exhaustively covers the legal-transition matrix for
// the Task lifecycle. State never moves backwards; terminal states are
// frozen. FR-009.
func TestCanTransition(t *testing.T) {
	t.Parallel()
	all := []TaskState{
		StatePending,
		StateRunning,
		StateAwaitingInput,
		StateCompleted,
		StateFailed,
		StateCancelled,
	}
	legal := map[[2]TaskState]bool{
		{StatePending, StateRunning}:           true,
		{StatePending, StateFailed}:            true,
		{StatePending, StateCancelled}:         true,
		{StateRunning, StateAwaitingInput}:     true,
		{StateRunning, StateCompleted}:         true,
		{StateRunning, StateFailed}:            true,
		{StateRunning, StateCancelled}:         true,
		{StateAwaitingInput, StateRunning}:     true,
		{StateAwaitingInput, StateCompleted}:   true,
		{StateAwaitingInput, StateFailed}:      true,
		{StateAwaitingInput, StateCancelled}:   true,
	}
	for _, from := range all {
		for _, to := range all {
			want := legal[[2]TaskState{from, to}]
			got := CanTransition(from, to)
			if got != want {
				t.Errorf("CanTransition(%s -> %s) = %v, want %v", from, to, got, want)
			}
		}
	}

	// Spec edge cases: explicit checks for the headline illegal moves.
	illegalCases := [][2]TaskState{
		{StateCompleted, StateRunning},
		{StateCancelled, StateCompleted},
		{StateFailed, StateRunning},
		{StateRunning, StatePending},  // backwards
		{StateCompleted, StateFailed}, // terminal -> terminal
	}
	for _, p := range illegalCases {
		if CanTransition(p[0], p[1]) {
			t.Errorf("CanTransition(%s -> %s) = true, want false", p[0], p[1])
		}
	}
}

func TestErrorClassification(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err         error
		isTransient bool
		isRefusal   bool
	}{
		{ErrPolicyDenied, false, true},
		{ErrTransportRefused, false, true},
		{ErrUnsupportedProtocolVersion, false, true},
		{ErrVerifierRejected, false, true},
		{ErrPeerNotFound, false, false},
		{ErrSkillNotFound, false, false},
		{ErrSchemaViolation, false, false},
		{ErrCancelled, false, false},
		{ErrCredentialResolution, false, false},
		{ErrIllegalStateTransition, false, false},
		{nil, false, false},
		{errors.New("wire blip"), true, false},
	}
	for _, c := range cases {
		if got := IsTransient(c.err); got != c.isTransient {
			t.Errorf("IsTransient(%v) = %v, want %v", c.err, got, c.isTransient)
		}
		if got := IsRefusal(c.err); got != c.isRefusal {
			t.Errorf("IsRefusal(%v) = %v, want %v", c.err, got, c.isRefusal)
		}
	}
}

func TestProtocolVersion(t *testing.T) {
	t.Parallel()
	if ProtocolVersion != "1.0" {
		t.Errorf("ProtocolVersion = %q, want %q", ProtocolVersion, "1.0")
	}
}

func TestDispatchOptions(t *testing.T) {
	t.Parallel()
	var opts DispatchOptions
	for _, fn := range []DispatchOption{
		WithParentSession("sess-1"),
		WithSnapshotID("snap-1"),
	} {
		fn(&opts)
	}
	if opts.ParentSessionID != "sess-1" {
		t.Errorf("ParentSessionID = %q, want sess-1", opts.ParentSessionID)
	}
	if opts.SnapshotID != "snap-1" {
		t.Errorf("SnapshotID = %q, want snap-1", opts.SnapshotID)
	}
}
