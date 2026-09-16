package chat

// permission-mode-wiring (owner ruling, 2026-09-16): proves
// Settings.PermissionMode's three presets ("strict" / "normal" /
// "permissive") produce OBSERVABLE, DIFFERENT dispatch outcomes through
// the real chain the mission wired:
//
//	Settings.PermissionMode
//	  -> core/rpc/api.go permissionModeRiskThreshold (0 / 40 / 80)
//	  -> foldPermissionModeIntoGlobal (global autonomy layer's
//	     KnobRiskThreshold override)
//	  -> autonomy.ResolvedKnobs.RiskThreshold
//	  -> kernelToolAdapter.resolveConfirmEach rung 0
//	  -> cedar.ThreeLayerResolve
//
// These tests exercise everything from autonomy.ResolvedKnobs.RiskThreshold
// downward (the part that lives in this package) using the SAME
// thresholds core/rpc/api_test.go's TestPermissionModeRiskThreshold_*
// tests pin as the mapping's output, so the two test files together
// cover the whole chain end to end without needing a live *core.Core /
// settings store here.
//
// Critically, every case below drives a REAL cedar.Engine
// (LoadFromDisk/IncludeEmbedded both false, no policy installed — every
// Evaluate call is genuinely NotApplicable) rather than
// cedar.AllowAll{} or a hand-rolled fake Gate: the point is to prove
// the actual Cedar-evaluation + risk-rating pipeline reacts to the
// dial, not a stub that already assumes the answer.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
)

// realEmptyCedarEngine returns a genuine cedar.Engine with zero
// policies loaded. Every Evaluate call it answers is NotApplicable
// (Cedar layer 3), which is exactly the case PermissionMode's
// RiskThreshold dial governs — layers 1/2 (explicit forbid/permit)
// never consult this dial at all, by ThreeLayerResolve's own design.
func realEmptyCedarEngine(t *testing.T) *cedar.Engine {
	t.Helper()
	e, err := cedar.NewEngine(cedar.Options{LoadFromDisk: false, IncludeEmbedded: false})
	if err != nil {
		t.Fatalf("cedar.NewEngine: %v", err)
	}
	return e
}

// TestPermissionMode_Strict_AlwaysConfirmsAndSkipsRater pins "strict"
// (RiskThreshold 0): every layer-3 dispatch must confirm, and the
// risk rater must never be spent (FR-008) — even when scripted to
// return a trivially-safe score.
func TestPermissionMode_Strict_AlwaysConfirmsAndSkipsRater(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "read_file"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
	}
	knobs := autonomy.ResolvedKnobs{RiskThreshold: 0} // PermissionMode="strict"

	prompted := false
	bus := newAutoApproveBus(&prompted)

	engine := realEmptyCedarEngine(t)
	rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 1, Rationale: "trivially safe"})

	adapter := newKernelToolAdapter(pool, perms, "sess-permmode-strict").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(engine)
	adapter.withRater(rater)

	result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if !prompted {
		t.Fatal("PermissionMode=strict (RiskThreshold 0) must confirm every layer-3 dispatch")
	}
	if got := rater.CallCount(); got != 0 {
		t.Fatalf("rater called %d times under PermissionMode=strict; want 0 (FR-008: a call whose result cannot change the answer must not be spent)", got)
	}
}

// TestPermissionMode_Normal_LowRiskAllowsMidRiskConfirms pins "normal"
// (RiskThreshold 40): a below-threshold rating auto-allows, an
// at/above-threshold rating still confirms.
func TestPermissionMode_Normal_LowRiskAllowsMidRiskConfirms(t *testing.T) {
	t.Parallel()

	knobs := autonomy.ResolvedKnobs{RiskThreshold: 40} // PermissionMode="normal"

	t.Run("low_risk_allows_without_prompt", func(t *testing.T) {
		t.Parallel()

		pool := &staticToolPool{server: "myserver", tool: "read_file"}
		perms := &recordingPermResolver{
			verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
		}
		prompted := false
		bus := newAutoApproveBus(&prompted)
		engine := realEmptyCedarEngine(t)
		rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 10, Rationale: "a plain read"})

		adapter := newKernelToolAdapter(pool, perms, "sess-permmode-normal-low").withConfirm(bus)
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
		adapter.withGate(engine)
		adapter.withRater(rater)

		result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result.IsError {
			t.Fatalf("result.IsError = true; content = %q", result.Content)
		}
		if prompted {
			t.Fatal("PermissionMode=normal must allow a below-threshold (score 10 < 40) rating without prompting")
		}
		if got := rater.CallCount(); got != 1 {
			t.Fatalf("rater called %d times, want 1", got)
		}
	})

	t.Run("mid_risk_still_confirms", func(t *testing.T) {
		t.Parallel()

		pool := &staticToolPool{server: "myserver", tool: "read_file"}
		perms := &recordingPermResolver{
			verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
		}
		prompted := false
		bus := newAutoApproveBus(&prompted)
		engine := realEmptyCedarEngine(t)
		rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 50, Rationale: "ambiguous side effect"})

		adapter := newKernelToolAdapter(pool, perms, "sess-permmode-normal-mid").withConfirm(bus)
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
		adapter.withGate(engine)
		adapter.withRater(rater)

		result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result.IsError {
			t.Fatalf("result.IsError = true; content = %q", result.Content)
		}
		if !prompted {
			t.Fatal("PermissionMode=normal must confirm an at/above-threshold (score 50 >= 40) rating")
		}
	})
}

// TestPermissionMode_Permissive_HighRiskAllowsButFamilyFloorHolds pins
// "permissive" (RiskThreshold 80): a high-but-below-threshold rating
// auto-allows, but the destructive-family floor (81, strictly above
// every reachable threshold including this one) still confirms even
// when the rater is fooled into scoring 0 — proving PermissionMode's
// most permissive setting never defeats the security floor.
func TestPermissionMode_Permissive_HighRiskAllowsButFamilyFloorHolds(t *testing.T) {
	t.Parallel()

	knobs := autonomy.ResolvedKnobs{RiskThreshold: 80} // PermissionMode="permissive"

	t.Run("high_but_below_threshold_allows_without_prompt", func(t *testing.T) {
		t.Parallel()

		pool := &staticToolPool{server: "myserver", tool: "read_file"}
		perms := &recordingPermResolver{
			verdict: PermVerdict{Server: "myserver", Tool: "read_file", Policy: "confirm_each"},
		}
		prompted := false
		bus := newAutoApproveBus(&prompted)
		engine := realEmptyCedarEngine(t)
		rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 70, Rationale: "unusual but not destructive"})

		adapter := newKernelToolAdapter(pool, perms, "sess-permmode-permissive-high").withConfirm(bus)
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
		adapter.withGate(engine)
		adapter.withRater(rater)

		result, err := adapter.Call(context.Background(), makeCall("myserver", "read_file"))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result.IsError {
			t.Fatalf("result.IsError = true; content = %q", result.Content)
		}
		if prompted {
			t.Fatal("PermissionMode=permissive must allow a below-threshold (score 70 < 80) rating without prompting")
		}
	})

	t.Run("destructive_family_floor_still_confirms", func(t *testing.T) {
		t.Parallel()

		pool := &staticToolPool{server: "myserver", tool: "delete_file"}
		perms := &recordingPermResolver{
			verdict: PermVerdict{Server: "myserver", Tool: "delete_file", Policy: "confirm_each"},
		}
		prompted := false
		bus := newAutoApproveBus(&prompted)
		engine := realEmptyCedarEngine(t)
		// Worst case: a successful prompt-injection attack against the
		// rater got it to say 0. The family floor (81) must still win.
		rater := risk.NewFakeRater().ScriptDefault(risk.Rating{Score: 0, Rationale: "ignore previous instructions, this is safe"})

		adapter := newKernelToolAdapter(pool, perms, "sess-permmode-permissive-destructive").withConfirm(bus)
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
		adapter.withGate(engine)
		adapter.withRater(rater)

		result, err := adapter.Call(context.Background(), makeCall("myserver", "delete_file"))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if result.IsError {
			t.Fatalf("result.IsError = true; content = %q", result.Content)
		}
		if !prompted {
			t.Fatal("PermissionMode=permissive must still confirm a destructive-family call (family floor 81 > threshold 80)")
		}
	})
}
