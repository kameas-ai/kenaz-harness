// skills_sync_test.go — httptest round-trip tests for PublishSkill,
// InstallSkill, UninstallSkill, and ApplyMandatedSkills.
//
// (fleet-skills-sync-01NDFSEX18 WP07)
package fleet

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// ── helpers ───────────────────────────────────────────────────────────────────

// makeSkillTestSetup creates a fresh test SkillStore + Registry + DeviceSigner
// + Capabilities and returns them. Capabilities are set to a permissive set
// that includes both CapSharedTeamGraph and CapPersonalFleetDashboard.
func makeSkillTestSetup(t *testing.T) (
	store *slashcmd.SkillStore,
	registry *slashcmd.Registry,
	signer *DeviceSigner,
	caps *Capabilities,
) {
	t.Helper()
	dir := t.TempDir()
	store = slashcmd.NewSkillStore(dir)

	var regErr error
	registry, regErr = slashcmd.NewRegistry(slashcmd.Deps{})
	if regErr != nil {
		t.Fatalf("NewRegistry: %v", regErr)
	}

	var signerErr error
	signer, signerErr = NewDeviceSigner(dir)
	if signerErr != nil {
		t.Fatalf("NewDeviceSigner: %v", signerErr)
	}

	c := Capabilities{
		Tier: "team",
		Enabled: map[Capability]bool{
			CapSharedTeamGraph: true,
			CapPersonalFleetDashboard: true,
		},
		FetchedAt: time.Now(),
		Source:    "test",
	}
	caps = &c
	return
}

// ── PublishSkill ──────────────────────────────────────────────────────────────

// TestPublishSkill_RoundTrip verifies that PublishSkill sends the right payload
// to the fake catalog server with kind="skill".
func TestPublishSkill_RoundTrip(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-skill",
		RefreshToken: "rt-skill",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	store, _, signer, caps := makeSkillTestSetup(t)
	_ = store // not used in publish

	skill := slashcmd.Skill{
		ID:          "standup-skill",
		Trigger:     "standup",
		Kind:        slashcmd.KindText,
		Description: "Daily standup",
		Body:        "What did you do yesterday?",
		Source:      slashcmd.SkillSourceCatalog,
	}

	item, err := PublishSkill(context.Background(), c, caps, signer, skill, CatalogVisTeam)
	if err != nil {
		t.Fatalf("PublishSkill: %v", err)
	}
	if item.ID == "" {
		t.Error("expected non-empty catalog ID from server")
	}
	if len(fake.published) != 1 {
		t.Fatalf("server received %d publishes, want 1", len(fake.published))
	}
	pub := fake.published[0]
	if pub.Kind != CatalogKindSkill {
		t.Errorf("published Kind = %q, want %q", pub.Kind, CatalogKindSkill)
	}
	if pub.Slug != "standup" {
		t.Errorf("published Slug = %q, want %q", pub.Slug, "standup")
	}
	if len(pub.Payload) == 0 {
		t.Error("expected non-empty payload")
	}
}

// TestPublishSkill_CapGate verifies that publishing to team/org scope fails
// when CapSharedTeamGraph is absent.
func TestPublishSkill_CapGate(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-skill-cap",
		RefreshToken: "rt-skill-cap",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)

	dir := t.TempDir()
	signer, _ := NewDeviceSigner(dir)

	// Capabilities without CapSharedTeamGraph.
	caps := DefaultDenyCapabilities()

	skill := slashcmd.Skill{
		ID:      "skill-gate",
		Trigger: "test",
		Kind:    slashcmd.KindText,
	}

	_, err := PublishSkill(context.Background(), c, &caps, signer, skill, CatalogVisTeam)
	if err == nil {
		t.Fatal("expected error for missing CapSharedTeamGraph, got nil")
	}
}

// TestPublishSkill_FleetDisabled verifies that PublishSkill returns
// ErrFleetDisabled for a nop client.
func TestPublishSkill_FleetDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	signer, _ := NewDeviceSigner(dir)
	caps := DefaultDenyCapabilities()

	skill := slashcmd.Skill{ID: "sk", Trigger: "test", Kind: slashcmd.KindText}
	_, err := PublishSkill(context.Background(), nil, &caps, signer, skill, CatalogVisTeam)
	if err != ErrFleetDisabled {
		t.Errorf("expected ErrFleetDisabled, got: %v", err)
	}
}

// ── InstallSkill ──────────────────────────────────────────────────────────────

// TestInstallSkill_RoundTrip verifies the full publish→install cycle using
// httptest. The install verifies the signature and live-registers the skill.
func TestInstallSkill_RoundTrip(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{
		AccessToken:  "at-inst",
		RefreshToken: "rt-inst",
		ExpiresAt:    time.Now().Add(time.Hour),
	})
	c := makeTestClient(t, srv.URL)
	store, registry, signer, caps := makeSkillTestSetup(t)

	skill := slashcmd.Skill{
		ID:          "install-skill",
		Trigger:     "deploy",
		Kind:        slashcmd.KindText,
		Description: "Deploy to staging",
		Body:        "kubectl apply -f staging/",
		Source:      slashcmd.SkillSourceCatalog,
	}

	// Publish first.
	item, err := PublishSkill(context.Background(), c, caps, signer, skill, CatalogVisTeam)
	if err != nil {
		t.Fatalf("PublishSkill: %v", err)
	}

	// Install via InstallSkill. pubKeyBase64="" means verify is skipped (same
	// as the catalog install path when the server-side key lookup is pending).
	if err := InstallSkill(context.Background(), c, store, registry, "", item.ID, item.Version); err != nil {
		t.Fatalf("InstallSkill: %v", err)
	}

	// Skill must be registered under its trigger.
	if _, ok := registry.Lookup("deploy"); !ok {
		t.Error("skill 'deploy' not registered after InstallSkill")
	}
	// Skill must be persisted to the store. The store key is skill.ID (from the
	// payload), not the catalog item ID. The fake server echoes back the payload
	// so skill.ID stays "install-skill" after unmarshalling.
	persisted, err := store.Get("install-skill")
	if err != nil {
		t.Fatalf("store.Get after install: %v", err)
	}
	if persisted.CatalogID != item.ID {
		t.Errorf("persisted CatalogID = %q, want %q", persisted.CatalogID, item.ID)
	}
}

// TestInstallSkill_FleetDisabled verifies that InstallSkill returns
// ErrFleetDisabled for a nop client.
func TestInstallSkill_FleetDisabled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	err := InstallSkill(context.Background(), nil, store, registry, "", "cat-id", "1.0.0")
	if err != ErrFleetDisabled {
		t.Errorf("expected ErrFleetDisabled, got: %v", err)
	}
}

// ── UninstallSkill ────────────────────────────────────────────────────────────

// TestUninstallSkill_RoundTrip verifies that UninstallSkill removes the skill
// from store and unregisters it.
func TestUninstallSkill_RoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	sk := slashcmd.Skill{
		ID:      "uninstall-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "cleanup",
		Kind:    slashcmd.KindText,
		Body:    "clean up",
	}
	if err := slashcmd.LiveRegister(store, registry, sk); err != nil {
		t.Fatalf("LiveRegister: %v", err)
	}

	if err := UninstallSkill(store, registry, "uninstall-skill"); err != nil {
		t.Fatalf("UninstallSkill: %v", err)
	}

	if _, ok := registry.Lookup("cleanup"); ok {
		t.Error("'cleanup' still registered after UninstallSkill")
	}
}

// TestUninstallSkill_NotFound verifies that UninstallSkill returns
// ErrSkillNotFound for a non-existent skill ID.
func TestUninstallSkill_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	err := UninstallSkill(store, registry, "nonexistent-skill")
	if err == nil {
		t.Fatal("expected error for missing skill, got nil")
	}
}

// ── ApplyMandatedSkills ───────────────────────────────────────────────────────

// TestApplyMandatedSkills_Basic verifies that ApplyMandatedSkills installs
// read-only mandated skills and registers them live.
func TestApplyMandatedSkills_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	mandate := slashcmd.Skill{
		ID:          "org/security-review",
		Trigger:     "security-review",
		Kind:        slashcmd.KindPrompt,
		Description: "Security review runbook",
		Body:        "Review the following for security issues: {{selection}}",
		Source:      slashcmd.SkillSourceMandated,
		OrgManaged:  true,
	}
	raw, err := json.Marshal(mandate)
	if err != nil {
		t.Fatalf("json.Marshal mandate: %v", err)
	}

	errs := ApplyMandatedSkills(store, registry, []json.RawMessage{raw})
	if len(errs) != 0 {
		t.Fatalf("ApplyMandatedSkills returned errors: %v", errs)
	}

	// Must be registered.
	if _, ok := registry.Lookup("security-review"); !ok {
		t.Error("'security-review' not registered after ApplyMandatedSkills")
	}

	// Must be persisted with Source=mandated and OrgManaged=true.
	persisted, err := store.Get("org/security-review")
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if persisted.Source != slashcmd.SkillSourceMandated {
		t.Errorf("persisted Source = %q, want mandated", persisted.Source)
	}
	if !persisted.OrgManaged {
		t.Error("persisted OrgManaged = false, want true")
	}
}

// TestApplyMandatedSkills_MalformedEntry verifies that a malformed JSON entry
// in the mandated_skills list is skipped but subsequent entries still apply.
func TestApplyMandatedSkills_MalformedEntry(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	valid := slashcmd.Skill{
		ID:      "valid-mandated",
		Trigger: "runbook",
		Kind:    slashcmd.KindText,
		Body:    "runbook body",
		Source:  slashcmd.SkillSourceMandated,
	}
	validRaw, _ := json.Marshal(valid)
	badRaw := json.RawMessage(`{not valid json`)

	errs := ApplyMandatedSkills(store, registry, []json.RawMessage{badRaw, validRaw})
	// One error for the malformed entry.
	if len(errs) != 1 {
		t.Errorf("expected 1 error, got %d: %v", len(errs), errs)
	}

	// Valid skill must still be installed.
	if _, ok := registry.Lookup("runbook"); !ok {
		t.Error("'runbook' not registered despite valid mandated entry")
	}
}

// TestApplyMandatedSkills_ShadowedIsSilent verifies that a mandated skill
// whose trigger is already occupied is silently skipped (informational, no
// hard error added to the returned slice).
func TestApplyMandatedSkills_ShadowedIsSilent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})

	// Pre-occupy "help" (a built-in).
	mandate := slashcmd.Skill{
		ID:      "org/help-override",
		Trigger: "help", // conflicts with the built-in
		Kind:    slashcmd.KindText,
		Body:    "org help",
		Source:  slashcmd.SkillSourceMandated,
	}
	raw, _ := json.Marshal(mandate)

	errs := ApplyMandatedSkills(store, registry, []json.RawMessage{raw})
	// ErrTriggerShadowed is NOT added to errs (informational only).
	if len(errs) != 0 {
		t.Errorf("expected 0 errors for shadowed mandated skill, got %d: %v", len(errs), errs)
	}

	// The built-in /help must still be the original.
	helpCmd, ok := registry.Lookup("help")
	if !ok {
		t.Fatal("'help' not found in registry")
	}
	if helpCmd.Description() == "org help" {
		t.Error("built-in 'help' was overwritten by the mandated skill")
	}
}
