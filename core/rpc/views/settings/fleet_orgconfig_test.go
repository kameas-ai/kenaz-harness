package settings

// fleet-generic-sync-framework-01NSYNC02 WP02.
//
// tasks.md WP02 acceptance: "signature round-trip incl. map ordering
// canonicalization; unknown-kind tolerance test; replay protection
// unchanged." The signature/canonicalization/replay assertions live in
// core/fleet/bundle_test.go (the payload shape). This file covers the
// OTHER half of WP02's contract: compositeConfigApplier.ApplyBundle's
// dispatch of a bundle's org_config keyed section to each entry's
// registered SyncKind.
//
// Test rule (mirrors fleet_wp02_test.go's spec §8 rule 4): the fixture
// constructs the seam the way production does — a zero-value fleetState
// with a real *fleet.KindRegistry wired via SetSyncKindRegistry, never a
// hand-rolled stand-in for ApplyBundle's dispatch logic.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// TestApplyBundle_OrgConfig_UnknownKind_LoggedSkipNotFatal is the
// "unknown-kind tolerance" acceptance: an org_config entry whose key has NO
// registration in this build (forward-compat — an older harness binary
// received a bundle from a newer fleet server advertising a kind it has
// never heard of) must not fail the apply. If this ever starts producing
// an error, an old binary would fail EVERY section in a bundle whenever
// the fleet admin turns on one new org kind, not just skip the one it
// doesn't understand.
func TestApplyBundle_OrgConfig_UnknownKind_LoggedSkipNotFatal(t *testing.T) {
	registry := fleet.NewKindRegistry()
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID: 1,
		OrgConfig: map[string]json.RawMessage{
			"a_kind_this_build_has_never_heard_of": json.RawMessage(`{"x":1}`),
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply for an unknown org_config kind (skip, not fatal), got errors: %v", errs)
	}
}

// TestApplyBundle_OrgConfig_RegistryUnwired_LoggedSkipNotFatal covers the
// registry-nil case distinctly from the unknown-kind case above: fleet
// sync registration never ran at all (offline / fleet-disabled boot), so
// there is no registry to consult. Same "skip, don't fail the rest of the
// bundle" treatment.
func TestApplyBundle_OrgConfig_RegistryUnwired_LoggedSkipNotFatal(t *testing.T) {
	applier := &compositeConfigApplier{state: &fleetState{}} // syncKindRegistry left nil
	b := &fleet.Bundle{
		BundleID: 1,
		OrgConfig: map[string]json.RawMessage{
			"provider_profiles": json.RawMessage(`{}`),
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply when the sync registry itself is unwired, got errors: %v", errs)
	}
}

// TestApplyBundle_OrgConfig_RegisteredKindWithoutOrgScope_Fails is the
// converse of the tolerance tests above: a kind IS registered in this
// build, so this is not a forward-compat gap — it is a real wiring bug (a
// kind that never declared ScopeOrg, or declared it with a nil Apply,
// showing up as an org_config key anyway). This must be a collected error,
// not a skip, or a genuinely-broken org kind would ACK "applied:true"
// forever.
func TestApplyBundle_OrgConfig_RegisteredKindWithoutOrgScope_Fails(t *testing.T) {
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:             "user_only_kind",
		Scopes:         []fleet.Scope{fleet.ScopeUser}, // no ScopeOrg
		Transport:      fleet.TransportLWWCategory,
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyLWW,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID: 1,
		OrgConfig: map[string]json.RawMessage{
			"user_only_kind": json.RawMessage(`{}`),
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected an error for an org_config entry naming a kind with no org scope; got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "user_only_kind") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected an error naming the misconfigured kind, got: %v", errs)
	}
}

// TestApplyBundle_OrgConfig_DispatchesToRegisteredKindApply is the positive
// path: a registered ScopeOrg kind's Apply function is actually invoked
// with (ctx, ScopeOrg, the entry's raw payload) — the concrete proof this
// is a real dispatch and not a no-op that merely avoids erroring.
func TestApplyBundle_OrgConfig_DispatchesToRegisteredKindApply(t *testing.T) {
	var gotScope fleet.Scope
	var gotPayload []byte
	calls := 0

	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:        "provider_profiles",
		Scopes:    []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport: fleet.TransportBundlePushdown,
		Apply: func(_ context.Context, scope fleet.Scope, payload []byte) error {
			calls++
			gotScope = scope
			gotPayload = append([]byte(nil), payload...)
			return nil
		},
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	wantPayload := `{"provider":"anthropic","default":true}`
	b := &fleet.Bundle{
		BundleID: 1,
		OrgConfig: map[string]json.RawMessage{
			"provider_profiles": json.RawMessage(wantPayload),
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply, got errors: %v", errs)
	}
	if calls != 1 {
		t.Fatalf("kind.Apply call count = %d, want 1", calls)
	}
	if gotScope != fleet.ScopeOrg {
		t.Errorf("Apply invoked with scope %q, want %q", gotScope, fleet.ScopeOrg)
	}
	if string(gotPayload) != wantPayload {
		t.Errorf("Apply invoked with payload %s, want %s", gotPayload, wantPayload)
	}
}

// TestApplyBundle_OrgConfig_ApplyErrorIsCollectedNotSwallowed proves a
// registered kind's Apply error surfaces in ApplyBundle's returned slice
// (so config_pull.go's "len(applyErrs) == 0" success check goes false and
// lastAppliedID does not advance) rather than being silently dropped.
func TestApplyBundle_OrgConfig_ApplyErrorIsCollectedNotSwallowed(t *testing.T) {
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:        "installed_mcp",
		Scopes:    []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport: fleet.TransportBundlePushdown,
		Apply: func(context.Context, fleet.Scope, []byte) error {
			return errApplyBoom
		},
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID: 1,
		OrgConfig: map[string]json.RawMessage{
			"installed_mcp": json.RawMessage(`{}`),
		},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected the kind's Apply error to be collected; got none")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), errApplyBoom.Error()) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the collected error to wrap errApplyBoom, got: %v", errs)
	}
}

var errApplyBoom = applyBoomError{}

type applyBoomError struct{}

func (applyBoomError) Error() string { return "org-config-apply-boom" }

// TestApplyBundle_OrgConfig_MarksProvenanceOnSuccess is the WP03 provenance
// acceptance: a successful ScopeOrg apply must be recorded on the registry
// so the Settings → Sync surface (WP06) can answer "is this kind currently
// org-provisioned" generically, without knowing the kind's payload shape.
func TestApplyBundle_OrgConfig_MarksProvenanceOnSuccess(t *testing.T) {
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:             "provider_profiles",
		Scopes:         []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport:      fleet.TransportBundlePushdown,
		Apply:          func(context.Context, fleet.Scope, []byte) error { return nil },
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if _, ok := registry.OrgAppliedAt("provider_profiles"); ok {
		t.Fatal("precondition: expected no provenance before any apply")
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID:  1,
		OrgConfig: map[string]json.RawMessage{"provider_profiles": json.RawMessage(`{"provider":"anthropic"}`)},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply, got errors: %v", errs)
	}
	if _, ok := registry.OrgAppliedAt("provider_profiles"); !ok {
		t.Error("expected provenance to be recorded after a successful org_config apply")
	}
}

// TestApplyBundle_OrgConfig_DoesNotMarkProvenanceOnFailure is the
// mutation-proof counterpart: a FAILED apply must not record provenance —
// otherwise a kind that never actually applied would still claim
// "provisioned by your org" on the Settings surface.
func TestApplyBundle_OrgConfig_DoesNotMarkProvenanceOnFailure(t *testing.T) {
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:             "installed_mcp",
		Scopes:         []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport:      fleet.TransportBundlePushdown,
		Apply:          func(context.Context, fleet.Scope, []byte) error { return errApplyBoom },
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID:  1,
		OrgConfig: map[string]json.RawMessage{"installed_mcp": json.RawMessage(`{}`)},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected the apply error to be collected")
	}
	if _, ok := registry.OrgAppliedAt("installed_mcp"); ok {
		t.Error("expected NO provenance to be recorded when the kind's Apply failed")
	}
}

// TestApplyBundle_OrgConfig_RefusesSecretShapedPayload proves the WP06
// ScopeOrg secret-shape backstop: an org_config entry whose payload looks
// like a credential is refused before the kind's Apply ever runs, and
// provenance is not recorded — mirroring the ScopeUser enforcement in
// SyncKind.CategoryConfig (core/fleet/synckind_test.go).
func TestApplyBundle_OrgConfig_RefusesSecretShapedPayload(t *testing.T) {
	applyCalled := false
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:        "provider_profiles",
		Scopes:    []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport: fleet.TransportBundlePushdown,
		Apply: func(context.Context, fleet.Scope, []byte) error {
			applyCalled = true
			return nil
		},
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID:  1,
		OrgConfig: map[string]json.RawMessage{"provider_profiles": json.RawMessage(`{"api_key":"sk-live-1234567890"}`)},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) == 0 {
		t.Fatal("expected the secret-shaped org_config payload to be refused with a collected error")
	}
	if applyCalled {
		t.Error("kind.Apply must not run when the org_config payload is refused as secret-shaped")
	}
	if _, ok := registry.OrgAppliedAt("provider_profiles"); ok {
		t.Error("expected NO provenance to be recorded when the payload was refused")
	}
}

// TestApplyBundle_OrgConfig_CleanSecretlessPayloadStillApplies is the
// mutation-proof for the secret-shape refusal above: an ordinary,
// legitimate org_config payload must still apply — a rejector that refuses
// everything is not a filter.
func TestApplyBundle_OrgConfig_CleanSecretlessPayloadStillApplies(t *testing.T) {
	applyCalled := false
	registry := fleet.NewKindRegistry()
	if err := registry.Register(fleet.SyncKind{
		ID:        "provider_profiles",
		Scopes:    []fleet.Scope{fleet.ScopeUser, fleet.ScopeOrg},
		Transport: fleet.TransportBundlePushdown,
		Apply: func(context.Context, fleet.Scope, []byte) error {
			applyCalled = true
			return nil
		},
		SecretPolicy:   fleet.SecretPolicyMustNotContainSecrets,
		ConflictPolicy: fleet.ConflictPolicyOrgWinsReadonly,
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	state := &fleetState{}
	state.syncKindRegistry = registry
	applier := &compositeConfigApplier{state: state}

	b := &fleet.Bundle{
		BundleID:  1,
		OrgConfig: map[string]json.RawMessage{"provider_profiles": json.RawMessage(`{"provider":"anthropic","default":true}`)},
	}
	errs := applier.ApplyBundle(context.Background(), b)
	if len(errs) != 0 {
		t.Fatalf("expected a clean apply, got errors: %v", errs)
	}
	if !applyCalled {
		t.Error("expected kind.Apply to run for a legitimate, secret-free payload")
	}
	if _, ok := registry.OrgAppliedAt("provider_profiles"); !ok {
		t.Error("expected provenance to be recorded for the successful apply")
	}
}

// TestStopFleetBackground_ClearsOrgProvenance is the sign-out counterpart
// to TestStopFleetBackground_ClearsOrgProvisionedRecipes
// (fleet_orgx01_provisioned_mcp_test.go), for the generic kind-agnostic
// provenance tracker rather than the mcp_recipes-specific merged catalog:
// FR-008 requires org layers to drop cleanly on sign-out, and a stale
// "provisioned by your org" timestamp surviving StopFleetBackground would
// violate that for the Settings → Sync surface even though the actual
// recipe overlay reverted correctly.
func TestStopFleetBackground_ClearsOrgProvenance(t *testing.T) {
	registry := fleet.NewKindRegistry()
	api := &API{}
	api.SetSyncKindRegistry(registry)
	registry.MarkOrgApplied("provider_profiles", time.Now())

	if _, ok := registry.OrgAppliedAt("provider_profiles"); !ok {
		t.Fatal("precondition: expected provenance to be recorded before sign-out")
	}

	api.StopFleetBackground()

	if _, ok := registry.OrgAppliedAt("provider_profiles"); ok {
		t.Error("expected StopFleetBackground to clear org provenance on sign-out")
	}
}
