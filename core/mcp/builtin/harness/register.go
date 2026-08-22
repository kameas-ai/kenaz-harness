package harness

import "encoding/json"

// Tool name constants. Cedar policies (WP06) target these literal names.
//
// Read-side tools (harness_read_*) are permitted in any session by
// default. Write-side tools (harness_write_*) are gated to
// kind=onboarding sessions by the default Cedar policies.
const (
	ToolListProviders      = "harness_read_list_providers"
	ToolListMCPRecipes     = "harness_read_list_mcp_recipes"
	ToolListSettings       = "harness_read_list_settings"
	ToolGetStatus          = "harness_read_get_status"
	ToolGetRecommendations = "harness_read_get_onboarding_recommendations"
	ToolListSessions       = "harness_read_list_sessions"
	ToolListModels         = "harness_read_list_models"
	// ToolMaterializeRun (model-authored-graphs-01PMGA01 UNIT-7, FR-011)
	// is a read tool: no Cedar gate, permitted like any other
	// harness_read_* tool (subject to the pre-existing harness-self
	// containment default, which does not restrict reads).
	ToolMaterializeRun = "harness_read_materialize_run"

	ToolAddProvider      = "harness_write_add_provider"
	ToolRemoveProvider   = "harness_write_remove_provider"
	ToolInstallMCPRecipe = "harness_write_install_mcp_recipe"
	// ToolSetSetting ("harness_write_set_setting") was REMOVED by
	// harness-self-attach-01PMHS01 UNIT-8 (G-4,
	// docs/escalation-register-2026-08-19.md Part 9). See handlers.go's
	// doc comment above ProjectWriter for the full removal rationale —
	// the allowlist backing this tool shrank to zero keys, so the tool
	// was unregistered rather than shipped with nothing it could do.
	ToolCreateProject = "harness_write_create_project"
	ToolCreateSession = "harness_write_create_session"
	// ToolDraftAgentGraph (model-authored-graphs-01PMGA01 UNIT-7,
	// FR-001/FR-005/FR-006) is gated TWICE: the pre-existing
	// harness_write_forbid.cedar/harness_write_onboarding.cedar pair
	// (this file's package, WP06) restricts every harness_write_* tool
	// to kind=onboarding sessions by default, and — independently —
	// Manager.saveGraph's own graph.author gate (UNIT-4) requires the
	// FR-006 consent dial before it persists anything, regardless of
	// session kind. Neither substitutes for the other; see spec.md §4.1.
	ToolDraftAgentGraph = "harness_write_draft_agent_graph"
)

// schemaObject is a tiny helper to build a top-level JSON schema object.
func schemaObject(properties string, required ...string) json.RawMessage {
	if len(required) == 0 {
		return json.RawMessage(`{"type":"object","properties":` + properties + `}`)
	}
	req, _ := json.Marshal(required)
	return json.RawMessage(`{"type":"object","properties":` + properties +
		`,"required":` + string(req) + `}`)
}

// RegisterAll installs every harness-self tool onto the server, wiring
// each handler to the matching method on Managers. Callers wire the
// concrete managers via Managers fields before calling RegisterAll.
//
// Returns the same Server for chaining convenience.
func RegisterAll(srv *Server, m Managers) *Server {
	if srv == nil {
		srv = NewServer()
	}

	// Read tools.
	srv.Register(ToolSpec{
		Name:        ToolListProviders,
		Description: "List configured LLM providers (id, kind, name, model). No secrets.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleListProviders,
	})
	srv.Register(ToolSpec{
		Name:        ToolListMCPRecipes,
		Description: "List curated MCP recipe registry entries available for install.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleListRecipes,
	})
	srv.Register(ToolSpec{
		Name:        ToolListSettings,
		Description: "List current harness settings.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleListSettings,
	})
	srv.Register(ToolSpec{
		Name:        ToolGetStatus,
		Description: "Counts of configured providers, MCP servers, sessions, projects, policies.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleGetStatus,
	})
	srv.Register(ToolSpec{
		Name:        ToolGetRecommendations,
		Description: "Curated onboarding next-step recommendations based on current state.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleGetRecommendations,
	})
	srv.Register(ToolSpec{
		Name:        ToolListSessions,
		Description: "List sessions (id, name, kind, timestamps). Never carries message content.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleListSessions,
	})
	srv.Register(ToolSpec{
		Name:        ToolListModels,
		Description: "List available LLM models across all configured providers.",
		InputSchema: schemaObject(`{}`),
		Handler:     m.handleListModels,
	})
	srv.Register(ToolSpec{
		Name: ToolMaterializeRun,
		Description: "Read back a run (including a chat turn you just ran) as a graph: node names, tool-call " +
			"argument KEYS (never values), and outcome shape. Use this to see what a conversation actually did " +
			"before drafting a reusable graph from it with harness_write_draft_agent_graph.",
		InputSchema: schemaObject(`{"run_id":{"type":"string"}}`, "run_id"),
		Handler:     m.handleMaterializeRun,
	})

	// Write tools.
	srv.Register(ToolSpec{
		Name:        ToolAddProvider,
		Description: "Configure a new LLM provider with the given API key.",
		InputSchema: schemaObject(`{
            "kind":{"type":"string","description":"Provider kind: anthropic | openai | openrouter"},
            "name":{"type":"string"},
            "model":{"type":"string"},
            "api_key":{"type":"string","description":"Stored in OS keychain. Redacted in audit."}
        }`, "kind", "api_key"),
		Handler: m.handleAddProvider,
		Redact:  []string{"api_key"},
	})
	srv.Register(ToolSpec{
		Name:        ToolRemoveProvider,
		Description: "Remove a configured provider by id.",
		InputSchema: schemaObject(`{"id":{"type":"string"}}`, "id"),
		Handler:     m.handleRemoveProvider,
	})
	srv.Register(ToolSpec{
		Name:        ToolInstallMCPRecipe,
		Description: "Install an MCP recipe by curated-registry id, or by inline config object.",
		InputSchema: schemaObject(`{
            "id":{"type":"string"},
            "config":{"type":"object"}
        }`, "id"),
		Handler: m.handleInstallRecipe,
	})
	srv.Register(ToolSpec{
		Name:        ToolCreateProject,
		Description: "Create a new project (group sessions under one umbrella).",
		InputSchema: schemaObject(`{
            "name":{"type":"string"},
            "description":{"type":"string"}
        }`, "name"),
		Handler: m.handleCreateProject,
	})
	srv.Register(ToolSpec{
		Name:        ToolCreateSession,
		Description: "Create a new chat session. kind defaults to \"chat\"; use \"onboarding\" for harness-self onboarding sessions.",
		InputSchema: schemaObject(`{
            "name":{"type":"string"},
            "kind":{"type":"string","description":"Session kind: chat (default) | onboarding"}
        }`, "name"),
		Handler: m.handleCreateSession,
	})
	srv.Register(ToolSpec{
		Name: ToolDraftAgentGraph,
		Description: "Draft a new reusable agent graph from YAML and save it as an UNREVIEWED draft in the " +
			"user's graph library. The draft will NOT run — no tool, including this one, can start it, and " +
			"it does not run in parallel or on a schedule — until a human opens it in the graph editor and " +
			"saves it, which marks it reviewed. Fails with a per-rule issue list if the YAML does not validate; " +
			"writes nothing on failure. Create-only: an id that already exists is refused — this tool cannot " +
			"overwrite or delete a graph.",
		InputSchema: schemaObject(`{
            "id":{"type":"string"},
            "yaml":{"type":"string","description":"Agent graph YAML — the same format the graph editor edits and Graph_Validate accepts."}
        }`, "id", "yaml"),
		Handler: m.handleDraftAgentGraph,
	})

	return srv
}
