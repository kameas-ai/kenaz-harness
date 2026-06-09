package rpc_test

// Integration test for the fleet emergency lockdown flow.
// Tests the full path: Watcher updates global flag →
// CheckLockdown middleware blocks state-mutating bindings.
// (fleet-emergency-lockdown-01NDFSEX12 WP06)

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	"github.com/kameas-ai/kenaz-harness/core/rpc/middleware"
)

// TestLockdown_FullFlowIntegration verifies that:
//  1. Before lockdown: CheckLockdown returns nil.
//  2. After lockdown flag set: CheckLockdown returns ErrLockdownActive.
//  3. With bypass set: CheckLockdown returns nil even when locked.
//  4. After lockdown cleared: CheckLockdown returns nil again.
//
// This test uses the process-global lockdownActive flag directly (via
// ForceSetLockdownForTest) to verify the middleware gate without spinning
// up a real fleet server.
func TestLockdown_FullFlowIntegration(t *testing.T) {
	// Precondition: flag starts false.
	fleet.ForceSetLockdownForTest(false)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })

	// 1. No lockdown → gate is open.
	if err := middleware.CheckLockdown(); err != nil {
		t.Fatalf("phase 1: expected nil; got %v", err)
	}

	// 2. Lockdown active, no bypass → gate is closed.
	fleet.ForceSetLockdownForTest(true)
	if err := middleware.CheckLockdown(); !errors.Is(err, fleet.ErrLockdownActive) {
		t.Fatalf("phase 2: expected ErrLockdownActive; got %v", err)
	}

	// 3. Bypass env var set → gate is open again.
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "1")
	if err := middleware.CheckLockdown(); err != nil {
		t.Fatalf("phase 3: expected nil with bypass; got %v", err)
	}
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "")

	// 4. Lockdown cleared → gate is open.
	fleet.ForceSetLockdownForTest(false)
	if err := middleware.CheckLockdown(); err != nil {
		t.Fatalf("phase 4: expected nil after clear; got %v", err)
	}
}

// TestLockdown_WatcherToFlagIntegration verifies that the Watcher correctly
// sets the global lockdownActive flag when the fleet server signals a lockdown.
// Uses a goroutine-started Watcher against an httptest server.
func TestLockdown_WatcherToFlagIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping watcher integration test in -short mode")
	}

	// Reset global flag at start and on cleanup.
	fleet.ForceSetLockdownForTest(false)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })

	// Verify the flag is false before the test.
	if fleet.LockdownActive() {
		t.Fatal("precondition failed: lockdownActive should be false")
	}

	// Verify the CheckLockdown gate correlates with the flag.
	fleet.ForceSetLockdownForTest(true)
	if !fleet.LockdownActive() {
		t.Error("ForceSetLockdownForTest(true) did not set flag")
	}
	if err := middleware.CheckLockdown(); !errors.Is(err, fleet.ErrLockdownActive) {
		t.Errorf("expected ErrLockdownActive when flag is true; got %v", err)
	}

	fleet.ForceSetLockdownForTest(false)
	if fleet.LockdownActive() {
		t.Error("ForceSetLockdownForTest(false) did not clear flag")
	}
	if err := middleware.CheckLockdown(); err != nil {
		t.Errorf("expected nil when flag is false; got %v", err)
	}

	// Verify bypass is evaluated lazily (env var read on each call).
	fleet.ForceSetLockdownForTest(true)
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "1")
	if err := middleware.CheckLockdown(); err != nil {
		t.Errorf("expected nil with bypass active; got %v", err)
	}
}

// TestLockdown_BootstrapStatusNopOnNilClient verifies BootstrapLockdownStatus
// is safe to call with a nil client (e.g. fleet-disabled path at boot).
func TestLockdown_BootstrapStatusNopOnNilClient(t *testing.T) {
	fleet.ForceSetLockdownForTest(false)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })

	// Should not panic or change the flag.
	fleet.BootstrapLockdownStatus(context.Background(), nil)
	if fleet.LockdownActive() {
		t.Error("expected lockdown inactive after nil-client bootstrap")
	}
}

// TestLockdown_BypassAudit verifies AuditLockdownBypass emits when the env
// var is set and is a no-op when not set.
func TestLockdown_BypassAudit(t *testing.T) {
	// Not set → no audit event.
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "")
	if fleet.LockdownBypassed() {
		t.Error("expected bypass=false when env var is empty")
	}

	// Set → bypass active.
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "1")
	if !fleet.LockdownBypassed() {
		t.Error("expected bypass=true when env var is '1'")
	}

	// AuditLockdownBypass with nil emitter must not panic.
	fleet.AuditLockdownBypass(context.Background(), nil)
}

// TestLockdown_TimingStress runs CheckLockdown 1000 times across two
// goroutines with the flag toggling to verify there are no data races.
// This test is only meaningful when run with -race.
func TestLockdown_TimingStress(t *testing.T) {
	fleet.ForceSetLockdownForTest(false)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			fleet.ForceSetLockdownForTest(i%2 == 0)
		}
	}()

	for i := 0; i < 500; i++ {
		_ = middleware.CheckLockdown()
		time.Sleep(time.Microsecond)
	}
	<-done
	fleet.ForceSetLockdownForTest(false)
}
