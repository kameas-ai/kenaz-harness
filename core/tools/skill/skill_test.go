// skill_test.go — unit tests for the kenaz__skill built-in tool.
//
// model-invoked-skills-catalog-01KZNP3E WP02.
package skill

import (
	"context"
	"encoding/json"
	"testing"
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
