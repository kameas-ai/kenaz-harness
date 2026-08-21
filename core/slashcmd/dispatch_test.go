package slashcmd_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// fakeToolDispatcher captures dispatch calls for assertions.
type fakeToolDispatcher struct {
	calls  []fakeToolCall
	output string
	err    error
}

type fakeToolCall struct {
	Name string
	Args []string
}

func (f *fakeToolDispatcher) DispatchTool(_ context.Context, name string, args []string) (string, error) {
	f.calls = append(f.calls, fakeToolCall{Name: name, Args: args})
	if f.err != nil {
		return "", f.err
	}
	return f.output, nil
}

func openDispatchDB(t *testing.T) (storage.DB, string) {
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

func TestDispatch_KindText(t *testing.T) {
	t.Parallel()
	db, dir := openDispatchDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := slashcmd.UserCommand{
		Name:        "standup",
		Scope:       slashcmd.ScopeGlobal,
		Kind:        slashcmd.KindText,
		Description: "Standup template",
		Body:        "What did I do yesterday?",
	}
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	d := slashcmd.NewDispatch(store, nil)
	result, err := d.Run(ctx, "standup", nil, slashcmd.SessionContext{SessionID: "s1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Kind != slashcmd.ResultKindInfo {
		t.Errorf("Kind = %q, want %q", result.Kind, slashcmd.ResultKindInfo)
	}
	if result.Text != "What did I do yesterday?" {
		t.Errorf("Text = %q", result.Text)
	}
}

func TestDispatch_KindPrompt_WithVars(t *testing.T) {
	t.Parallel()
	db, dir := openDispatchDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := slashcmd.UserCommand{
		Name:        "review",
		Scope:       slashcmd.ScopeGlobal,
		Kind:        slashcmd.KindPrompt,
		Description: "Code review",
		Body:        "Review the code in {{cwd}}: {{selection}}",
	}
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	d := slashcmd.NewDispatch(store, nil)
	result, err := d.Run(ctx, "review", nil, slashcmd.SessionContext{
		SessionID: "s2",
		CWD:       "/project",
		Selection: "func main() {}",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "Review the code in /project: func main() {}" {
		t.Errorf("Text = %q", result.Text)
	}
}

func TestDispatch_KindTool_ArgsAsSlice(t *testing.T) {
	t.Parallel()
	db, dir := openDispatchDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := slashcmd.UserCommand{
		Name:             "deploy",
		Scope:            slashcmd.ScopeGlobal,
		Kind:             slashcmd.KindTool,
		Description:      "Deploy to env",
		Tool:             "bash",
		ToolArgsTemplate: "./deploy.sh --env {{env}}",
		Inputs: []slashcmd.UserCommandInput{
			{Name: "env", Kind: slashcmd.InputKindText, Required: true},
		},
	}
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	fakeTool := &fakeToolDispatcher{output: "deploy success"}
	d := slashcmd.NewDispatch(store, fakeTool)
	result, err := d.Run(ctx, "deploy",
		map[string]string{"env": "staging"},
		slashcmd.SessionContext{SessionID: "s3"},
	)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Text != "deploy success" {
		t.Errorf("Text = %q", result.Text)
	}
	if len(fakeTool.calls) != 1 {
		t.Fatalf("tool calls = %d, want 1", len(fakeTool.calls))
	}
	call := fakeTool.calls[0]
	if call.Name != "bash" {
		t.Errorf("tool name = %q, want %q", call.Name, "bash")
	}
	// Args must be a slice, not a shell string.
	wantArgs := []string{"./deploy.sh", "--env", "staging"}
	if len(call.Args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", call.Args, wantArgs)
	}
	for i := range wantArgs {
		if call.Args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, call.Args[i], wantArgs[i])
		}
	}
}

func TestDispatch_NotFound(t *testing.T) {
	t.Parallel()
	db, dir := openDispatchDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	d := slashcmd.NewDispatch(store, nil)
	result, err := d.Run(ctx, "no-such-cmd", nil, slashcmd.SessionContext{})
	if err == nil {
		t.Error("expected error for unknown command")
	}
	if !errors.Is(err, slashcmd.ErrCommandNotFound) {
		t.Errorf("expected ErrCommandNotFound, got %v", err)
	}
	if result.Kind != slashcmd.ResultKindError {
		t.Errorf("Kind = %q, want error", result.Kind)
	}
}

// TestDispatch_KindTool_NilDispatcher_ReturnsError is AC-004
// (automation-actually-runs-01PMZ404 UNIT-3). Before this unit, a nil
// tools dispatcher took the dry-run branch: ResultKindInfo, a "would
// dispatch: ..." bubble, and a NIL error — indistinguishable from success
// to any caller that only checks err. Nothing was ever dispatched. Now
// the same nil-dispatcher state returns ResultKindError and a non-nil
// error naming the gap, matching the `tools` field's own doc comment
// ("may be nil (kind:tool commands return an error)") for the first
// time.
func TestDispatch_KindTool_NilDispatcher_ReturnsError(t *testing.T) {
	t.Parallel()
	db, dir := openDispatchDB(t)
	store := slashcmd.NewStore(db, dir)
	ctx := context.Background()

	cmd := slashcmd.UserCommand{
		Name:             "lint",
		Scope:            slashcmd.ScopeGlobal,
		Kind:             slashcmd.KindTool,
		Description:      "Run linter",
		Tool:             "bash",
		ToolArgsTemplate: "golangci-lint run ./...",
	}
	if err := store.SaveUser(ctx, cmd); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	// nil tool dispatcher — must fail loudly, not dry-run.
	d := slashcmd.NewDispatch(store, nil)
	result, err := d.Run(ctx, "lint", nil, slashcmd.SessionContext{SessionID: "s4"})
	if err == nil {
		t.Fatal("Run with nil dispatcher returned a nil error — a caller branching on err would treat this as success")
	}
	if result.Kind != slashcmd.ResultKindError {
		t.Errorf("Kind = %q, want error", result.Kind)
	}
	if result.ToolName != "bash" {
		t.Errorf("ToolName = %q, want %q", result.ToolName, "bash")
	}
	if !strings.Contains(err.Error(), "bash") {
		t.Errorf("err = %v, want it to name the tool %q", err, "bash")
	}
}
