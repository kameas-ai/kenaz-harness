package fleet

import (
	"context"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/context/audit"
)

// fakeAuditEmitter is a minimal audit.Emitter for bypass tests.
type fakeAuditEmitter struct {
	events []audit.Event
}

func (f *fakeAuditEmitter) Emit(_ context.Context, e audit.Event) error {
	f.events = append(f.events, e)
	return nil
}

func TestLockdownBypassed_NotSet(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "")
	if LockdownBypassed() {
		t.Error("expected LockdownBypassed()=false when env var is empty")
	}
}

func TestLockdownBypassed_Set(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "1")
	if !LockdownBypassed() {
		t.Error("expected LockdownBypassed()=true when env var is '1'")
	}
}

func TestLockdownBypassed_SetTrue(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "true")
	if !LockdownBypassed() {
		t.Error("expected LockdownBypassed()=true when env var is 'true'")
	}
}

func TestAuditLockdownBypass_EmitsWhenSet(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "1")
	em := &fakeAuditEmitter{}
	AuditLockdownBypass(context.Background(), em)

	if len(em.events) != 1 {
		t.Fatalf("expected 1 audit event; got %d", len(em.events))
	}
	if em.events[0].Kind != audit.KindFleetLockdownBypassUsed {
		t.Errorf("expected kind %q; got %q", audit.KindFleetLockdownBypassUsed, em.events[0].Kind)
	}
}

func TestAuditLockdownBypass_NoEmitWhenNotSet(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "")
	em := &fakeAuditEmitter{}
	AuditLockdownBypass(context.Background(), em)

	if len(em.events) != 0 {
		t.Errorf("expected 0 audit events when bypass not set; got %d", len(em.events))
	}
}

func TestAuditLockdownBypass_NilEmitterIsNoop(t *testing.T) {
	t.Setenv(lockdownBypassEnvVar, "1")
	// Should not panic with a nil emitter.
	AuditLockdownBypass(context.Background(), nil)
}
