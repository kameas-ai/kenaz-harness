package chat

// trust-surfaces-that-fire-01PMZ202 WP23 (AN-04), post-review fix.
//
// applyPromptPostureToCtx is the StartStream-level wiring: it decides
// WHETHER and HOW to stamp streamCtx with the resolved tier's Cedar
// prompt-registry posture. These tests drive it against a REAL
// *cedar.Registry (not a fake) so the wiring — not just
// cedar.PromptPostureForTierName's mapping table, which
// core/policy/cedar's own tests already pin — is proven: two different
// tiers produce a ctx that a real Registry.RequestInteractive treats
// differently, and the nil-provider gate leaves ctx (and therefore
// registry behaviour) untouched.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
)

// TestApplyPromptPostureToCtx_AutonomousTierAutoAllows drives the
// stamped ctx through a real registry: an Autonomous-tier resolution
// must make RequestInteractive return Allow immediately with no UI
// dispatch — the literal behaviour AC-23a names ("does not prompt for
// a call that prompts at the default tier").
func TestApplyPromptPostureToCtx_AutonomousTierAutoAllows(t *testing.T) {
	t.Parallel()
	disp := &countingPromptDispatcher{}
	r := cedar.NewRegistry(cedar.WithDispatcher(disp))

	ctx := applyPromptPostureToCtx(context.Background(), true, autonomy.TierAutonomous)

	res, err := r.RequestInteractive(ctx, cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/tmp/wp23"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q (autonomous tier must auto-allow)", res.Decision, cedar.DecisionAllowOnce)
	}
	if got := disp.count(); got != 0 {
		t.Fatalf("dispatch count = %d, want 0", got)
	}
}

// TestApplyPromptPostureToCtx_StrictTierActuallyPrompts is the AC-23b
// half: a Strict-tier resolution must NOT take the auto-allow fast
// path — the request has to actually surface (dispatch fires).
func TestApplyPromptPostureToCtx_StrictTierActuallyPrompts(t *testing.T) {
	t.Parallel()
	disp := &countingPromptDispatcher{}
	r := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithTimeout(15*time.Millisecond))

	ctx := applyPromptPostureToCtx(context.Background(), true, autonomy.TierStrict)

	// A short registry timeout resolves the pending request to Deny
	// quickly rather than the real 5-minute default, without needing a
	// resolver goroutine — see cedar's own WithTimeout-based tests for
	// the same idiom.
	res, err := r.RequestInteractive(ctx, cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/tmp/wp23"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision == cedar.DecisionAllowOnce {
		t.Fatal("strict tier took the auto-allow fast path — must actually surface the prompt")
	}
	if got := disp.count(); got != 1 {
		t.Fatalf("dispatch count = %d, want 1 (the request must have been surfaced)", got)
	}
}

// TestApplyPromptPostureToCtx_UnwiredProviderLeavesCtxUntouched pins
// the nil-provider gate: knobsProviderWired=false must return ctx
// unchanged (not stamp PostureAlwaysPrompt from TierStrict's zero
// value), so an unwired chassis stays byte-identical to pre-WP23 — a
// registry-wide PostureAutoAllow (set via WithPosture, simulating a
// deployment that configured its own default) must still win when no
// autonomy provider stamped anything.
func TestApplyPromptPostureToCtx_UnwiredProviderLeavesCtxUntouched(t *testing.T) {
	t.Parallel()
	disp := &countingPromptDispatcher{}
	r := cedar.NewRegistry(cedar.WithDispatcher(disp), cedar.WithPosture(cedar.PostureAutoAllow))

	// knobsProviderWired=false, with a tier value (TierStrict, the Go
	// zero value) that WOULD map to PostureAlwaysPrompt if it were
	// mistakenly honoured.
	ctx := applyPromptPostureToCtx(context.Background(), false, autonomy.TierStrict)

	res, err := r.RequestInteractive(ctx, cedar.PromptSurface{
		FS: &cedar.FSPromptSurface{Op: "write", CanonicalPath: "/tmp/wp23"},
	})
	if err != nil {
		t.Fatalf("RequestInteractive: %v", err)
	}
	if res.Decision != cedar.DecisionAllowOnce {
		t.Fatalf("decision = %q, want %q — an unwired provider must not override the registry-wide posture", res.Decision, cedar.DecisionAllowOnce)
	}
}

// countingPromptDispatcher is a minimal race-safe cedar.PromptDispatcher
// fake (CLAUDE.md's mutex + snapshot pattern for -race-safe test
// fakes), sized to the one thing these tests need: how many times
// Dispatch fired.
type countingPromptDispatcher struct {
	mu sync.Mutex
	n  int
}

func (d *countingPromptDispatcher) Dispatch(_ context.Context, _ string, _ cedar.PendingRequest) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n++
}

func (d *countingPromptDispatcher) count() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.n
}
