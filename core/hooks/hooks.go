// Package hooks is the harness's lifecycle-hook subsystem. Hooks are
// configurable side-effects that fire on chat-pipeline events
// (pre_send, post_send, pre_save_session, post_assistant_turn_complete).
// Each hook is one of three kinds:
//
//   - builtin — a registered Go function shipped by the harness.
//     Memory retrieve / persist are the v1 builtins.
//   - shell — exec a user-configured command, pipe lifecycle JSON over
//     stdin, read mutated JSON from stdout. Mirrors Claude Code's hook
//     stdin/stdout protocol.
//   - mcp — invoke a configured MCP server tool by name. Stub-only for
//     v1; the seam is documented for the MCP-pool wiring mission.
//
// Persistence: <DataDir>/hooks.json (mode 0600). The Registry is the
// only writer; all callers go through Registry.Add / Update / Remove
// to keep the on-disk file in sync.
package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)


// Event names. Kept as exported strings so JSON config files can name
// them without a Go enum decoder.
//
// v1 chat-pipeline events (all four remain supported):
const (
	EventPreSend                   = "pre_send"
	EventPostSend                  = "post_send"
	EventPreSaveSession            = "pre_save_session"
	EventPostAssistantTurnComplete = "post_assistant_turn_complete"
)

// v2 lifecycle events (hooks-event-surface-expansion-01KZNP3A).
const (
	// Tool-loop events
	EventPreToolUse         = "pre_tool_use"
	EventPostToolUse        = "post_tool_use"
	EventPostToolUseFailure = "post_tool_use_failure"

	// User-interaction events
	EventUserPromptSubmit = "user_prompt_submit"

	// Session lifecycle events
	EventSessionStart = "session_start"
	EventSetup        = "setup"

	// Sub-agent events
	EventSubagentStart = "subagent_start"

	// File-system events
	EventCwdChanged  = "cwd_changed"
	EventFileChanged = "file_changed"

	// Permission events
	EventPermissionRequest = "permission_request"
	EventPermissionDenied  = "permission_denied"

	// Notification / task events
	EventNotification          = "notification"
	EventBackgroundTaskComplete = "background_task_complete"
	EventWorktreeCreate         = "worktree_create"
)

// AllEvents is the ordered union of v1 + v2 events. It is used by
// Validate to accept any known event name.
var AllEvents = []string{
	EventPreSend,
	EventPostSend,
	EventPreSaveSession,
	EventPostAssistantTurnComplete,
	EventPreToolUse,
	EventPostToolUse,
	EventPostToolUseFailure,
	EventUserPromptSubmit,
	EventSessionStart,
	EventSetup,
	EventSubagentStart,
	EventCwdChanged,
	EventFileChanged,
	EventPermissionRequest,
	EventPermissionDenied,
	EventNotification,
	EventBackgroundTaskComplete,
	EventWorktreeCreate,
}

// HookOutput is the discriminated-union result a hook returns.
// Shell hooks encode this as JSON on stdout; builtin hooks return a Go
// struct; MCP hooks encode the tool result as this shape.
//
// Variants are composable: a single HookOutput may carry an
// UpdatedInput and an AdditionalContext simultaneously.
//
// Decision semantics:
//   - "approve" — hook is satisfied; pass through to next gate.
//   - "block"   — short-circuit dispatch; surface Reason to the caller.
//
// PermissionDecision semantics (permission_request / permission_denied):
//   - "allow" — hook pre-approves; skip interactive prompt.
//   - "deny"  — hook rejects; short-circuit (Cedar may still override).
//
// Async: when true the hook was dispatched via the worker pool; its
// output is never merged into the sync pipeline (logged only).
type HookOutput struct {
	// Decision is "approve" or "block". Empty means no opinion.
	Decision string `json:"decision,omitempty"`
	// Reason explains the decision; surfaced in audit log + UI.
	Reason string `json:"reason,omitempty"`

	// AdditionalContext is injected as a system message into the next
	// LLM turn when returned from pre_tool_use or user_prompt_submit.
	// Multiple AdditionalContexts from peer hooks are joined with "\n\n".
	AdditionalContext string `json:"additional_context,omitempty"`

	// UpdatedInput carries the rewritten tool-call arguments (pre_tool_use
	// only). Must be a valid JSON object. The audit log records both the
	// original and the rewritten input.
	UpdatedInput json.RawMessage `json:"updated_input,omitempty"`

	// UpdatedMCPOutput replaces the MCP tool result (post_tool_use only,
	// kind=mcp). Allows hooks to sanitize or transform raw tool output.
	UpdatedMCPOutput json.RawMessage `json:"updated_mcp_tool_output,omitempty"`

	// PermissionDecision is "allow" or "deny" (permission_request events).
	PermissionDecision string `json:"permission_decision,omitempty"`
	// PermissionDecisionReason explains the permission decision.
	PermissionDecisionReason string `json:"permission_decision_reason,omitempty"`

	// WatchPaths is a list of absolute paths the harness should add to
	// the fsnotify watcher for the current session. Returned from
	// session_start or cwd_changed hooks.
	WatchPaths []string `json:"watch_paths,omitempty"`

	// Async, when true, means the hook was executed asynchronously via
	// the worker pool. Its other fields are logged but not merged into
	// the sync pipeline.
	Async bool `json:"async,omitempty"`
	// AsyncTimeoutMs overrides the default async timeout (60 000 ms).
	AsyncTimeoutMs int `json:"async_timeout_ms,omitempty"`
}

// Hook kinds.
const (
	KindBuiltin = "builtin"
	KindShell   = "shell"
	KindMCP     = "mcp"
)

// Builtin hook IDs shipped by the harness.
const (
	BuiltinMemoryRetrieve = "memory.retrieve"
	BuiltinMemoryPersist  = "memory.persist"
)

// Hook is a configured lifecycle hook. The persisted shape mirrors
// the JSON the operator edits in the UI.
type Hook struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Event   string         `json:"event"`
	Kind    string         `json:"kind"`
	Enabled bool           `json:"enabled"`
	Match   HookMatch      `json:"match"`
	Builtin string         `json:"builtin,omitempty"`
	Command string         `json:"command,omitempty"`
	MCPTool string         `json:"mcp_tool,omitempty"`
	Config  map[string]any `json:"config,omitempty"`

	// TimeoutMs is the per-hook execution timeout in milliseconds.
	// 0 uses the kind default: sync hooks default to 5 000 ms;
	// async hooks default to 60 000 ms.
	TimeoutMs int `json:"timeout_ms,omitempty"`
}

// HookMatch is the optional filter that gates a hook on session id,
// provider kind, or model. Empty fields match anything.
type HookMatch struct {
	SessionIDs []string `json:"session_ids,omitempty"`
	Kinds      []string `json:"kinds,omitempty"`
	Models     []string `json:"models,omitempty"`
}

// Matches returns true when the supplied (session, kind, model)
// triple satisfies every non-empty filter.
func (m HookMatch) Matches(sessionID, kind, model string) bool {
	if len(m.SessionIDs) > 0 && !contains(m.SessionIDs, sessionID) {
		return false
	}
	if len(m.Kinds) > 0 && !contains(m.Kinds, kind) {
		return false
	}
	if len(m.Models) > 0 && !contains(m.Models, model) {
		return false
	}
	return true
}

func contains(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}

// BuiltinDescriptor describes a builtin hook for the UI's
// "AvailableBuiltins" dropdown.
type BuiltinDescriptor struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Events      []string `json:"events"`
	DefaultConfig map[string]any `json:"default_config,omitempty"`
}

// Validate checks the hook's required fields.
func (h Hook) Validate() error {
	if h.ID == "" {
		return errors.New("hooks: id required")
	}
	if h.Name == "" {
		return errors.New("hooks: name required")
	}
	if !isKnownEvent(h.Event) {
		return fmt.Errorf("hooks: unknown event %q", h.Event)
	}
	switch h.Kind {
	case KindBuiltin:
		if h.Builtin == "" {
			return errors.New("hooks: kind=builtin requires builtin id")
		}
	case KindShell:
		if h.Command == "" {
			return errors.New("hooks: kind=shell requires command")
		}
	case KindMCP:
		if h.MCPTool == "" {
			return errors.New("hooks: kind=mcp requires mcp_tool")
		}
	default:
		return fmt.Errorf("hooks: unknown kind %q", h.Kind)
	}
	return nil
}

// isKnownEvent returns true for any registered event name.
func isKnownEvent(event string) bool {
	for _, e := range AllEvents {
		if e == event {
			return true
		}
	}
	return false
}

// DefaultSyncTimeoutMs is the default timeout for synchronous hooks.
const DefaultSyncTimeoutMs = 5_000

// DefaultAsyncTimeoutMs is the default timeout for async hooks.
const DefaultAsyncTimeoutMs = 60_000

// EffectiveTimeoutMs returns the hook's configured timeout or the
// appropriate default for its kind.
func (h Hook) EffectiveTimeoutMs() int {
	if h.TimeoutMs > 0 {
		return h.TimeoutMs
	}
	return DefaultSyncTimeoutMs
}

// Registry holds the ordered hook list and persists changes to
// <dataDir>/hooks.json (mode 0600). Safe for concurrent callers.
type Registry struct {
	mu    sync.RWMutex
	path  string
	hooks []Hook
}

// NewRegistry opens (or creates) a registry rooted at dataDir. An
// empty dataDir gives an in-memory registry (test-friendly default).
// On a missing-file the registry is empty; on a corrupt file the
// error is surfaced so the user sees the problem.
func NewRegistry(dataDir string) (*Registry, error) {
	r := &Registry{}
	if dataDir != "" {
		r.path = filepath.Join(dataDir, "hooks.json")
	}
	if err := r.load(); err != nil {
		return nil, err
	}
	return r, nil
}

// Path returns the on-disk path. Empty when in-memory.
func (r *Registry) Path() string { return r.path }

func (r *Registry) load() error {
	if r.path == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	data, err := os.ReadFile(r.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("hooks: read: %w", err)
	}
	var got []Hook
	if err := json.Unmarshal(data, &got); err != nil {
		return fmt.Errorf("hooks: parse: %w", err)
	}
	r.hooks = got
	return nil
}

// saveLocked writes the current hook list atomically. Caller holds
// r.mu (write).
func (r *Registry) saveLocked() error {
	if r.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return fmt.Errorf("hooks: mkdir: %w", err)
	}
	data, err := json.MarshalIndent(r.hooks, "", "  ")
	if err != nil {
		return fmt.Errorf("hooks: marshal: %w", err)
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("hooks: write tmp: %w", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("hooks: rename: %w", err)
	}
	return nil
}

// List returns a defensive copy of the current hook slice.
func (r *Registry) List() []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Hook, len(r.hooks))
	copy(out, r.hooks)
	return out
}

// Get returns the hook with the given ID.
func (r *Registry) Get(id string) (Hook, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, h := range r.hooks {
		if h.ID == id {
			return h, nil
		}
	}
	return Hook{}, ErrHookNotFound
}

// Add appends a hook. Returns ErrHookExists if the ID is taken.
func (r *Registry) Add(h Hook) error {
	if err := h.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.hooks {
		if existing.ID == h.ID {
			return ErrHookExists
		}
	}
	r.hooks = append(r.hooks, h)
	if err := r.saveLocked(); err != nil {
		// Roll back on save failure.
		r.hooks = r.hooks[:len(r.hooks)-1]
		return err
	}
	return nil
}

// Update replaces the hook with the matching ID.
func (r *Registry) Update(h Hook) error {
	if err := h.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.hooks {
		if existing.ID == h.ID {
			prev := r.hooks[i]
			r.hooks[i] = h
			if err := r.saveLocked(); err != nil {
				r.hooks[i] = prev
				return err
			}
			return nil
		}
	}
	return ErrHookNotFound
}

// Remove deletes the hook with the given ID.
func (r *Registry) Remove(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.hooks {
		if existing.ID == id {
			prev := append([]Hook{}, r.hooks...)
			r.hooks = append(r.hooks[:i], r.hooks[i+1:]...)
			if err := r.saveLocked(); err != nil {
				r.hooks = prev
				return err
			}
			return nil
		}
	}
	return ErrHookNotFound
}

// HasBuiltin returns true if any hook with the given builtin id is
// configured. The settings auto-install path uses this so toggling
// "long-term memory" off only removes hooks the toggle installed.
func (r *Registry) HasBuiltin(builtinID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, h := range r.hooks {
		if h.Kind == KindBuiltin && h.Builtin == builtinID {
			return true
		}
	}
	return false
}

// ErrHookNotFound / ErrHookExists are surfaced through the rpc layer
// so the UI can render a sensible message without parsing strings.
var (
	ErrHookNotFound = errors.New("hooks: not found")
	ErrHookExists   = errors.New("hooks: id already exists")
)

// Now is the package-level clock the runner uses for timing logs.
// Tests override it to make output deterministic.
var Now = time.Now

// EnabledForEvent returns the subset of registered hooks for the
// given event, in declaration order, that are enabled and match the
// supplied (sessionID, kind, model) triple.
func (r *Registry) EnabledForEvent(event, sessionID, kind, model string) []Hook {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []Hook
	for _, h := range r.hooks {
		if !h.Enabled {
			continue
		}
		if h.Event != event {
			continue
		}
		if !h.Match.Matches(sessionID, kind, model) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// withCtxValue lets tests stamp metadata onto the context that the
// runner reads (e.g. forcing the shell-hook timeout). Kept here so
// the hooks package owns its own context keys.
type ctxKey int

const (
	ctxShellTimeout ctxKey = iota
)

// WithShellTimeout overrides the default 10-second timeout for shell
// hooks fired through the supplied context.
func WithShellTimeout(ctx context.Context, d time.Duration) context.Context {
	return context.WithValue(ctx, ctxShellTimeout, d)
}

func shellTimeoutFromCtx(ctx context.Context) time.Duration {
	if v, ok := ctx.Value(ctxShellTimeout).(time.Duration); ok && v > 0 {
		return v
	}
	return 10 * time.Second
}
