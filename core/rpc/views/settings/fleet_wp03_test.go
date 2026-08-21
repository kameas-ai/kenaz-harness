package settings

// fleet-enforcement-truth-01PMZ505 WP03.
//
// Spec §1.1/§5.2, tasks.md AC-003: SetCedarEngine had zero callers
// repo-wide. This proves the wiring shape rpc.New() now uses — pass the
// engine via SetCedarEngine, apply a cedar_delta bundle through the real
// compositeConfigApplier (same struct rpc.New() wires SetFleetClient's
// poller with), and read the result back FROM THE LIVE ENGINE.
// docs/unwired-ledger.md records the in-session policy editor once
// reporting a rule as live when it was not — asserting on
// ApplyCedarDelta's nil return instead of the engine would repeat that
// mistake.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
	cedarpolicy "github.com/kameas-ai/kenaz-harness/core/policy/cedar"

	gocedar "github.com/cedar-policy/cedar-go"
)

func TestSetCedarEngine_WiresApplierToLiveEngine(t *testing.T) {
	engine, err := cedarpolicy.NewEngine(cedarpolicy.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	api := &API{}
	// The production call added by this WP: settingsImpl.SetCedarEngine(a.cedarEngine)
	// in rpc.New(). Before this WP the setter had no caller and every
	// cedar_delta bundle dead-ended at a nil engine read (WP02's error).
	api.SetCedarEngine(engine)

	// Mirrors SetFleetClient's own construction: applier := &compositeConfigApplier{state: a.fleet}.
	applier := &compositeConfigApplier{state: api.fleet}
	b := &fleet.Bundle{
		BundleID:   1,
		CedarDelta: json.RawMessage(`{"rules":{"wp03-team-forbid":"forbid(principal, action, resource);"}}`),
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("ApplyBundle returned errors with the engine wired: %v", errs)
	}

	// Read back from the LIVE ENGINE, not the applier's return value.
	files := engine.ListPolicies()
	found := false
	for _, f := range files {
		if f.Name == "fleet-team/wp03-team-forbid" {
			found = true
			if !f.ParseOK {
				t.Errorf("fleet-team/wp03-team-forbid failed to parse: %s", f.ParseErr)
			}
		}
	}
	if !found {
		t.Fatal("fleet-team/wp03-team-forbid not present in the live engine's policy set after ApplyBundle")
	}

	// And a request the rule forbids now evaluates to Deny, read from the
	// same live engine SetCedarEngine wired.
	dec := engine.Evaluate(context.Background(), gocedar.EntityUID{}, cedarpolicy.ActionModelSelect, gocedar.EntityUID{}, nil)
	if dec.Outcome != cedarpolicy.Deny {
		t.Errorf("Evaluate outcome = %v, want Deny (the fleet-team forbid rule should have applied)", dec.Outcome)
	}
}

// The mutation check: with SetCedarEngine never called, the same bundle
// apply must return the WP02 error and the engine must see no fleet-team
// policy at all — proving the wire, not just the applier's error path,
// is what changed.
func TestSetCedarEngine_UnwiredEngine_DoesNotInstall(t *testing.T) {
	engine, err := cedarpolicy.NewEngine(cedarpolicy.Options{
		LoadFromDisk:    false,
		IncludeEmbedded: false,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	// SetCedarEngine is deliberately NOT called on this API instance.
	applier := &compositeConfigApplier{state: &fleetState{}}
	b := &fleet.Bundle{
		BundleID:   1,
		CedarDelta: json.RawMessage(`{"rules":{"wp03-team-forbid":"forbid(principal, action, resource);"}}`),
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected the WP02 apply error with the engine unwired")
	}
	if len(engine.ListPolicies()) != 0 {
		t.Errorf("expected no policies installed into an engine SetCedarEngine never wired, got %d", len(engine.ListPolicies()))
	}
}
