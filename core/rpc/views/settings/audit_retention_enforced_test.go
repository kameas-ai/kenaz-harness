package settings

import (
	"context"
	"testing"
)

// TestGetAuditSettings_RetentionEnforcedDerivedFromWiring is WP05's
// required Go test, matching AC-015's shape: the RetentionEnforced
// boolean must come from SetAuditRetentionEnforced (the wiring),
// never from a literal baked into GetAuditSettings.
//
// Mutation: hardcode `RetentionEnforced: false` (or any constant)
// inside GetAuditSettings, ignoring a.auditRetentionEnforced — the
// "wired true" case below must go red.
func TestGetAuditSettings_RetentionEnforcedDerivedFromWiring(t *testing.T) {
	ctx := context.Background()

	unwired := NewAPI(nil)
	got, err := unwired.GetAuditSettings(ctx)
	if err != nil {
		t.Fatalf("GetAuditSettings (unwired): %v", err)
	}
	if got.RetentionEnforced {
		t.Errorf("RetentionEnforced = true with SetAuditRetentionEnforced never called, want false (the honest pre-UNIT-8 default)")
	}

	wiredFalse := NewAPI(nil)
	wiredFalse.SetAuditRetentionEnforced(false)
	got, err = wiredFalse.GetAuditSettings(ctx)
	if err != nil {
		t.Fatalf("GetAuditSettings (wired false): %v", err)
	}
	if got.RetentionEnforced {
		t.Errorf("RetentionEnforced = true after SetAuditRetentionEnforced(false), want false")
	}

	wiredTrue := NewAPI(nil)
	wiredTrue.SetAuditRetentionEnforced(true)
	got, err = wiredTrue.GetAuditSettings(ctx)
	if err != nil {
		t.Fatalf("GetAuditSettings (wired true): %v", err)
	}
	if !got.RetentionEnforced {
		t.Errorf("RetentionEnforced = false after SetAuditRetentionEnforced(true), want true — a literal inside GetAuditSettings would fail exactly this case")
	}

	// The strategy/window default path must ALSO carry the wired fact —
	// it is a separate early-return branch in GetAuditSettings and is
	// exactly the kind of second code path a partial fix leaves behind.
	wiredTrueDefaults := NewAPI(nil)
	wiredTrueDefaults.SetAuditRetentionEnforced(true)
	got, err = wiredTrueDefaults.GetAuditSettings(ctx)
	if err != nil {
		t.Fatalf("GetAuditSettings (wired true, defaults path): %v", err)
	}
	if got.Strategy != "keep_forever" {
		t.Fatalf("Strategy = %q, want keep_forever (defaults path precondition)", got.Strategy)
	}
	if !got.RetentionEnforced {
		t.Errorf("RetentionEnforced = false on the defaults-early-return path with retention wired true, want true")
	}

	// SetAuditSettings must not let a caller-supplied RetentionEnforced
	// value leak through and override the wiring on the next Get.
	if err := wiredTrue.SetAuditSettings(ctx, AuditSettings{Strategy: "keep_forever", WindowDays: 90, RetentionEnforced: false}); err != nil {
		t.Fatalf("SetAuditSettings: %v", err)
	}
	got, err = wiredTrue.GetAuditSettings(ctx)
	if err != nil {
		t.Fatalf("GetAuditSettings after SetAuditSettings: %v", err)
	}
	if !got.RetentionEnforced {
		t.Errorf("RetentionEnforced = false after SetAuditSettings tried to smuggle false through, want true (still wired true)")
	}
}
