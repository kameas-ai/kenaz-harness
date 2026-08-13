package chat

// WP04 — autonomy-dial toolloop posture tests.
//
// These tests verify that kernelToolAdapter reads the resolved
// AutoApproveFamilies knob before each tool call and correctly bypasses
// the interactive-prompt path for autonomous tiers while preserving
// Cedar deny as the floor.

import (
	"context"
	"testing"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// recordingPermResolver captures Resolve calls and returns a scripted verdict.
type recordingPermResolver struct {
	calls   []permResolveCall
	verdict PermVerdict
}

type permResolveCall struct {
	sessionID, server, tool string
}

func (r *recordingPermResolver) Resolve(_ context.Context, sessionID, server, tool string) (PermVerdict, error) {
	r.calls = append(r.calls, permResolveCall{sessionID: sessionID, server: server, tool: tool})
	return r.verdict, nil
}

// staticToolPool is a minimal ToolPool that reports a single tool and
// succeeds on Call.
type staticToolPool struct {
	server string
	tool   string
	result []byte
}

func (p *staticToolPool) Tools(_ context.Context) ([]ToolEntry, error) {
	return []ToolEntry{{Server: p.server, Name: p.tool}}, nil
}

func (p *staticToolPool) Call(_ context.Context, _, _ string, _ []byte) ([]byte, error) {
	if p.result == nil {
		return []byte(`"ok"`), nil
	}
	return p.result, nil
}

// makeCall builds a ToolCall for the given server+tool pair using the
// "__" separator convention.
func makeCall(server, tool string) coreag.ToolCall {
	return coreag.ToolCall{Name: server + "__" + tool}
}

// knobsFromTier returns a ResolvedKnobs pre-populated with the preset
// for the given tier by running autonomy.Resolve with that tier on all
// three layers.
func knobsFromTier(t autonomy.Tier) autonomy.ResolvedKnobs {
	tier := t
	layer := autonomy.Layer{Level: &tier, Overrides: map[autonomy.Knob]any{}}
	return autonomy.Resolve(layer, autonomy.Layer{}, autonomy.Layer{})
}

// TestKernelToolAdapter_AutonomousSkipsPrompt verifies that when the
// resolved knobs include FamilyShellSafe (autonomous / bold tier), the
// permission resolver's "confirm_each" verdict is bypassed and the call
// succeeds — simulating the auto-pilot posture skip.
func TestKernelToolAdapter_AutonomousSkipsPrompt(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "echo"}
	// Resolver returns "confirm_each" (what would trigger an interactive
	// prompt under the default posture).
	perms := &recordingPermResolver{
		verdict: PermVerdict{
			Server: "myserver",
			Tool:   "echo",
			Policy: "confirm_each",
		},
	}

	// Autonomous knobs include shell-safe → adapter should skip prompt.
	knobs := knobsFromTier(autonomy.TierAutonomous)
	if !knobs.AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatalf("test precondition: autonomous tier should include shell-safe; got %v",
			knobs.AutoApproveFamilies.Sorted())
	}

	// A live bus that records whether the user was asked. Under the
	// autonomous posture it must never fire.
	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: false})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-auto").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })

	result, err := adapter.Call(context.Background(), makeCall("myserver", "echo"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q (want successful call)", result.Content)
	}
	if prompted {
		t.Fatal("autonomous posture prompted the user — AutoApproveFamilies must skip the confirmation")
	}

	// The resolver was still called (we still check for explicit deny),
	// but because Policy != "deny" AND skipPrompt=true, the call proceeded.
	if len(perms.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1", len(perms.calls))
	}
}

// TestKernelToolAdapter_StrictDoesNotSkipPrompt verifies that when the
// resolved knobs are from TierStrict (empty AutoApproveFamilies), a
// "confirm_each" verdict really does prompt.
//
// This test used to script an "auto_allow" verdict, so the strict path
// never exercised confirm_each at all and the two tiers were
// interchangeable (spec §1.3). It now scripts confirm_each and fails if
// the prompt is skipped — swapping the tiers between this test and
// TestKernelToolAdapter_AutonomousSkipsPrompt now breaks both.
func TestKernelToolAdapter_StrictDoesNotSkipPrompt(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "echo"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{
			Server: "myserver",
			Tool:   "echo",
			Policy: "confirm_each",
		},
	}

	// Strict knobs: empty AutoApproveFamilies → no posture bypass.
	knobs := knobsFromTier(autonomy.TierStrict)
	if knobs.AutoApproveFamilies.Has(autonomy.FamilyShellSafe) {
		t.Fatalf("test precondition: strict tier must not include shell-safe; got %v",
			knobs.AutoApproveFamilies.Sorted())
	}

	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})

	adapter := newKernelToolAdapter(pool, perms, "sess-strict").withConfirm(bus)
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })

	result, err := adapter.Call(context.Background(), makeCall("myserver", "echo"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !prompted {
		t.Fatal("strict posture skipped the confirmation prompt")
	}
	if result.IsError {
		t.Fatalf("result.IsError = true after approval; content = %q", result.Content)
	}
	// Resolver was consulted (skipPrompt=false path).
	if len(perms.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1", len(perms.calls))
	}
}

// TestKernelToolAdapter_DenyRemainsCedarFloor verifies that an explicit
// Cedar deny ("deny" Policy from the resolver) is honoured even when the
// autonomy posture is autonomous — the auto-approve bypass only skips
// interactive prompts, NOT explicit denials.
func TestKernelToolAdapter_DenyRemainsCedarFloor(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "myserver", tool: "dangerous_tool"}
	// Resolver returns "deny" (explicit Cedar forbid).
	perms := &recordingPermResolver{
		verdict: PermVerdict{
			Server: "myserver",
			Tool:   "dangerous_tool",
			Policy: "deny",
			Reason: "cedar forbid rule matched",
		},
	}

	// Autonomous knobs: skip prompt for non-deny cases.
	knobs := knobsFromTier(autonomy.TierAutonomous)

	adapter := newKernelToolAdapter(pool, perms, "sess-auto-deny")
	adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobs })

	result, err := adapter.Call(context.Background(), makeCall("myserver", "dangerous_tool"))
	if err != nil {
		t.Fatalf("Call: unexpected error %v", err)
	}
	// The cedar deny must propagate as IsError=true, not be swallowed by
	// the autonomy bypass.
	if !result.IsError {
		t.Fatalf("result.IsError = false; want IsError=true (cedar deny must be the floor)")
	}
	if result.Content == "" {
		t.Fatalf("result.Content empty; want denial reason")
	}
}

// TestKernelToolAdapter_NilAutonomyProvider_FallsThrough verifies that
// when no AutonomyKnobsProvider is set, the adapter uses the v0.3.0
// baseline behaviour: the resolver is consulted for every call.
func TestKernelToolAdapter_NilAutonomyProvider_FallsThrough(t *testing.T) {
	t.Parallel()

	pool := &staticToolPool{server: "s", tool: "t"}
	perms := &recordingPermResolver{
		verdict: PermVerdict{Server: "s", Tool: "t", Policy: "auto_allow"},
	}

	adapter := newKernelToolAdapter(pool, perms, "sess-nil")
	// No withAutonomy call.

	result, err := adapter.Call(context.Background(), makeCall("s", "t"))
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if result.IsError {
		t.Fatalf("result.IsError = true; content = %q", result.Content)
	}
	if len(perms.calls) != 1 {
		t.Fatalf("resolver call count = %d, want 1 (nil autonomy should not skip resolver)", len(perms.calls))
	}
}
