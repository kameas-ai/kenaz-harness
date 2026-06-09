// Package slashcmd is the view-scoped accessor for the slash-command
// surface. It is the boundary between the chat composer's
// `Slash_Execute` / `Slash_List` Wails bindings and the
// `core/slashcmd` registry.
//
// Wire shapes here are deliberately small + camelCase-tagged so the
// JSON payload that crosses the Wails boundary is stable for the
// frontend's harnessClient.ts adapter.
package slashcmd

import (
	"context"

	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
)

// CommandInfo is the wire shape returned by List for the autocomplete
// dropdown. ComingSoon flags stub commands (the ones whose real
// implementation lives in the agent-kernel-graph mission); the
// frontend renders them with a "(coming soon)" tag.
type CommandInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ComingSoon  bool   `json:"comingSoon"`
	// IsUser, when true, marks this as a user-defined command
	// (as opposed to a built-in). The frontend renders a "user" chip.
	IsUser bool `json:"isUser,omitempty"`
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

// UserCommandWire is the full wire shape for a user-defined command.
// Mirrors coreslashcmd.UserCommand with JSON tags for the Wails boundary.
type UserCommandWire struct {
	Name             string                          `json:"name"`
	Scope            string                          `json:"scope"`
	ProjectID        string                          `json:"projectId,omitempty"`
	Kind             string                          `json:"kind"`
	Description      string                          `json:"description"`
	WhenToUse        string                          `json:"whenToUse,omitempty"`
	DoesNotHandle    string                          `json:"doesNotHandle,omitempty"`
	ModelInvokable   bool                            `json:"modelInvokable"`
	Icon             string                          `json:"icon,omitempty"`
	HiddenFromPanel  bool                            `json:"hiddenFromPanel,omitempty"`
	Body             string                          `json:"body,omitempty"`
	Tool             string                          `json:"tool,omitempty"`
	ToolArgsTemplate string                          `json:"toolArgsTemplate,omitempty"`
	Inputs           []coreslashcmd.UserCommandInput `json:"inputs,omitempty"`
	PayloadPath      string                          `json:"payloadPath,omitempty"`
	CreatedAt        int64                           `json:"createdAt,omitempty"`
	UpdatedAt        int64                           `json:"updatedAt,omitempty"`
}

// UserCommandSummaryWire is the lightweight wire shape returned by
// Slashcmd_List.
type UserCommandSummaryWire struct {
	Name           string `json:"name"`
	Scope          string `json:"scope"`
	ProjectID      string `json:"projectId,omitempty"`
	Kind           string `json:"kind"`
	Description    string `json:"description"`
	ModelInvokable bool   `json:"modelInvokable"`
	Icon           string `json:"icon,omitempty"`
	UpdatedAt      int64  `json:"updatedAt,omitempty"`
}

// RunResultWire is the wire shape returned by Slashcmd_Run.
type RunResultWire struct {
	Kind         string         `json:"kind"`
	Text         string         `json:"text"`
	RenderedArgs []string       `json:"renderedArgs,omitempty"`
	ToolName     string         `json:"toolName,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// SkillItemWire is the wire shape for a fleet-installed skill (returned
// by SkillList). Mirrors slashcmd.Skill with JSON tags.
// (fleet-skills-sync-01NDFSEX18 WP02)
type SkillItemWire struct {
	ID           string `json:"id"`
	CatalogID    string `json:"catalogId,omitempty"`
	Version      string `json:"version,omitempty"`
	Source       string `json:"source"`
	Trigger      string `json:"trigger"`
	LocalTrigger string `json:"localTrigger,omitempty"`
	Kind         string `json:"kind"`
	Description  string `json:"description,omitempty"`
	Body         string `json:"body,omitempty"`
	OrgManaged   bool   `json:"orgManaged"`
	Disabled     bool   `json:"disabled,omitempty"`
	// Shadowed is true when the effective trigger is occupied by a
	// higher-priority personal or built-in command (FR-401).
	Shadowed     bool   `json:"shadowed,omitempty"`
	InstalledAt  int64  `json:"installedAt,omitempty"`
	UpdatedAt    int64  `json:"updatedAt,omitempty"`
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

	// ── user command CRUD ─────────────────────────────────────────

	// Slashcmd_List returns all user commands visible to the given
	// project (global + project-scoped).
	UserList(ctx context.Context, projectID string) ([]UserCommandSummaryWire, error)
	// UserGet returns the full detail of a single user command.
	UserGet(ctx context.Context, name, projectID string) (UserCommandWire, error)
	// UserSave creates or updates a user command (both surfaces).
	UserSave(ctx context.Context, cmd UserCommandWire) error
	// UserDelete removes a user command by name + projectID.
	UserDelete(ctx context.Context, name, projectID string) error
	// UserRun dispatches a user command by name with the given args.
	// This is the human-invoked entry point; the __skill builtin tool
	// calls Dispatch.Run directly.
	UserRun(ctx context.Context, name string, args map[string]string, sessionID, projectID, cwd, selection string) (RunResultWire, error)

	// ── fleet skill CRUD (fleet-skills-sync-01NDFSEX18 WP02/03/04) ────

	// SkillList returns all fleet-installed skills (catalog + mandated).
	SkillList(ctx context.Context) ([]SkillItemWire, error)
	// SkillPublish signs and publishes a user command as a fleet skill
	// with the given visibility scope. Requires fleet + capability.
	SkillPublish(ctx context.Context, name, projectID, visibility string) error
	// SkillInstall downloads, verifies, and live-registers a skill from
	// the catalog. No restart required (FR-202).
	SkillInstall(ctx context.Context, catalogID, version string) error
	// SkillUninstall removes and live-unregisters a skill by its store ID.
	// For catalog skills the ID is the catalogID; for mandated skills it
	// is the slug assigned by the fleet admin.
	SkillUninstall(ctx context.Context, skillID string) error
}
