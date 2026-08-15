package harness

import "encoding/json"

// Tool name constants. Cedar policies (WP06) target these literal names.
//
// Read-side tools (harness_read_*) are permitted in any session by
// default. Write-side tools (harness_write_*) are gated to
// kind=onboarding sessions by the default Cedar policies.
const (
	ToolListProviders        = "harness_read_list_providers"
	ToolListMCPRecipes       = "harness_read_list_mcp_recipes"
	ToolListSettings         = "harness_read_list_settings"
	ToolGetStatus            = "harness_read_get_status"
	ToolGetRecommendations   = "harness_read_get_onboarding_recommendations"
	ToolListSessions         = "harness_read_list_sessions"
	ToolListModels           = "harness_read_list_models"

	ToolAddProvider          = "harness_write_add_provider"
	ToolRemoveProvider       = "harness_write_remove_provider"
	ToolInstallMCPRecipe     = "harness_write_install_mcp_recipe"
	ToolSetSetting           = "harness_write_set_setting"
	ToolCreateProject        = "harness_write_create_project"
	ToolCreateSession        = "harness_write_create_session"
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
		Name:        ToolSetSetting,
		Description: "Update a single allowlisted harness setting.",
		InputSchema: schemaObject(`{
            "key":{"type":"string"},
            "value":{}
        }`, "key", "value"),
		Handler: m.handleSetSetting,
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

	return srv
}
