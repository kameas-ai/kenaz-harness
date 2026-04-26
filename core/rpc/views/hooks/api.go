// Package hooks defines the HooksAPI view-scoped accessor that
// surfaces the lifecycle-hook configuration to the frontend. The
// concrete implementation lives in impl.go and wraps core/hooks.Registry
// + core/hooks.BuiltinRegistry so the rpc layer doesn't import the
// dispatch internals directly.
//
// HookInput is the wire shape the UI POSTs at the Add / Update RPCs;
// it mirrors core/hooks.Hook minus the Validate() helper (the rpc
// layer calls Validate after translating to the storage shape).
package hooks

import "context"

// Match is the wire shape of HookMatch. JSON tags use camelCase to
// match the chassis-wide convention.
type Match struct {
	SessionIDs []string `json:"sessionIds,omitempty"`
	Kinds      []string `json:"kinds,omitempty"`
	Models     []string `json:"models,omitempty"`
}

// Hook is the wire shape of core/hooks.Hook. Tag conventions match
// the rest of the rpc surface (camelCase).
type Hook struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Event   string         `json:"event"`
	Kind    string         `json:"kind"`
	Enabled bool           `json:"enabled"`
	Match   Match          `json:"match"`
	Builtin string         `json:"builtin,omitempty"`
	Command string         `json:"command,omitempty"`
	MCPTool string         `json:"mcpTool,omitempty"`
	Config  map[string]any `json:"config,omitempty"`
}

// HookInput is the payload for Add / Update.
type HookInput = Hook

// BuiltinDescriptor is the wire shape returned by AvailableBuiltins.
// Mirrors core/hooks.BuiltinDescriptor.
type BuiltinDescriptor struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Events        []string       `json:"events"`
	DefaultConfig map[string]any `json:"defaultConfig,omitempty"`
}

// HooksAPI is the view-scoped accessor.
type HooksAPI interface {
	List(ctx context.Context) ([]Hook, error)
	Get(ctx context.Context, id string) (Hook, error)
	Add(ctx context.Context, h HookInput) (Hook, error)
	Update(ctx context.Context, h HookInput) error
	Remove(ctx context.Context, id string) error
	AvailableBuiltins(ctx context.Context) ([]BuiltinDescriptor, error)

	// InstallStarterMemoryHooks adds the two preinstalled memory hooks
	// (memory.retrieve + memory.persist). Idempotent: hooks already
	// present are left untouched. Surfaces the "enable long-term
	// memory" CTA in Settings + the Defaults CTA on /hooks's first
	// visit.
	InstallStarterMemoryHooks(ctx context.Context) error
	// RemoveStarterMemoryHooks removes the starter memory hooks. Pairs
	// with the settings toggle going off.
	RemoveStarterMemoryHooks(ctx context.Context) error
}
