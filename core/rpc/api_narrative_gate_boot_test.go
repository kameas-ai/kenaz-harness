package rpc

// api_narrative_gate_boot_test.go — boot-wire regression for
// narrative.SetSettingsGate (01PMGX01 WP17 wired it; the final review
// left "delete the api.go call → nothing fails" as an open nit; closed
// by the 2026-08-13 adversarial review).
//
// The gate's whole history is the defect this pins: SetSettingsGate's
// doc said "call this once at harness boot" and for a full release
// nothing did, so flipping MemoryNarrativeEnabled in Settings changed
// nothing. The narrative package's own tests exercise the FUNCTION;
// only a test that boots the real chassis exercises the WIRE.

import (
	"context"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core"
	"github.com/kameas-ai/kenaz-harness/core/memory/narrative"
)

// Not t.Parallel(): narrative's settings gate is process-global state,
// and every rpc.New re-points it at that instance's settings store.
func TestBootWiresNarrativeSettingsGate(t *testing.T) {
	// The env var overrides the settings gate entirely (precedence 1/2
	// in narrative.Enabled) — make the test hermetic against a dev
	// shell that has it exported.
	t.Setenv("HARNESS_MEMORY_NARRATIVE_LAYER", "")

	c, err := core.New(core.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatalf("core.New: %v", err)
	}
	api := New(c)
	if api == nil {
		t.Fatal("rpc.New returned nil")
	}
	ctx := context.Background()

	if err := api.Settings().SetMemoryNarrativeEnabled(ctx, true); err != nil {
		t.Fatalf("SetMemoryNarrativeEnabled(true): %v", err)
	}
	if !narrative.Enabled() {
		t.Fatal("narrative.Enabled() = false after enabling the Settings dial — the boot wire (narrative.SetSettingsGate in api.New) is missing or dead")
	}

	if err := api.Settings().SetMemoryNarrativeEnabled(ctx, false); err != nil {
		t.Fatalf("SetMemoryNarrativeEnabled(false): %v", err)
	}
	if narrative.Enabled() {
		t.Fatal("narrative.Enabled() = true after disabling the Settings dial — the gate is not reading the live store")
	}
}
