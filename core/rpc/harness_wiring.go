// harness_wiring.go wires the harness-self MCP server (WP04+WP05) with
// real manager adapters that call into the production view APIs.
//
// Import contract: this file lives in package rpc so it can freely import
// both core/mcp/builtin/harness (which has no rpc dependency) and any
// core/rpc/views/... package without creating an import cycle.
//
// Privacy: adapters project only the fields the harness tool handlers need.
// No prompt content, message bodies, or plaintext credentials appear in the
// returned summaries.
package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	coreag "github.com/kameas-ai/kenaz-harness/core/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/event/kind"
	harness "github.com/kameas-ai/kenaz-harness/core/mcp/builtin/harness"
	"github.com/kameas-ai/kenaz-harness/core/mcp/recipes"
	"github.com/kameas-ai/kenaz-harness/core/policy/cedar"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	projectsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/projects"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/session"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"
)

// ---- WP04 read adapters ----

// settingsReaderAdapter wraps settings.SettingsAPI and implements
// harness.SettingsReader. Returns the full Settings as a map[string]any so
// the MCP agent can inspect any field without a schema assumption.
type settingsReaderAdapter struct{ api settings.SettingsAPI }

func (a settingsReaderAdapter) ListSettings(ctx context.Context) (map[string]any, error) {
	s, err := a.api.Get(ctx)
	if err != nil {
		return nil, err
	}
	// Round-trip through JSON to produce a key→value map with the same
	// field names the frontend uses. Privacy: the Settings struct never
	// carries plaintext credentials — it is safe to return wholesale.
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("harness.settings: marshal: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("harness.settings: unmarshal map: %w", err)
	}
	return out, nil
}

// llmProvidersAdapter wraps llm.LLMConnectorAPI and implements
// harness.ProviderLister. Projects Provider → ProviderSummary; no
// credentials or redaction data are forwarded.
type llmProvidersAdapter struct{ api llm.LLMConnectorAPI }

func (a llmProvidersAdapter) ListProviders(ctx context.Context) ([]harness.ProviderSummary, error) {
	rows, err := a.api.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]harness.ProviderSummary, 0, len(rows))
	for _, p := range rows {
		out = append(out, harness.ProviderSummary{
			ID:    p.ID,
			Kind:  p.Kind,
			Name:  p.Name,
			Model: p.Model,
		})
	}
	return out, nil
}

// llmModelsAdapter wraps llm.LLMConnectorAPI and implements
// harness.ModelLister. Enumerates all models across all configured
// providers by reading each Provider's Models slice.
type llmModelsAdapter struct{ api llm.LLMConnectorAPI }

func (a llmModelsAdapter) ListModels(ctx context.Context) ([]harness.ModelSummary, error) {
	providers, err := a.api.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	var out []harness.ModelSummary
	for _, p := range providers {
		models := p.Models
		if len(models) == 0 && p.Model != "" {
			models = []string{p.Model}
		}
		for i, m := range models {
			ms := harness.ModelSummary{
				ProviderID:   p.ID,
				ProviderKind: p.Kind,
				ModelID:      m,
			}
			// Populate DisplayName from ModelInfos when available (parallel
			// by index with Models slice).
			if i < len(p.ModelInfos) {
				ms.DisplayName = p.ModelInfos[i].DisplayName
			}
			out = append(out, ms)
		}
	}
	return out, nil
}

// recipesAdapter wraps the merged recipe catalog and implements
// harness.RecipeLister. The catalog is read-only here.
type recipesAdapter struct{ cat *recipes.MergedCatalog }

func (a recipesAdapter) ListRecipes(_ context.Context) ([]harness.RecipeSummary, error) {
	all := a.cat.Recipes()
	out := make([]harness.RecipeSummary, 0, len(all))
	for _, r := range all {
		out = append(out, harness.RecipeSummary{
			ID:          r.ID,
			Name:        r.DisplayName,
			Description: r.Description,
		})
	}
	return out, nil
}

// sessionsListAdapter wraps sessions.SessionsAPI and implements
// harness.SessionLister. Projects Session → SessionSummary; no message
// content is forwarded.
type sessionsListAdapter struct{ api sessions.SessionsAPI }

func (a sessionsListAdapter) ListSessions(ctx context.Context) ([]harness.SessionSummary, error) {
	rows, err := a.api.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]harness.SessionSummary, 0, len(rows))
	for _, s := range rows {
		out = append(out, harness.SessionSummary{
			ID:        s.ID,
			Name:      s.Name,
			Kind:      s.Kind,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.UpdatedAt,
		})
	}
	return out, nil
}

// ---- WP05 write adapters ----

// settingsWriterAdapter and settingsKeyToJSON were removed by
// harness-self-attach-01PMHS01 UNIT-8 (G-4,
// docs/escalation-register-2026-08-19.md Part 9). Every key
// settingsWriterAdapter.SetSetting ever reached mapped through
// settingsKeyToJSON to a `_`-prefixed sentinel the JSON round-trip
// silently discarded — the tool reported OK:true and changed nothing
// for all five formerly allowlisted keys (OnboardingCompleted,
// HarnessSelfMCPDisabled, DefaultProvider, DefaultModel,
// AutoTitleEnabled). The owner ruled removal rather than wiring a real
// writer for any of them: see the doc comment above
// harness.ProjectWriter (core/mcp/builtin/harness/handlers.go) for the
// full rationale. harness.Managers.SettingsWriter, the SettingsWriter
// interface, and handleSetSetting/ToolSetSetting were removed in the
// same commit.

// llmProviderWriterAdapter wraps llm.LLMConnectorAPI and implements
// harness.ProviderWriter. The PlaintextAPIKey is stored in the OS
// keychain by the concrete LLMConnectorAPI.AddProvider implementation
// (privacy: api_key is in the ToolSpec.Redact list and never logged).
type llmProviderWriterAdapter struct{ api llm.LLMConnectorAPI }

func (a llmProviderWriterAdapter) AddProvider(ctx context.Context, kind, name, model, apiKey string) (harness.ProviderSummary, error) {
	in := llm.AddProviderInput{
		Kind:            kind,
		Name:            name,
		Model:           model,
		PlaintextAPIKey: apiKey,
		// Cred.Kind and Cred.Locator are resolved by the llm impl when
		// PlaintextAPIKey is set — the impl writes the key to the keychain
		// and fills in the locator before persisting. ID is auto-assigned.
		Cred: llm.CredentialReference{Kind: "keychain", Locator: kind + "-" + name},
	}
	if err := a.api.AddProvider(ctx, in); err != nil {
		return harness.ProviderSummary{}, err
	}
	// Re-read the list to surface the newly created provider's id.
	providers, err := a.api.ListProviders(ctx)
	if err != nil {
		return harness.ProviderSummary{}, err
	}
	for _, p := range providers {
		if p.Name == name && p.Kind == kind {
			return harness.ProviderSummary{ID: p.ID, Kind: p.Kind, Name: p.Name, Model: p.Model}, nil
		}
	}
	// Fallback: return a summary without an id if the re-read missed it.
	return harness.ProviderSummary{Kind: kind, Name: name, Model: model}, nil
}

func (a llmProviderWriterAdapter) RemoveProvider(ctx context.Context, id string) error {
	return a.api.RemoveProvider(ctx, id)
}

// sessionCreatorAdapter wraps core session.Manager and implements
// harness.SessionCreator. Uses CreateWithKind so the session kind is
// persisted correctly for Cedar policy gating.
type sessionCreatorAdapter struct{ mgr *session.Manager }

func (a sessionCreatorAdapter) CreateSession(ctx context.Context, name, kind string) (harness.SessionSummary, error) {
	if kind == "" {
		kind = session.SessionKindChat
	}
	rec, err := a.mgr.CreateWithKind(ctx, name, nil, kind)
	if err != nil {
		return harness.SessionSummary{}, err
	}
	return harness.SessionSummary{
		ID:        rec.ID,
		Name:      rec.Name,
		Kind:      rec.Kind,
		CreatedAt: rec.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: rec.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

// projectWriterAdapter wraps projectsview.ProjectsAPI and implements
// harness.ProjectWriter (handleCreateProject's dependency).
//
// harness-self-attach-01PMHS01 UNIT-6: before this adapter existed,
// buildHarnessManagers never set Managers.ProjectsWriter, so
// harness_write_create_project always returned errNotConfigured — one
// of the 13 canonical tools, silently broken for every caller. It was
// unreachable (and so unobserved) before this mission's attach gave
// the server a production caller at all; TestHarnessSelfAttach_AC001_
// OnboardingSessionReachesRealHandlers is what surfaced it.
type projectWriterAdapter struct{ api projectsview.ProjectsAPI }

var _ harness.ProjectWriter = projectWriterAdapter{}

func (a projectWriterAdapter) CreateProject(ctx context.Context, name, description string) (string, error) {
	p, err := a.api.Create(ctx, name, description)
	if err != nil {
		return "", err
	}
	return p.ID, nil
}

// graphAuthorAdapter wraps graphview.API and implements both
// harness.GraphAuthorWriter and harness.GraphMaterializer
// (model-authored-graphs-01PMGA01 UNIT-7).
//
// DraftAgentGraph classifies Manager.saveGraph's error (via
// graphview.API.SaveGraph(..., initiator="model")) into one of
// harness.GraphDraftOutcome's three shapes using errors.As on the two
// concrete error types the manager returns — *graphview.
// ValidationFailedError (FR-002/FR-003) and *cedar.PolicyDeniedError
// (FR-005/FR-006/FR-008). This is the ONLY place that type-switch
// happens: core/mcp/builtin/harness stays free of both
// core/rpc/views/agentgraph and core/policy/cedar imports (see the
// doc comment on GraphDraftOutcome).
//
// It also emits kind.KindGraphAuthorAttempted (FR-012) on every
// attempt, permitted or refused, in addition to (not instead of) the
// generic KindHarnessSelfToolCalled every harness-self tool call
// already gets from harnessmcp.WithAudit.
type graphAuthorAdapter struct {
	api   graphview.API
	audit *audit.API
}

var (
	_ harness.GraphAuthorWriter = (*graphAuthorAdapter)(nil)
	_ harness.GraphMaterializer = (*graphAuthorAdapter)(nil)
)

// graphNodeKindsForAudit mirrors graphview's private collectNodeKinds
// (core/rpc/views/agentgraph/manager.go) — sorted, de-duplicated,
// comma-joined `kind:` values — so this adapter can report node_kinds/
// node_count on the audit kind without either exporting a helper out
// of an already-shipped, gated package (UNIT-2..UNIT-5 landed and
// tested before this unit existed) or re-parsing through it. The
// logic is ~10 lines over public coreag.Graph fields; duplicating it
// here is smaller than the surface-area change either alternative
// would require.
func graphNodeKindsForAudit(g coreag.Graph) (kinds string, count int) {
	if len(g.Nodes) == 0 {
		return "", 0
	}
	seen := make(map[string]struct{}, len(g.Nodes))
	var ks []string
	for _, n := range g.Nodes {
		k := string(n.Kind)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ","), len(g.Nodes)
}

// emitDraftAttempt is the FR-012 emission. Payload is deliberately
// narrow: session_id, graph_id, node_kinds, node_count, outcome,
// decision_reason — NEVER the graph YAML, a node attrs value, or a
// write_file path. AC-011 asserts this by searching the serialised
// entry for a distinctive string planted in a submitted YAML's attrs
// and requiring it find nothing.
func (a *graphAuthorAdapter) emitDraftAttempt(ctx context.Context, graphID, nodeKinds string, nodeCount int, outcome, decisionReason string) {
	if a == nil || a.audit == nil {
		return
	}
	sessionID := toolloop.SessionIDFromContext(ctx)
	trailing := fmt.Sprintf(
		"session_id=%s graph_id=%s node_kinds=%s node_count=%d outcome=%s decision_reason=%s",
		sessionID, graphID, nodeKinds, nodeCount, outcome, decisionReason,
	)
	a.audit.Push(audit.Entry{
		ID:        fmt.Sprintf("agentgraph-author-%d", time.Now().UnixNano()),
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Category:  "MCP",
		Subject:   string(kind.KindGraphAuthorAttempted),
		Trailing:  trailing,
	})
}

// DraftAgentGraph implements harness.GraphAuthorWriter.
func (a *graphAuthorAdapter) DraftAgentGraph(ctx context.Context, id, yaml string) (harness.GraphDraftOutcome, error) {
	if a == nil || a.api == nil {
		return harness.GraphDraftOutcome{}, errors.New("agentgraph: manager not configured")
	}

	// Parsed independently of SaveGraph purely to report node_kinds/
	// node_count on the audit kind even when SaveGraph itself refuses
	// before reaching the gate. A parse failure here just means the
	// audit entry's node_kinds/node_count are empty/zero — SaveGraph's
	// own parse error is still what's returned to the caller below.
	var nodeKinds string
	var nodeCount int
	if parsed, perr := coreag.LoadYAML([]byte(yaml)); perr == nil {
		nodeKinds, nodeCount = graphNodeKindsForAudit(parsed)
	}

	saveErr := a.api.SaveGraph(ctx, graphview.GraphSpec{ID: id, YAML: yaml}, "model")

	if saveErr == nil {
		a.emitDraftAttempt(ctx, id, nodeKinds, nodeCount, "permitted", "")
		return harness.GraphDraftOutcome{OK: true, PathHint: "/agentgraph/edit/" + id}, nil
	}

	var vfe *graphview.ValidationFailedError
	if errors.As(saveErr, &vfe) {
		issues := make([]harness.GraphDraftIssue, 0, len(vfe.Issues))
		for _, iss := range vfe.Issues {
			issues = append(issues, harness.GraphDraftIssue{Rule: iss.Rule, Message: iss.Message})
		}
		a.emitDraftAttempt(ctx, id, nodeKinds, nodeCount, "refused", "validation_failed")
		return harness.GraphDraftOutcome{OK: false, Issues: issues}, nil
	}

	// Create-only refusal (E-008). Raised inside saveGraph at the write
	// seam, after the gate — see GraphExistsError for why it is not a
	// caller-side LoadGraph probe any more.
	var gee *graphview.GraphExistsError
	if errors.As(saveErr, &gee) {
		a.emitDraftAttempt(ctx, id, nodeKinds, nodeCount, "refused", "id already exists (create-only)")
		return harness.GraphDraftOutcome{
			OK:           false,
			DeniedReason: fmt.Sprintf("id %q already exists; this tool is create-only and cannot overwrite it", id),
		}, nil
	}

	var pde *cedar.PolicyDeniedError
	if errors.As(saveErr, &pde) {
		a.emitDraftAttempt(ctx, id, nodeKinds, nodeCount, "refused", pde.Decision.Reason)
		return harness.GraphDraftOutcome{OK: false, DeniedReason: pde.Decision.Reason}, nil
	}

	// Anything else (empty yaml, yaml id mismatch, data dir not
	// configured, ...): surface as a generic Go error rather than a
	// classified outcome; still audited as refused.
	a.emitDraftAttempt(ctx, id, nodeKinds, nodeCount, "refused", "error")
	return harness.GraphDraftOutcome{}, saveErr
}

// MaterializeRun implements harness.GraphMaterializer — a straight
// passthrough to graphview.API.MaterializeRun (FR-011). No new
// redaction: materialize.go already reduces arguments to key names,
// results to byte counts, and errors to a closed constant vocabulary.
// SpecProvenance is copied verbatim, including a degraded
// "library_fallback" marker (C-006) — never normalised away.
func (a *graphAuthorAdapter) MaterializeRun(ctx context.Context, runID string) (harness.GraphMaterializeResult, error) {
	if a == nil || a.api == nil {
		return harness.GraphMaterializeResult{}, errors.New("agentgraph: manager not configured")
	}
	spec, err := a.api.MaterializeRun(ctx, runID)
	if err != nil {
		return harness.GraphMaterializeResult{}, err
	}
	return harness.GraphMaterializeResult{
		ID:             spec.ID,
		Name:           spec.Name,
		Scope:          spec.Scope,
		YAML:           spec.YAML,
		SpecProvenance: spec.SpecProvenance,
	}, nil
}

// harnessServer wraps a harness.Server and exposes its concrete type for
// the in-process transport (WP09). Held on rpc.API so future WPs can
// attach the server to the MCP pool without re-constructing it.
type harnessServer struct {
	srv *harness.Server
}

// Server returns the underlying harness.Server.
func (h *harnessServer) Server() *harness.Server { return h.srv }

// buildHarnessManagers constructs a fully-wired harness.Managers from the
// live API instances held on the rpc.API struct. nil components fall back
// gracefully — the handlers nil-check before calling.
func buildHarnessManagers(
	llmAPI llm.LLMConnectorAPI,
	settingsAPI settings.SettingsAPI,
	sessionsAPI sessions.SessionsAPI,
	sessionMgr *session.Manager,
	cat *recipes.MergedCatalog,
	projectsAPI projectsview.ProjectsAPI,
	graphAPI graphview.API,
	auditImpl *audit.API,
) harness.Managers {
	m := harness.Managers{}

	if settingsAPI != nil {
		m.Settings = settingsReaderAdapter{api: settingsAPI}
		// m.SettingsWriter no longer exists — removed by
		// harness-self-attach-01PMHS01 UNIT-8. See the doc comment on
		// harness.Managers' write-side field block for why.
	}
	if llmAPI != nil {
		m.Providers = llmProvidersAdapter{api: llmAPI}
		m.Models = llmModelsAdapter{api: llmAPI}
		m.ProvidersWriter = llmProviderWriterAdapter{api: llmAPI}
	}
	if sessionsAPI != nil {
		m.Sessions = sessionsListAdapter{api: sessionsAPI}
	}
	if sessionMgr != nil {
		m.SessionsWriter = sessionCreatorAdapter{mgr: sessionMgr}
	}
	if cat != nil {
		m.Recipes = recipesAdapter{cat: cat}
	}
	if projectsAPI != nil {
		// harness-self-attach-01PMHS01 UNIT-6: previously unwired — see
		// projectWriterAdapter's doc comment.
		m.ProjectsWriter = projectWriterAdapter{api: projectsAPI}
	}
	if graphAPI != nil {
		// model-authored-graphs-01PMGA01 UNIT-7. One adapter satisfies
		// both the write (GraphAuthorWriter) and read (GraphMaterializer)
		// interfaces — see graphAuthorAdapter's doc comment. auditImpl
		// may be nil (test chassis without a wired audit sink);
		// emitDraftAttempt no-ops on a nil audit field rather than
		// panicking or silently skipping the save/gate/persist path.
		ga := &graphAuthorAdapter{api: graphAPI, audit: auditImpl}
		m.GraphAuthor = ga
		m.GraphMaterializer = ga
	}
	// Managers.Status (StatusReporter) and Managers.RecipesWriter
	// (RecipeWriter) remain unwired: no adapter exists yet for either.
	// harness_read_get_status and harness_write_install_mcp_recipe are
	// therefore still errNotConfigured for every caller — a real,
	// pre-existing gap this unit's attach makes reachable (and so
	// observable) for the first time. Not fixed here: StatusReporter
	// needs a projects count PLUS an "installed MCP servers" count PLUS
	// a policy count, none of which has an obvious single source on
	// this call path yet, and RecipeWriter's idOrConfig contract (a
	// curated-registry id OR a raw config blob, ToolsAPI.InstallRecipe
	// wants id+env+config separately) needs a real design decision, not
	// a guessed adapter. Flagged in the mission report as a follow-up.
	return m
}
