package rpc

// trust-surfaces-that-fire-01PMZ202 WP23 (AN-04).
//
// cedar.NewRegistry was constructed with WithDispatcher only
// (core/rpc/api.go's a.promptRegistry assignment); cedar.WithPosture /
// (*Registry).SetPosture had zero non-test callers, so prompt.go:838's
// `if r.posture == PostureAutoAllow` and :847's
// `if r.posture != PostureAlwaysPrompt` were frozen at the empty string
// forever — an autonomy tier the user selected never changed whether a
// tool call prompted for confirmation.
//
// These tests pin the wiring — not just promptPostureForTier's mapping
// table — against a REAL *cedar.Registry (not a fake), so a future edit
// that silently drops the SetPosture call reddens a test instead of
// only reverting a comment. Mirrors the existing cedar-package coverage
// of WithPosture/SetPosture themselves (prompt_test.go's
// TestPostureAutoAllow_* / TestSetPosture_DynamicUpdate) one level up,
// at the production call site this WP adds.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestPromptPostureForTier_TiersMapToDistinctPostures pins the mapping
// table itself: Strict/Cautious must map to AlwaysPrompt,
// Bold/Autonomous to AutoAllow, Default (and any unrecognised tier) to
// the v0.3.0-baseline PostureDefault.
func TestPromptPostureForTier_TiersMapToDistinctPostures(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tier autonomy.Tier
		want cedar.PromptPosture
	}{
		{autonomy.TierStrict, cedar.PostureAlwaysPrompt},
		{autonomy.TierCautious, cedar.PostureAlwaysPrompt},
		{autonomy.TierDefault, cedar.PostureDefault},
		{autonomy.TierBold, cedar.PostureAutoAllow},
		{autonomy.TierAutonomous, cedar.PostureAutoAllow},
		{autonomy.Tier(99), cedar.PostureDefault}, // unknown tier degrades safely
	}
	for _, tc := range cases {
		if got := promptPostureForTier(tc.tier); got != tc.want {
			t.Errorf("promptPostureForTier(%v) = %q, want %q", tc.tier, got, tc.want)
		}
	}
}

// TestSetPromptRegistryPostureFromKnobs_AutonomousSkipsThePrompt drives
// the wiring end to end against a real registry: an Autonomous-tier
// resolution must make RequestInteractive return Allow immediately,
// with no broker dispatch and nothing left pending — the literal
// behaviour AC-23a names ("does not prompt for a call that prompts at
// the default tier").
func TestSetPromptRegistryPostureFromKnobs_AutonomousSkipsThePrompt(t *testing.T) {
	t.Parallel()
	disp := &countingDispatcher{}
	r := cedar.NewRegistry(cedar.WithDispatcher(disp))

	setPromptRegistryPostureFromKnobs(autonomy.ResolvedKnobs{EffectiveTier: autonomy.TierAutonomous}, r)

	res, err := r.RequestInteractive(context.Background(), cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/tmp/wp23"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q (autonomous tier must auto-allow)", res.Decision, cedar.DecisionAllowOnce)
	}
	if got := disp.count(); got != 0 {
		t.Fatalf("dispatch count = %d, want 0 (auto-allow must skip the UI prompt entirely)", got)
	}
	if r.PendingCount() != 0 {
		t.Fatalf("pending = %d, want 0", r.PendingCount())
	}
}

// TestSetPromptRegistryPostureFromKnobs_StrictActuallyPrompts is the
// AC-23b half: a Strict-tier resolution must NOT auto-allow — the
// request has to actually surface to the UI (dispatch fires, the
// request parks) rather than resolving instantly the way the
// Autonomous case above does. A short registry timeout stands in for
// "nobody answers the modal" so the test doesn't wait the real 5
// minutes; what's being asserted is that the call does NOT take the
// auto-allow fast path, not the literal timeout duration.
func TestSetPromptRegistryPostureFromKnobs_StrictActuallyPrompts(t *testing.T) {
	t.Parallel()
	disp := &countingDispatcher{}
	r := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithTimeout(30*time.Millisecond))

	setPromptRegistryPostureFromKnobs(autonomy.ResolvedKnobs{EffectiveTier: autonomy.TierStrict}, r)

	res, err := r.RequestInteractive(context.Background(), cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/tmp/wp23"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionDeny || res.Reason != "timeout" {
		t.Fatalf("res = %+v, want Deny/timeout — a strict-tier session must actually surface the prompt instead of auto-allowing", res)
	}
	if got := disp.count(); got != 1 {
		t.Fatalf("dispatch count = %d, want 1 (the request must have been surfaced to the UI)", got)
	}
}

// TestSetPromptRegistryPostureFromKnobs_NilRegistryIsANoop matches
// every other optional-dependency degrade in this file: a nil registry
// (boot-time construction failure / nil-core test chassis) must not
// panic.
func TestSetPromptRegistryPostureFromKnobs_NilRegistryIsANoop(t *testing.T) {
	t.Parallel()
	setPromptRegistryPostureFromKnobs(autonomy.ResolvedKnobs{EffectiveTier: autonomy.TierAutonomous}, nil)
}

// countingDispatcher is a minimal race-safe cedar.PromptDispatcher fake
// — CLAUDE.md's race-safe-test-fake pattern (mutex + snapshot), sized
// down to the one thing these tests need: how many times Dispatch
// fired. go test -race exercises this concurrently (RequestInteractive
// dispatches from the calling goroutine here, but the mutex keeps this
// safe under -race regardless of call pattern).
type countingDispatcher struct {
	mu sync.Mutex
	n  int
}

func (d *countingDispatcher) Dispatch(_ context.Context, _ string, _ cedar.PendingRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n++
}

func (d *countingDispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}
