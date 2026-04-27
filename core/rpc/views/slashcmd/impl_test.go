package slashcmd_test

import (
	"context"
	"strings"
	"testing"

	slashview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/slashcmd"
	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
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

func TestAPI_List_SortedAndIncludesComingSoon(t *testing.T) {
	t.Parallel()
	api := newView(t)
	cmds, err := api.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(cmds) != 7 {
		t.Fatalf("len(cmds) = %d, want 7", len(cmds))
	}
	want := []string{"branch", "clear", "forget", "help", "memorize", "model", "recall"}
	for i, name := range want {
		if cmds[i].Name != name {
			t.Errorf("cmds[%d].Name = %q, want %q", i, cmds[i].Name, name)
		}
	}
	stubs := map[string]bool{
		"branch": true, "forget": true, "memorize": true, "recall": true,
	}
	for _, c := range cmds {
		want := stubs[c.Name]
		if c.ComingSoon != want {
			t.Errorf("%s.ComingSoon = %v, want %v", c.Name, c.ComingSoon, want)
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
