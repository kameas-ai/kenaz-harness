package rpc

// harness_self_mcp_disabled_wiring_test.go — harness-self-attach-01PMHS01
// UNIT-7, AC-008. Drives the REAL production wire: rpc.New's
// onboardingSettingsDialAdapter reading through the REAL
// settings.SettingsStore, not a hand-built stand-in.
//
// Falsifiable: reverting onboardingSettingsDialAdapter.
// IsHarnessSelfMCPDisabled to `return false, nil` (the pre-UNIT-7 shape)
// makes TestHarnessSelfMCPDisabled_KillSwitchReadsTheStore fail, because
// the persisted true value would no longer surface.

import (
	"context"
	"testing"
)

// TestHarnessSelfMCPDisabled_KillSwitchReadsTheStore is AC-008's core
// assertion for the read side: flipping the persisted setting changes
// OnboardingAPI.State()'s HarnessSelfMCPDisabled field, through the real
// settings store, in a fresh read — no restart required.
//
// Mutation: revert onboardingSettingsDialAdapter.IsHarnessSelfMCPDisabled
// to `return false, nil`. Must fail — verified by hand below.
func TestHarnessSelfMCPDisabled_KillSwitchReadsTheStore(t *testing.T) {
	_, api := bootAPIWithCore(t, t.TempDir(), "")
	ctx := context.Background()

	// Default (fresh install, key absent): false — the same behaviour
	// every install had before this field existed.
	st, err := api.Onboarding().State(ctx)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if st.HarnessSelfMCPDisabled {
		t.Fatal("fresh install: HarnessSelfMCPDisabled = true, want false (absent must read as the pre-existing default)")
	}

	// Flip it true through the real store — the same store the resolver
	// and the FileStore-backed settings API share.
	store := api.SettingsStore()
	if store == nil {
		t.Fatal("api.SettingsStore() is nil — cannot drive the real store")
	}
	if err := store.SaveHarnessSelfMCPDisabled(true); err != nil {
		t.Fatalf("SaveHarnessSelfMCPDisabled(true): %v", err)
	}

	// Re-read through the SAME live API instance — no restart, no new
	// API construction. If the adapter cached the value at construction
	// time rather than reading live, this assertion is what would catch
	// it.
	st, err = api.Onboarding().State(ctx)
	if err != nil {
		t.Fatalf("State after enabling kill switch: %v", err)
	}
	if !st.HarnessSelfMCPDisabled {
		t.Fatal("HarnessSelfMCPDisabled did not read back true after SaveHarnessSelfMCPDisabled(true) — " +
			"this is exactly the pre-UNIT-7 hardcoded-false shape")
	}

	// Flip back false and confirm the read tracks it in both directions,
	// not just false->true.
	if err := store.SaveHarnessSelfMCPDisabled(false); err != nil {
		t.Fatalf("SaveHarnessSelfMCPDisabled(false): %v", err)
	}
	st, err = api.Onboarding().State(ctx)
	if err != nil {
		t.Fatalf("State after disabling kill switch: %v", err)
	}
	if st.HarnessSelfMCPDisabled {
		t.Fatal("HarnessSelfMCPDisabled stayed true after SaveHarnessSelfMCPDisabled(false)")
	}
}
