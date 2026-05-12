// Package todo implements the kaneaz__todo_write builtin tool
// (builtin-tools-search-and-elicitation-01KZNP3D WP05).
//
// The tool lets the model maintain a structured, session-scoped task list.
// The list is written atomically as a whole — the model sends the full
// updated list on every call; no incremental patch. This keeps the tool
// schema simple and the audit trail clear.
//
// Session scoping: items are keyed by session ID extracted from context.
// When the session ends the store is cleared via Drop(sessionID). The
// process-global Store is instantiated once at startup (same pattern as
// core/tools/fsbuiltins.ReadSet) and shared across all concurrent sessions.
//
// Cedar gate: the tool reports ActionToolTodoWrite on every call so policy
// authors can independently control todo access without touching the broader
// filesystem write surface.
//
// DIRECTIVE_001: backend-only; no CGo, no GUI imports.
package todo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/toolloop"
)

// ToolName is the canonical fully-qualified name exposed to the model.
const ToolName = "kaneaz__todo_write"

// StatusPending, StatusInProgress, StatusDone, StatusCancelled are the
// closed enum values for TodoItem.Status. The model MUST send one of
// these values; unknown strings are rejected with ErrInvalidStatus.
const (
	StatusPending    = "pending"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
	StatusCancelled  = "cancelled"
)

// PriorityHigh, PriorityMedium, PriorityLow are the closed enum values
// for TodoItem.Priority. When omitted the store defaults to PriorityMedium.
const (
	PriorityHigh   = "high"
	PriorityMedium = "medium"
	PriorityLow    = "low"
)

// ErrInvalidStatus is returned when a TodoItem carries an unknown status.
var ErrInvalidStatus = errors.New("todo_write: invalid item status (must be pending, in_progress, done, or cancelled)")

// ErrInvalidPriority is returned when a TodoItem carries an unknown priority.
var ErrInvalidPriority = errors.New("todo_write: invalid item priority (must be high, medium, or low)")

// ErrDuplicateID is returned when two items in the same write carry the same id.
var ErrDuplicateID = errors.New("todo_write: duplicate item id in the same write")

// ErrItemContentEmpty is returned when an item's content field is empty.
var ErrItemContentEmpty = errors.New("todo_write: item content must not be empty")

// ErrTooManyItems is returned when the model attempts to write more than
// MaxItems items in a single call.
var ErrTooManyItems = errors.New("todo_write: too many items (max 200)")

// MaxItems is the per-session item cap. Writing a list with more than
// MaxItems items is rejected to prevent runaway context growth.
const MaxItems = 200

// TodoItem is one entry in the session task list.
type TodoItem struct {
	// ID is a stable identifier for the item. MUST be unique within the
	// list. The model is responsible for generating and maintaining IDs —
	// the store does not auto-generate them. Typically a short string like
	// "1", "task-a", "step-3".
	ID string `json:"id"`
	// Content is the human-readable task description.
	Content string `json:"content"`
	// Status is one of: pending, in_progress, done, cancelled.
	Status string `json:"status"`
	// Priority is one of: high, medium, low (default: medium).
	Priority string `json:"priority,omitempty"`
}

// validate checks that the item's fields satisfy the invariants.
func (item TodoItem) validate() error {
	if item.Content == "" {
		return ErrItemContentEmpty
	}
	switch item.Status {
	case StatusPending, StatusInProgress, StatusDone, StatusCancelled:
	default:
		return fmt.Errorf("%w: got %q", ErrInvalidStatus, item.Status)
	}
	if item.Priority != "" {
		switch item.Priority {
		case PriorityHigh, PriorityMedium, PriorityLow:
		default:
			return fmt.Errorf("%w: got %q", ErrInvalidPriority, item.Priority)
		}
	}
	return nil
}

// Store is a process-global per-session in-memory store for todo lists.
// The store is goroutine-safe. Session data is evicted when Drop is called
// (the session lifecycle manager calls it when a session ends).
type Store struct {
	mu   sync.RWMutex
	data map[string][]TodoItem // sessionID → items
}

// NewStore returns an empty Store.
func NewStore() *Store {
	return &Store{data: map[string][]TodoItem{}}
}

// Write atomically replaces the item list for sessionID. The list is
// validated before writing; any error leaves the previous list intact.
// An empty list clears the session's todo list.
func (s *Store) Write(sessionID string, items []TodoItem) error {
	if len(items) > MaxItems {
		return ErrTooManyItems
	}
	// Validate and check for duplicates.
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if err := item.validate(); err != nil {
			return err
		}
		if item.ID == "" {
			return errors.New("todo_write: item id must not be empty")
		}
		if _, dup := seen[item.ID]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateID, item.ID)
		}
		seen[item.ID] = struct{}{}
	}
	// Copy so the caller's slice can't alias our storage.
	stored := make([]TodoItem, len(items))
	copy(stored, items)
	// Normalise omitted priorities to medium.
	for i := range stored {
		if stored[i].Priority == "" {
			stored[i].Priority = PriorityMedium
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[sessionID] = stored
	return nil
}

// Read returns the current list for sessionID. Returns nil when the
// session has no list (never written or already dropped).
func (s *Store) Read(sessionID string) []TodoItem {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := s.data[sessionID]
	if items == nil {
		return nil
	}
	out := make([]TodoItem, len(items))
	copy(out, items)
	return out
}

// Drop removes all data for sessionID. Called when a session ends.
func (s *Store) Drop(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, sessionID)
}

// ── Tool implementation ──────────────────────────────────────────────────

// Options is the constructor config for TodoWriteTool.
type Options struct {
	// Store is the process-global todo store. Required.
	Store *Store
	// Enabled is consulted on every Call. When nil, all calls are
	// accepted. When the function returns false the tool returns a
	// "feature not enabled" error without touching the store.
	Enabled func() bool
}

// TodoWriteTool implements kaneaz__todo_write.
type TodoWriteTool struct {
	store   *Store
	enabled func() bool
}

// New constructs the todo_write tool.
func New(opts Options) *TodoWriteTool {
	return &TodoWriteTool{
		store:   opts.Store,
		enabled: opts.Enabled,
	}
}

func (t *TodoWriteTool) Name() string { return ToolName }

func (t *TodoWriteTool) Description() string {
	return "Write the session's structured task list. " +
		"Sends the FULL list on every call — the previous list is replaced atomically. " +
		"Items have id, content, status (pending|in_progress|done|cancelled), and " +
		"optional priority (high|medium|low, default medium). " +
		"Send an empty list to clear all tasks. " +
		"IDs are stable across writes; the model is responsible for generating them."
}

const inputSchema = `{
  "type": "object",
  "properties": {
    "todos": {
      "type": "array",
      "description": "The complete list of todo items to store. Replaces the previous list.",
      "items": {
        "type": "object",
        "properties": {
          "id": {
            "type": "string",
            "description": "Stable unique identifier for this item (e.g. '1', 'task-a')."
          },
          "content": {
            "type": "string",
            "description": "Human-readable task description."
          },
          "status": {
            "type": "string",
            "enum": ["pending", "in_progress", "done", "cancelled"],
            "description": "Current status of the task."
          },
          "priority": {
            "type": "string",
            "enum": ["high", "medium", "low"],
            "description": "Task priority. Default medium when omitted."
          }
        },
        "required": ["id", "content", "status"]
      }
    }
  },
  "required": ["todos"]
}`

func (t *TodoWriteTool) InputSchema() json.RawMessage { return json.RawMessage(inputSchema) }

type todoWriteArgs struct {
	Todos []TodoItem `json:"todos"`
}

type todoWriteResult struct {
	Written int    `json:"written"`
	Message string `json:"message"`
}

func errResult(msg string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"is_error":true,"error":%s}`, jsonString(msg)))
}

func okResult(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (t *TodoWriteTool) Call(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if t.enabled != nil && !t.enabled() {
		return errResult("todo_write: TodoEnabled is off"), nil
	}

	var args todoWriteArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return errResult("todo_write: invalid args: " + err.Error()), nil
	}

	sessionID := toolloop.SessionIDFromContext(ctx)
	if t.store == nil {
		return errResult("todo_write: no store wired"), nil
	}

	if err := t.store.Write(sessionID, args.Todos); err != nil {
		return errResult("todo_write: " + err.Error()), nil
	}

	n := len(args.Todos)
	msg := fmt.Sprintf("%d item(s) stored", n)
	if n == 0 {
		msg = "todo list cleared"
	}
	return okResult(todoWriteResult{Written: n, Message: msg}), nil
}

// compile-time BuiltinTool interface assertion.
var _ toolloop.BuiltinTool = (*TodoWriteTool)(nil)
