package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ToolResult is the canonical wrapped shape every harness-self handler
// returns. Mirrors FR-009: each tool result carries a human-readable
// confirmation message the agent can echo, an `ok` flag, and an opaque
// `data` payload for richer fields.
type ToolResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Managers carries the host-side dependencies the harness-self tool
// handlers reach into. Keeping the surface narrow lets tests stub one
// dependency at a time. WP04/WP05 add real fields; WP02 ships an empty
// struct so the wiring path is in place.
//
// TODO(v0.3.x): wire concrete managers (llm.Registry, recipes.Registry,
// session.Manager, settings.API, projects manager, cedar engine).
type Managers struct {
	// Read-side managers (WP04). Nil-safe: handlers fall back to a
	// "not configured" ToolResult when their backing manager is nil.
	Providers ProviderLister
	Recipes   RecipeLister
	Settings  SettingsReader
	Status    StatusReporter

	// Write-side managers (WP05). Nil-safe in the same way.
	ProvidersWriter ProviderWriter
	RecipesWriter   RecipeWriter
	SettingsWriter  SettingsWriter
	ProjectsWriter  ProjectWriter

	// Cedar proposal broker (WP07). Nil-safe.
	CedarProposer CedarProposer
}

// ---- Read-side interfaces (WP04 stubs) ----

// ProviderLister returns a sanitized list of configured providers.
type ProviderLister interface {
	ListProviders(ctx context.Context) ([]ProviderSummary, error)
}

// ProviderSummary is the JSON shape returned to the model. NEVER carries
// secret material.
type ProviderSummary struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Model string `json:"model,omitempty"`
}

// RecipeLister returns the curated MCP recipe registry.
type RecipeLister interface {
	ListRecipes(ctx context.Context) ([]RecipeSummary, error)
}

type RecipeSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// SettingsReader / Writer expose the JSON-backed settings store.
type SettingsReader interface {
	ListSettings(ctx context.Context) (map[string]any, error)
}

// StatusReporter returns aggregate counts (providers, MCP servers,
// sessions, projects). Used by harness_read_get_status.
type StatusReporter interface {
	HarnessStatus(ctx context.Context) (StatusSnapshot, error)
}

type StatusSnapshot struct {
	Providers int `json:"providers"`
	MCPInstalled int `json:"mcpInstalled"`
	Sessions  int `json:"sessions"`
	Projects  int `json:"projects"`
	Policies  int `json:"policies"`
}

// ---- Write-side interfaces (WP05 stubs) ----

// ProviderWriter creates / removes provider configurations.
type ProviderWriter interface {
	AddProvider(ctx context.Context, kind, name, model, apiKey string) (ProviderSummary, error)
	RemoveProvider(ctx context.Context, id string) error
}

// RecipeWriter installs MCP recipes by id (curated registry) or by raw
// config object.
type RecipeWriter interface {
	InstallRecipe(ctx context.Context, idOrConfig string, config json.RawMessage) error
}

type SettingsWriter interface {
	SetSetting(ctx context.Context, key string, value any) error
}

type ProjectWriter interface {
	CreateProject(ctx context.Context, name, description string) (string, error)
}

// CedarProposer brokers the propose_cedar_policy confirm-flow (WP07).
type CedarProposer interface {
	Propose(ctx context.Context, name, body string) error
}

// SettingsAllowlist enumerates the keys harness_write_set_setting is
// permitted to mutate. Hard-coded to keep the surface area auditable.
// Update with care; an over-broad allowlist defeats Cedar's session-kind
// gate.
var SettingsAllowlist = map[string]struct{}{
	"OnboardingCompleted":     {},
	"HarnessSelfMCPDisabled":  {},
	"DefaultProvider":         {},
	"DefaultModel":            {},
	"AutoTitleEnabled":        {},
}

// errNotConfigured is returned by handlers whose backing manager is nil.
// We surface it as a tool-level error (isError=true) rather than an RPC
// error so the agent can recover gracefully.
var errNotConfigured = errors.New("harness-self: tool backend not configured for this harness build")

// ---- Read tool handlers (WP04 stubs) ----

func (m Managers) handleListProviders(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Providers == nil {
		return nil, errNotConfigured
	}
	out, err := m.Providers.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("%d provider(s) configured", len(out)), Data: out}, nil
}

func (m Managers) handleListRecipes(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Recipes == nil {
		return nil, errNotConfigured
	}
	out, err := m.Recipes.ListRecipes(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("%d recipe(s) available", len(out)), Data: out}, nil
}

func (m Managers) handleListSettings(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Settings == nil {
		return nil, errNotConfigured
	}
	out, err := m.Settings.ListSettings(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: "settings snapshot", Data: out}, nil
}

func (m Managers) handleGetStatus(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Status == nil {
		return nil, errNotConfigured
	}
	out, err := m.Status.HarnessStatus(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{
		OK: true,
		Message: fmt.Sprintf("status: %d providers, %d MCP servers, %d sessions, %d projects",
			out.Providers, out.MCPInstalled, out.Sessions, out.Projects),
		Data: out,
	}, nil
}

// handleGetRecommendations is a stub — WP04 lands the real curated
// recommendation logic. Today it returns a static pointer to "configure
// a provider first" so the agent has something to anchor on.
func (m Managers) handleGetRecommendations(_ context.Context, _ json.RawMessage) (any, error) {
	return ToolResult{
		OK:      true,
		Message: "configure your first provider to unlock further recommendations",
		Data: []map[string]string{
			{"id": "configure-provider", "label": "Add an LLM provider"},
			{"id": "install-filesystem", "label": "Install the filesystem MCP recipe"},
			{"id": "create-project", "label": "Create a project to group sessions"},
		},
	}, nil
}

// ---- Write tool handlers (WP05 stubs) ----

func (m Managers) handleAddProvider(ctx context.Context, args json.RawMessage) (any, error) {
	if m.ProvidersWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		Kind   string `json:"kind"`
		Name   string `json:"name"`
		Model  string `json:"model"`
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("harness_write_add_provider: %w", err)
	}
	if p.Kind == "" || p.APIKey == "" {
		return nil, errors.New("harness_write_add_provider: kind and api_key are required")
	}
	sum, err := m.ProvidersWriter.AddProvider(ctx, p.Kind, p.Name, p.Model, p.APIKey)
	if err != nil {
		return nil, err
	}
	return ToolResult{
		OK:      true,
		Message: fmt.Sprintf("Added %s provider %q", sum.Kind, sum.Name),
		Data:    sum,
	}, nil
}

func (m Managers) handleRemoveProvider(ctx context.Context, args json.RawMessage) (any, error) {
	if m.ProvidersWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	if err := m.ProvidersWriter.RemoveProvider(ctx, p.ID); err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Removed provider %q", p.ID)}, nil
}

func (m Managers) handleInstallRecipe(ctx context.Context, args json.RawMessage) (any, error) {
	if m.RecipesWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		ID     string          `json:"id"`
		Config json.RawMessage `json:"config,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	if err := m.RecipesWriter.InstallRecipe(ctx, p.ID, p.Config); err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Installed recipe %q", p.ID)}, nil
}

func (m Managers) handleSetSetting(ctx context.Context, args json.RawMessage) (any, error) {
	if m.SettingsWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	if _, ok := SettingsAllowlist[p.Key]; !ok {
		return nil, fmt.Errorf("harness_write_set_setting: key %q is not in the allowlist", p.Key)
	}
	if err := m.SettingsWriter.SetSetting(ctx, p.Key, p.Value); err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Set %s", p.Key)}, nil
}

func (m Managers) handleCreateProject(ctx context.Context, args json.RawMessage) (any, error) {
	if m.ProjectsWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, errors.New("harness_write_create_project: name is required")
	}
	id, err := m.ProjectsWriter.CreateProject(ctx, p.Name, p.Description)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Created project %q", p.Name), Data: map[string]string{"id": id}}, nil
}

func (m Managers) handleProposeCedarPolicy(ctx context.Context, args json.RawMessage) (any, error) {
	if m.CedarProposer == nil {
		return nil, errNotConfigured
	}
	var p struct {
		Name string `json:"name"`
		Body string `json:"body"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, err
	}
	if err := m.CedarProposer.Propose(ctx, p.Name, p.Body); err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Cedar policy %q queued for confirmation", p.Name)}, nil
}
