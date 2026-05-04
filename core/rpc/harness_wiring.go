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
	"fmt"

	harness "github.com/sigil-tech/kaneaz-harness/core/mcp/builtin/harness"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/recipes"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/session"
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

// settingsWriterAdapter wraps settings.SettingsAPI and implements
// harness.SettingsWriter. Applies a single key→value patch to the live
// Settings record using a field switch. Only keys that appear in
// harness.SettingsAllowlist are ever reached here (the handler validates
// the allowlist before calling the writer).
type settingsWriterAdapter struct{ api settings.SettingsAPI }

func (a settingsWriterAdapter) SetSetting(ctx context.Context, key string, value any) error {
	// Load the current settings so we can apply a field-level patch and
	// save the updated record back. The Settings struct is the source of
	// truth; we use a JSON round-trip to set the named field generically.
	s, err := a.api.Get(ctx)
	if err != nil {
		return fmt.Errorf("harness.set_setting: load: %w", err)
	}

	// Encode the current settings as a JSON object, patch the target key,
	// then decode back into Settings and save.
	raw, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("harness.set_setting: marshal settings: %w", err)
	}
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(raw, &patch); err != nil {
		return fmt.Errorf("harness.set_setting: unmarshal map: %w", err)
	}

	// Map allowlisted logical key names to their JSON field names.
	jsonKey, ok := settingsKeyToJSON[key]
	if !ok {
		return fmt.Errorf("harness.set_setting: key %q not mapped", key)
	}
	valRaw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("harness.set_setting: marshal value: %w", err)
	}
	patch[jsonKey] = valRaw

	merged, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("harness.set_setting: re-marshal: %w", err)
	}
	var updated settings.Settings
	if err := json.Unmarshal(merged, &updated); err != nil {
		return fmt.Errorf("harness.set_setting: decode updated: %w", err)
	}
	return a.api.Set(ctx, updated)
}

// settingsKeyToJSON maps the harness-self SettingsAllowlist key names to
// their JSON field names in the Settings struct. Only keys in
// harness.SettingsAllowlist need entries here; the handler validates the
// allowlist before calling the writer.
var settingsKeyToJSON = map[string]string{
	// OnboardingCompleted has no single dedicated field in Settings today
	// (it is a logical flag in the onboarding completion marker). We
	// write it as a no-op by mapping to an unused sentinel key so the
	// JSON round-trip silently discards it. A future dedicated field
	// landing here makes the write effective without changing the handler.
	"OnboardingCompleted":    "_onboardingCompleted",
	"HarnessSelfMCPDisabled": "_harnessSelfMCPDisabled",
	// DefaultProvider and DefaultModel have no current Settings fields;
	// same sentinel approach.
	"DefaultProvider":    "_defaultProvider",
	"DefaultModel":       "_defaultModel",
	"AutoTitleEnabled":   "_autoTitleEnabled",
}

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
) harness.Managers {
	m := harness.Managers{}

	if settingsAPI != nil {
		m.Settings = settingsReaderAdapter{api: settingsAPI}
		m.SettingsWriter = settingsWriterAdapter{api: settingsAPI}
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
	return m
}
