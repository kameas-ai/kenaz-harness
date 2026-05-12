package asks

// deferred_test.go — WP06: deferred-ask registry tests.

import (
	"testing"
	"time"
)

func TestDeferredRegistry_RegisterAndGet(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	if err := reg.Register("sess-1", "ask-1"); err != nil {
		t.Fatalf("Register: %v", err)
	}
	a, err := reg.Get("ask-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if a.Status != StatusPending {
		t.Errorf("status = %q, want pending", a.Status)
	}
	if a.SessionID != "sess-1" {
		t.Errorf("session = %q, want sess-1", a.SessionID)
	}
}

func TestDeferredRegistry_Answer(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	_ = reg.Register("sess-1", "ask-1")
	if err := reg.Answer("ask-1", "the answer"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	a, _ := reg.Get("ask-1")
	if a.Status != StatusAnswered {
		t.Errorf("status = %q, want answered", a.Status)
	}
	if a.Answer != "the answer" {
		t.Errorf("answer = %v, want 'the answer'", a.Answer)
	}
}

func TestDeferredRegistry_Decline(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	_ = reg.Register("sess-1", "ask-1")
	if err := reg.Decline("ask-1", "hook_blocked"); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	a, _ := reg.Get("ask-1")
	if a.Status != StatusDeclined {
		t.Errorf("status = %q, want declined", a.Status)
	}
	if a.DeclineReason != "hook_blocked" {
		t.Errorf("reason = %q, want hook_blocked", a.DeclineReason)
	}
}

func TestDeferredRegistry_TooManyPending(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	for i := 0; i < MaxConcurrentDeferred; i++ {
		if err := reg.Register("sess-1", makeID(i)); err != nil {
			t.Fatalf("Register %d: %v", i, err)
		}
	}
	// 6th should fail.
	err := reg.Register("sess-1", "ask-6")
	if err != ErrTooManyPending {
		t.Errorf("expected ErrTooManyPending, got %v", err)
	}
}

func TestDeferredRegistry_TooManyPending_DifferentSession(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	for i := 0; i < MaxConcurrentDeferred; i++ {
		_ = reg.Register("sess-1", makeID(i))
	}
	// Different session: should succeed.
	if err := reg.Register("sess-2", "ask-6"); err != nil {
		t.Errorf("different session should not be rate-limited: %v", err)
	}
}

func TestDeferredRegistry_SweepExpired(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(1 * time.Nanosecond) // very short expiry
	_ = reg.Register("sess-1", "ask-1")
	time.Sleep(5 * time.Millisecond)
	n := reg.SweepExpired()
	if n != 1 {
		t.Errorf("sweep count = %d, want 1", n)
	}
	a, _ := reg.Get("ask-1")
	if a.Status != StatusExpired {
		t.Errorf("status = %q, want expired", a.Status)
	}
}

func TestDeferredRegistry_ListPending(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	_ = reg.Register("sess-1", "ask-1")
	_ = reg.Register("sess-1", "ask-2")
	_ = reg.Register("sess-2", "ask-3")
	_ = reg.Answer("ask-2", "ans")

	pending := reg.ListPending("sess-1")
	if len(pending) != 1 {
		t.Errorf("pending len = %d, want 1 (ask-2 answered, ask-3 different session)", len(pending))
	}
	if pending[0].AskID != "ask-1" {
		t.Errorf("pending ask = %q, want ask-1", pending[0].AskID)
	}
}

func TestDeferredRegistry_UnknownAsk(t *testing.T) {
	t.Parallel()
	reg := NewDeferredRegistry(time.Hour)
	if _, err := reg.Get("no-such"); err != ErrUnknownAsk {
		t.Errorf("Get unknown: expected ErrUnknownAsk, got %v", err)
	}
	if err := reg.Answer("no-such", nil); err != ErrUnknownAsk {
		t.Errorf("Answer unknown: expected ErrUnknownAsk, got %v", err)
	}
}

func TestSystemReminderText(t *testing.T) {
	t.Parallel()
	text := SystemReminderText("ask-42", "yes")
	if text == "" {
		t.Error("SystemReminderText should not be empty")
	}
}

func makeID(i int) string {
	return "ask-" + string(rune('a'+i))
}
