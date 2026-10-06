// skills_sync_test.go — httptest round-trip tests for PublishSkill,
// InstallSkill, UninstallSkill, and ApplyMandatedSkills.
//
// (fleet-skills-sync-01NDFSEX18 WP07)
package fleet

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
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

	// Install the way the install framework's skill provider does:
	// FetchCatalogItem (Verify) then InstallSkillPayload (Install) on the
	// same bytes. The signature verdict is the framework's step, pinned in
	// TestCatalogSignatureVerdict_*.
	if err := installSkillForTest(t, c, store, registry, item.ID, item.Version); err != nil {
		t.Fatalf("install: %v", err)
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

// TestFetchCatalogItem_FleetDisabled verifies that the fetch step returns
// ErrFleetDisabled for a nop client.
func TestFetchCatalogItem_FleetDisabled(t *testing.T) {
	t.Parallel()
	if _, err := FetchCatalogItem(context.Background(), nil, "cat-id", "1.0.0"); err != ErrFleetDisabled {
		t.Errorf("expected ErrFleetDisabled, got: %v", err)
	}
}

// TestInstallSkillPayload_MalformedPayloadIsANamedError pins FR-2's "opaque
// payload → named install error, never a silent success" for skills.
func TestInstallSkillPayload_MalformedPayloadIsANamedError(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	for _, payload := range [][]byte{[]byte("not json"), []byte(`{"body":"no id"}`)} {
		err := InstallSkillPayload(store, registry, "cat", "1.0.0", payload)
		if !errors.Is(err, ErrCatalogPayloadMalformed) {
			t.Errorf("payload %q: got %v, want ErrCatalogPayloadMalformed", payload, err)
		}
	}
	if skills, _ := store.List(); len(skills) != 0 {
		t.Errorf("a malformed payload persisted %d skill(s)", len(skills))
	}
}

// TestCatalogSignatureVerdict pins the single verification hook's fleet
// half (register C-2): no key → unverified with the C-2 reason, no error;
// a real key verifies a good signature and rejects a bad one.
func TestCatalogSignatureVerdict(t *testing.T) {
	t.Parallel()
	ok, reason, err := CatalogSignatureVerdict("", []byte("p"), "sig")
	if ok || err != nil || !strings.Contains(reason, "C-2") {
		t.Fatalf("no key: got (%v, %q, %v)", ok, reason, err)
	}
	// The reason states the real design (fleet owner, 2026-10-05): only the
	// config bundle is signed; no per-device/per-org catalog key is coming.
	if !strings.Contains(reason, "config bundle") || strings.Contains(reason, "per-device") {
		t.Fatalf("no key: reason %q must say only the config bundle is signed, not promise a per-device key", reason)
	}

	pub, priv, _ := ed25519.GenerateKey(nil)
	pubB64 := base64.StdEncoding.EncodeToString(pub)
	payload := []byte(`{"id":"x"}`)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, payload))
	if ok, _, err := CatalogSignatureVerdict(pubB64, payload, sig); !ok || err != nil {
		t.Fatalf("good signature: got (%v, %v)", ok, err)
	}
	if ok, _, err := CatalogSignatureVerdict(pubB64, []byte("tampered"), sig); ok || !errors.Is(err, ErrCatalogSignatureMismatch) {
		t.Fatalf("tampered payload: got (%v, %v), want ErrCatalogSignatureMismatch", ok, err)
	}
}

// installSkillForTest composes the provider's Verify-fetch and Install
// steps without the framework.
func installSkillForTest(t *testing.T, c *Client, store *slashcmd.SkillStore, registry *slashcmd.Registry, id, version string) error {
	t.Helper()
	item, err := FetchCatalogItem(context.Background(), c, id, version)
	if err != nil {
		return err
	}
	return InstallSkillPayload(store, registry, id, version, item.PayloadBytes)
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

// TestUninstallSkill_ByCatalogID — install-framework-01DOGF0B WP01. The
// catalog browse (then the Marketplace, now the Capabilities surface) knows
// only the catalog_id; the store keys on the payload's
// skill ID. Pre-fix, UninstallSkill(catalogID) returned ErrSkillNotFound for
// every SkillPublish'd skill, so the Uninstall button the registry-backed
// badge shows would always fail.
func TestUninstallSkill_ByCatalogID(t *testing.T) {
	fake := &fakeCatalogServer{}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	stubTokens(t, TokenSet{AccessToken: "at-u", RefreshToken: "rt-u", ExpiresAt: time.Now().Add(time.Hour)})
	c := makeTestClient(t, srv.URL)
	store, registry, signer, caps := makeSkillTestSetup(t)

	skill := slashcmd.Skill{
		ID: "by-catalog", Trigger: "bycat", Kind: slashcmd.KindText,
		Body: "x", Source: slashcmd.SkillSourceCatalog,
	}
	item, err := PublishSkill(context.Background(), c, caps, signer, skill, CatalogVisTeam)
	if err != nil {
		t.Fatalf("PublishSkill: %v", err)
	}
	if item.ID == skill.ID {
		t.Fatalf("fixture invalid: catalog ID %q equals store ID; the test needs them distinct", item.ID)
	}
	if err := installSkillForTest(t, c, store, registry, item.ID, item.Version); err != nil {
		t.Fatalf("install: %v", err)
	}

	if err := UninstallSkill(store, registry, item.ID); err != nil {
		t.Fatalf("UninstallSkill(catalogID): %v", err)
	}
	if _, ok := registry.Lookup("bycat"); ok {
		t.Error("'bycat' still registered after UninstallSkill by catalog ID")
	}
	if _, err := store.Get("by-catalog"); err == nil {
		t.Error("skill still in store after UninstallSkill by catalog ID")
	}
}

// TestResolveSkillStoreID_CatalogIDWinsOverCollidingStoreID — review F3.
// Skill A is stored under ID "shared"; skill B came from the catalog with
// catalog_id "shared" but is stored under "b-local". The catalog browse sends
// "shared" meaning B. Exact-store-ID-first resolution deleted A instead.
func TestResolveSkillStoreID_CatalogIDWinsOverCollidingStoreID(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	a := slashcmd.Skill{ID: "shared", Trigger: "skilla", Kind: slashcmd.KindText, Body: "a", Source: slashcmd.SkillSourceCatalog}
	b := slashcmd.Skill{ID: "b-local", CatalogID: "shared", Version: "2.0.0", Trigger: "skillb", Kind: slashcmd.KindText, Body: "b", Source: slashcmd.SkillSourceCatalog}
	for _, sk := range []slashcmd.Skill{a, b} {
		if err := slashcmd.LiveRegister(store, registry, sk); err != nil {
			t.Fatalf("LiveRegister %s: %v", sk.ID, err)
		}
	}

	if got := ResolveSkillStoreID(store, "shared", "2.0.0"); got != "b-local" {
		t.Errorf("ResolveSkillStoreID(shared, 2.0.0) = %q, want b-local", got)
	}
	if err := UninstallSkill(store, registry, "shared"); err != nil {
		t.Fatalf("UninstallSkill(shared): %v", err)
	}
	if _, err := store.Get("shared"); err != nil {
		t.Errorf("skill A (store ID \"shared\") was deleted by a catalog-id uninstall: %v", err)
	}
	if _, err := store.Get("b-local"); err == nil {
		t.Error("skill B (catalog_id \"shared\") still in store after uninstall by its catalog_id")
	}
	if _, ok := registry.Lookup("skilla"); !ok {
		t.Error("skill A's trigger was unregistered")
	}
}

// Re-review low 5: only an org-mandated push may take a skill id another
// skill holds. ApplyMandatedSkills over a catalog skill and over a local
// skill swaps BOTH the store row (now mandated / org-managed) and the
// dispatch (the trigger now runs the mandated body).
func TestApplyMandatedSkills_ReplacesCatalogAndLocalSkillsCleanly(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	registry, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	for _, sk := range []slashcmd.Skill{
		{ID: "from-cat", CatalogID: "cat-1", Source: slashcmd.SkillSourceCatalog, Trigger: "catcmd", Kind: slashcmd.KindText, Body: "catalog", Description: "catalog"},
		{ID: "from-local", Trigger: "localcmd", Kind: slashcmd.KindText, Body: "local", Description: "local"},
	} {
		if err := slashcmd.LiveRegister(store, registry, sk); err != nil {
			t.Fatalf("setup %s: %v", sk.ID, err)
		}
	}
	var raws []json.RawMessage
	for _, sk := range []slashcmd.Skill{
		{ID: "from-cat", Trigger: "catcmd", Kind: slashcmd.KindText, Body: "org", Description: "org-cat"},
		{ID: "from-local", Trigger: "localcmd", Kind: slashcmd.KindText, Body: "org", Description: "org-local"},
	} {
		b, _ := json.Marshal(sk)
		raws = append(raws, b)
	}
	if errs := ApplyMandatedSkills(store, registry, raws); len(errs) != 0 {
		t.Fatalf("ApplyMandatedSkills: %v", errs)
	}
	for id, trig := range map[string]string{"from-cat": "catcmd", "from-local": "localcmd"} {
		got, err := store.Get(id)
		if err != nil || got.Source != slashcmd.SkillSourceMandated || !got.OrgManaged || got.Body != "org" {
			t.Fatalf("%s stored = %+v, %v — want the mandated copy", id, got, err)
		}
		cmd, ok := registry.Lookup(trig)
		if !ok || !strings.HasPrefix(cmd.Description(), "org-") {
			t.Fatalf("/%s dispatch = %v (found %v) — still the replaced skill", trig, cmd, ok)
		}
	}
}
