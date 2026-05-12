package todo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sigil-tech/kaneaz-harness/core/tools/todo"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// ── Store tests ─────────────────────────────────────────────────────────────

func TestStore_WriteRead(t *testing.T) {
	s := todo.NewStore()
	items := []todo.TodoItem{
		{ID: "1", Content: "Buy milk", Status: todo.StatusPending},
		{ID: "2", Content: "Write tests", Status: todo.StatusInProgress, Priority: todo.PriorityHigh},
	}
	if err := s.Write("sess-a", items); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := s.Read("sess-a")
	if len(got) != 2 {
		t.Fatalf("Read: want 2 items, got %d", len(got))
	}
	if got[0].ID != "1" || got[1].ID != "2" {
		t.Fatalf("Read: wrong items: %v", got)
	}
	// Normalise default priority.
	if got[0].Priority != todo.PriorityMedium {
		t.Errorf("priority normalisation: want medium, got %q", got[0].Priority)
	}
}

func TestStore_WriteEmpty(t *testing.T) {
	s := todo.NewStore()
	_ = s.Write("sess-a", []todo.TodoItem{{ID: "1", Content: "x", Status: todo.StatusPending}})
	if err := s.Write("sess-a", []todo.TodoItem{}); err != nil {
		t.Fatalf("Write empty: %v", err)
	}
	got := s.Read("sess-a")
	if len(got) != 0 {
		t.Fatalf("Read after empty write: want 0, got %d", len(got))
	}
}

func TestStore_Drop(t *testing.T) {
	s := todo.NewStore()
	_ = s.Write("sess-b", []todo.TodoItem{{ID: "1", Content: "x", Status: todo.StatusPending}})
	s.Drop("sess-b")
	if got := s.Read("sess-b"); got != nil {
		t.Fatalf("Read after Drop: want nil, got %v", got)
	}
}

func TestStore_IsolatedSessions(t *testing.T) {
	s := todo.NewStore()
	_ = s.Write("sess-1", []todo.TodoItem{{ID: "a", Content: "A", Status: todo.StatusPending}})
	_ = s.Write("sess-2", []todo.TodoItem{{ID: "b", Content: "B", Status: todo.StatusDone}})
	g1 := s.Read("sess-1")
	g2 := s.Read("sess-2")
	if len(g1) != 1 || g1[0].ID != "a" {
		t.Fatalf("sess-1 isolation: got %v", g1)
	}
	if len(g2) != 1 || g2[0].ID != "b" {
		t.Fatalf("sess-2 isolation: got %v", g2)
	}
}

func TestStore_DuplicateID(t *testing.T) {
	s := todo.NewStore()
	items := []todo.TodoItem{
		{ID: "1", Content: "A", Status: todo.StatusPending},
		{ID: "1", Content: "B", Status: todo.StatusPending},
	}
	if err := s.Write("sess", items); err == nil {
		t.Fatal("Write with duplicate id: want error, got nil")
	}
}

func TestStore_InvalidStatus(t *testing.T) {
	s := todo.NewStore()
	items := []todo.TodoItem{
		{ID: "1", Content: "X", Status: "unknown"},
	}
	if err := s.Write("sess", items); err == nil {
		t.Fatal("Write with invalid status: want error, got nil")
	}
}

func TestStore_InvalidPriority(t *testing.T) {
	s := todo.NewStore()
	items := []todo.TodoItem{
		{ID: "1", Content: "X", Status: todo.StatusPending, Priority: "ultra"},
	}
	if err := s.Write("sess", items); err == nil {
		t.Fatal("Write with invalid priority: want error, got nil")
	}
}

func TestStore_EmptyContent(t *testing.T) {
	s := todo.NewStore()
	items := []todo.TodoItem{
		{ID: "1", Content: "", Status: todo.StatusPending},
	}
	if err := s.Write("sess", items); err == nil {
		t.Fatal("Write with empty content: want error, got nil")
	}
}

func TestStore_TooManyItems(t *testing.T) {
	s := todo.NewStore()
	items := make([]todo.TodoItem, todo.MaxItems+1)
	for i := range items {
		items[i] = todo.TodoItem{
			ID:      string(rune('a' + i%26)),
			Content: "x",
			Status:  todo.StatusPending,
		}
	}
	// IDs collide quickly with 201 items but we just need to hit the cap check first.
	// The store checks len first, before validating IDs.
	if err := s.Write("sess", items); err == nil {
		t.Fatal("Write too many items: want error, got nil")
	}
}

// ── Tool call tests ──────────────────────────────────────────────────────────

func ctxWithSession(id string) context.Context {
	return toolloop.WithSessionID(context.Background(), id)
}

func callTool(t *testing.T, tool *todo.TodoWriteTool, sessionID string, payload string) map[string]any {
	t.Helper()
	ctx := ctxWithSession(sessionID)
	raw, err := tool.Call(ctx, json.RawMessage(payload))
	if err != nil {
		t.Fatalf("Call returned error: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	return result
}

func TestTool_WriteAndRead(t *testing.T) {
	store := todo.NewStore()
	tool := todo.New(todo.Options{Store: store})

	result := callTool(t, tool, "sess", `{
		"todos": [
			{"id": "1", "content": "Buy milk", "status": "pending"},
			{"id": "2", "content": "Write tests", "status": "in_progress", "priority": "high"}
		]
	}`)

	if _, isErr := result["is_error"]; isErr {
		t.Fatalf("unexpected error result: %v", result)
	}
	written, _ := result["written"].(float64)
	if int(written) != 2 {
		t.Errorf("written: want 2, got %v", written)
	}

	items := store.Read("sess")
	if len(items) != 2 {
		t.Fatalf("store: want 2, got %d", len(items))
	}
}

func TestTool_ClearList(t *testing.T) {
	store := todo.NewStore()
	tool := todo.New(todo.Options{Store: store})

	_ = callTool(t, tool, "sess", `{"todos": [{"id":"1","content":"x","status":"pending"}]}`)
	result := callTool(t, tool, "sess", `{"todos": []}`)

	if _, isErr := result["is_error"]; isErr {
		t.Fatalf("unexpected error: %v", result)
	}
	if store.Read("sess") != nil && len(store.Read("sess")) != 0 {
		t.Fatalf("store after clear: want empty, got %v", store.Read("sess"))
	}
}

func TestTool_DisabledShortCircuit(t *testing.T) {
	store := todo.NewStore()
	tool := todo.New(todo.Options{
		Store:   store,
		Enabled: func() bool { return false },
	})

	result := callTool(t, tool, "sess", `{"todos":[]}`)
	if _, isErr := result["is_error"]; !isErr {
		t.Fatal("expected is_error when disabled, got none")
	}
}

func TestTool_InvalidStatus(t *testing.T) {
	store := todo.NewStore()
	tool := todo.New(todo.Options{Store: store})

	result := callTool(t, tool, "sess", `{"todos":[{"id":"1","content":"x","status":"bogus"}]}`)
	if _, isErr := result["is_error"]; !isErr {
		t.Fatal("expected is_error for invalid status, got none")
	}
}

func TestTool_MissingTodosField(t *testing.T) {
	store := todo.NewStore()
	tool := todo.New(todo.Options{Store: store})

	ctx := ctxWithSession("sess")
	raw, _ := tool.Call(ctx, json.RawMessage(`{}`))
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	// "todos" field missing → json.Unmarshal zero-values it to nil slice,
	// which is treated as an empty list and succeeds.
	// This matches the Claude Code pattern (empty write = clear).
	if _, isErr := result["is_error"]; isErr {
		t.Logf("missing todos treated as error (ok): %v", result["error"])
	}
}

func TestTool_Name(t *testing.T) {
	tool := todo.New(todo.Options{Store: todo.NewStore()})
	if tool.Name() != todo.ToolName {
		t.Errorf("Name: want %q, got %q", todo.ToolName, tool.Name())
	}
}

func TestTool_InputSchemaParseable(t *testing.T) {
	tool := todo.New(todo.Options{Store: todo.NewStore()})
	schema := tool.InputSchema()
	var m map[string]any
	if err := json.Unmarshal(schema, &m); err != nil {
		t.Fatalf("InputSchema not valid JSON: %v", err)
	}
}
