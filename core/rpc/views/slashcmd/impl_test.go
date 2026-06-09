package slashcmd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

type fakeAppender struct{ called int }

func (f *fakeAppender) AppendSystemMessage(_ context.Context, sessionID, _ string) (string, error) {
	f.called++
	return "msg-" + sessionID, nil
}

type fakeProviders struct {
	rows []coreslashcmd.Provider
}

func (f *fakeProviders) ListProviders(_ context.Context) ([]coreslashcmd.Provider, error) {
	return f.rows, nil
}

func newView(t *testing.T) *slashview.API {
	t.Helper()
	reg, err := coreslashcmd.NewRegistry(coreslashcmd.Deps{
		Sessions: &fakeAppender{},
		Providers: &fakeProviders{rows: []coreslashcmd.Provider{
			{ID: "anthropic-1", Name: "Anthropic", DefaultModel: "claude-sonnet-4-7"},
			{ID: "openai-1", Name: "OpenAI", DefaultModel: "gpt-4o"},
		}},
	})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return slashview.New(reg)
}

func TestAPI_List_SortedAndNoneComingSoon(t *testing.T) {
	t.Parallel()
	api := newView(t)
	cmds, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cmds) != 10 {
		t.Fatalf("len(cmds) = %d, want 10", len(cmds))
	}
	want := []string{"branch", "clear", "effort", "forget", "help", "memorize", "model", "recall", "secret", "wf"}
	for i, name := range want {
		if cmds[i].Name != name {
			t.Errorf("cmds[%d].Name = %q, want %q", i, cmds[i].Name, name)
		}
	}
	// /memorize, /recall, /forget, /branch were stubs in v0.2.0; v0.3.0
	// wires them to the memory + branches subsystems so none of the
	// default seven commands renders the (coming soon) tag.
	for _, c := range cmds {
		if c.ComingSoon {
			t.Errorf("%s.ComingSoon = true, want false (all default commands are wired in v0.3.0)", c.Name)
		}
	}
}

func TestAPI_Execute_Help(t *testing.T) {
	t.Parallel()
	api := newView(t)
	res, err := api.Execute(context.Background(), "s", "/help")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != coreslashcmd.ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if !strings.Contains(res.Text, "/help") {
		t.Errorf("Text missing /help: %q", res.Text)
	}
}

func TestAPI_Execute_ModelCarriesMetadata(t *testing.T) {
	t.Parallel()
	api := newView(t)
	res, err := api.Execute(context.Background(), "s", "/model gpt-4o")
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Kind != coreslashcmd.ResultKindInfo {
		t.Errorf("Kind = %q, want info", res.Kind)
	}
	if got, want := res.Metadata[coreslashcmd.MetaKeyProviderID], "openai-1"; got != want {
		t.Errorf("providerId = %v, want %v", got, want)
	}
	if got, want := res.Metadata[coreslashcmd.MetaKeyModelID], "gpt-4o"; got != want {
		t.Errorf("modelId = %v, want %v", got, want)
	}
}

func TestAPI_Execute_UnknownCommand(t *testing.T) {
	t.Parallel()
	api := newView(t)
	res, err := api.Execute(context.Background(), "s", "/foobar")
	if err == nil {
		t.Fatal("expected error for unknown command")
	}
	if res.Kind != coreslashcmd.ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if res.Text != coreslashcmd.UnknownCommandMessage {
		t.Errorf("Text = %q, want %q", res.Text, coreslashcmd.UnknownCommandMessage)
	}
}

func TestAPI_Execute_NilRegistryDegradesGracefully(t *testing.T) {
	t.Parallel()
	api := slashview.New(nil)
	res, err := api.Execute(context.Background(), "s", "/help")
	if err == nil {
		t.Fatal("expected error from nil registry")
	}
	if res.Kind != coreslashcmd.ResultKindError {
		t.Errorf("Kind = %q, want error", res.Kind)
	}
	if !strings.Contains(res.Text, "not wired") {
		t.Errorf("Text = %q", res.Text)
	}
	cmds, listErr := api.List(context.Background())
	if listErr != nil {
		t.Fatalf("List: %v", listErr)
	}
	if len(cmds) != 0 {
		t.Errorf("len(cmds) = %d, want 0 on nil registry", len(cmds))
	}
}

// ── user command RPC tests ────────────────────────────────────────────

func openUserRPCDB(t *testing.T) (storage.DB, string) {
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

func newViewWithStore(t *testing.T) *slashview.API {
	t.Helper()
	reg, err := coreslashcmd.NewRegistry(coreslashcmd.Deps{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	db, dir := openUserRPCDB(t)
	store := coreslashcmd.NewStore(db, dir)
	dispatch := coreslashcmd.NewDispatch(store, nil)
	return slashview.NewWithStore(reg, store, dispatch)
}

func TestAPIUserSaveList(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newViewWithStore(t)

	err := api.UserSave(ctx, slashview.UserCommandWire{
		Name:        "standup",
		Scope:       "global",
		Kind:        "text",
		Description: "Daily standup",
		Body:        "What did I do?",
	})
	if err != nil {
		t.Fatalf("UserSave: %v", err)
	}

	list, err := api.UserList(ctx, "")
	if err != nil {
		t.Fatalf("UserList: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("UserList len = %d, want 1", len(list))
	}
	if list[0].Name != "standup" {
		t.Errorf("Name = %q, want %q", list[0].Name, "standup")
	}
}

func TestAPIUserGet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newViewWithStore(t)

	if err := api.UserSave(ctx, slashview.UserCommandWire{
		Name:        "lint",
		Scope:       "global",
		Kind:        "text",
		Description: "Run lint",
		Body:        "golangci-lint run ./...",
	}); err != nil {
		t.Fatalf("UserSave: %v", err)
	}

	got, err := api.UserGet(ctx, "lint", "")
	if err != nil {
		t.Fatalf("UserGet: %v", err)
	}
	if got.Body != "golangci-lint run ./..." {
		t.Errorf("Body = %q", got.Body)
	}
}

func TestAPIUserDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newViewWithStore(t)

	if err := api.UserSave(ctx, slashview.UserCommandWire{
		Name:        "foo",
		Scope:       "global",
		Kind:        "text",
		Description: "test",
		Body:        "body",
	}); err != nil {
		t.Fatalf("UserSave: %v", err)
	}
	if err := api.UserDelete(ctx, "foo", ""); err != nil {
		t.Fatalf("UserDelete: %v", err)
	}
	_, err := api.UserGet(ctx, "foo", "")
	if !errors.Is(err, coreslashcmd.ErrCommandNotFound) {
		t.Errorf("after delete UserGet = %v, want ErrCommandNotFound", err)
	}
}

func TestAPIUserRun_Text(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	api := newViewWithStore(t)

	if err := api.UserSave(ctx, slashview.UserCommandWire{
		Name:        "greet",
		Scope:       "global",
		Kind:        "text",
		Description: "Greeting",
		Body:        "Hello!",
	}); err != nil {
		t.Fatalf("UserSave: %v", err)
	}

	result, err := api.UserRun(ctx, "greet", nil, "s1", "", "", "")
	if err != nil {
		t.Fatalf("UserRun: %v", err)
	}
	if result.Text != "Hello!" {
		t.Errorf("Text = %q, want %q", result.Text, "Hello!")
	}
}

func TestAPIUserSave_NilStore(t *testing.T) {
	t.Parallel()
	api := slashview.New(nil)
	err := api.UserSave(context.Background(), slashview.UserCommandWire{})
	if err == nil {
		t.Error("expected error from nil store")
	}
}
