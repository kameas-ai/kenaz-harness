package llm

// fleet-enforcement-truth-01PMZ505 WP04.
//
// Spec §1.2/§5.8, tasks.md AC-004/AC-005. fleetProviderAllowlist and
// fleetDefaultModelValue are process-global state (mirroring
// core/mcp/recipes.AllowlistFilter's own singleton), so every test here
// resets it via t.Cleanup — leaking state into an unrelated test in this
// package would be exactly the kind of silent cross-test contamination
// CLAUDE.md's race-safe-fakes canon warns about, just via a global
// instead of a goroutine.

import (
	"context"
	"strings"
	"testing"

	corellm "github.com/kameas-ai/kenaz-harness/core/llm"
)

// AC-004 — a fleet ProviderAllowlist restricts BOTH the list AND
// execution. Filtering only the list would be a UI suggestion, not
// governance (spec §5.8). ListProviders reads bundle-tier profiles from
// BundleSource.BundleProfiles(), not the registry — fakeBundles is the
// existing fixture other ListProviders tests in this package use
// (impl_providers_test.go).
func TestListProviders_FleetAllowlistFilters(t *testing.T) {
	t.Cleanup(ClearFleetModelPrefs)
	ApplyFleetModelPrefs("", []string{"anthropic"})

	bundles := &fakeBundles{profiles: []corellm.ProviderProfile{
		{ID: "p-anthropic", Kind: "anthropic", Model: "claude-sonnet-4-5",
			Cred: corellm.CredentialReference{Kind: "env", Locator: "K"}},
		{ID: "p-openai", Kind: "openai", Model: "gpt-4o",
			Cred: corellm.CredentialReference{Kind: "env", Locator: "K"}},
	}}
	api := New(Config{Bundles: bundles})

	provs, err := api.ListProviders(context.Background())
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(provs) != 1 || provs[0].ID != "p-anthropic" {
		t.Fatalf("expected only the allow-listed provider, got %+v", provs)
	}
}

func TestStartStream_FleetAllowlistBlocksExcludedProfile(t *testing.T) {
	t.Cleanup(ClearFleetModelPrefs)
	ApplyFleetModelPrefs("", []string{"anthropic"})

	reg := &fakeRegistry{}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{
		{ID: "p-openai", Kind: "openai", Model: "gpt-4o"},
	}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	// No ChatRunner wired — if the allow-list check did not block first,
	// this would fail with the generic "chat runner not wired" error
	// instead, which would mean the block never ran.
	api := New(Config{Registry: reg})

	_, err := api.StartStream(context.Background(), "p-openai", "sess-1", "")
	if err == nil {
		t.Fatal("expected StartStream to fail for a provider excluded by the fleet allow-list")
	}
	if !strings.Contains(err.Error(), "allow-list") {
		t.Errorf("expected the fleet-allowlist error, got: %v", err)
	}
}

// A permitted profile is unaffected — the allow-list must not block
// everything.
func TestStartStream_FleetAllowlistPermitsIncludedProfile(t *testing.T) {
	t.Cleanup(ClearFleetModelPrefs)
	ApplyFleetModelPrefs("", []string{"anthropic"})

	reg := &fakeRegistry{}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{
		{ID: "p-anthropic", Kind: "anthropic", Model: "claude-sonnet-4-5"},
	}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	api := New(Config{Registry: reg})

	_, err := api.StartStream(context.Background(), "p-anthropic", "sess-1", "")
	// No ChatRunner wired, so it still fails — but it MUST fail with the
	// nil-chat-runner error, not the fleet-allowlist error, proving the
	// allow-list check let it through.
	if err == nil {
		t.Fatal("expected the nil-chat-runner error")
	}
	if strings.Contains(err.Error(), "allow-list") {
		t.Errorf("permitted provider was blocked by the fleet allow-list: %v", err)
	}
}

// AC-005 — two assertions in one test: with no explicit per-run
// selection, a fleet DefaultModel seeds the resolution; with an explicit
// selection, the user's choice survives untouched (D-3).
func TestProfileKindAndModel_FleetDefaultSeedsButDoesNotOverride(t *testing.T) {
	t.Cleanup(ClearFleetModelPrefs)
	ApplyFleetModelPrefs("fleet-default-model", nil)

	reg := &fakeRegistry{}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{
		{ID: "p1", Kind: "anthropic", Model: "user-configured-model"},
	}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	api := New(Config{Registry: reg})

	// No explicit per-run override: the fleet default seeds it.
	if _, model := api.profileKindAndModel("p1", ""); model != "fleet-default-model" {
		t.Errorf("no explicit selection: model = %q, want the fleet default %q", model, "fleet-default-model")
	}

	// An explicit per-run override: the user's choice wins outright.
	if _, model := api.profileKindAndModel("p1", "user-picked-this-run"); model != "user-picked-this-run" {
		t.Errorf("explicit selection: model = %q, want the user's override %q — a fleet default must never win here", model, "user-picked-this-run")
	}
}

// Mutation control: with no fleet default applied, the profile's own
// configured model is unaffected — WP04 must not change behaviour for
// devices with no fleet governance.
func TestProfileKindAndModel_NoFleetDefault_UsesProfileModel(t *testing.T) {
	t.Cleanup(ClearFleetModelPrefs)
	// Deliberately do not call ApplyFleetModelPrefs.

	reg := &fakeRegistry{}
	if err := reg.LoadProfiles([]corellm.ProviderProfile{
		{ID: "p1", Kind: "anthropic", Model: "user-configured-model"},
	}); err != nil {
		t.Fatalf("LoadProfiles: %v", err)
	}
	api := New(Config{Registry: reg})

	if _, model := api.profileKindAndModel("p1", ""); model != "user-configured-model" {
		t.Errorf("model = %q, want the profile's own model %q", model, "user-configured-model")
	}
}
