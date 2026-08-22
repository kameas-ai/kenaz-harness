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
type Managers struct {
	// Read-side managers (WP04). Nil-safe: handlers fall back to a
	// "not configured" ToolResult when their backing manager is nil.
	Providers ProviderLister
	Recipes   RecipeLister
	Settings  SettingsReader
	Status    StatusReporter
	Sessions  SessionLister
	Models    ModelLister

	// Write-side managers (WP05). Nil-safe in the same way.
	//
	// SettingsWriter was removed by harness-self-attach-01PMHS01 UNIT-8
	// (G-4, docs/escalation-register-2026-08-19.md Part 9: "the model
	// writes no settings keys"). It backed harness_write_set_setting,
	// whose five allowlisted keys all routed through settingsKeyToJSON
	// sentinels the JSON round-trip silently discarded — the tool
	// reported success and changed nothing. The owner ruled removal
	// rather than wiring a real writer: none of the five keys may be
	// model-writable, so there is nothing left for this manager to do.
	ProvidersWriter ProviderWriter
	RecipesWriter   RecipeWriter
	ProjectsWriter  ProjectWriter
	SessionsWriter  SessionCreator

	// GraphAuthor / GraphMaterializer (model-authored-graphs-01PMGA01
	// UNIT-7). GraphAuthor is write-side (harness_write_draft_agent_graph);
	// GraphMaterializer is read-side (harness_read_materialize_run) —
	// grouped here rather than split into the two blocks above because
	// they are one capability pair and the mission that owns them treats
	// them as such.
	GraphAuthor       GraphAuthorWriter
	GraphMaterializer GraphMaterializer
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

// SessionLister returns a sanitized list of sessions.
type SessionLister interface {
	ListSessions(ctx context.Context) ([]SessionSummary, error)
}

// SessionSummary is the JSON shape returned to the model. Never carries
// message content.
type SessionSummary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// ModelLister returns a list of models available for a given provider kind.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelSummary, error)
}

// ModelSummary is the JSON shape returned to the model for each LLM model.
type ModelSummary struct {
	ProviderID   string `json:"providerId"`
	ProviderKind string `json:"providerKind"`
	ModelID      string `json:"modelId"`
	DisplayName  string `json:"displayName,omitempty"`
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

// SettingsWriter, SettingsAllowlist, and handleSetSetting/
// harness_write_set_setting were removed by harness-self-attach-01PMHS01
// UNIT-8. All five formerly allowlisted keys (OnboardingCompleted,
// HarnessSelfMCPDisabled, DefaultProvider, DefaultModel, AutoTitleEnabled)
// routed through settingsKeyToJSON sentinels
// (core/rpc/harness_wiring.go) the JSON round-trip silently discarded —
// the tool reported OK:true and changed nothing. G-4
// (docs/escalation-register-2026-08-19.md Part 9, "HS01 §10.4: the model
// writes no settings keys") ruled removal, not a real writer: "The lie
// ends immediately and no new model-write capability is created. In
// particular the model cannot change DefaultProvider or DefaultModel, so
// a prompt-injected model cannot redirect which vendor receives the
// user's traffic and data." With zero keys left in the allowlist the
// tool had no remaining purpose, so it was unregistered
// (core/mcp/builtin/harness/register.go) rather than shipped as a tool
// that reports failure for every call — one of the two honest exits
// FR-009 named, and the one that applied once the allowlist reached
// zero. harness_read_list_settings (SettingsReader, above) is unaffected
// — reading settings was never the lie; writing them was.

// SessionCreator creates a new session with an optional kind tag.
type SessionCreator interface {
	CreateSession(ctx context.Context, name, kind string) (SessionSummary, error)
}

// ---- Agent-graph authoring interfaces (model-authored-graphs-01PMGA01
// UNIT-7) ----
//
// These mirror graphview types locally (GraphDraftIssue mirrors
// graphview.ValidationIssue, GraphMaterializeResult mirrors
// graphview.GraphSpec) rather than importing core/rpc/views/agentgraph
// directly — the same convention every other manager interface in this
// file already follows (SessionSummary, ProviderSummary, ...): this
// package stays a leaf with no core/rpc/views/* or core/policy/cedar
// dependency, and the adapter in core/rpc/harness_wiring.go (which CAN
// import both) does the translation, including the errors.As
// type-switch on Manager.saveGraph's *ValidationFailedError /
// *cedar.PolicyDeniedError.

// GraphDraftIssue is one validator rule violation, mirroring
// graphview.ValidationIssue.
type GraphDraftIssue struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// GraphDraftOutcome is the adapter's already-classified report of one
// draft attempt. Exactly one of three shapes is populated:
//   - OK true, PathHint set — the draft was validated, gated, stamped
//     and persisted (FR-001/FR-002/FR-005/FR-009).
//   - OK false, Issues non-empty — coreag.Validate rejected the YAML;
//     nothing was written (FR-002/FR-003).
//   - OK false, DeniedReason non-empty — the graph.author Cedar gate
//     denied (FR-005/FR-006/FR-008); nothing was written.
//
// A returned Go error (see GraphAuthorWriter) is reserved for anything
// that isn't one of these three classified outcomes (malformed
// arguments, an id collision with an existing graph — this tool is
// create-only per E-008 — or a manager-level configuration error).
type GraphDraftOutcome struct {
	OK           bool
	PathHint     string
	Issues       []GraphDraftIssue
	DeniedReason string
}

// GraphAuthorWriter drafts a model-authored agent graph via
// Manager.saveGraph(initiator="model") — validated, gated on
// graph.author, and stamped spec_provenance=model_authored server-side
// (FR-001, FR-002, FR-005, FR-009). It never starts the graph: no
// method on this interface reaches Manager.startRun (FR-007).
type GraphAuthorWriter interface {
	DraftAgentGraph(ctx context.Context, id, yaml string) (GraphDraftOutcome, error)
}

// GraphMaterializeResult mirrors graphview.GraphSpec — the projected,
// already-redacted shape of one run (FR-011). SpecProvenance is
// surfaced verbatim, INCLUDING the degraded "library_fallback" marker
// (C-006) — a caller must not treat an empty SpecProvenance the same
// as a faithful one.
type GraphMaterializeResult struct {
	ID             string `json:"id"`
	Name           string `json:"name,omitempty"`
	Scope          string `json:"scope"`
	YAML           string `json:"yaml"`
	SpecProvenance string `json:"specProvenance,omitempty"`
}

// GraphMaterializer exposes Manager.materializeRun (via the agentgraph
// API's MaterializeRun) as a read tool (FR-011). Straight through, no
// new redaction: materialize.go already reduces tool-call arguments to
// key names, results to byte counts, and errors to a closed constant
// vocabulary.
type GraphMaterializer interface {
	MaterializeRun(ctx context.Context, runID string) (GraphMaterializeResult, error)
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

func (m Managers) handleListSessions(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Sessions == nil {
		return nil, errNotConfigured
	}
	out, err := m.Sessions.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("%d session(s)", len(out)), Data: out}, nil
}

func (m Managers) handleListModels(ctx context.Context, _ json.RawMessage) (any, error) {
	if m.Models == nil {
		return nil, errNotConfigured
	}
	out, err := m.Models.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("%d model(s) across configured providers", len(out)), Data: out}, nil
}

// ---- Agent-graph authoring handlers (model-authored-graphs-01PMGA01
// UNIT-7) ----

func (m Managers) handleDraftAgentGraph(ctx context.Context, args json.RawMessage) (any, error) {
	if m.GraphAuthor == nil {
		return nil, errNotConfigured
	}
	var p struct {
		ID   string `json:"id"`
		YAML string `json:"yaml"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("harness_write_draft_agent_graph: %w", err)
	}
	if p.ID == "" || p.YAML == "" {
		return nil, errors.New("harness_write_draft_agent_graph: id and yaml are required")
	}
	outcome, err := m.GraphAuthor.DraftAgentGraph(ctx, p.ID, p.YAML)
	if err != nil {
		return nil, err
	}
	if !outcome.OK {
		if len(outcome.Issues) > 0 {
			return ToolResult{
				OK:      false,
				Message: fmt.Sprintf("Draft %q failed validation (%d issue(s)); nothing was written.", p.ID, len(outcome.Issues)),
				Data:    map[string]any{"issues": outcome.Issues},
			}, nil
		}
		return ToolResult{
			OK:      false,
			Message: fmt.Sprintf("Draft %q was refused: %s", p.ID, outcome.DeniedReason),
		}, nil
	}
	return ToolResult{
		OK: true,
		Message: fmt.Sprintf(
			"Draft %q saved as an UNREVIEWED draft at %s. It will NOT run — no tool, including this one, can start it — until a human opens it in the graph editor and saves it, which marks it reviewed.",
			p.ID, outcome.PathHint,
		),
		Data: map[string]any{"id": p.ID, "path_hint": outcome.PathHint, "reviewed": false},
	}, nil
}

func (m Managers) handleMaterializeRun(ctx context.Context, args json.RawMessage) (any, error) {
	if m.GraphMaterializer == nil {
		return nil, errNotConfigured
	}
	var p struct {
		RunID string `json:"run_id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("harness_read_materialize_run: %w", err)
	}
	if p.RunID == "" {
		return nil, errors.New("harness_read_materialize_run: run_id is required")
	}
	result, err := m.GraphMaterializer.MaterializeRun(ctx, p.RunID)
	if err != nil {
		return nil, err
	}
	return ToolResult{
		OK:      true,
		Message: fmt.Sprintf("Materialized run %q as graph %q (spec_provenance=%q).", p.RunID, result.ID, result.SpecProvenance),
		Data:    result,
	}, nil
}

func (m Managers) handleCreateSession(ctx context.Context, args json.RawMessage) (any, error) {
	if m.SessionsWriter == nil {
		return nil, errNotConfigured
	}
	var p struct {
		Name string `json:"name"`
		Kind string `json:"kind,omitempty"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return nil, fmt.Errorf("harness_write_create_session: %w", err)
	}
	if p.Name == "" {
		return nil, errors.New("harness_write_create_session: name is required")
	}
	if p.Kind == "" {
		p.Kind = "chat"
	}
	sum, err := m.SessionsWriter.CreateSession(ctx, p.Name, p.Kind)
	if err != nil {
		return nil, err
	}
	return ToolResult{OK: true, Message: fmt.Sprintf("Created session %q (kind=%s)", sum.Name, sum.Kind), Data: sum}, nil
}

