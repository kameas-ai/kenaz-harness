package slashcmd_test

import (
	"context"
	"errors"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

func openSlashTestDB(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	}
	db, err := storagesqlite.Open(cfg)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, dir
}

func globalTextCmd(name string) slashcmd.UserCommand {
	return slashcmd.UserCommand{
		Name:        name,
		Scope:       slashcmd.ScopeGlobal,
		Kind:        slashcmd.KindText,
		Description: "A global text command",
		Body:        "Hello from " + name,
	}
}

func projectToolCmd(name, projectID string) slashcmd.UserCommand {
	return slashcmd.UserCommand{
		Name:             name,
		Scope:            slashcmd.ScopeProject,
		ProjectID:        projectID,
		Kind:             slashcmd.KindTool,
		Description:      "A project tool command",
		Tool:             "bash",
		ToolArgsTemplate: "./scripts/" + name + ".sh",
	}
}

// ── SaveUser + LoadUserOne round-trip ────────────────────────────────

func TestStore_SaveLoad_Global(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := globalTextCmd("standup")
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	got, err := store.LoadUserOne(ctx, "standup", "")
	if err != nil {
		t.Fatalf("LoadUserOne: %v", err)
	}
	if got.Name != "standup" {
		t.Errorf("Name = %q, want %q", got.Name, "standup")
	}
	if got.Kind != slashcmd.KindText {
		t.Errorf("Kind = %q, want %q", got.Kind, slashcmd.KindText)
	}
	if got.Body != "Hello from standup" {
		t.Errorf("Body = %q, want %q", got.Body, "Hello from standup")
	}
}

func TestStore_SaveLoad_Project(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := projectToolCmd("deploy", "proj-123")
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	got, err := store.LoadUserOne(ctx, "deploy", "proj-123")
	if err != nil {
		t.Fatalf("LoadUserOne: %v", err)
	}
	if got.ProjectID != "proj-123" {
		t.Errorf("ProjectID = %q, want %q", got.ProjectID, "proj-123")
	}
	if got.Kind != slashcmd.KindTool {
		t.Errorf("Kind = %q, want %q", got.Kind, slashcmd.KindTool)
	}
}

// ── Project command shadows global on same name ───────────────────────

func TestStore_LoadUser_ProjectShadowsGlobal(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	// Save a global text command named "deploy".
	globalCmd := globalTextCmd("deploy")
	if err := store.SaveUser(ctx, globalCmd); err != nil {
		t.Fatalf("SaveUser global: %v", err)
	}

	// Save a project tool command with the same name.
	projCmd := projectToolCmd("deploy", "proj-abc")
	if err := store.SaveUser(ctx, projCmd); err != nil {
		t.Fatalf("SaveUser project: %v", err)
	}

	// LoadUser for proj-abc should return the project-scoped version.
	cmds, err := store.LoadUser(ctx, "proj-abc")
	if err != nil {
		t.Fatalf("LoadUser: %v", err)
	}
	var found *slashcmd.UserCommand
	for i := range cmds {
		if cmds[i].Name == "deploy" {
			found = &cmds[i]
		}
	}
	if found == nil {
		t.Fatal("deploy command not found in LoadUser result")
	}
	if found.Scope != slashcmd.ScopeProject {
		t.Errorf("deploy Scope = %q, want %q", found.Scope, slashcmd.ScopeProject)
	}
}

// ── Cross-scope isolation: project command absent from other project ──

func TestStore_LoadUser_CrossScopeIsolation(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	// Save a command scoped to proj-A.
	if err := store.SaveUser(ctx, projectToolCmd("build", "proj-A")); err != nil {
		t.Fatalf("SaveUser proj-A: %v", err)
	}

	// LoadUser for proj-B should NOT return the proj-A command.
	cmds, err := store.LoadUser(ctx, "proj-B")
	if err != nil {
		t.Fatalf("LoadUser proj-B: %v", err)
	}
	for _, c := range cmds {
		if c.Name == "build" {
			t.Errorf("proj-A command 'build' leaked into proj-B result")
		}
	}
}

// ── DeleteUser ────────────────────────────────────────────────────────

func TestStore_DeleteUser(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	if err := store.SaveUser(ctx, globalTextCmd("lint")); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if err := store.DeleteUser(ctx, "lint", ""); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	_, err := store.LoadUserOne(ctx, "lint", "")
	if !errors.Is(err, slashcmd.ErrCommandNotFound) {
		t.Errorf("after delete LoadUserOne = %v, want ErrCommandNotFound", err)
	}
}

// ── LoadUserOne not-found ─────────────────────────────────────────────

func TestStore_LoadUserOne_NotFound(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	_, err := store.LoadUserOne(ctx, "no-such-command", "")
	if !errors.Is(err, slashcmd.ErrCommandNotFound) {
		t.Errorf("LoadUserOne missing = %v, want ErrCommandNotFound", err)
	}
}

// ── ListModelInvokable ────────────────────────────────────────────────

func TestStore_ListModelInvokable(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	// Save one model-invokable and one non-invokable.
	inv := slashcmd.UserCommand{
		Name:            "review",
		Scope:           slashcmd.ScopeGlobal,
		Kind:            slashcmd.KindPrompt,
		Description:     "Review code",
		Body:            "Review: {{selection}}",
		ModelInvokable:  true,
	}
	plain := globalTextCmd("standup")
	if err := store.SaveUser(ctx, inv); err != nil {
		t.Fatalf("SaveUser invokable: %v", err)
	}
	if err := store.SaveUser(ctx, plain); err != nil {
		t.Fatalf("SaveUser plain: %v", err)
	}

	result, err := store.ListModelInvokable(ctx)
	if err != nil {
		t.Fatalf("ListModelInvokable: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("ListModelInvokable len = %d, want 1", len(result))
	}
	if result[0].Name != "review" {
		t.Errorf("ListModelInvokable[0].Name = %q, want %q", result[0].Name, "review")
	}
}

// ── Validate propagates errors from SaveUser ──────────────────────────

func TestStore_SaveUser_ValidateError(t *testing.T) {
	t.Parallel()
	db, dir := openSlashTestDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	bad := slashcmd.UserCommand{
		Name:  "BAD_NAME", // violates name regex
		Scope: slashcmd.ScopeGlobal,
		Kind:  slashcmd.KindText,
		Body:  "body",
	}
	err := store.SaveUser(ctx, bad)
	if !errors.Is(err, slashcmd.ErrValidation) {
		t.Errorf("SaveUser bad name = %v, want ErrValidation", err)
	}
}
