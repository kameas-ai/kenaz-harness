// sync_categories_slashcmd_test.go — tests for the WP05 genericity proof
// (fleet-generic-sync-framework-01NSYNC02): registering slash_commands as a
// new user-scoped SyncKind through the registry built in WP01.
package rpc

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// newTestSlashStore constructs a real (sqlite + filesystem backed)
// *coreslashcmd.Store rooted at a temp directory, mirroring
// core/slashcmd's own openSlashTestDB test helper.
func newTestSlashStore(t *testing.T) *coreslashcmd.Store {
	t.Helper()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("storagesqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return coreslashcmd.NewStore(db, dir)
}

func globalTextCmd(name string) coreslashcmd.UserCommand {
	return coreslashcmd.UserCommand{
		Name:        name,
		Scope:       coreslashcmd.ScopeGlobal,
		Kind:        coreslashcmd.KindText,
		Description: "a global text command",
		Body:        "hello from " + name,
	}
}

func projectToolCmd(name, projectID string) coreslashcmd.UserCommand {
	return coreslashcmd.UserCommand{
		Name:             name,
		Scope:            coreslashcmd.ScopeProject,
		ProjectID:        projectID,
		Kind:             coreslashcmd.KindTool,
		Description:      "a project tool command",
		Tool:             "kenaz__bash",
		ToolArgsTemplate: "./scripts/" + name + ".sh",
	}
}

// ── slashCommandsKind: registry entry shape ─────────────────────────────

func TestSlashCommandsKind_RegistersWithValidFields(t *testing.T) {
	store := newTestSlashStore(t)
	k := slashCommandsKind(store)
	if k.ID != "slash_commands" {
		t.Errorf("ID = %q, want slash_commands", k.ID)
	}
	if !k.HasScope(corefleet.ScopeUser) {
		t.Error("expected slash_commands to declare ScopeUser")
	}
	if k.Transport != corefleet.TransportLWWCategory {
		t.Errorf("Transport = %q, want lww_category", k.Transport)
	}
	if k.SecretPolicy != corefleet.SecretPolicyMustNotContainSecrets {
		t.Errorf("SecretPolicy = %q, want must_not_contain_secrets", k.SecretPolicy)
	}
	if k.ConflictPolicy != corefleet.ConflictPolicyLWW {
		t.Errorf("ConflictPolicy = %q, want lww", k.ConflictPolicy)
	}

	registry := corefleet.NewKindRegistry()
	if err := registry.Register(k); err != nil {
		t.Fatalf("Register: %v", err)
	}
}

// ── Collect: global-only, credential-free, device-path stripped ────────

func TestSlashCommandsKind_CollectGlobalOnly(t *testing.T) {
	store := newTestSlashStore(t)
	ctx := context.Background()

	if err := store.SaveUser(ctx, globalTextCmd("standup")); err != nil {
		t.Fatalf("SaveUser global: %v", err)
	}
	if err := store.SaveUser(ctx, projectToolCmd("deploy", "proj-1")); err != nil {
		t.Fatalf("SaveUser project: %v", err)
	}

	k := slashCommandsKind(store)
	raw, err := k.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}

	var p slashCommandSyncPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Items) != 1 {
		t.Fatalf("collected %d items, want 1 (project-scoped must be excluded)", len(p.Items))
	}
	if p.Items[0].Name != "standup" {
		t.Errorf("collected item = %q, want standup", p.Items[0].Name)
	}
	if p.Items[0].Scope != coreslashcmd.ScopeGlobal {
		t.Errorf("collected item scope = %q, want global", p.Items[0].Scope)
	}
	if p.Items[0].PayloadPath != "" {
		t.Errorf("collected item PayloadPath = %q, want empty (device-local, must not be on the wire)", p.Items[0].PayloadPath)
	}
}

func TestSlashCommandsKind_CollectCredentialFree(t *testing.T) {
	store := newTestSlashStore(t)
	ctx := context.Background()
	cmd := globalTextCmd("greet")
	cmd.Body = "just a plain greeting, nothing sensitive"
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	k := slashCommandsKind(store)
	raw, err := k.Collect(ctx)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	rawStr := strings.ToLower(string(raw))
	for _, forbidden := range []string{"api_key", "apikey", "secret", "password", "credential"} {
		if strings.Contains(rawStr, forbidden) {
			t.Errorf("credential-like token %q found in slash_commands payload: %s", forbidden, raw)
		}
	}
}

func TestSlashCommandsKind_CollectEmptyWhenNoCommands(t *testing.T) {
	store := newTestSlashStore(t)
	k := slashCommandsKind(store)
	raw, err := k.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	var p slashCommandSyncPayload
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(p.Items) != 0 {
		t.Errorf("expected 0 items, got %d", len(p.Items))
	}
}

// ── Apply: round trip, invalid-item skip, scope enforcement ────────────

func TestSlashCommandsKind_ApplyRoundTrip(t *testing.T) {
	store := newTestSlashStore(t)
	ctx := context.Background()
	k := slashCommandsKind(store)

	payload, _ := json.Marshal(slashCommandSyncPayload{
		Items: []coreslashcmd.UserCommand{globalTextCmd("standup")},
	})
	if err := k.Apply(ctx, corefleet.ScopeUser, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := store.LoadUserOne(ctx, "standup", "")
	if err != nil {
		t.Fatalf("LoadUserOne: %v", err)
	}
	if got.Description != "a global text command" {
		t.Errorf("Description = %q, want %q", got.Description, "a global text command")
	}
	if got.Body != "hello from standup" {
		t.Errorf("Body = %q, want %q", got.Body, "hello from standup")
	}
}

func TestSlashCommandsKind_ApplySkipsInvalidButAppliesRest(t *testing.T) {
	store := newTestSlashStore(t)
	ctx := context.Background()
	k := slashCommandsKind(store)

	invalid := globalTextCmd("Bad Name!") // fails nameRe
	valid := globalTextCmd("goodname")
	payload, _ := json.Marshal(slashCommandSyncPayload{Items: []coreslashcmd.UserCommand{invalid, valid}})

	if err := k.Apply(ctx, corefleet.ScopeUser, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if _, err := store.LoadUserOne(ctx, "goodname", ""); err != nil {
		t.Errorf("expected valid command to be applied: %v", err)
	}
	if _, err := store.LoadUserOne(ctx, "Bad Name!", ""); err == nil {
		t.Error("expected invalid command to be skipped, not persisted")
	}
}

func TestSlashCommandsKind_ApplyForcesGlobalScope(t *testing.T) {
	store := newTestSlashStore(t)
	ctx := context.Background()
	k := slashCommandsKind(store)

	// A payload item smuggling a project scope must land as global with no
	// project_id — this category never carries project-scoped commands.
	smuggled := projectToolCmd("sneaky", "some-other-project")
	payload, _ := json.Marshal(slashCommandSyncPayload{Items: []coreslashcmd.UserCommand{smuggled}})

	if err := k.Apply(ctx, corefleet.ScopeUser, payload); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := store.LoadUserOne(ctx, "sneaky", "")
	if err != nil {
		t.Fatalf("expected command to be applied as global: %v", err)
	}
	if got.Scope != coreslashcmd.ScopeGlobal {
		t.Errorf("Scope = %q, want global", got.Scope)
	}
	if got.ProjectID != "" {
		t.Errorf("ProjectID = %q, want empty", got.ProjectID)
	}
}

// ── registerSlashCommandsSyncKind wiring ────────────────────────────────

func TestRegisterSlashCommandsSyncKind_NilGuards(t *testing.T) {
	// Must not panic with any nil combination.
	registerSlashCommandsSyncKind(nil, nil, nil)

	store := newTestSlashStore(t)
	registerSlashCommandsSyncKind(nil, corefleet.NewKindRegistry(), store)

	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)
	registerSlashCommandsSyncKind(syncer, nil, store)
	if len(syncer.RegisteredCategories()) != 0 {
		t.Error("expected no registration when registry is nil")
	}
}

func TestRegisterSlashCommandsSyncKind_Registers(t *testing.T) {
	store := newTestSlashStore(t)
	registry := corefleet.NewKindRegistry()
	syncer := corefleet.NewSyncer(nil)
	t.Cleanup(syncer.Stop)

	registerSlashCommandsSyncKind(syncer, registry, store)

	if _, ok := registry.Kind("slash_commands"); !ok {
		t.Error("expected slash_commands to be registered in the KindRegistry")
	}
	found := false
	for _, cat := range syncer.RegisteredCategories() {
		if cat == SyncCategorySlashCommands {
			found = true
		}
	}
	if !found {
		t.Error("expected slash_commands to be registered on the Syncer")
	}

	// Collect must work through the Syncer path too (proves the
	// CategoryConfig adapter wiring, not just the raw SyncKind).
	if _, err := syncer.CollectCategory(context.Background(), SyncCategorySlashCommands); err != nil {
		t.Errorf("CollectCategory(slash_commands): %v", err)
	}
}

// TestSlashCommandsKind_TwoDeviceSync simulates the WP05 acceptance
// criterion ("kind syncs across two devices in dev") at the collect/apply
// level: device A's collected payload, handed to device B's applier,
// reproduces the command on device B — the same round trip a real push+pull
// through fleet's /api/v1/sync/slash_commands would perform, minus the HTTP
// hop (core/fleet's own sync_test.go covers that transport layer generically
// for any category).
func TestSlashCommandsKind_TwoDeviceSync(t *testing.T) {
	ctx := context.Background()
	deviceA := newTestSlashStore(t)
	deviceB := newTestSlashStore(t)

	if err := deviceA.SaveUser(ctx, globalTextCmd("standup")); err != nil {
		t.Fatalf("device A SaveUser: %v", err)
	}

	kindA := slashCommandsKind(deviceA)
	raw, err := kindA.Collect(ctx)
	if err != nil {
		t.Fatalf("device A Collect: %v", err)
	}

	kindB := slashCommandsKind(deviceB)
	if err := kindB.Apply(ctx, corefleet.ScopeUser, raw); err != nil {
		t.Fatalf("device B Apply: %v", err)
	}

	got, err := deviceB.LoadUserOne(ctx, "standup", "")
	if err != nil {
		t.Fatalf("device B LoadUserOne: %v", err)
	}
	if got.Body != "hello from standup" {
		t.Errorf("device B Body = %q, want %q", got.Body, "hello from standup")
	}
}
