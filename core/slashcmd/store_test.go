package slashcmd_test

import (
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
)

// ── SkillStore: Save + Get round-trip ────────────────────────────────────────

func TestSkillStore_SaveGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	sk := slashcmd.Skill{
		ID:          "org-standup-1",
		CatalogID:   "cat-abc",
		Version:     "1.0.0",
		Source:      slashcmd.SkillSourceCatalog,
		Trigger:     "standup",
		Kind:        slashcmd.KindText,
		Description: "Daily standup helper",
		Body:        "What did you do yesterday?",
	}
	if err := store.Save(sk); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Get("org-standup-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != "org-standup-1" {
		t.Errorf("ID = %q, want %q", got.ID, "org-standup-1")
	}
	if got.Trigger != "standup" {
		t.Errorf("Trigger = %q, want %q", got.Trigger, "standup")
	}
	if got.Body != "What did you do yesterday?" {
		t.Errorf("Body = %q, want body text", got.Body)
	}
	if got.InstalledAt == 0 {
		t.Error("InstalledAt must be set after Save")
	}
	if got.UpdatedAt == 0 {
		t.Error("UpdatedAt must be set after Save")
	}
}

// ── SkillStore: Get not-found returns ErrSkillNotFound ───────────────────────

func TestSkillStore_Get_NotFound(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	_, err := store.Get("nonexistent")
	if !errors.Is(err, slashcmd.ErrSkillNotFound) {
		t.Errorf("Get nonexistent = %v, want ErrSkillNotFound", err)
	}
}

// ── SkillStore: Delete is idempotent ─────────────────────────────────────────

func TestSkillStore_Delete_Idempotent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	// Save then delete.
	sk := slashcmd.Skill{
		ID:      "to-delete",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "todelete",
		Kind:    slashcmd.KindText,
		Body:    "body",
	}
	if err := store.Save(sk); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := store.Delete("to-delete"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Second delete is a no-op.
	if err := store.Delete("to-delete"); err != nil {
		t.Errorf("Delete idempotent: %v", err)
	}
	_, err := store.Get("to-delete")
	if !errors.Is(err, slashcmd.ErrSkillNotFound) {
		t.Errorf("Get after delete = %v, want ErrSkillNotFound", err)
	}
}

// ── SkillStore: List returns all saved skills ─────────────────────────────────

func TestSkillStore_List(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	skills := []slashcmd.Skill{
		{ID: "s1", Source: slashcmd.SkillSourceCatalog, Trigger: "alpha", Kind: slashcmd.KindText, Body: "a"},
		{ID: "s2", Source: slashcmd.SkillSourceCatalog, Trigger: "beta", Kind: slashcmd.KindText, Body: "b"},
		{ID: "s3", Source: slashcmd.SkillSourceMandated, Trigger: "gamma", Kind: slashcmd.KindPrompt, Body: "c"},
	}
	for _, sk := range skills {
		if err := store.Save(sk); err != nil {
			t.Fatalf("Save %q: %v", sk.ID, err)
		}
	}

	list, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Errorf("List len = %d, want 3", len(list))
	}

	// Verify OrgManaged derived correctly.
	for _, sk := range list {
		if sk.ID == "s3" && !sk.OrgManaged {
			t.Errorf("mandated skill s3: OrgManaged = false, want true")
		}
		if sk.ID != "s3" && sk.OrgManaged {
			t.Errorf("catalog skill %s: OrgManaged = true, want false", sk.ID)
		}
	}
}

// ── BootLoad: non-disabled skills are registered; built-ins win ──────────────

func TestBootLoad_RegistersSkills(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	// Save two skills: one enabled, one disabled.
	if err := store.Save(slashcmd.Skill{
		ID:      "enabled-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "myskill",
		Kind:    slashcmd.KindText,
		Body:    "Hello from myskill",
	}); err != nil {
		t.Fatalf("Save enabled: %v", err)
	}
	if err := store.Save(slashcmd.Skill{
		ID:       "disabled-skill",
		Source:   slashcmd.SkillSourceCatalog,
		Trigger:  "disabledskill",
		Kind:     slashcmd.KindText,
		Body:     "should not register",
		Disabled: true,
	}); err != nil {
		t.Fatalf("Save disabled: %v", err)
	}

	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	count, err := slashcmd.BootLoad(store, reg)
	if err != nil {
		t.Fatalf("BootLoad: %v", err)
	}
	if count != 1 {
		t.Errorf("BootLoad registered %d skills, want 1", count)
	}
	if _, ok := reg.Lookup("myskill"); !ok {
		t.Error("myskill not registered after BootLoad")
	}
	if _, ok := reg.Lookup("disabledskill"); ok {
		t.Error("disabledskill must not be registered (disabled=true)")
	}
}

// ── BootLoad: built-ins are NOT persisted (FR-003) ───────────────────────────

func TestBootLoad_BuiltinsNotPersisted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	// The built-in "/help" exists in every registry.  Try to boot-load a
	// skill with the same trigger — it should be silently skipped (shadowed
	// by the built-in), and the store file itself must NOT be deleted.
	if err := store.Save(slashcmd.Skill{
		ID:      "conflict-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "help", // conflicts with built-in /help
		Kind:    slashcmd.KindText,
		Body:    "conflict",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	// BootLoad must not overwrite the built-in.
	count, bootErr := slashcmd.BootLoad(store, reg)
	if bootErr != nil {
		t.Fatalf("BootLoad: %v", bootErr)
	}
	if count != 0 {
		t.Errorf("BootLoad registered %d (conflicting) skills, want 0", count)
	}

	// The file must still be on disk (built-ins are not persisted by store;
	// the skill file should survive even if shadowed).
	if _, err := store.Get("conflict-skill"); err != nil {
		t.Errorf("skill file missing after shadowed boot-load: %v", err)
	}

	// The /help command must still be the built-in (not the skill).
	helpCmd, ok := reg.Lookup("help")
	if !ok {
		t.Fatal("help not found in registry")
	}
	// Built-in /help is not a skill command — its description must not match
	// our test body.
	if helpCmd.Description() == "conflict" {
		t.Error("built-in /help was overwritten by the skill")
	}
}

// ── LiveRegister + LiveUnregister round-trip ─────────────────────────────────

func TestLiveRegister_LiveUnregister(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	sk := slashcmd.Skill{
		ID:      "live-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "livecmd",
		Kind:    slashcmd.KindText,
		Body:    "live body",
	}

	// LiveRegister must persist and register.
	if err := slashcmd.LiveRegister(store, reg, sk); err != nil {
		t.Fatalf("LiveRegister: %v", err)
	}
	if _, ok := reg.Lookup("livecmd"); !ok {
		t.Error("livecmd not registered after LiveRegister")
	}
	if _, err := store.Get("live-skill"); err != nil {
		t.Errorf("skill not persisted after LiveRegister: %v", err)
	}

	// LiveUnregister must remove from disk and registry.
	if err := slashcmd.LiveUnregister(store, reg, "live-skill"); err != nil {
		t.Fatalf("LiveUnregister: %v", err)
	}
	if _, ok := reg.Lookup("livecmd"); ok {
		t.Error("livecmd still registered after LiveUnregister")
	}
	if _, err := store.Get("live-skill"); !errors.Is(err, slashcmd.ErrSkillNotFound) {
		t.Errorf("skill still on disk after LiveUnregister: %v", err)
	}
}

// ── ErrTriggerShadowed when live-registering a conflicting trigger ────────────

func TestLiveRegister_TriggerShadowed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)

	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	sk := slashcmd.Skill{
		ID:      "shadow-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "help", // conflicts with built-in /help
		Kind:    slashcmd.KindText,
		Body:    "shadow body",
	}
	err = slashcmd.LiveRegister(store, reg, sk)
	if !errors.Is(err, slashcmd.ErrTriggerShadowed) {
		t.Errorf("LiveRegister conflicting = %v, want ErrTriggerShadowed", err)
	}
	// Skill must still be persisted even when shadowed.
	if _, getErr := store.Get("shadow-skill"); getErr != nil {
		t.Errorf("shadowed skill not persisted: %v", getErr)
	}
}

// ── Re-install replaces the skill's own registration (install-framework WP05) ─

// TestLiveRegister_ReinstallReplacesOwnRegistration: an update re-registers a
// skill already in the store. Before the fix the skill's previous
// registration occupied its own trigger, so every update failed as shadowed.
// A re-install of a shadowed skill must still never evict the command that
// shadows it.
func TestLiveRegister_ReinstallReplacesOwnRegistration(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	v1 := slashcmd.Skill{ID: "s", CatalogID: "cat-s", Source: slashcmd.SkillSourceCatalog, Trigger: "scmd", Kind: slashcmd.KindText, Body: "v1", Version: "1.0.0"}
	if err := slashcmd.LiveRegister(store, reg, v1); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := slashcmd.RenameLocalTrigger(store, reg, "s", "mys"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	v2 := v1
	v2.Body, v2.Version = "v2", "2.0.0"
	if err := slashcmd.LiveRegister(store, reg, v2); err != nil {
		t.Fatalf("re-install (update) failed: %v", err)
	}
	got, err := store.Get("s")
	if err != nil || got.Version != "2.0.0" || got.LocalTrigger != "mys" {
		t.Fatalf("stored = %+v, %v — want v2 with the user's local alias kept", got, err)
	}
	if _, ok := reg.Lookup("mys"); !ok {
		t.Fatal("updated skill not registered under its local alias")
	}

	// A shadowed skill re-installed must not evict the built-in /help.
	shadow := slashcmd.Skill{ID: "h", CatalogID: "cat-h", Source: slashcmd.SkillSourceCatalog, Trigger: "help", Kind: slashcmd.KindText, Body: "x"}
	_ = slashcmd.LiveRegister(store, reg, shadow)
	if err := slashcmd.LiveRegister(store, reg, shadow); !errors.Is(err, slashcmd.ErrTriggerShadowed) {
		t.Fatalf("re-install of a shadowed skill = %v, want ErrTriggerShadowed", err)
	}
	if cmd, ok := reg.Lookup("help"); !ok || cmd.Description() == "" {
		t.Fatal("built-in /help was evicted by a shadowed skill's re-install")
	}
}

// ── Ownership is the catalog item, not the payload's ID (review H3) ──────────

// TestLiveRegister_DifferentCatalogItemSameIDRefused: catalog item B
// shipping a payload with A's skill ID must not replace A (nor inherit A's
// local alias). Before the fix "own registration" was decided by the
// payload-controlled ID alone.
func TestLiveRegister_DifferentCatalogItemSameIDRefused(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	a := slashcmd.Skill{ID: "dup", CatalogID: "cat-a", Source: slashcmd.SkillSourceCatalog, Trigger: "acmd", Kind: slashcmd.KindText, Body: "A"}
	if err := slashcmd.LiveRegister(store, reg, a); err != nil {
		t.Fatal(err)
	}
	if err := slashcmd.RenameLocalTrigger(store, reg, "dup", "myalias"); err != nil {
		t.Fatal(err)
	}
	b := slashcmd.Skill{ID: "dup", CatalogID: "cat-b", Source: slashcmd.SkillSourceCatalog, Trigger: "bcmd", Kind: slashcmd.KindText, Body: "B"}
	if err := slashcmd.LiveRegister(store, reg, b); !errors.Is(err, slashcmd.ErrSkillIDCollision) {
		t.Fatalf("got %v, want ErrSkillIDCollision", err)
	}
	got, err := store.Get("dup")
	if err != nil || got.CatalogID != "cat-a" || got.Body != "A" || got.LocalTrigger != "myalias" {
		t.Fatalf("A was replaced: %+v, %v", got, err)
	}
	if _, ok := reg.Lookup("myalias"); !ok {
		t.Fatal("A's registration was removed")
	}
	if _, ok := reg.Lookup("bcmd"); ok {
		t.Fatal("B was registered")
	}
}

// TestLiveRegister_CatalogInstallCannotReplaceOrgMandated: a team-catalog
// payload carrying a mandated skill's ID is refused; the mandated skill
// stays stored as OrgManaged and keeps dispatching.
func TestLiveRegister_CatalogInstallCannotReplaceOrgMandated(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	m := slashcmd.Skill{ID: "policy", Source: slashcmd.SkillSourceMandated, OrgManaged: true, Trigger: "policy", Kind: slashcmd.KindText, Body: "org"}
	if err := slashcmd.LiveRegister(store, reg, m); err != nil {
		t.Fatal(err)
	}
	team := slashcmd.Skill{ID: "policy", CatalogID: "cat-team", Source: slashcmd.SkillSourceCatalog, Trigger: "policy", Kind: slashcmd.KindText, Body: "team"}
	if err := slashcmd.LiveRegister(store, reg, team); !errors.Is(err, slashcmd.ErrSkillOrgManaged) {
		t.Fatalf("got %v, want ErrSkillOrgManaged", err)
	}
	got, err := store.Get("policy")
	if err != nil || !got.OrgManaged || got.Source != slashcmd.SkillSourceMandated || got.Body != "org" {
		t.Fatalf("mandated skill replaced: %+v, %v", got, err)
	}
	if cmd, ok := reg.Lookup("policy"); !ok || cmd.Description() != m.Description {
		t.Fatal("mandated dispatch was disturbed")
	}
	// The org itself may re-push it.
	m2 := m
	m2.Body = "org v2"
	if err := slashcmd.LiveRegister(store, reg, m2); err != nil {
		t.Fatalf("mandated re-push: %v", err)
	}
}

// TestLiveUnregister_ShadowedSkillDoesNotEvictTheBuiltin (review L3):
// uninstalling (or renaming) a skill whose trigger is shadowed by a built-in
// must not remove the built-in.
func TestLiveUnregister_ShadowedSkillDoesNotEvictTheBuiltin(t *testing.T) {
	t.Parallel()
	store := slashcmd.NewSkillStore(t.TempDir())
	reg, _ := slashcmd.NewRegistry(slashcmd.Deps{})
	sh := slashcmd.Skill{ID: "sh", CatalogID: "cat-sh", Source: slashcmd.SkillSourceCatalog, Trigger: "help", Kind: slashcmd.KindText, Body: "x"}
	if err := slashcmd.LiveRegister(store, reg, sh); !errors.Is(err, slashcmd.ErrTriggerShadowed) {
		t.Fatalf("setup: %v", err)
	}
	if err := slashcmd.RenameLocalTrigger(store, reg, "sh", "myhelp"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, ok := reg.Lookup("help"); !ok {
		t.Fatal("renaming a shadowed skill removed the built-in /help")
	}
	if err := slashcmd.LiveUnregister(store, reg, "sh"); err != nil {
		t.Fatalf("LiveUnregister: %v", err)
	}
	if _, ok := reg.Lookup("help"); !ok {
		t.Fatal("uninstalling a shadowed skill removed the built-in /help")
	}
	if _, ok := reg.Lookup("myhelp"); ok {
		t.Fatal("the skill's own alias registration survived uninstall")
	}

	// And the shadowed-then-uninstalled path without a rename.
	sh2 := slashcmd.Skill{ID: "sh2", CatalogID: "cat-sh2", Source: slashcmd.SkillSourceCatalog, Trigger: "help", Kind: slashcmd.KindText, Body: "y"}
	_ = slashcmd.LiveRegister(store, reg, sh2)
	if err := slashcmd.LiveUnregister(store, reg, "sh2"); err != nil {
		t.Fatal(err)
	}
	if _, ok := reg.Lookup("help"); !ok {
		t.Fatal("uninstalling a shadowed skill (no rename) removed the built-in /help")
	}
}

// ── EffectiveTrigger uses LocalTrigger when set ───────────────────────────────

func TestSkill_EffectiveTrigger(t *testing.T) {
	t.Parallel()
	sk := slashcmd.Skill{Trigger: "review", LocalTrigger: "myreview"}
	if sk.EffectiveTrigger() != "myreview" {
		t.Errorf("EffectiveTrigger = %q, want %q", sk.EffectiveTrigger(), "myreview")
	}
	sk2 := slashcmd.Skill{Trigger: "review"}
	if sk2.EffectiveTrigger() != "review" {
		t.Errorf("EffectiveTrigger = %q, want %q", sk2.EffectiveTrigger(), "review")
	}
}

// ── RenameLocalTrigger (WP06 FR-401) ─────────────────────────────────────────

// TestRenameLocalTrigger_Basic verifies the happy path: renaming a local
// trigger unregisters the old trigger and registers the new one.
func TestRenameLocalTrigger_Basic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	sk := slashcmd.Skill{
		ID:      "rename-skill",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "review",
		Kind:    slashcmd.KindText,
		Body:    "review body",
	}
	if err := slashcmd.LiveRegister(store, reg, sk); err != nil {
		t.Fatalf("LiveRegister: %v", err)
	}

	// Rename /review → /myreview.
	if err := slashcmd.RenameLocalTrigger(store, reg, "rename-skill", "myreview"); err != nil {
		t.Fatalf("RenameLocalTrigger: %v", err)
	}

	// Old trigger must be gone.
	if _, ok := reg.Lookup("review"); ok {
		t.Error("old trigger 'review' still registered after rename")
	}
	// New trigger must be present.
	if _, ok := reg.Lookup("myreview"); !ok {
		t.Error("new trigger 'myreview' not registered after rename")
	}
	// Persisted skill must have LocalTrigger set.
	persisted, err := store.Get("rename-skill")
	if err != nil {
		t.Fatalf("Get after rename: %v", err)
	}
	if persisted.LocalTrigger != "myreview" {
		t.Errorf("persisted LocalTrigger = %q, want %q", persisted.LocalTrigger, "myreview")
	}
}

// TestRenameLocalTrigger_ClearAlias verifies that passing an empty newTrigger
// clears the alias and reverts to the canonical trigger.
func TestRenameLocalTrigger_ClearAlias(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	sk := slashcmd.Skill{
		ID:           "rename-clear",
		Source:       slashcmd.SkillSourceCatalog,
		Trigger:      "pr",
		LocalTrigger: "mypr",
		Kind:         slashcmd.KindText,
		Body:         "pr body",
	}
	if err := slashcmd.LiveRegister(store, reg, sk); err != nil {
		t.Fatalf("LiveRegister: %v", err)
	}

	// skill is registered under "mypr" (the LocalTrigger).
	if _, ok := reg.Lookup("mypr"); !ok {
		t.Error("'mypr' not registered after LiveRegister with LocalTrigger")
	}

	// Clear the alias.
	if err := slashcmd.RenameLocalTrigger(store, reg, "rename-clear", ""); err != nil {
		t.Fatalf("RenameLocalTrigger (clear): %v", err)
	}

	// Old local trigger must be gone.
	if _, ok := reg.Lookup("mypr"); ok {
		t.Error("'mypr' still registered after clearing alias")
	}
	// Canonical trigger must be registered.
	if _, ok := reg.Lookup("pr"); !ok {
		t.Error("canonical 'pr' not registered after clearing alias")
	}
	// LocalTrigger must be cleared on disk.
	persisted, err := store.Get("rename-clear")
	if err != nil {
		t.Fatalf("Get after clear: %v", err)
	}
	if persisted.LocalTrigger != "" {
		t.Errorf("persisted LocalTrigger = %q after clear, want empty", persisted.LocalTrigger)
	}
}

// TestRenameLocalTrigger_ShadowedNewTrigger verifies that renaming to a trigger
// already occupied by another command returns ErrTriggerShadowed.
func TestRenameLocalTrigger_ShadowedNewTrigger(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	// Register the skill we want to rename.
	sk := slashcmd.Skill{
		ID:      "rename-shadow",
		Source:  slashcmd.SkillSourceCatalog,
		Trigger: "deploy",
		Kind:    slashcmd.KindText,
		Body:    "deploy body",
	}
	if err := slashcmd.LiveRegister(store, reg, sk); err != nil {
		t.Fatalf("LiveRegister: %v", err)
	}

	// Attempt to rename to "help" which is a built-in.
	renameErr := slashcmd.RenameLocalTrigger(store, reg, "rename-shadow", "help")
	if !errors.Is(renameErr, slashcmd.ErrTriggerShadowed) {
		t.Errorf("expected ErrTriggerShadowed, got: %v", renameErr)
	}
}

// TestRenameLocalTrigger_NotFound verifies that renaming an unknown skill
// returns ErrSkillNotFound.
func TestRenameLocalTrigger_NotFound(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := slashcmd.NewSkillStore(dir)
	reg, err := slashcmd.NewRegistry(slashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	renameErr := slashcmd.RenameLocalTrigger(store, reg, "nonexistent", "something")
	if !errors.Is(renameErr, slashcmd.ErrSkillNotFound) {
		t.Errorf("expected ErrSkillNotFound, got: %v", renameErr)
	}
}
