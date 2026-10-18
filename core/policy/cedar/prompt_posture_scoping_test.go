package cedar

// trust-surfaces-that-fire-01PMZ202 WP23, post-review fix.
//
// An earlier version of this WP wired the resolved autonomy tier into
// Registry.posture via SetPosture, called once per StartStream.
// Registry is an explicit process-wide singleton (core/rpc/api.go's
// a.promptRegistry doc comment), so that made posture global state
// keyed by whichever session most recently resolved a turn — a
// Strict-tier session's tool confirmations could be silently
// auto-allowed the instant a concurrently-running session or a
// synchronously-spawned subagent with a looser tier called StartStream.
// A reviewer reproduced this as a genuine data race under -race too:
// RequestInteractive's two r.posture reads had no lock, unlike
// SetPosture's write and unlike Posture()'s own accessor.
//
// The fix threads posture through ctx (WithPromptPosture /
// promptPostureFromContext) instead of a shared field: ctx values are
// immutable and scoped to exactly the goroutine chain that derived
// them, the same mechanism runposture.Unattended already uses for an
// analogous per-run marker. These tests are the direct regression
// proof: two goroutines sharing ONE *Registry, driven with DIFFERENT
// ctx-stamped postures, must each see only their own.

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestPromptPostureForTierName_TiersMapToDistinctPostures pins the
// mapping table itself.
func TestPromptPostureForTierName_TiersMapToDistinctPostures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tierName string
		want     PromptPosture
	}{
		{"strict", PostureAlwaysPrompt},
		{"cautious", PostureAlwaysPrompt},
		{"default", PostureDefault},
		{"bold", PostureAutoAllow},
		{"autonomous", PostureAutoAllow},
		{"", PostureDefault},
		{"bogus-tier", PostureDefault},
	}
	for _, tc := range cases {
		if got := PromptPostureForTierName(tc.tierName); got != tc.want {
			t.Errorf("PromptPostureForTierName(%q) = %q, want %q", tc.tierName, got, tc.want)
		}
	}
}

// TestWithPromptPosture_OverridesRegistryWideDefault confirms a
// ctx-stamped posture wins over whatever the registry's own r.posture
// currently is — the AutoAllow fast path fires even though the
// registry itself was constructed with no posture option (a plain
// PostureDefault).
func TestWithPromptPosture_OverridesRegistryWideDefault(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp))

	ctx := WithPromptPosture(context.Background(), PostureAutoAllow)
	res, err := r.RequestInteractive(ctx, makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q", res.Decision, DecisionAllowOnce)
	}
	if got := len(disp.snapshot()); got != 0 {
		t.Fatalf("dispatch count = %d, want 0 (ctx-stamped auto-allow must skip the UI prompt)", got)
	}
}

// TestRequestInteractive_NoCtxStampFallsBackToRegistryPosture is the
// other half: a call with no WithPromptPosture stamp must behave
// exactly as it did before this mechanism existed — driven by the
// registry-wide r.posture (WithPosture / SetPosture), unchanged.
func TestRequestInteractive_NoCtxStampFallsBackToRegistryPosture(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	r := NewRegistry(WithDispatcher(disp), WithPosture(PostureAutoAllow))

	res, err := r.RequestInteractive(context.Background(), makeBashSurface("ls"))
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q (unstamped call must fall back to the registry-wide posture)", res.Decision, DecisionAllowOnce)
	}
}

// TestConcurrentSessions_DoNotLeakPostureAcrossEachOther is the exact
// scenario the review flagged: a Strict-tier session and an
// Autonomous-tier session sharing ONE *Registry — the process-wide
// singleton every real deployment has exactly one of — running
// RequestInteractive CONCURRENTLY. Before the ctx-scoping fix, whichever
// session's SetPosture call landed last on the shared field decided
// BOTH outcomes; a Strict-tier confirmation could resolve to
// auto-allow. Run under -race (CI's `go test -race`) to also prove the
// unsynchronized-read data race the reviewer reported is gone.
func TestConcurrentSessions_DoNotLeakPostureAcrossEachOther(t *testing.T) {
	t.Parallel()
	disp := newRecordingDispatcher()
	// A short registry-wide timeout stands in for "the strict session's
	// prompt actually surfaced and nobody answered it" — what matters is
	// that it does NOT take the auto-allow fast path, not the literal
	// duration.
	r := NewRegistry(WithDispatcher(disp), WithTimeout(15*time.Millisecond))

	const iterations = 20
	var wg sync.WaitGroup
	wg.Add(2)

	autoAllowOK := true
	strictNeverAutoAllowed := true

	// "Session A" — Autonomous tier, ctx-stamped AutoAllow — hammers
	// RequestInteractive on its own goroutine.
	go func() {
		defer wg.Done()
		ctx := WithPromptPosture(context.Background(), PostureAutoAllow)
		for i := 0; i < iterations; i++ {
			res, err := r.RequestInteractive(ctx, makeBashSurface("ls"))
			if err != nil || res.Decision != DecisionAllowOnce {
				autoAllowOK = false
			}
		}
	}()

	// "Session B" — Strict tier, ctx-stamped AlwaysPrompt — hammers
	// RequestInteractive concurrently on its own goroutine. If Session
	// A's posture ever leaked onto this goroutine's read, at least one
	// of these would come back DecisionAllowOnce via the auto-allow fast
	// path instead of Deny/timeout.
	go func() {
		defer wg.Done()
		ctx := WithPromptPosture(context.Background(), PostureAlwaysPrompt)
		for i := 0; i < iterations; i++ {
			res, err := r.RequestInteractive(ctx, makeBashSurface("ls"))
			if err != nil {
				strictNeverAutoAllowed = false
				continue
			}
			if res.Decision == DecisionAllowOnce {
				strictNeverAutoAllowed = false
			}
		}
	}()

	wg.Wait()

	if !autoAllowOK {
		t.Error("session A (autonomous, ctx-stamped auto-allow) did not consistently auto-allow — cross-goroutine interference")
	}
	if !strictNeverAutoAllowed {
		t.Error("session B (strict, ctx-stamped always-prompt) was auto-allowed at least once — posture leaked from the concurrently-running autonomous session (the exact defect this test guards)")
	}
}
