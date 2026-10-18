package chat

// WP09 (risk-rated-autonomy-01PMRA01) — proof that
// tool-dispatch-never-allow-recommended.cedar's layer-1 forbids deny the
// call AND the risk rater is never invoked, exercised through the real
// dispatch path (kernelToolAdapter.Call), not just cedar.ThreeLayerResolve
// in isolation. The Cedar-layer half of this proof
// (TestToolDispatchNeverAllowRecommended_DeniesAtLayer1) lives in
// core/policy/cedar; this file is the "rater never invoked" half plan.md's
// verification strategy calls for ("assert call counts, not just
// outcomes").

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/policy/risk"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// newRealEngineWithNeverAllowPolicy builds a real *cedar.Engine with the
// embedded default bundle PLUS tool-dispatch-never-allow-recommended.cedar
// activated the same way an operator would — copied into
// <DataDir>/policy/ and picked up by a real Engine.Reload.
func newRealEngineWithNeverAllowPolicy(t *testing.T) *cedar.Engine {
	t.Helper()
	dataDir := t.TempDir()
	policyDir := filepath.Join(dataDir, cedar.PolicyDir)
	if err := os.MkdirAll(policyDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	src, err := cedar.PoliciesFS.ReadFile("policies/tool-dispatch-never-allow-recommended.cedar")
	if err != nil {
		t.Fatalf("reading embedded tool-dispatch-never-allow-recommended.cedar: %v", err)
	}
	if err := os.WriteFile(filepath.Join(policyDir, "tool-dispatch-never-allow-recommended.cedar"), src, 0o644); err != nil {
		t.Fatalf("writing to DataDir/policy: %v", err)
	}
	e, err := cedar.NewEngine(cedar.Options{DataDir: dataDir, LoadFromDisk: true, IncludeEmbedded: true})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

// TestKernelToolAdapter_NeverAllowPolicy_RaterNeverInvoked dispatches a
// tool call whose name matches one of tool-dispatch-never-allow-
// recommended.cedar's forbid patterns (force-push) at the autonomous tier
// — the tier where, absent this file, layer 3's rater would otherwise be
// consulted. Asserts the call is denied AND the rater's call count stays
// zero: a Cedar forbid at layer 1 must short-circuit before the rater is
// ever constructed or invoked, regardless of what a (possibly
// prompt-injected) rating would have said.
func TestKernelToolAdapter_NeverAllowPolicy_RaterNeverInvoked(t *testing.T) {
	t.Parallel()

	engine := newRealEngineWithNeverAllowPolicy(t)
	fakeRater := risk.NewFakeRater()
	// Script an artificially LOW score for this exact tool — if the
	// rater's result somehow leaked past the layer-1 forbid, this would
	// resolve to Allow (0 < 80). The test must never observe that.
	fakeRater.ScriptFor("force_push_branch", risk.Rating{Score: 0, Rationale: "would-be-allow if reached"})

	pool := &staticToolPool{server: "gitmcp", tool: "force_push_branch"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "gitmcp", Tool: "force_push_branch", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous)

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-never-allow").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(engine)
	adapter.withRater(fakeRater)

	result, err := adapter.Call(context.Background(), makeCall("gitmcp", "force_push_branch"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true — tool-dispatch-never-allow-recommended.cedar's "+
			"force-push forbid must deny this call at layer 1; content = %q", result.Content)
	}
	if prompted {
		t.Fatal("user was prompted for a layer-1-forbidden call — a forbid must never reach the prompt")
	}
	if got := fakeRater.CallCount(); got != 0 {
		t.Fatalf("rater called %d times, want 0 — a Cedar forbid at layer 1 must short-circuit before "+
			"the rater is ever consulted, regardless of what a (possibly prompt-injected) rating would "+
			"have said", got)
	}
}

// TestKernelToolAdapter_NeverAllowPolicy_UnrelatedToolStillRates pins the
// negative control: an ordinary tool name NOT matching any never-allow
// pattern is unaffected by this file and still reaches the rater as
// normal at the autonomous tier (below-threshold score allows without a
// prompt) — proving the file's forbids are scoped to their patterns, not
// a blanket "never consult the rater" regression.
func TestKernelToolAdapter_NeverAllowPolicy_UnrelatedToolStillRates(t *testing.T) {
	t.Parallel()

	engine := newRealEngineWithNeverAllowPolicy(t)
	fakeRater := risk.NewFakeRater()
	fakeRater.ScriptFor("read_status", risk.Rating{Score: 10, Rationale: "trivial read"})

	pool := &staticToolPool{server: "gitmcp", tool: "read_status"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "gitmcp", Tool: "read_status", Policy: "confirm_each"},
	}
	knobs := knobsFromTier(autonomy.TierAutonomous)

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: false})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-never-allow-negative").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })
	adapter.withGate(engine)
	adapter.withRater(fakeRater)

	result, err := adapter.Call(context.Background(), makeCall("gitmcp", "read_status"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q (an unrelated, low-scored tool must proceed)", result.Content)
	}
	if prompted {
		t.Fatal("user was prompted despite a below-threshold rating — the rater's result should have allowed this")
	}
	if got := fakeRater.CallCount(); got != 1 {
		t.Fatalf("rater called %d times, want 1 — an unrelated tool must still reach layer 3's rater", got)
	}
}
