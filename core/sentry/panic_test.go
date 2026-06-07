package sentry_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/context/audit"
	"github.com/kameas-ai/kenaz-harness/core/sentry"
)

// fakeEmitter records audit events in a race-safe manner.
type fakeEmitter struct {
	mu      sync.Mutex
	emitted []audit.Event
}

func (f *fakeEmitter) Emit(_ context.Context, e audit.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.emitted = append(f.emitted, e)
	return nil
}

func (f *fakeEmitter) snapshot() []audit.Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]audit.Event, len(f.emitted))
	copy(out, f.emitted)
	return out
}

func TestRecoverGoroutine_SwallowsPanic(t *testing.T) {
	em := &fakeEmitter{}
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sentry.RecoverGoroutine(ctx, "test-goroutine", em)
		panic("synthetic test panic")
	}()
	wg.Wait() // Should not block forever — panic was swallowed.

	// Audit event should have been emitted.
	events := em.snapshot()
	if len(events) == 0 {
		t.Fatal("expected at least one audit event")
	}
	if events[0].Kind != audit.KindPanicRecovered {
		t.Errorf("expected KindPanicRecovered, got %q", events[0].Kind)
	}
	// Payload should contain goroutine label.
	var payload audit.PanicRecoveredPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.GoroutineLabel != "test-goroutine" {
		t.Errorf("expected goroutine_label=test-goroutine, got %q", payload.GoroutineLabel)
	}
}

func TestRecoverGoroutine_NilPanic(t *testing.T) {
	em := &fakeEmitter{}
	ctx := context.Background()

	// No panic — should be a no-op.
	func() {
		defer sentry.RecoverGoroutine(ctx, "no-panic", em)
		// No panic here.
	}()

	if len(em.snapshot()) != 0 {
		t.Error("expected no audit events when no panic occurs")
	}
}

func TestRecoverGoroutine_NilEmitter(t *testing.T) {
	ctx := context.Background()

	// Should not crash when emitter is nil.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sentry.RecoverGoroutine(ctx, "no-emitter", nil)
		panic("panic with nil emitter")
	}()
	wg.Wait()
}

func TestRecoverGoroutine_RedactsSecret(t *testing.T) {
	em := &fakeEmitter{}
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sentry.RecoverGoroutine(ctx, "secret-panic", em)
		panic(fmt.Sprintf("key=sk-ant-api03-SECRETSECRETAABBCC123456 failed"))
	}()
	wg.Wait()

	events := em.snapshot()
	if len(events) == 0 {
		t.Fatal("expected audit event")
	}
	var payload audit.PanicRecoveredPayload
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The raw key must not appear in the summary.
	if containsPlain(payload.Summary, "sk-ant-api03-") {
		t.Errorf("Anthropic key not redacted in audit payload: %q", payload.Summary)
	}
}

func TestRecoverGoroutine_BreadcrumbsPreserved(t *testing.T) {
	// Seed some breadcrumbs before the panic.
	sentry.AddBreadcrumb(sentry.Breadcrumb{
		TS:      time.Now(),
		Level:   "info",
		Message: "pre-panic breadcrumb",
	})

	em := &fakeEmitter{}
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sentry.RecoverGoroutine(ctx, "breadcrumb-test", em)
		panic("breadcrumb test panic")
	}()
	wg.Wait()

	// Ring buffer should still have the breadcrumb.
	snaps := sentry.SnapshotBreadcrumbs()
	found := false
	for _, b := range snaps {
		if b.Message == "pre-panic breadcrumb" {
			found = true
		}
	}
	if !found {
		t.Error("expected pre-panic breadcrumb to be preserved in ring buffer")
	}
}

func containsPlain(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || len(s) > 0 && stringContains(s, sub))
}

func stringContains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
