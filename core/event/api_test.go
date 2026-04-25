package event

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestULIDZeroValue(t *testing.T) {
	var u ULID
	if !u.IsZero() {
		t.Fatalf("zero ULID should report IsZero=true, got %q", u)
	}
	if !ULID("00000000000000000000000000").IsZero() {
		t.Fatalf("explicit zero ULID should report IsZero=true")
	}
	if ULID("01HFXY8B5VJ6T6T7AXJF9JT9F9").IsZero() {
		t.Fatalf("non-zero ULID should not report IsZero=true")
	}
}

func TestEmitterIDString(t *testing.T) {
	if got := EmitterID("llm/anthropic").String(); got != "llm/anthropic" {
		t.Fatalf("EmitterID.String=%q want llm/anthropic", got)
	}
}

func TestKindString(t *testing.T) {
	if got := Kind("llm.request.started").String(); got != "llm.request.started" {
		t.Fatalf("Kind.String=%q want llm.request.started", got)
	}
}

func TestUniformHelperShapes(t *testing.T) {
	ctx := context.Background()
	ctx = WithSession(ctx, ULID("01HFXY8B5VJ6T6T7AXJF9JT9F9"))

	c := Cancellation(ctx, EmitterID("scheduler/"), CancellationInfo{Reason: "user-aborted"})
	if c.Kind != Kind("event-log.cancellation") {
		t.Fatalf("Cancellation kind=%q", c.Kind)
	}
	if c.SessionID == nil {
		t.Fatalf("Cancellation should pull session from ctx")
	}

	e := Error(ctx, EmitterID("llm/anthropic"), ErrorInfo{Code: "EAUTH", Message: "auth failed"})
	if e.Kind != Kind("event-log.error") {
		t.Fatalf("Error kind=%q", e.Kind)
	}

	to := Timeout(ctx, EmitterID("mcp/client"), TimeoutInfo{Operation: "tool.call", After: 30 * time.Second})
	if to.Kind != Kind("event-log.timeout") {
		t.Fatalf("Timeout kind=%q", to.Kind)
	}
}

func TestSessionFromContextZero(t *testing.T) {
	if _, ok := SessionFromContext(context.Background()); ok {
		t.Fatalf("plain ctx should have no session")
	}
	ctx := WithSession(context.Background(), ULID(""))
	if _, ok := SessionFromContext(ctx); ok {
		t.Fatalf("zero ULID session should not register")
	}
	ctx = WithSession(context.Background(), ULID("01HFXY8B5VJ6T6T7AXJF9JT9F9"))
	if _, ok := SessionFromContext(ctx); !ok {
		t.Fatalf("set session should be retrievable")
	}
}

func TestRawReplayAuthorizationGate(t *testing.T) {
	if _, ok := IsRawReplayAuthorized(context.Background()); ok {
		t.Fatalf("plain context must not be raw-authorized")
	}
	ctx := AuthorizeRawReplay(context.Background(), "alec@clearvest.co")
	op, ok := IsRawReplayAuthorized(ctx)
	if !ok || op != "alec@clearvest.co" {
		t.Fatalf("authorized ctx should report operator id, got op=%q ok=%v", op, ok)
	}
}

// TestArchivedErrorWraps verifies the wrapping/unwrapping contract.
func TestArchivedErrorWraps(t *testing.T) {
	ae := &ArchivedError{SessionID: ULID("01HFXY8B5VJ6T6T7AXJF9JT9F9"), Ref: ArchiveRef{Path: "/tmp/archive"}}
	if !errors.Is(ae, ErrAlreadyArchived) {
		t.Fatalf("ArchivedError must wrap ErrAlreadyArchived")
	}
	if !IsAlreadyArchived(ae) {
		t.Fatalf("IsAlreadyArchived should classify ArchivedError")
	}
}
