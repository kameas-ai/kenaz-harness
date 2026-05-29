package middleware

import (
	"errors"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/fleet"
)

// TestCheckLockdown_NotActive: flag is false → no error.
func TestCheckLockdown_NotActive(t *testing.T) {
	// Ensure global flag is false.
	fleet.ForceSetLockdownForTest(false)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })

	if err := CheckLockdown(); err != nil {
		t.Errorf("expected nil error when lockdown inactive; got %v", err)
	}
}

// TestCheckLockdown_Active: flag is true, no bypass → ErrLockdownActive.
func TestCheckLockdown_Active(t *testing.T) {
	fleet.ForceSetLockdownForTest(true)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "")

	err := CheckLockdown()
	if !errors.Is(err, fleet.ErrLockdownActive) {
		t.Errorf("expected ErrLockdownActive; got %v", err)
	}
}

// TestCheckLockdown_ActiveButBypassed: flag true + bypass → no error.
func TestCheckLockdown_ActiveButBypassed(t *testing.T) {
	fleet.ForceSetLockdownForTest(true)
	t.Cleanup(func() { fleet.ForceSetLockdownForTest(false) })
	t.Setenv("HARNESS_FLEET_LOCKDOWN_BYPASS", "1")

	if err := CheckLockdown(); err != nil {
		t.Errorf("expected nil when bypass set; got %v", err)
	}
}
