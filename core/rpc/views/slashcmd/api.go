// Package slashcmd is the view-scoped accessor for the slash-command
// surface. It is the boundary between the chat composer's
// `Slash_Execute` / `Slash_List` Wails bindings and the
// `core/slashcmd` registry.
//
// Wire shapes here are deliberately small + camelCase-tagged so the
// JSON payload that crosses the Wails boundary is stable for the
// frontend's harnessClient.ts adapter.
package slashcmd

import "context"

// CommandInfo is the wire shape returned by List for the autocomplete
// dropdown. ComingSoon flags stub commands (the ones whose real
// implementation lives in the agent-kernel-graph mission); the
// frontend renders them with a "(coming soon)" tag.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ComingSoon  bool   `json:"comingSoon"`
}

// ExecuteResult is the wire shape returned by Execute. Text is the
// user-visible body the chat surface renders in a system / info /
// error / warning bubble (Kind discriminates). Metadata carries
// well-known keys (modelId, providerId, owningMission) the frontend
// reads to apply local side effects.
type ExecuteResult struct {
	Text     string         `json:"text"`
	Kind     string         `json:"kind"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// SlashAPI is the view-scoped surface backing the chat composer's
// slash-command path.
type SlashAPI interface {
	// Execute parses raw ("/foo arg1 arg2"), dispatches to the
	// registered handler, and returns a typed result. Unknown
	// commands and parse errors surface as ExecuteResults with
	// Kind="error" — the surface never crashes the composer.
	Execute(ctx context.Context, sessionID, raw string) (ExecuteResult, error)
	// List returns the visible registered commands sorted by name.
	// Hidden commands are filtered out; ComingSoon commands stay
	// in the list (so /help and the autocomplete advertise them).
	List(ctx context.Context) ([]CommandInfo, error)
}
