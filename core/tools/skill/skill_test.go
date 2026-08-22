// skill_test.go — unit tests for the kenaz__skill built-in tool.
//
// model-invoked-skills-catalog-01KZNP3E WP02.
package skill

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	"github.com/kameas-ai/kenaz-harness/core/storage"
	storagesqlite "github.com/kameas-ai/kenaz-harness/core/storage/sqlite"
)

// stubDispatch is a minimal Dispatch-alike that lets tests control Run output.
// We cannot instantiate *coreslashcmd.Dispatch directly in unit tests because
// it requires a real store; instead we wrap the Tool's Call method by
// injecting a thin stub via the Options.Dispatch field using a real *Dispatch
// with an overridden lookup path.
//
// Since coreslashcmd.Dispatch is a concrete struct (not an interface) we must
// test through the public Call surface and verify the JSON output shape.
//
// The test harness path exercises:
//   - nil Dispatch → marshalErr "not configured"
//   - empty name   → marshalErr "'name' argument is required"
//   - name too long → marshalErr "name exceeds … characters"
//   - disabled tool → marshalErr "skill tool is disabled"
//   - JSON schema  → valid json.RawMessage
//
// Full round-trip (Dispatch.Run) is covered by the integration path in the
// slashcmd package tests. Here we focus on the input-validation layer.

func TestTool_Name(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	if tool.Name() != ToolName {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolName)
	}
}

func TestTool_Description(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestTool_InputSchema_ValidJSON(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	schema := tool.InputSchema()
	var v any
	if err := json.Unmarshal(schema, &v); err != nil {
		t.Fatalf("InputSchema() is not valid JSON: %v", err)
	}
}

func TestTool_Call_NilDispatch_ReturnsError(t *testing.T) {
	t.Parallel()
	tool := New(Options{Dispatch: nil})
	args := mustMarshal(t, map[string]any{"name": "summarize"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}
	var e struct {
		IsError bool   `json:"isError"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(out, &e); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if !e.IsError {
		t.Error("expected isError=true when dispatch is nil")
	}
	if e.Error == "" {
		t.Error("expected non-empty error message when dispatch is nil")
	}
}

func TestTool_Call_DisabledTool_ReturnsError(t *testing.T) {
	t.Parallel()
	// Use a nil Dispatch — the disabled check fires before dispatch is consulted.
	tool := New(Options{
		Dispatch: nil,
		Enabled:  func() bool { return false },
	})
	args := mustMarshal(t, map[string]any{"name": "summarize"})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}
	var e struct {
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, out, &e)
	if !e.IsError {
		t.Error("expected isError=true when tool is disabled")
	}
}

func TestTool_Call_InvalidJSON_ReturnsError(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	out, err := tool.Call(context.Background(), json.RawMessage(`{invalid}`))
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}
	var e struct {
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, out, &e)
	if !e.IsError {
		t.Error("expected isError=true for invalid JSON args")
	}
}

func TestTool_Call_EmptyName_ReturnsError(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	args := mustMarshal(t, map[string]any{"name": ""})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}
	var e struct {
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, out, &e)
	if !e.IsError {
		t.Error("expected isError=true for empty name")
	}
}

func TestTool_Call_NameTooLong_ReturnsError(t *testing.T) {
	t.Parallel()
	tool := New(Options{})
	longName := make([]byte, MaxNameLen+1)
	for i := range longName {
		longName[i] = 'a'
	}
	args := mustMarshal(t, map[string]any{"name": string(longName)})
	out, err := tool.Call(context.Background(), args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}
	var e struct {
		IsError bool `json:"isError"`
	}
	mustUnmarshal(t, out, &e)
	if !e.IsError {
		t.Error("expected isError=true for name exceeding MaxNameLen")
	}
}

// ── model_invokable enforcement, driven through the real model entry
// point (trust-surfaces-that-fire-01PMZ202 WP20 / AC-14) ───────────────
//
// These three tests exercise Tool.Call end to end: a real *coreslashcmd.Store
// backed by real sqlite (not a fixture map — CLAUDE.md blind spot #2), a
// real *coreslashcmd.Dispatch wired the way core/rpc/api.go wires it, and
// the actual kenaz__skill Call() path. Unit-testing Dispatch.RunModelInvoked
// directly would prove the guard exists; it would not prove the model's own
// tool call reaches it — that is the gap this WP closes.

func openSkillTestDB(t *testing.T) (storage.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := storagesqlite.Open(storage.Config{
		DataDir:          dir,
		EncryptionStatus: storage.EncryptionStatusDisabledWithDiskEncryption,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, dir
}

// TestTool_Call_ModelInvokableFalse_Refused is the deny leg: a command
// saved with model_invokable unset (the default) must be refused when the
// model calls kenaz__skill, not silently run.
func TestTool_Call_ModelInvokableFalse_Refused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, dir := openSkillTestDB(t)
	store := coreslashcmd.NewStore(db, dir)

	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:        "not-for-model",
		Scope:       coreslashcmd.ScopeGlobal,
		Kind:        coreslashcmd.KindText,
		Description: "Human-only command",
		Body:        "this body must never reach the model",
		// ModelInvokable left false.
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	dispatch := coreslashcmd.NewDispatch(store, nil)
	tool := New(Options{Dispatch: dispatch})

	args := mustMarshal(t, map[string]any{"name": "not-for-model"})
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}

	var e struct {
		IsError bool   `json:"isError"`
		Error   string `json:"error"`
	}
	mustUnmarshal(t, out, &e)
	if !e.IsError {
		t.Fatalf("expected isError=true for a model_invokable=false command, got success: %s", out)
	}
	if strings.Contains(e.Error, "this body must never reach the model") {
		t.Errorf("refusal leaked the command body: %q", e.Error)
	}
}

// TestTool_Call_ModelInvokableTrue_Runs is the allow leg: a command
// explicitly marked model_invokable=true must still run through
// kenaz__skill and produce the real output.
func TestTool_Call_ModelInvokableTrue_Runs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, dir := openSkillTestDB(t)
	store := coreslashcmd.NewStore(db, dir)

	if err := store.SaveUser(ctx, coreslashcmd.UserCommand{
		Name:           "for-model",
		Scope:          coreslashcmd.ScopeGlobal,
		Kind:           coreslashcmd.KindText,
		Description:    "Model-eligible command",
		Body:           "eligible output",
		ModelInvokable: true,
	}); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	dispatch := coreslashcmd.NewDispatch(store, nil)
	tool := New(Options{Dispatch: dispatch})

	args := mustMarshal(t, map[string]any{"name": "for-model"})
	out, err := tool.Call(ctx, args)
	if err != nil {
		t.Fatalf("Call() should never return a Go error, got: %v", err)
	}

	var res struct {
		Output  string `json:"output"`
		IsError bool   `json:"isError"`
	}
	mustUnmarshal(t, out, &res)
	if res.IsError {
		t.Fatalf("expected success for a model_invokable=true command, got error: %s", out)
	}
	if res.Output != "eligible output" {
		t.Errorf("Output = %q, want %q", res.Output, "eligible output")
	}
}

// ── helpers ────────────────────────────────────────────────────────────────

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

func mustUnmarshal(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("json.Unmarshal: %v — raw: %s", err, b)
	}
}
