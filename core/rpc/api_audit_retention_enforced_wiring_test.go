package rpc

// audit-that-tells-the-truth-01PMZA10 UNIT-8: the production-wiring
// half of AC-006/D-8. core/rpc/views/settings's
// TestGetAuditSettings_RetentionEnforcedDerivedFromWiring (landed with
// UNIT-4, v0.65.0) already proves the MECHANISM — GetAuditSettings
// reflects whatever SetAuditRetentionEnforced was called with, never a
// literal. What that test cannot see is WHERE core/rpc/api.go calls
// SetAuditRetentionEnforced FROM: before this unit it was a hardcoded
// `false` (the honest UNIT-4 state — nothing swept yet); UNIT-8 changed
// that one call to `a.localAuditRetentionScheduler != nil`. This test
// is the fact-driven claim in AuditSettingsPanel.vue's own script-block
// comment made concrete: "It is false until UNIT-8 lands a real sweep;
// when it flips, this file needs NO further edit." The panel file
// genuinely was not touched (see the WP10 commit) — this proves the
// input it reads now flips to true in real production wiring.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
)

// TestAuditRetentionEnforced_TrueWithRealBackend_FalseWithout is the
// production-wiring proof: with a real Core + DataDir (so
// localAuditRetentionScheduler is constructed), GetAuditSettings
// reports RetentionEnforced: true — the AuditSettingsPanel.vue
// archive_after_window / delete_after_window copy lines are now
// genuinely true, not aspirational, with ZERO frontend file touched.
// With no Core (the test-chassis path this repo's New(nil) supports),
// it stays honestly false.
//
// Mutation: revert core/rpc/api.go's
// `settingsImpl.SetAuditRetentionEnforced(a.localAuditRetentionScheduler != nil)`
// to the old hardcoded `false` — the "real backend" half of this test
// must go red.
func TestAuditRetentionEnforced_TrueWithRealBackend_FalseWithout(t *testing.T) {
	ctx := context.Background()

	t.Run("real backend", func(t *testing.T) {
		sandboxUserConfigDir(t)
		c, err := core.New(core.Options{DataDir: t.TempDir()})
		if err != nil {
			t.Fatalf("core.New: %v", err)
		}
		api := New(c)
		if api.localAuditRetentionScheduler == nil {
			t.Fatal("localAuditRetentionScheduler is nil with a real DataDir — construction gate did not fire")
		}
		got, err := api.settingsImpl.GetAuditSettings(ctx)
		if err != nil {
			t.Fatalf("GetAuditSettings: %v", err)
		}
		if !got.RetentionEnforced {
			t.Error("RetentionEnforced = false with a real backend, want true — " +
				"AuditSettingsPanel.vue's archive_after_window/delete_after_window copy would still claim " +
				"\"not yet available in this build\" despite UNIT-8's real sweep existing")
		}
	})

	t.Run("no core (test chassis)", func(t *testing.T) {
		api := New(nil)
		if api.localAuditRetentionScheduler != nil {
			t.Fatal("localAuditRetentionScheduler is non-nil with no Core — construction gate should not have fired")
		}
		got, err := api.settingsImpl.GetAuditSettings(ctx)
		if err != nil {
			t.Fatalf("GetAuditSettings: %v", err)
		}
		if got.RetentionEnforced {
			t.Error("RetentionEnforced = true with no backend at all, want false")
		}
	})
}
