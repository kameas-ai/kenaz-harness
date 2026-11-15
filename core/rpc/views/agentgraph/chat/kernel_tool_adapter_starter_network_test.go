package chat

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// Launch spec L09 (host starter policy, network leg): a confirm_each
// verdict carrying AskDespitePermit reaches the prompt even though the
// embedded Cedar bundle permits every kenaz tool. A forbid still denies;
// without the flag the permit still skips the prompt (the layer-2 test).
func TestKernelToolAdapter_StarterNetwork_PermitDoesNotSkipPrompt(t *testing.T) {
	t.Parallel()
	for _, approve := range []bool{true, false} {
		pool := &staticToolPool{server: "kenaz", tool: "web_fetch"}
		perms := &recordingPermResolver{verdict: PermVerdict{
			Server: "kenaz", Tool: "web_fetch", Policy: "confirm_each",
			Reason: toolloop.StarterNetworkReason, AskDespitePermit: true,
		}}
		prompted := false
		var bus *toolloop.ConfirmBus
		bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
			prompted = true
			_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: approve})
		})
		gate := &fixedGate{decision: cedar.Decision{Outcome: cedar.Allow, Reason: "permit: kenaz tools"}}
		adapter := newKernelToolAdapter(pool, perms, "sess-starter-net").withConfirm(bus)
		adapter.withAutonomy(func(context.Context, string) autonomy.ResolvedKnobs { return knobsFromTier(autonomy.TierStrict) })
		adapter.withGate(gate)

		result, err := adapter.Call(context.Background(), makeCall("kenaz", "web_fetch"))
		if err != nil {
			t.Fatalf("Call: %v", err)
		}
		if !prompted {
			t.Fatal("starter policy network call ran without asking: the Cedar permit skipped the prompt")
		}
		if result.IsError == approve {
			t.Fatalf("approve=%v: IsError=%v content=%q", approve, result.IsError, result.Content)
		}
	}
}

func TestKernelToolAdapter_StarterNetwork_ForbidStillDenies(t *testing.T) {
	t.Parallel()
	pool := &staticToolPool{server: "kenaz", tool: "web_fetch"}
	perms := &recordingPermResolver{verdict: PermVerdict{
		Server: "kenaz", Tool: "web_fetch", Policy: "confirm_each", AskDespitePermit: true,
	}}
	prompted := false
	var bus *toolloop.ConfirmBus
	bus = toolloop.NewConfirmBus(func(req toolloop.ConfirmRequest) {
		prompted = true
		_ = bus.Resolve(req.SessionID, req.CallID, toolloop.ConfirmDecision{Approved: true})
	})
	adapter := newKernelToolAdapter(pool, perms, "sess-starter-forbid").withConfirm(bus)
	adapter.withGate(&fixedGate{decision: cedar.Decision{Outcome: cedar.Deny, Reason: "forbid"}})
	result, err := adapter.Call(context.Background(), makeCall("kenaz", "web_fetch"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError || prompted {
		t.Fatalf("forbid: IsError=%v prompted=%v, want denied without a prompt", result.IsError, prompted)
	}
}
