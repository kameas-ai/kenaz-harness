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
