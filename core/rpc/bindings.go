package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/autonomy"
	"github.com/kameas-ai/kenaz-harness/core/toolloop"

	"github.com/kameas-ai/kenaz-harness/core/rpc/views/a2a"
	graphview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agentgraph"
	artifactsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/attachments"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/audit"
	eventlog "github.com/kameas-ai/kenaz-harness/core/event/log"
	agentsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/agents"
	branchesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/branches"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/bundle"
	cedarpolicyview "github.com/kameas-ai/kenaz-harness/core/rpc/views/cedarpolicy"
	compactionview "github.com/kameas-ai/kenaz-harness/core/rpc/views/compaction"
	contextsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/contexts"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/contextview"
	corpusview "github.com/kameas-ai/kenaz-harness/core/rpc/views/corpus"
	dialsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/dials"
	hooksview "github.com/kameas-ai/kenaz-harness/core/rpc/views/hooks"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/llm"
	coremcp "github.com/kameas-ai/kenaz-harness/core/mcp"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/mcp"
	memoryview "github.com/kameas-ai/kenaz-harness/core/rpc/views/memory"
	nodesview "github.com/kameas-ai/kenaz-harness/core/rpc/views/nodes"
	onboardingview "github.com/kameas-ai/kenaz-harness/core/rpc/views/onboarding"
	permissionsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/permissions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/policy"
	projectsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/projects"
	searchview "github.com/kameas-ai/kenaz-harness/core/rpc/views/search"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/sessions"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/shell"
	slashview "github.com/kameas-ai/kenaz-harness/core/rpc/views/slashcmd"
	coreslashcmd "github.com/kameas-ai/kenaz-harness/core/slashcmd"
	llmcap "github.com/kameas-ai/kenaz-harness/core/llm/capabilities"
	"github.com/kameas-ai/kenaz-harness/core/llm/gemini"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/tools"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/trust"
	updateview "github.com/kameas-ai/kenaz-harness/core/rpc/views/update"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/workflow"
	workflowsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/workflows"
	scheduledchatview "github.com/kameas-ai/kenaz-harness/core/rpc/views/scheduledchat"
	storageview "github.com/kameas-ai/kenaz-harness/core/rpc/views/storage"
	elicitview "github.com/kameas-ai/kenaz-harness/core/rpc/views/elicit"
	secretsview "github.com/kameas-ai/kenaz-harness/core/rpc/views/secrets"
	planmodeview "github.com/kameas-ai/kenaz-harness/core/rpc/views/planmode"
	sentryview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sentry"
	catalogview "github.com/kameas-ai/kenaz-harness/core/rpc/views/catalog"
	fleetview "github.com/kameas-ai/kenaz-harness/core/rpc/views/fleet"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/mcp/stdio"
	"github.com/kameas-ai/kenaz-harness/core/rpc/middleware"
	"github.com/kameas-ai/kenaz-harness/core/sentry"
)

// Bindings is the Wails-reflected JS-callable surface. Every method has a
// flat name `<View>_<Operation>` so Wails can reflect it; the typed TS
// client (frontend/src/lib/harnessClient.ts) re-shapes them into nested
// view-scoped client objects.
//
// View and operation names MUST NOT contain underscores per plan §8 R-6;
// `_` is reserved as the view/operation separator. scripts/ci/check-binding-names.sh
// enforces this at PR gate.
//
// IMPORTANT — Wails-bound methods cannot accept context.Context as a
// parameter (Wails cannot serialize it from JS). The app context captured
// via OnStartup is held internally on Bindings; bound methods derive a
// per-call ctx from it (or fall back to context.Background() before
// OnStartup fires).
type Bindings struct {
	api     HarnessAPI
	storeFn func() settings.SettingsStore // injected; nil until WP13 wires
	appCtx  context.Context               // captured via SetContext from OnStartup
}

// NewBindings constructs the Wails-reflected surface.
func NewBindings(api HarnessAPI) *Bindings {
	return &Bindings{api: api}
}

// SetSettingsStore wires the persistence backend used by LoadRoute /
// SaveRoute / LoadTheme / SaveTheme. Safe to call once at construction;
// later calls overwrite. Nil clears the store and reinstates the
// memory-default behaviour for tests.
func (b *Bindings) SetSettingsStore(store settings.SettingsStore) {
	if store == nil {
		b.storeFn = nil
		return
	}
	b.storeFn = func() settings.SettingsStore { return store }
}

// SetContext is invoked from main.go's OnStartup callback to hand the app
// context down to the bound surface. Bound methods use this for their
// per-call context. Safe to call before any bound method runs.
func (b *Bindings) SetContext(ctx context.Context) { b.appCtx = ctx }

// ctx returns the captured app context, or context.Background() if
// SetContext has not run yet (calls before OnStartup).
func (b *Bindings) ctx() context.Context {
	if b.appCtx != nil {
		return b.appCtx
	}
	return context.Background()
}

// ── top-level cross-cutting ────────────────────────────────────────────

func (b *Bindings) ShellStatus() (ShellStatus, error) {
	defer sentry.WrapBinding("ShellStatus")()
	return b.api.ShellStatus(b.ctx())
}

func (b *Bindings) AppInfo() (AppInfo, error) {
	defer sentry.WrapBinding("AppInfo")()
	return b.api.AppInfo(b.ctx())
}

// ── settings (privacy CI invariant #5; WP13 fleshes out persistence) ───

func (b *Bindings) LoadRoute() (string, error) {
	defer sentry.WrapBinding("LoadRoute")()
	if b.storeFn == nil {
		return "/sessions", nil
	}
	return b.storeFn().LoadRoute()
}

func (b *Bindings) SaveRoute(route string) error {
	defer sentry.WrapBinding("SaveRoute")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveRoute(route)
}

func (b *Bindings) LogRouteChange(from, to string) error {
	defer sentry.WrapBinding("LogRouteChange")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().LogRouteChange(from, to)
}

func (b *Bindings) LoadTheme() (string, error) {
	defer sentry.WrapBinding("LoadTheme")()
	if b.storeFn == nil {
		return "system", nil
	}
	return b.storeFn().LoadTheme()
}

func (b *Bindings) SaveTheme(theme string) error {
	defer sentry.WrapBinding("SaveTheme")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveTheme(theme)
}

// ── sessions ───────────────────────────────────────────────────────────

func (b *Bindings) Sessions_List() ([]sessions.Session, error) {
	defer sentry.WrapBinding("Sessions_List")()
	return b.api.Sessions().List(b.ctx())
}
func (b *Bindings) Sessions_Get(id string) (sessions.Session, error) {
	defer sentry.WrapBinding("Sessions_Get")()
	return b.api.Sessions().Get(b.ctx(), id)
}
func (b *Bindings) Sessions_Create(name string) (sessions.Session, error) {
	defer sentry.WrapBinding("Sessions_Create")()
	return b.api.Sessions().Create(b.ctx(), name)
}
func (b *Bindings) Sessions_Rename(id, name string) error {
	defer sentry.WrapBinding("Sessions_Rename")()
	return b.api.Sessions().Rename(b.ctx(), id, name)
}
func (b *Bindings) Sessions_Delete(id string) error {
	defer sentry.WrapBinding("Sessions_Delete")()
	return b.api.Sessions().Delete(b.ctx(), id)
}
func (b *Bindings) Sessions_DeleteWithOptions(id string, opts sessions.DeleteOptions) error {
	defer sentry.WrapBinding("Sessions_DeleteWithOptions")()
	return b.api.Sessions().DeleteWithOptions(b.ctx(), id, opts)
}
func (b *Bindings) Sessions_Reorder(ids []string) error {
	defer sentry.WrapBinding("Sessions_Reorder")()
	return b.api.Sessions().Reorder(b.ctx(), ids)
}
func (b *Bindings) Sessions_StartStream(id string) (string, error) {
	defer sentry.WrapBinding("Sessions_StartStream")()
	return b.api.Sessions().StartStream(b.ctx(), id)
}
func (b *Bindings) Sessions_StopStream(subID string) error {
	defer sentry.WrapBinding("Sessions_StopStream")()
	return b.api.Sessions().StopStream(b.ctx(), subID)
}
func (b *Bindings) Sessions_ListMessages(id string) ([]sessions.Message, error) {
	defer sentry.WrapBinding("Sessions_ListMessages")()
	return b.api.Sessions().ListMessages(b.ctx(), id)
}
func (b *Bindings) Sessions_ListMessagesActive(id string) (sessions.ListMessagesResult, error) {
	defer sentry.WrapBinding("Sessions_ListMessagesActive")()
	return b.api.Sessions().ListMessagesActive(b.ctx(), id)
}
func (b *Bindings) Sessions_ListMessagesAll(id string) (sessions.ListMessagesResult, error) {
	defer sentry.WrapBinding("Sessions_ListMessagesAll")()
	return b.api.Sessions().ListMessagesAll(b.ctx(), id)
}
func (b *Bindings) Sessions_AppendMessage(id, role, content string) (sessions.Message, error) {
	defer sentry.WrapBinding("Sessions_AppendMessage")()
	return b.api.Sessions().AppendMessage(b.ctx(), id, role, content)
}
func (b *Bindings) Sessions_SendMessageWithBlocks(id string, contentBlocks []sessions.ContentBlock) (sessions.Message, error) {
	defer sentry.WrapBinding("Sessions_SendMessageWithBlocks")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze chat dispatch during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return sessions.Message{}, err
	}
	return b.api.Sessions().SendMessageWithBlocks(b.ctx(), id, contentBlocks)
}
func (b *Bindings) Sessions_SaveDraft(id, draft string) error {
	defer sentry.WrapBinding("Sessions_SaveDraft")()
	return b.api.Sessions().SaveDraft(b.ctx(), id, draft)
}
func (b *Bindings) Sessions_LoadDraft(id string) (string, error) {
	defer sentry.WrapBinding("Sessions_LoadDraft")()
	return b.api.Sessions().LoadDraft(b.ctx(), id)
}
func (b *Bindings) Sessions_SetSystemPrompt(id, content, kind string) error {
	defer sentry.WrapBinding("Sessions_SetSystemPrompt")()
	return b.api.Sessions().SetSystemPrompt(b.ctx(), id, content, kind)
}
func (b *Bindings) Sessions_MoveToProject(id, projectID string) error {
	defer sentry.WrapBinding("Sessions_MoveToProject")()
	return b.api.Sessions().MoveToProject(b.ctx(), id, projectID)
}

// Sessions_SuggestTitle triggers a manual auto-title generation for the
// session identified by id. Returns the generated title string on success
// (session-auto-titling-01KQ8TDS WP04).
func (b *Bindings) Sessions_SuggestTitle(id string) (string, error) {
	defer sentry.WrapBinding("Sessions_SuggestTitle")()
	return b.api.Sessions().SuggestTitle(b.ctx(), id)
}

// Sessions_GetUsage returns the cumulative token + cost aggregate for
// the session (token-cost-telemetry-01KQ8TD7 WP03). Returns a zeroed
// aggregate with costSource="unknown" for sessions with no usage data.
func (b *Bindings) Sessions_GetUsage(id string) (sessions.SessionUsage, error) {
	defer sentry.WrapBinding("Sessions_GetUsage")()
	return b.api.Sessions().GetUsage(b.ctx(), id)
}

// Sessions_ClearTitle resets the session's name to "" and auto_titled=0,
// re-enabling future auto-title attempts
// (session-auto-titling-01KQ8TDS WP04).
func (b *Bindings) Sessions_ClearTitle(id string) error {
	defer sentry.WrapBinding("Sessions_ClearTitle")()
	return b.api.Sessions().ClearTitle(b.ctx(), id)
}

// Sessions_StartCapture begins recording an eval capture for sessionID.
// The capture file is written to <DataDir>/eval-captures/<sessionID>.jsonl.
// Idempotent: repeated calls for an active session are no-ops.
// (eval-harness-replay)
func (b *Bindings) Sessions_StartCapture(sessionID string) error {
	defer sentry.WrapBinding("Sessions_StartCapture")()
	return b.api.Sessions_StartCapture(b.ctx(), sessionID)
}

// Sessions_StopCapture finalizes and closes the eval capture for sessionID.
// No-op when no active capture exists. (eval-harness-replay)
func (b *Bindings) Sessions_StopCapture(sessionID string) error {
	defer sentry.WrapBinding("Sessions_StopCapture")()
	return b.api.Sessions_StopCapture(b.ctx(), sessionID)
}

// Sessions_ResumeMessage opens a continuation stream against the partial
// assistant row identified by sessionID + messageID. The row must have
// streaming_failed_at set and streaming_recoverable=true. Returns a
// ResumeMessageResult whose SubscriptionID can be drained via the same
// LLM stream-chunk / closed topics used for normal turns.
// (long-turn-resilience-01KR3PRS WP03 / p0-wiring-fixes-3TVMG0MX WP06)
func (b *Bindings) Sessions_ResumeMessage(sessionID, messageID string) (sessions.ResumeMessageResult, error) {
	return b.api.Sessions().ResumeMessage(b.ctx(), sessionID, messageID)
}

// ── llm ────────────────────────────────────────────────────────────────

func (b *Bindings) LLM_ListProviders() ([]llm.Provider, error) {
	defer sentry.WrapBinding("LLM_ListProviders")()
	return b.api.LLMConnector().ListProviders(b.ctx())
}
func (b *Bindings) LLM_StartStream(profileID, sessionID, modelOverride string) (string, error) {
	defer sentry.WrapBinding("LLM_StartStream")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze LLM dispatch during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return "", err
	}
	return b.api.LLMConnector().StartStream(b.ctx(), profileID, sessionID, modelOverride)
}
func (b *Bindings) LLM_StopStream(subID string) error {
	defer sentry.WrapBinding("LLM_StopStream")()
	return b.api.LLMConnector().StopStream(b.ctx(), subID)
}
func (b *Bindings) LLM_AddProvider(input llm.AddProviderInput) error {
	defer sentry.WrapBinding("LLM_AddProvider")()
	return b.api.LLMConnector().AddProvider(b.ctx(), input)
}
func (b *Bindings) LLM_RemoveProvider(id string) error {
	defer sentry.WrapBinding("LLM_RemoveProvider")()
	return b.api.LLMConnector().RemoveProvider(b.ctx(), id)
}
func (b *Bindings) LLM_UpdateProvider(input llm.AddProviderInput) error {
	defer sentry.WrapBinding("LLM_UpdateProvider")()
	return b.api.LLMConnector().UpdateProvider(b.ctx(), input)
}

// Diag_LogClientEvent appends a structured record from the frontend
// into the harness log file at ~/.kenaz/harness.log. Used by the
// frontend's eventLog.ts to give support engineers a single trail
// when debugging send/stream issues.
func (b *Bindings) Diag_LogClientEvent(level, message string, attrs map[string]any) {
	defer sentry.WrapBinding("Diag_LogClientEvent")()
	logging.LogClientEvent(level, message, attrs)
}

// Diag_LogPath returns the resolved log path so the settings UI can
// surface "logging to ~/.kenaz/harness.log" or the fallback reason.
func (b *Bindings) Diag_LogPath() string {
	defer sentry.WrapBinding("Diag_LogPath")()
	return logging.PathOrError()
}
func (b *Bindings) LLM_TestProvider(id string) (llm.TestResult, error) {
	defer sentry.WrapBinding("LLM_TestProvider")()
	return b.api.LLMConnector().TestProvider(b.ctx(), id)
}
func (b *Bindings) LLM_ListModels(kind, plaintextApiKey string) ([]llm.ModelInfo, error) {
	defer sentry.WrapBinding("LLM_ListModels")()
	return b.api.LLMConnector().ListModels(b.ctx(), kind, plaintextApiKey)
}

// LLM_ResolveConfirm completes a pending confirm-each tool call
// (WP05). The frontend modal calls this with the request id surfaced
// on the `llm:tool-confirm-request` topic and one of the four
// canonical decisions ("allow" | "deny" | "always_allow" |
// "always_deny"). The toolloop goroutine waiting on the request id
// unblocks and continues / blocks accordingly.
func (b *Bindings) LLM_ResolveConfirm(requestID, decision string) error {
	defer sentry.WrapBinding("LLM_ResolveConfirm")()
	return b.api.LLMConnector().ResolveConfirm(b.ctx(), requestID, decision)
}

// LLM_UpdateProviderCredential writes a new plaintext API key for
// profileID to the OS keychain and zeroes the in-memory buffer before
// returning (credential-store-01KQ8TDD WP05 / FR-007). The frontend
// ONLY calls this when the user has typed a new key value — the
// "leave blank to keep current" flow is preserved.
func (b *Bindings) LLM_UpdateProviderCredential(profileID, plaintext string) error {
	defer sentry.WrapBinding("LLM_UpdateProviderCredential")()
	return b.api.LLMConnector().UpdateProviderCredential(b.ctx(), profileID, plaintext)
}

// LLM_GetAttachmentLimits returns the resolved per-provider attachment
// capability limits for the given provider kind + model. The frontend uses
// these to replace hard-coded byte caps in the attachment tray
// (multimodal-io-01KQ8TDF WP04 / FR-007).
func (b *Bindings) LLM_GetAttachmentLimits(provider, model string) (llm.AttachmentLimitsView, error) {
	defer sentry.WrapBinding("LLM_GetAttachmentLimits")()
	return b.api.LLMConnector().GetAttachmentLimits(b.ctx(), provider, model)
}

// LLM_TestAndRotateKey validates plaintextApiKey against the provider's
// /models endpoint and, on success, writes it to the keychain and emits
// a KindProviderKeyRotated audit event. source should be "inline-toast"
// or "manual". The plaintext is consumed and zeroed before returning.
// (provider-keychain-rotation-01KQ8TD9 WP04)
func (b *Bindings) LLM_TestAndRotateKey(profileID, plaintextApiKey, source string) (llm.RotationResult, error) {
	defer sentry.WrapBinding("LLM_TestAndRotateKey")()
	return b.api.LLMConnector().TestAndRotateKey(b.ctx(), profileID, plaintextApiKey, source)
}

// LLM_ResumeAfterKeyRotation drives a fresh kernel run for the paused
// chat turn identified by resumeToken (the profileID returned by
// TestAndRotateKey). Safe to call when no paused turn exists.
// (provider-keychain-rotation-01KQ8TD9 WP04)
func (b *Bindings) LLM_ResumeAfterKeyRotation(resumeToken string) error {
	defer sentry.WrapBinding("LLM_ResumeAfterKeyRotation")()
	return b.api.LLMConnector().ResumeAfterKeyRotation(b.ctx(), resumeToken)
}

// LLM_TestProviderKey validates a plaintext API key against the given provider
// kind and resource host without writing to the keychain. Used by the
// AddProvider form to show connection status before the user clicks Submit.
// The plaintext key is consumed and zeroed before returning.
// (azure-openai-adapter-01KQ8VMZ WP03)
func (b *Bindings) LLM_TestProviderKey(kind, host, plaintextKey string) (llm.ProviderKeyTestResult, error) {
	defer sentry.WrapBinding("LLM_TestProviderKey")()
	return b.api.LLMConnector().TestProviderKey(b.ctx(), kind, host, plaintextKey)
}

// LLM_ListCustomTemplates returns the built-in custom endpoint template
// summaries from the adapter's embedded registry. Returns an empty slice
// when the adapter is not registered (HARNESS_CUSTOM_OPENAI=0).
// (custom-openai-compatible-endpoint-01KQ8VN0 WP06)
func (b *Bindings) LLM_ListCustomTemplates() ([]llm.CustomTemplateSummary, error) {
	defer sentry.WrapBinding("LLM_ListCustomTemplates")()
	return b.api.LLMConnector().ListCustomTemplates(b.ctx())
}

// LLM_RecognizeTemplate looks up the best-matching template for rawURL
// via glob matching. Returns {matched:false} when no template matches.
// (custom-openai-compatible-endpoint-01KQ8VN0 WP06)
func (b *Bindings) LLM_RecognizeTemplate(rawURL string) (llm.RecognizeTemplateResult, error) {
	defer sentry.WrapBinding("LLM_RecognizeTemplate")()
	return b.api.LLMConnector().RecognizeTemplate(b.ctx(), rawURL)
}

// LLM_ProbeCustomEndpoint runs the three-step capability probe against a
// custom OpenAI-compatible endpoint. The plaintext API key is consumed and
// zeroed server-side before returning.
// (custom-openai-compatible-endpoint-01KQ8VN0 WP06)
func (b *Bindings) LLM_ProbeCustomEndpoint(in llm.ProbeCustomEndpointInput) (llm.ProbeCustomEndpointResult, error) {
	defer sentry.WrapBinding("LLM_ProbeCustomEndpoint")()
	return b.api.LLMConnector().ProbeCustomEndpoint(b.ctx(), in)
}

// ── Fallback chain CRUD (model-fallback-routing-01NDFSEX04 WP04) ─────────────

// LLM_ListFallbackChains returns all known fallback chain summaries.
func (b *Bindings) LLM_ListFallbackChains() ([]llm.FallbackChainSummary, error) {
	defer sentry.WrapBinding("LLM_ListFallbackChains")()
	return b.api.LLMConnector().ListFallbackChains(b.ctx())
}

// LLM_LoadChain returns the full chain definition for the given id.
func (b *Bindings) LLM_LoadChain(id string) (llm.FallbackChainView, error) {
	defer sentry.WrapBinding("LLM_LoadChain")()
	return b.api.LLMConnector().LoadChain(b.ctx(), id)
}

// LLM_SaveChain persists a chain (create or overwrite).
func (b *Bindings) LLM_SaveChain(chain llm.FallbackChainView) error {
	defer sentry.WrapBinding("LLM_SaveChain")()
	return b.api.LLMConnector().SaveChain(b.ctx(), chain)
}

// LLM_DeleteChain removes the chain with the given id from FSStore.
func (b *Bindings) LLM_DeleteChain(id string) error {
	defer sentry.WrapBinding("LLM_DeleteChain")()
	return b.api.LLMConnector().DeleteChain(b.ctx(), id)
}

// ── mcp ────────────────────────────────────────────────────────────────

func (b *Bindings) MCP_ListServers() ([]mcp.Server, error) {
	defer sentry.WrapBinding("MCP_ListServers")()
	return b.api.MCP().ListServers(b.ctx())
}
func (b *Bindings) MCP_StartStream(id string) (string, error) {
	defer sentry.WrapBinding("MCP_StartStream")()
	return b.api.MCP().StartStream(b.ctx(), id)
}
func (b *Bindings) MCP_StopStream(subID string) error {
	defer sentry.WrapBinding("MCP_StopStream")()
	return b.api.MCP().StopStream(b.ctx(), subID)
}

// MCP_TestRecipe runs a one-shot connection test against the recipe
// identified by recipeID (WP07 of mission mcp-server-install-01KQ8TDP).
// env and config override the recipe's stored values; both are nil-safe.
// The result is always non-nil; transport-level failures are reflected in
// TestResult.OK=false / TestResult.Error rather than the Go error return.
// The Go error return is set only for pre-flight failures (recipe not
// found, catalog not wired).
func (b *Bindings) MCP_TestRecipe(recipeID string, env map[string]string, config map[string]any) (coremcp.TestResult, error) {
	defer sentry.WrapBinding("MCP_TestRecipe")()
	return b.api.MCP().TestRecipe(b.ctx(), recipeID, env, config)
}

// MCP_ImportClaudeDesktopConfig is the user-facing RPC behind the
// "Paste config" tab of the Add-MCP-Server modal (mission
// mcp-server-install-01KQ8TDP, WP08). It accepts the verbatim
// clipboard JSON, runs the translator, and — when dryRun=false —
// writes per-entry artefacts under
// <DataDir>/mcp/recipes/_imports/<id>.{yaml,json}. dryRun=true is
// pure-read: the modal renders the report's per-entry rows before
// the user commits.
func (b *Bindings) MCP_ImportClaudeDesktopConfig(req mcp.ImportRequest) (mcp.ImportResponse, error) {
	defer sentry.WrapBinding("MCP_ImportClaudeDesktopConfig")()
	importer := b.api.MCPImport()
	if importer == nil {
		return mcp.ImportResponse{}, mcp.ErrImportNotConfigured
	}
	return importer.ImportClaudeDesktopConfig(b.ctx(), req)
}

// MCP_HealthSnapshot returns the current health status for every installed
// MCP recipe as a map of recipe-id → HealthEntry.
// (mcp-server-health-ui-01KQ8TD6 WP01)
func (b *Bindings) MCP_HealthSnapshot() (map[string]mcp.HealthEntry, error) {
	defer sentry.WrapBinding("MCP_HealthSnapshot")()
	return b.api.MCP().HealthSnapshot(b.ctx())
}

// MCP_SubscribeHealthChanges registers a broker subscription for
// mcp:health-changed events. Returns a subscription id for use with
// MCP_StopStream.
// (mcp-server-health-ui-01KQ8TD6 WP02)
func (b *Bindings) MCP_SubscribeHealthChanges() (string, error) {
	defer sentry.WrapBinding("MCP_SubscribeHealthChanges")()
	return b.api.MCP().SubscribeHealthChanges(b.ctx())
}

// ── a2a ────────────────────────────────────────────────────────────────

func (b *Bindings) A2A_ListCards() ([]a2a.Card, error) {
	defer sentry.WrapBinding("A2A_ListCards")()
	return b.api.A2A().ListCards(b.ctx())
}
func (b *Bindings) A2A_StartStream() (string, error) {
	defer sentry.WrapBinding("A2A_StartStream")()
	return b.api.A2A().StartStream(b.ctx())
}
func (b *Bindings) A2A_StopStream(subID string) error {
	defer sentry.WrapBinding("A2A_StopStream")()
	return b.api.A2A().StopStream(b.ctx(), subID)
}

// ── workflow ───────────────────────────────────────────────────────────

func (b *Bindings) Workflow_ListJobs() ([]workflow.Job, error) {
	defer sentry.WrapBinding("Workflow_ListJobs")()
	return b.api.Workflow().ListJobs(b.ctx())
}
func (b *Bindings) Workflow_StartStream() (string, error) {
	defer sentry.WrapBinding("Workflow_StartStream")()
	return b.api.Workflow().StartStream(b.ctx())
}
func (b *Bindings) Workflow_StopStream(subID string) error {
	defer sentry.WrapBinding("Workflow_StopStream")()
	return b.api.Workflow().StopStream(b.ctx(), subID)
}

// ── trust ──────────────────────────────────────────────────────────────

func (b *Bindings) Trust_ListSecretReferences() ([]trust.SecretReference, error) {
	defer sentry.WrapBinding("Trust_ListSecretReferences")()
	return b.api.Trust().ListSecretReferences(b.ctx())
}
func (b *Bindings) Trust_GetSecretReference(id string) (trust.SecretReference, error) {
	defer sentry.WrapBinding("Trust_GetSecretReference")()
	return b.api.Trust().GetSecretReference(b.ctx(), id)
}

// ── context ────────────────────────────────────────────────────────────

func (b *Bindings) Context_List() ([]contextview.ContextEntry, error) {
	defer sentry.WrapBinding("Context_List")()
	return b.api.Context().List(b.ctx())
}
func (b *Bindings) Context_StartStream() (string, error) {
	defer sentry.WrapBinding("Context_StartStream")()
	return b.api.Context().StartStream(b.ctx())
}
func (b *Bindings) Context_StopStream(subID string) error {
	defer sentry.WrapBinding("Context_StopStream")()
	return b.api.Context().StopStream(b.ctx(), subID)
}

// ── contexts (library content pool — WP01) ────────────────────────────

func (b *Bindings) Contexts_List() (contextsview.Node, error) {
	defer sentry.WrapBinding("Contexts_List")()
	return b.api.Contexts().List(b.ctx())
}
func (b *Bindings) Contexts_ListAll() (contextsview.Node, error) {
	defer sentry.WrapBinding("Contexts_ListAll")()
	return b.api.Contexts().ListAll(b.ctx())
}
func (b *Bindings) Contexts_Get(path string) (string, error) {
	defer sentry.WrapBinding("Contexts_Get")()
	return b.api.Contexts().Get(b.ctx(), path)
}
func (b *Bindings) Contexts_Save(path, content string) error {
	defer sentry.WrapBinding("Contexts_Save")()
	return b.api.Contexts().Save(b.ctx(), path, content)
}
func (b *Bindings) Contexts_CreateFolder(path string) error {
	defer sentry.WrapBinding("Contexts_CreateFolder")()
	return b.api.Contexts().CreateFolder(b.ctx(), path)
}
func (b *Bindings) Contexts_Rename(oldPath, newPath string) error {
	defer sentry.WrapBinding("Contexts_Rename")()
	return b.api.Contexts().Rename(b.ctx(), oldPath, newPath)
}
func (b *Bindings) Contexts_Delete(path string) error {
	defer sentry.WrapBinding("Contexts_Delete")()
	return b.api.Contexts().Delete(b.ctx(), path)
}
func (b *Bindings) Contexts_RecentlyApplied(limit int) ([]string, error) {
	defer sentry.WrapBinding("Contexts_RecentlyApplied")()
	return b.api.Contexts().RecentlyApplied(b.ctx(), limit)
}
func (b *Bindings) Contexts_RootPath() (string, error) {
	defer sentry.WrapBinding("Contexts_RootPath")()
	return b.api.Contexts().RootPath(b.ctx())
}

// Contexts_ContextPublish publishes a local context entry to the fleet
// context graph. Requires the ContextGraphSyncer to be wired (via
// API.WithSyncer) and CapSharedTeamGraph capability.
// (fleet-context-graph-sync-01NDFSEX17 WP06)
func (b *Bindings) Contexts_ContextPublish(req contextsview.ContextPublishRequest) (contextsview.ContextPublishResult, error) {
	defer sentry.WrapBinding("Contexts_ContextPublish")()
	return b.api.Contexts().Context_Publish(b.ctx(), req)
}

// Contexts_ContextPromote elevates a team_shared context entry to org_shared
// on the fleet server. Requires CapSharedTeamGraph.
// (fleet-context-graph-sync-01NDFSEX17 WP06)
func (b *Bindings) Contexts_ContextPromote(nodeID string) (contextsview.ContextPromoteResult, error) {
	defer sentry.WrapBinding("Contexts_ContextPromote")()
	return b.api.Contexts().Context_Promote(b.ctx(), nodeID)
}

// Contexts_ContextSyncStatus returns a snapshot of the fleet context-graph
// syncer state: cursor, last pull time, error strings, pull count, and team
// cap flag. Always returns a non-error result.
// (fleet-context-graph-sync-01NDFSEX17 WP06)
func (b *Bindings) Contexts_ContextSyncStatus() (contextsview.ContextSyncStatusView, error) {
	defer sentry.WrapBinding("Contexts_ContextSyncStatus")()
	return b.api.Contexts().Context_SyncStatus(b.ctx())
}

// Contexts_ContextSearch runs a server-side search over the caller's visible
// context graph (title+body match in v0). teamID is optional; limit <= 0 lets
// the server pick a default. Returns an empty result when fleet is disabled /
// unentitled. (harness-fleet-sync-activation-01NSYNC01 gap #5)
func (b *Bindings) Contexts_ContextSearch(query, teamID string, limit int) ([]contextsview.ContextSearchHitView, error) {
	defer sentry.WrapBinding("Contexts_ContextSearch")()
	return b.api.Contexts().Context_Search(b.ctx(), query, teamID, limit)
}

// Contexts_ContextExport streams the caller's visible context graph as NDJSON
// ("jsonl", default) or a gzipped tarball ("tarball"). teamID optionally
// narrows to a single team. The payload is base64-encoded in the view.
// (harness-fleet-sync-activation-01NSYNC01 gap #5)
func (b *Bindings) Contexts_ContextExport(teamID, format string) (contextsview.ContextExportView, error) {
	defer sentry.WrapBinding("Contexts_ContextExport")()
	return b.api.Contexts().Context_Export(b.ctx(), teamID, format)
}

// Contexts_AttachModule creates a context-module attachment for the given
// directory. scopeKind is one of "global", "project", "session"; scopeID is
// the project or session id (empty for global); dirPath is the library-
// relative path of a module directory (containing context.md / agents.md).
//
// The returned ModuleAttachment contains the resolved content (root file +
// always:-listed files) and is persisted as a context_attachments row so
// it participates in the existing global→project→session injection order.
// On-demand files in the module are available via kenaz__read_context_file.
//
// (unified-context-artifacts-01NCTXU01)
func (b *Bindings) Contexts_AttachModule(scopeKind, scopeID, dirPath string) (contextsview.ModuleAttachment, error) {
	defer sentry.WrapBinding("Contexts_AttachModule")()
	return b.api.Contexts().AttachModule(b.ctx(), scopeKind, scopeID, dirPath)
}

// ── bundle ─────────────────────────────────────────────────────────────

func (b *Bindings) Bundle_List() ([]bundle.Bundle, error) {
	defer sentry.WrapBinding("Bundle_List")()
	return b.api.Bundle().List(b.ctx())
}
func (b *Bindings) Bundle_Get(id string) (bundle.Bundle, error) {
	defer sentry.WrapBinding("Bundle_Get")()
	return b.api.Bundle().Get(b.ctx(), id)
}
func (b *Bindings) Bundle_Install(req bundle.InstallRequest) (bundle.Bundle, error) {
	defer sentry.WrapBinding("Bundle_Install")()
	return b.api.Bundle().Install(b.ctx(), req)
}
func (b *Bindings) Bundle_Remove(id string) error {
	defer sentry.WrapBinding("Bundle_Remove")()
	return b.api.Bundle().Remove(b.ctx(), id)
}

// ── policy ─────────────────────────────────────────────────────────────

func (b *Bindings) Policy_Explain(input map[string]any) (policy.Denial, error) {
	defer sentry.WrapBinding("Policy_Explain")()
	return b.api.Policy().Explain(b.ctx(), input)
}
func (b *Bindings) Policy_StartStream() (string, error) {
	defer sentry.WrapBinding("Policy_StartStream")()
	return b.api.Policy().StartStream(b.ctx())
}
func (b *Bindings) Policy_StopStream(subID string) error {
	defer sentry.WrapBinding("Policy_StopStream")()
	return b.api.Policy().StopStream(b.ctx(), subID)
}

// ── cedar policy panel (cedar-credential-policy-01KQ8TDE, WP02) ────────

// CedarPolicy_ListPolicies returns the per-file parse status for every
// .cedar source the engine has loaded. The frontend's Policy panel
// renders this list on mount and after a successful reload.
func (b *Bindings) CedarPolicy_ListPolicies() ([]cedarpolicyview.PolicyFile, error) {
	defer sentry.WrapBinding("CedarPolicy_ListPolicies")()
	return b.api.CedarPolicy().ListPolicies(b.ctx())
}

// CedarPolicy_ReloadPolicies re-walks <DataDir>/policy/ and rebuilds
// the active policy bundle. Per-file parse failures are reported via
// the next CedarPolicy_ListPolicies call; errors do not abort reload.
func (b *Bindings) CedarPolicy_ReloadPolicies() error {
	defer sentry.WrapBinding("CedarPolicy_ReloadPolicies")()
	return b.api.CedarPolicy().ReloadPolicies(b.ctx())
}

// CedarPolicy_RecentDecisions returns up to limit most-recent gate
// decisions, newest first. Used by the audit panel.
func (b *Bindings) CedarPolicy_RecentDecisions(limit int) ([]cedarpolicyview.Decision, error) {
	defer sentry.WrapBinding("CedarPolicy_RecentDecisions")()
	return b.api.CedarPolicy().RecentDecisions(b.ctx(), limit)
}

// ── permissions (cedar-credential-policy-01KQ8TDE, WP02) ───────────────

// Permissions_Resolve routes a modal decision (allow_once / allow_always
// / deny) back into the cedar prompt registry. requestID came in on one
// of the four `<family>:permission-pending` broker topics.
func (b *Bindings) Permissions_Resolve(requestID string, decision string) error {
	defer sentry.WrapBinding("Permissions_Resolve")()
	return b.api.Permissions().Resolve(b.ctx(), requestID, permissionsview.Decision(decision))
}

// Permissions_ListGrants enumerates accumulated grants — both persisted
// `<family>_allow_*.cedar` files in <DataDir>/policy/ and the per-process
// transient (Allow-once) cache. When family is non-empty ("bash" / "fs"
// / "cred" / "tool") only grants of that family are returned; empty
// string returns all four families.
func (b *Bindings) Permissions_ListGrants(family string) ([]permissionsview.Grant, error) {
	defer sentry.WrapBinding("Permissions_ListGrants")()
	return b.api.Permissions().ListGrants(b.ctx(), family)
}

// Permissions_RevokeGrant removes a grant. Persisted grants delete the
// underlying .cedar file and trigger an engine reload; transient grants
// drop the in-memory cache entry.
func (b *Bindings) Permissions_RevokeGrant(grantID string) error {
	defer sentry.WrapBinding("Permissions_RevokeGrant")()
	return b.api.Permissions().RevokeGrant(b.ctx(), grantID)
}

// Permissions_ListPending returns in-flight pending prompts. The
// frontend uses this to reconcile its modal queue on app start / after
// a hot reload.
func (b *Bindings) Permissions_ListPending() ([]permissionsview.PendingRequest, error) {
	defer sentry.WrapBinding("Permissions_ListPending")()
	return b.api.Permissions().ListPending(b.ctx())
}

// ── audit ──────────────────────────────────────────────────────────────

func (b *Bindings) Audit_ListEntries(filter audit.Filter) ([]audit.Entry, error) {
	defer sentry.WrapBinding("Audit_ListEntries")()
	return b.api.Audit().ListEntries(b.ctx(), filter)
}
func (b *Bindings) Audit_VerifyEntry(id string) (bool, error) {
	defer sentry.WrapBinding("Audit_VerifyEntry")()
	return b.api.Audit().VerifyEntry(b.ctx(), id)
}
func (b *Bindings) Audit_VerifyChain(fromID, toID string) (audit.VerifyChainResult, error) {
	defer sentry.WrapBinding("Audit_VerifyChain")()
	return b.api.Audit().VerifyChain(b.ctx(), fromID, toID)
}
func (b *Bindings) Audit_Filter(query eventlog.FilterQuery) ([]audit.Entry, error) {
	defer sentry.WrapBinding("Audit_Filter")()
	return b.api.Audit().Filter(b.ctx(), query)
}
func (b *Bindings) Audit_ListSavedQueries() ([]eventlog.SavedQuery, error) {
	defer sentry.WrapBinding("Audit_ListSavedQueries")()
	return b.api.Audit().ListSavedQueries(b.ctx())
}
func (b *Bindings) Audit_SaveQuery(q eventlog.SavedQuery) error {
	defer sentry.WrapBinding("Audit_SaveQuery")()
	return b.api.Audit().SaveQuery(b.ctx(), q)
}
func (b *Bindings) Audit_DeleteQuery(id string) error {
	defer sentry.WrapBinding("Audit_DeleteQuery")()
	return b.api.Audit().DeleteQuery(b.ctx(), id)
}
func (b *Bindings) Audit_Export(opts eventlog.ExportOptions) (string, error) {
	defer sentry.WrapBinding("Audit_Export")()
	return b.api.Audit().Export(b.ctx(), opts)
}
func (b *Bindings) Audit_BulkPurge(eventIDs []string) error {
	defer sentry.WrapBinding("Audit_BulkPurge")()
	return b.api.Audit().BulkPurge(b.ctx(), eventIDs)
}
func (b *Bindings) Audit_StartStream(filter audit.Filter) (string, error) {
	defer sentry.WrapBinding("Audit_StartStream")()
	return b.api.Audit().StartStream(b.ctx(), filter)
}
func (b *Bindings) Audit_StopStream(subID string) error {
	defer sentry.WrapBinding("Audit_StopStream")()
	return b.api.Audit().StopStream(b.ctx(), subID)
}

// ── settings ───────────────────────────────────────────────────────────

func (b *Bindings) Settings_Get() (settings.Settings, error) {
	defer sentry.WrapBinding("Settings_Get")()
	return b.api.Settings().Get(b.ctx())
}
func (b *Bindings) Settings_Set(s settings.Settings) error {
	defer sentry.WrapBinding("Settings_Set")()
	return b.api.Settings().Set(b.ctx(), s)
}

// Settings_GetMemory exposes the long-term-memory opt-in independently
// of the full settings round-trip. The frontend toggle reads / writes
// this so the privacy default (off) stays the cheap path.
func (b *Bindings) Settings_GetMemory() (bool, error) {
	defer sentry.WrapBinding("Settings_GetMemory")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadMemory()
}

func (b *Bindings) Settings_SetMemory(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetMemory")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveMemory(enabled)
}

// Settings_GetWebSearch exposes the local-first web-search built-in
// opt-in flag (default false). Surfaced as a toggle row in the Tools
// panel; toolloop reads this on every Run boundary so toggling takes
// effect on the next chat.
func (b *Bindings) Settings_GetWebSearch() (bool, error) {
	defer sentry.WrapBinding("Settings_GetWebSearch")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadWebSearch()
}

// Settings_SetWebSearch persists the web-search built-in opt-in flag.
func (b *Bindings) Settings_SetWebSearch(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetWebSearch")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveWebSearch(enabled)
}

// Settings_GetWebFetchEnabled exposes the kenaz__web_fetch built-in
// opt-in flag (default false). Surfaced as a toggle row in the Tools
// panel; toolloop reads this on every Run boundary so toggling takes
// effect on the next chat.
func (b *Bindings) Settings_GetWebFetchEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetWebFetchEnabled")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadWebFetchEnabled()
}

// Settings_SetWebFetchEnabled persists the kenaz__web_fetch built-in opt-in flag.
func (b *Bindings) Settings_SetWebFetchEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetWebFetchEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveWebFetchEnabled(enabled)
}

// Settings_GetBash exposes the local-first bash built-in opt-in flag
// (default false). The bash tool is also gated by the per-command
// allowlist regardless of this toggle.
func (b *Bindings) Settings_GetBash() (bool, error) {
	defer sentry.WrapBinding("Settings_GetBash")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadBash()
}

// Settings_SetBash persists the bash built-in opt-in flag.
func (b *Bindings) Settings_SetBash(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetBash")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveBash(enabled)
}

// Settings_GetSaveArtifactEnabled exposes the kenaz__save_artifact
// built-in opt-in flag. Default true (on) — saving deliverables is a
// low-risk primitive that should work on a fresh install. Surfaced as a
// toggle row in the Tools panel; toolloop reads this on every Run
// boundary so toggling takes effect on the next chat.
func (b *Bindings) Settings_GetSaveArtifactEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetSaveArtifactEnabled")()
	if b.storeFn == nil {
		return true, nil
	}
	return b.storeFn().LoadSaveArtifactEnabled()
}

// Settings_SetSaveArtifactEnabled persists the save_artifact built-in
// opt-in flag.
func (b *Bindings) Settings_SetSaveArtifactEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetSaveArtifactEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveSaveArtifactEnabled(enabled)
}

// Settings_GetFSRequestAccessEnabled exposes the
// kenaz__request_filesystem_access built-in opt-in flag (default true).
// The toolloop EnabledFilter reads this on every Run boundary so toggling
// takes effect on the next chat.
func (b *Bindings) Settings_GetFSRequestAccessEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetFSRequestAccessEnabled")()
	if b.storeFn == nil {
		return true, nil
	}
	return b.storeFn().LoadFSRequestAccessEnabled()
}

// Settings_SetFSRequestAccessEnabled persists the
// request_filesystem_access built-in opt-in flag.
func (b *Bindings) Settings_SetFSRequestAccessEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetFSRequestAccessEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveFSRequestAccessEnabled(enabled)
}

// ── Builtin filesystem tool dials (builtin-filesystem-tools-01KR3N4P) ────
//
// Settings_GetFSReadEnabled exposes the read-family builtin filesystem
// tool opt-in (default false — tools off until the user enables them from
// the Tools panel). The toolloop's EnabledFilter consults this on every
// Run boundary so a toggle takes effect on the next chat turn.
func (b *Bindings) Settings_GetFSReadEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetFSReadEnabled")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadFSReadEnabled()
}

// Settings_SetFSReadEnabled persists the read-family filesystem tool opt-in.
func (b *Bindings) Settings_SetFSReadEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetFSReadEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveFSReadEnabled(enabled)
}

// Settings_GetFSWriteEnabled exposes the write-family builtin filesystem
// tool opt-in (default false — write tools off until the user enables them).
func (b *Bindings) Settings_GetFSWriteEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetFSWriteEnabled")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadFSWriteEnabled()
}

// Settings_SetFSWriteEnabled persists the write-family filesystem tool opt-in.
func (b *Bindings) Settings_SetFSWriteEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetFSWriteEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveFSWriteEnabled(enabled)
}

// ── Todo tool dial (builtin-tools-search-and-elicitation-01KZNP3D WP07) ──

// Settings_GetTodoEnabled exposes the kenaz__todo_write builtin opt-in
// (default false — tool off until the user enables it from the Tools panel).
// The toolloop's EnabledFilter consults this on every Run boundary so a
// toggle takes effect on the next chat turn.
func (b *Bindings) Settings_GetTodoEnabled() (bool, error) {
	defer sentry.WrapBinding("Settings_GetTodoEnabled")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadTodoEnabled()
}

// Settings_SetTodoEnabled persists the kenaz__todo_write opt-in flag.
func (b *Bindings) Settings_SetTodoEnabled(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetTodoEnabled")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveTodoEnabled(enabled)
}

// Settings_GetMaxAgentTurns exposes the chat-graph LoopNode iteration
// cap (default DefaultMaxAgentTurns = 25). The chassis (chat runner)
// reads the effective value on every chat run start so the dial takes
// effect on the next user turn. The wire returns the persisted raw
// value: zero on the wire means "use the spec default" — frontend
// callers can render the placeholder accordingly.
func (b *Bindings) Settings_GetMaxAgentTurns() (int, error) {
	defer sentry.WrapBinding("Settings_GetMaxAgentTurns")()
	if b.storeFn == nil {
		return 0, nil
	}
	return b.storeFn().LoadMaxAgentTurns()
}

// Settings_SetMaxAgentTurns persists the chat-graph LoopNode
// iteration cap. Zero clears the override (resets to the spec
// default); negatives are normalised to zero by the store.
func (b *Bindings) Settings_SetMaxAgentTurns(turns int) error {
	defer sentry.WrapBinding("Settings_SetMaxAgentTurns")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveMaxAgentTurns(turns)
}

// Settings_GetMonthlyCostNotifyUSD returns the per-month spend
// notification threshold dial (token-cost-telemetry-01KQ8TD7 WP06).
// Zero (the default) means the scheduler is disabled — the frontend
// renders the placeholder accordingly.
func (b *Bindings) Settings_GetMonthlyCostNotifyUSD() (float64, error) {
	defer sentry.WrapBinding("Settings_GetMonthlyCostNotifyUSD")()
	if b.storeFn == nil {
		return 0, nil
	}
	return b.storeFn().LoadMonthlyCostNotifyUSD()
}

// Settings_SetMonthlyCostNotifyUSD persists the per-month spend
// notification threshold dial. Zero disables the scheduler;
// negatives are normalised to zero; values above the documented cap
// (settings.MaxMonthlyCostNotifyUSD = $10,000) are rejected with the
// typed ErrInvalidMonthlyCostNotifyUSD so the UI can render specific
// copy.
func (b *Bindings) Settings_SetMonthlyCostNotifyUSD(usd float64) error {
	defer sentry.WrapBinding("Settings_SetMonthlyCostNotifyUSD")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveMonthlyCostNotifyUSD(usd)
}

// ── WP08 permission dials ──────────────────────────────────────────

// Settings_GetPermissionMode returns the global permission posture.
// Default "normal" when unset.
func (b *Bindings) Settings_GetPermissionMode() (string, error) {
	defer sentry.WrapBinding("Settings_GetPermissionMode")()
	if b.storeFn == nil {
		return "normal", nil
	}
	return b.storeFn().LoadPermissionMode()
}

// Settings_SetPermissionMode persists the global permission posture.
// Valid values: "strict", "normal", "permissive". Switching to
// "permissive" is gated by a confirm dialog on the frontend side.
func (b *Bindings) Settings_SetPermissionMode(mode string) error {
	defer sentry.WrapBinding("Settings_SetPermissionMode")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SavePermissionMode(mode)
}

// Settings_GetPermissionCacheDangerousOps returns the dangerous-ops
// override flag (default false).
func (b *Bindings) Settings_GetPermissionCacheDangerousOps() (bool, error) {
	defer sentry.WrapBinding("Settings_GetPermissionCacheDangerousOps")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadPermissionCacheDangerousOps()
}

// Settings_SetPermissionCacheDangerousOps persists the dangerous-ops
// override flag. Enabling requires a confirm dialog on the frontend.
func (b *Bindings) Settings_SetPermissionCacheDangerousOps(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetPermissionCacheDangerousOps")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SavePermissionCacheDangerousOps(enabled)
}

// Settings_GetBashAllowlistMigrated returns the WP10 migration
// marker. The UI reads this to suppress the one-time migration toast
// after WP10's first-boot migration has run.
func (b *Bindings) Settings_GetBashAllowlistMigrated() (bool, error) {
	defer sentry.WrapBinding("Settings_GetBashAllowlistMigrated")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadBashAllowlistMigrated()
}

// Settings_SetBashAllowlistMigrated marks the WP10 migration as done.
func (b *Bindings) Settings_SetBashAllowlistMigrated(migrated bool) error {
	defer sentry.WrapBinding("Settings_SetBashAllowlistMigrated")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveBashAllowlistMigrated(migrated)
}

// Settings_GetPermissionsMigrationToastShown returns the one-time toast
// shown marker.
func (b *Bindings) Settings_GetPermissionsMigrationToastShown() (bool, error) {
	defer sentry.WrapBinding("Settings_GetPermissionsMigrationToastShown")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadPermissionsMigrationToastShown()
}

// Settings_SetPermissionsMigrationToastShown marks the migration toast
// as having been shown so it never appears again.
func (b *Bindings) Settings_SetPermissionsMigrationToastShown(shown bool) error {
	defer sentry.WrapBinding("Settings_SetPermissionsMigrationToastShown")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SavePermissionsMigrationToastShown(shown)
}

// Settings_GetCedarStrictCredentialMode exposes the WP05
// credential-gate strictness dial (default false / lenient). When
// false, NotApplicable Cedar outcomes allow credential access. When
// true (strict), NotApplicable for non-mcp_spawn purposes is treated
// as deny — the credstore becomes fail-closed for unmatched patterns.
// The UI dial for this setting is a Settings-panel follow-up; the
// binding is wired now so the frontend can surface it without a
// re-deploy.
func (b *Bindings) Settings_GetCedarStrictCredentialMode() (bool, error) {
	defer sentry.WrapBinding("Settings_GetCedarStrictCredentialMode")()
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadCedarStrictCredentialMode()
}

// Settings_SetCedarStrictCredentialMode persists the credential-gate
// strictness flag. The credstore.Store reads this via its StrictMode
// callback on every Use call; changes take effect on the next
// credential use without restarting the harness.
func (b *Bindings) Settings_SetCedarStrictCredentialMode(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetCedarStrictCredentialMode")()
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveCedarStrictCredentialMode(enabled)
}

// Settings_GetShortcuts returns the full user-override keyboard shortcut
// map. Missing settings file returns an empty map (no error).
// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7)
func (b *Bindings) Settings_GetShortcuts() (map[string]string, error) {
	defer sentry.WrapBinding("Settings_GetShortcuts")()
	if b.storeFn == nil {
		return map[string]string{}, nil
	}
	store := b.storeFn()
	if ss, ok := store.(interface {
		LoadShortcuts() (map[string]string, error)
	}); ok {
		return ss.LoadShortcuts()
	}
	// Fallback: full Settings round-trip.
	s, err := store.LoadAll()
	if err != nil {
		return nil, err
	}
	if s.KeyboardShortcuts == nil {
		return map[string]string{}, nil
	}
	return s.KeyboardShortcuts, nil
}

// Settings_SetShortcut persists a single shortcut override. An empty
// binding value clears the override for that id (resets to registry
// default). Emits KindShortcutOverridden audit event on success.
func (b *Bindings) Settings_SetShortcut(id, binding string) error {
	defer sentry.WrapBinding("Settings_SetShortcut")()
	if b.storeFn == nil {
		return nil
	}
	store := b.storeFn()
	if ss, ok := store.(interface {
		LoadShortcuts() (map[string]string, error)
		SaveShortcuts(map[string]string) error
	}); ok {
		m, err := ss.LoadShortcuts()
		if err != nil {
			return err
		}
		m[id] = binding
		return ss.SaveShortcuts(m)
	}
	// Fallback: full round-trip.
	s, err := store.LoadAll()
	if err != nil {
		return err
	}
	if s.KeyboardShortcuts == nil {
		s.KeyboardShortcuts = make(map[string]string)
	}
	s.KeyboardShortcuts[id] = binding
	return store.SaveAll(s)
}

// Settings_SetShortcuts atomically replaces the full keyboard shortcut
// overrides map. Used by the settings panel's reset-all and batch-save
// flows. Emits one KindShortcutOverridden audit event per changed entry.
func (b *Bindings) Settings_SetShortcuts(m map[string]string) error {
	defer sentry.WrapBinding("Settings_SetShortcuts")()
	if b.storeFn == nil {
		return nil
	}
	store := b.storeFn()
	if ss, ok := store.(interface {
		SaveShortcuts(map[string]string) error
	}); ok {
		return ss.SaveShortcuts(m)
	}
	// Fallback: full round-trip.
	s, err := store.LoadAll()
	if err != nil {
		return err
	}
	s.KeyboardShortcuts = m
	return store.SaveAll(s)
}

// Settings_GetMCPAutoRestart returns whether MCP servers should auto-restart
// after two consecutive ping failures. Default true.
// (mcp-server-health-ui-01KQ8TD6 WP06)
func (b *Bindings) Settings_GetMCPAutoRestart() (bool, error) {
	defer sentry.WrapBinding("Settings_GetMCPAutoRestart")()
	return b.api.Settings().GetMCPAutoRestart(b.ctx())
}

// Settings_SetMCPAutoRestart persists the MCP auto-restart dial.
func (b *Bindings) Settings_SetMCPAutoRestart(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetMCPAutoRestart")()
	return b.api.Settings().SetMCPAutoRestart(b.ctx(), enabled)
}

// Settings_GetAutoTitleEnabled returns whether session auto-titling is on.
// Default true on a fresh install.
// (p0-wiring-fixes-3TVMG0MX WP05)
func (b *Bindings) Settings_GetAutoTitleEnabled() (bool, error) {
	return b.api.Settings().GetAutoTitleEnabled(b.ctx())
}

// Settings_SetAutoTitleEnabled persists the auto-title feature toggle.
func (b *Bindings) Settings_SetAutoTitleEnabled(enabled bool) error {
	return b.api.Settings().SetAutoTitleEnabled(b.ctx(), enabled)
}

// EmbedderConfigResult is the wire shape returned by
// Settings_GetEmbedderConfig so the frontend can bind both fields in
// a single RPC call.
type EmbedderConfigResult struct {
	ProfileID     string `json:"profileId"`
	ModelOverride string `json:"modelOverride"`
}

// Settings_GetEmbedderConfig returns the persisted embedder provider
// selection and optional model override.
// (v0.5.2 universal-embedder fix)
func (b *Bindings) Settings_GetEmbedderConfig() (EmbedderConfigResult, error) {
	defer sentry.WrapBinding("Settings_GetEmbedderConfig")()
	id, model, err := b.api.Settings().GetEmbedderConfig(b.ctx())
	return EmbedderConfigResult{ProfileID: id, ModelOverride: model}, err
}

// Settings_SetEmbedderConfig persists the embedder provider selection
// and optional model override.  Empty strings reset to auto-pick /
// per-Kind-default behaviour.
func (b *Bindings) Settings_SetEmbedderConfig(profileID, modelOverride string) error {
	defer sentry.WrapBinding("Settings_SetEmbedderConfig")()
	return b.api.Settings().SetEmbedderConfig(b.ctx(), profileID, modelOverride)
}

// ArtifactPreviewConfig is the wire shape returned by
// Settings_GetArtifactPreview so the frontend can bind all fields in a
// single RPC call (artifact-preview-binary-rendering-01KQ8TD5 WP07).
type ArtifactPreviewConfig struct {
	Enabled   bool  `json:"enabled"`
	MaxBytes  int64 `json:"maxBytes"`
	TimeoutMs int64 `json:"timeoutMs"`
}

// Settings_GetArtifactPreview returns the runtime artifact-preview feature
// config: enabled flag, byte cap, and timeout.
// (artifact-preview-binary-rendering-01KQ8TD5 WP07)
func (b *Bindings) Settings_GetArtifactPreview() (ArtifactPreviewConfig, error) {
	defer sentry.WrapBinding("Settings_GetArtifactPreview")()
	enabled, maxBytes, timeoutMs, err := b.api.Settings().GetArtifactPreview(b.ctx())
	return ArtifactPreviewConfig{
		Enabled:   enabled,
		MaxBytes:  maxBytes,
		TimeoutMs: timeoutMs,
	}, err
}


// Settings_GetShowPerMessageTokenMeter returns whether the per-message
// token meter chip is enabled (default false — chip hidden by default to
// keep the chat uncluttered). (per-message-token-meter-01KR3PQR)
func (b *Bindings) Settings_GetShowPerMessageTokenMeter() (bool, error) {
	defer sentry.WrapBinding("Settings_GetShowPerMessageTokenMeter")()
	if b.storeFn == nil {
		return false, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return false, err
	}
	return s.ShowPerMessageTokenMeter, nil
}

// Settings_SetShowPerMessageTokenMeter persists the per-message token
// meter chip toggle. Changes take effect immediately on the next render
// without restarting the harness.
func (b *Bindings) Settings_SetShowPerMessageTokenMeter(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetShowPerMessageTokenMeter")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	s.ShowPerMessageTokenMeter = enabled
	return b.storeFn().SaveAll(s)
}

// ── WP08 — multimodal input feature flag ──────────────────────────────

// Settings_GetMultimodalInput returns whether the multimodal input feature
// (image + PDF attachments) is enabled. Default true on a fresh install.
// When false, ChatInput.vue hides the paperclip button and drop overlay.
// Note: the HARNESS_MULTIMODAL_IN env flag can independently disable this.
// (multimodal-io-01KQ8TDF WP08 / FR-022 / FR-023)
func (b *Bindings) Settings_GetMultimodalInput() (bool, error) {
	defer sentry.WrapBinding("Settings_GetMultimodalInput")()
	if b.storeFn == nil {
		return true, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return true, err
	}
	return s.MultimodalInputEnabled(), nil
}

// Settings_SetMultimodalInput persists the multimodal input feature flag.
// When false, ChatInput.vue hides the paperclip button, the drop overlay,
// and the paste handler becomes a no-op for image/PDF clipboard items.
// (multimodal-io-01KQ8TDF WP08 / FR-023 / FR-024)
func (b *Bindings) Settings_SetMultimodalInput(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetMultimodalInput")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	// Inverted storage: Disabled = !enabled.
	s.MultimodalInputDisabled = !enabled
	return b.storeFn().SaveAll(s)
}

// ── Multimodal output capture dials (multimodal-io-extended-01KQ8TD2 WP06) ──

// Settings_GetAutoCaptureGeneratedImages returns whether model-generated images
// are automatically captured into the artifact store. Default true on a fresh
// install (zero-value AutoCaptureGeneratedImagesDisabled → enabled).
// (multimodal-io-extended-01KQ8TD2 WP06)
func (b *Bindings) Settings_GetAutoCaptureGeneratedImages() (bool, error) {
	defer sentry.WrapBinding("Settings_GetAutoCaptureGeneratedImages")()
	if b.storeFn == nil {
		return true, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return true, err
	}
	return s.AutoCaptureGeneratedImages(), nil
}

// Settings_SetAutoCaptureGeneratedImages persists the auto-capture flag.
// When false, model-generated images are streamed to the UI but not written
// to the artifact store.
// (multimodal-io-extended-01KQ8TD2 WP06)
func (b *Bindings) Settings_SetAutoCaptureGeneratedImages(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetAutoCaptureGeneratedImages")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	// Inverted storage: Disabled = !enabled.
	s.AutoCaptureGeneratedImagesDisabled = !enabled
	return b.storeFn().SaveAll(s)
}

// Settings_GetMaxGeneratedImageBytes returns the per-image byte cap for the
// auto-capture pipeline. Returns the effective value (default 20 MiB when the
// persisted value is zero or negative).
// (multimodal-io-extended-01KQ8TD2 WP06)
func (b *Bindings) Settings_GetMaxGeneratedImageBytes() (int64, error) {
	defer sentry.WrapBinding("Settings_GetMaxGeneratedImageBytes")()
	if b.storeFn == nil {
		return settings.DefaultMaxGeneratedImageBytes, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return settings.DefaultMaxGeneratedImageBytes, err
	}
	return s.EffectiveMaxGeneratedImageBytes(), nil
}

// Settings_SetMaxGeneratedImageBytes persists the per-image byte cap. Zero
// resets to the spec default (DefaultMaxGeneratedImageBytes = 20 MiB).
// Negative values are clamped to zero before save.
// (multimodal-io-extended-01KQ8TD2 WP06)
func (b *Bindings) Settings_SetMaxGeneratedImageBytes(bytes int64) error {
	defer sentry.WrapBinding("Settings_SetMaxGeneratedImageBytes")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	if bytes < 0 {
		bytes = 0
	}
	s.MaxGeneratedImageBytes = bytes
	return b.storeFn().SaveAll(s)
}

// ── key-rotation settings (provider-keychain-rotation-01KQ8TD9 WP07) ──

// Settings_GetAutoResumeOnKeyRotation returns whether the harness should
// automatically redrive the paused chat turn after the user rotates an API
// key. Default true on a fresh install (zero-value Disabled → enabled).
// Hidden in the Settings UI when AppInfo.keychainRotationEnabled = false.
// (provider-keychain-rotation-01KQ8TD9 WP07)
func (b *Bindings) Settings_GetAutoResumeOnKeyRotation() (bool, error) {
	defer sentry.WrapBinding("Settings_GetAutoResumeOnKeyRotation")()
	if b.storeFn == nil {
		return true, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return true, err
	}
	return s.EffectiveAutoResumeOnKeyRotation(), nil
}

// Settings_SetAutoResumeOnKeyRotation persists the auto-resume-on-key-rotation
// dial. When false, TestAndRotateKey returns an empty AutoResumeToken and
// the user must manually resend the failed turn.
// (provider-keychain-rotation-01KQ8TD9 WP07)
func (b *Bindings) Settings_SetAutoResumeOnKeyRotation(enabled bool) error {
	defer sentry.WrapBinding("Settings_SetAutoResumeOnKeyRotation")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	s.AutoResumeOnKeyRotationDisabled = !enabled
	return b.storeFn().SaveAll(s)
}

// ── audit settings (audit-log-enhancement-01KX5R8F WP07) ────────────────────

func (b *Bindings) Settings_GetAuditSettings() (settings.AuditSettings, error) {
	defer sentry.WrapBinding("Settings_GetAuditSettings")()
	return b.api.Settings().GetAuditSettings(b.ctx())
}
func (b *Bindings) Settings_SetAuditSettings(s settings.AuditSettings) error {
	defer sentry.WrapBinding("Settings_SetAuditSettings")()
	return b.api.Settings().SetAuditSettings(b.ctx(), s)
}

// ── fleet auth (fleet-auth-foundation-01NDFSEX08 WP05) ──────────────────────

// Settings_FleetSignIn kicks off the PKCE loopback auth flow. Opens the
// system browser, waits for callback, exchanges code, enrolls with fleet.
func (b *Bindings) Settings_FleetSignIn() (settings.FleetIdentity, error) {
	defer sentry.WrapBinding("Settings_FleetSignIn")()
	return b.api.Settings().FleetSignIn(b.ctx())
}

// Settings_FleetSignOut clears tokens and identity cache.
func (b *Bindings) Settings_FleetSignOut() error {
	defer sentry.WrapBinding("Settings_FleetSignOut")()
	return b.api.Settings().FleetSignOut(b.ctx())
}

// Settings_FleetSignedIn reports whether valid tokens exist.
func (b *Bindings) Settings_FleetSignedIn() (bool, error) {
	defer sentry.WrapBinding("Settings_FleetSignedIn")()
	return b.api.Settings().FleetSignedIn(b.ctx())
}

// Settings_FleetRefreshIdentity re-enrolls with fleet and updates the cache.
func (b *Bindings) Settings_FleetRefreshIdentity() (settings.FleetIdentity, error) {
	defer sentry.WrapBinding("Settings_FleetRefreshIdentity")()
	return b.api.Settings().FleetRefreshIdentity(b.ctx())
}

// Settings_FleetProfile returns the active env profile info for UI rendering.
// Does not expose ClientID, APIAudience, or any secret fields.
func (b *Bindings) Settings_FleetProfile() (settings.FleetProfileInfo, error) {
	defer sentry.WrapBinding("Settings_FleetProfile")()
	return b.api.Settings().FleetProfile(b.ctx())
}

// ── fleet capabilities (fleet-capability-surface-01NDFSEX09 WP11) ───────────

// Settings_FleetCapabilities returns the in-memory capability snapshot.
// Returns an empty CapabilitiesView when signed out or fleet is disabled.
func (b *Bindings) Settings_FleetCapabilities() (settings.CapabilitiesView, error) {
	defer sentry.WrapBinding("Settings_FleetCapabilities")()
	return b.api.Settings().FleetCapabilities(b.ctx())
}

// Settings_FleetRefreshCapabilities forces an immediate capability fetch from
// the fleet server. Returns the updated snapshot on success.
func (b *Bindings) Settings_FleetRefreshCapabilities() (settings.CapabilitiesView, error) {
	defer sentry.WrapBinding("Settings_FleetRefreshCapabilities")()
	return b.api.Settings().FleetRefreshCapabilities(b.ctx())
}

// Settings_FleetConfigPullStatus returns the current config-pull poller state:
// last applied bundle_id, last applied timestamp, last error, source, and
// the checksum of the last-seen bundle (for 304 Not Modified gating).
// (fleet-config-pull-01NDFSEX10 WP02)
func (b *Bindings) Settings_FleetConfigPullStatus() (settings.FleetConfigPullStatusView, error) {
	defer sentry.WrapBinding("Settings_FleetConfigPullStatus")()
	return b.api.Settings().FleetConfigPullStatus(b.ctx())
}

// Settings_FleetLockdownStatus returns the current emergency lockdown state.
// Called by the frontend LockdownBanner on mount and after receiving a
// fleet:lockdown:changed event. (fleet-emergency-lockdown-01NDFSEX12 WP02)
func (b *Bindings) Settings_FleetLockdownStatus() (settings.LockdownStatusView, error) {
	defer sentry.WrapBinding("Settings_FleetLockdownStatus")()
	return b.api.Settings().FleetLockdownStatus(b.ctx())
}

// Settings_FleetTelemetryOptIns returns the per-class telemetry opt-in set
// from the fleet store (the source of truth, replacing local-only JSON).
// (harness-fleet-sync-activation-01NSYNC01 gap #4)
func (b *Bindings) Settings_FleetTelemetryOptIns() ([]settings.TelemetryOptInView, error) {
	defer sentry.WrapBinding("Settings_FleetTelemetryOptIns")()
	return b.api.Settings().FleetTelemetryOptIns(b.ctx())
}

// Settings_FleetSetTelemetryOptIn flips a single telemetry class opt-in in the
// fleet store (source becomes 'user_self') and refreshes the local cache.
// (harness-fleet-sync-activation-01NSYNC01 gap #4)
func (b *Bindings) Settings_FleetSetTelemetryOptIn(class string, optedIn bool) error {
	defer sentry.WrapBinding("Settings_FleetSetTelemetryOptIn")()
	return b.api.Settings().FleetSetTelemetryOptIn(b.ctx(), class, optedIn)
}

// ── memory ─────────────────────────────────────────────────────────────

func (b *Bindings) Memory_ListChunks(filter memoryview.ListFilter) ([]memoryview.Chunk, error) {
	defer sentry.WrapBinding("Memory_ListChunks")()
	return b.api.Memory().ListChunks(b.ctx(), filter)
}

func (b *Bindings) Memory_RememberMessage(sessionID, messageID, scope string) (string, error) {
	defer sentry.WrapBinding("Memory_RememberMessage")()
	return b.api.Memory().RememberMessage(b.ctx(), sessionID, messageID, scope)
}

func (b *Bindings) Memory_PromoteScope(chunkID, newScopeKind, newScopeID string) (string, error) {
	defer sentry.WrapBinding("Memory_PromoteScope")()
	return b.api.Memory().PromoteScope(b.ctx(), chunkID, newScopeKind, newScopeID)
}

func (b *Bindings) Memory_Forget(id string) error {
	defer sentry.WrapBinding("Memory_Forget")()
	return b.api.Memory().Forget(b.ctx(), id)
}

func (b *Bindings) Memory_Pin(id string, pinned bool) error {
	defer sentry.WrapBinding("Memory_Pin")()
	return b.api.Memory().Pin(b.ctx(), id, pinned)
}

func (b *Bindings) Memory_JournalTail(scope string, sinceSeq int64, limit int) ([]memoryview.JournalEntry, error) {
	defer sentry.WrapBinding("Memory_JournalTail")()
	return b.api.Memory().JournalTail(b.ctx(), scope, sinceSeq, limit)
}

func (b *Bindings) Memory_PrunePreview(scope string) (memoryview.PrunePreview, error) {
	defer sentry.WrapBinding("Memory_PrunePreview")()
	return b.api.Memory().PrunePreview(b.ctx(), scope)
}

func (b *Bindings) Memory_RunPruneNow(scope string) (memoryview.PruneStats, error) {
	defer sentry.WrapBinding("Memory_RunPruneNow")()
	return b.api.Memory().RunPruneNow(b.ctx(), scope)
}

// Memory_HealthSnapshot returns an at-a-glance health snapshot for the
// §2.4 memory health dashboard (memory-inspection-ui-01KX5R8E §2.4).
func (b *Bindings) Memory_HealthSnapshot() (memoryview.HealthSnapshot, error) {
	defer sentry.WrapBinding("Memory_HealthSnapshot")()
	return b.api.Memory().HealthSnapshot(b.ctx())
}

// Memory_TestEmbedder probes the wired embedder against "hello world"
// and returns the resulting vector dimensions. Used by the §2.4
// "Test embedder" button.
func (b *Bindings) Memory_TestEmbedder() (int, error) {
	defer sentry.WrapBinding("Memory_TestEmbedder")()
	return b.api.Memory().TestEmbedder(b.ctx())
}

// Memory_CaptureRate returns a snapshot of the live memory capture
// velocity and embedder health for the §2.7 LegendBar pill.
func (b *Bindings) Memory_CaptureRate() (memoryview.CaptureRateSnapshot, error) {
	defer sentry.WrapBinding("Memory_CaptureRate")()
	return b.api.Memory().CaptureRate(b.ctx())
}

// Memory_EmbedderEligibility inspects the configured provider profiles and
// returns eligibility metadata without constructing an Embedder. The
// frontend's Settings → Memory banner calls this on mount to determine
// whether to surface the "no memory provider" affordance.
func (b *Bindings) Memory_EmbedderEligibility() (memoryview.EmbedderEligibility, error) {
	defer sentry.WrapBinding("Memory_EmbedderEligibility")()
	return b.api.Memory().EmbedderEligibility(b.ctx())
}

// Memory_MarkImportant sets the user-pin counter for a chunk, making it
// a candidate for long-term scope promotion (memory-narrative-layer WP07).
func (b *Bindings) Memory_MarkImportant(chunkID string, pinned bool) error {
	defer sentry.WrapBinding("Memory_MarkImportant")()
	return b.api.Memory().MarkImportant(b.ctx(), chunkID, pinned)
}

// Memory_NarrativeFailedCount returns the number of narrative synthesis
// jobs that have exhausted all retry attempts (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeFailedCount() (int, error) {
	defer sentry.WrapBinding("Memory_NarrativeFailedCount")()
	return b.api.Memory().NarrativeFailedCount(b.ctx())
}

// Memory_NarrativeFailedList returns the narrative synthesis jobs that
// have exhausted all retry attempts (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeFailedList() ([]memoryview.NarrativeJobStatus, error) {
	defer sentry.WrapBinding("Memory_NarrativeFailedList")()
	return b.api.Memory().NarrativeFailedList(b.ctx())
}

// Memory_RetryFailedNarrative resets a failed narrative job so the
// Promoter worker will retry it (memory-narrative-layer WP07).
func (b *Bindings) Memory_RetryFailedNarrative(jobID string) error {
	defer sentry.WrapBinding("Memory_RetryFailedNarrative")()
	return b.api.Memory().RetryFailedNarrative(b.ctx(), jobID)
}

// Memory_NarrativeMetricsForChunk returns the retrieval/citation/pin
// counters and computed promotion score for a single chunk
// (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeMetricsForChunk(chunkID string) (memoryview.NarrativeMetrics, error) {
	defer sentry.WrapBinding("Memory_NarrativeMetricsForChunk")()
	return b.api.Memory().NarrativeMetricsForChunk(b.ctx(), chunkID)
}

// ── Memory capstone (memory-inspection-ui-01KX5R8E) ───────────────────

// Memory_LastRetrieval returns the most recent retrieval report for the
// given session (§2.1 active-session retrieval inspector, FR-001).
func (b *Bindings) Memory_LastRetrieval(sessionID string) (memoryview.RetrievalReport, error) {
	defer sentry.WrapBinding("Memory_LastRetrieval")()
	return b.api.Memory().LastRetrieval(b.ctx(), sessionID)
}

// Memory_EmbeddingProbe embeds the given query and returns up to limit
// scored chunks ranked by cosine similarity (§2.2 embedding inspector,
// FR-003). limit is capped at 50 server-side.
func (b *Bindings) Memory_EmbeddingProbe(query string, limit int) ([]memoryview.ScoredChunk, error) {
	defer sentry.WrapBinding("Memory_EmbeddingProbe")()
	return b.api.Memory().EmbeddingProbe(b.ctx(), query, limit)
}

// Memory_ResummarizeChunk re-runs narrative synthesis on the chunk with
// the given ID (§2.3, FR-004). Rate-limited to one call per chunk per 60s.
func (b *Bindings) Memory_ResummarizeChunk(chunkID string) (memoryview.Chunk, error) {
	defer sentry.WrapBinding("Memory_ResummarizeChunk")()
	return b.api.Memory().ResummarizeChunk(b.ctx(), chunkID)
}

// Memory_GetChunkProvenance returns the full audit chain for a chunk
// (§2.6 provenance drawer, FR-007).
func (b *Bindings) Memory_GetChunkProvenance(chunkID string) (memoryview.ChunkProvenance, error) {
	defer sentry.WrapBinding("Memory_GetChunkProvenance")()
	return b.api.Memory().GetChunkProvenance(b.ctx(), chunkID)
}

// ── dials (Bundle E WP17) ──────────────────────────────────────────────

func (b *Bindings) Dials_Get(key dialsview.ScopeKey) (dialsview.DialConfig, error) {
	defer sentry.WrapBinding("Dials_Get")()
	return b.api.Dials().GetDials(b.ctx(), key)
}

func (b *Bindings) Dials_Set(key dialsview.ScopeKey, cfg dialsview.DialConfig) error {
	defer sentry.WrapBinding("Dials_Set")()
	return b.api.Dials().SetDials(b.ctx(), key, cfg)
}

func (b *Bindings) Dials_GetEffective(projectID, sessionID, graphID, runID string) (dialsview.EffectiveDials, error) {
	defer sentry.WrapBinding("Dials_GetEffective")()
	return b.api.Dials().GetEffective(b.ctx(), projectID, sessionID, graphID, runID)
}

func (b *Bindings) Dials_BumpAndResume(runID string, delta dialsview.DialDelta) error {
	defer sentry.WrapBinding("Dials_BumpAndResume")()
	return b.api.Dials().BumpAndResume(b.ctx(), runID, delta)
}

// ── projects ───────────────────────────────────────────────────────────

func (b *Bindings) Projects_List() ([]projectsview.Project, error) {
	defer sentry.WrapBinding("Projects_List")()
	return b.api.Projects().List(b.ctx())
}
func (b *Bindings) Projects_Get(id string) (projectsview.Project, error) {
	defer sentry.WrapBinding("Projects_Get")()
	return b.api.Projects().Get(b.ctx(), id)
}
func (b *Bindings) Projects_Create(name, description string) (projectsview.Project, error) {
	defer sentry.WrapBinding("Projects_Create")()
	return b.api.Projects().Create(b.ctx(), name, description)
}
func (b *Bindings) Projects_Rename(id, name string) error {
	defer sentry.WrapBinding("Projects_Rename")()
	return b.api.Projects().Rename(b.ctx(), id, name)
}
func (b *Bindings) Projects_UpdateDescription(id, description string) error {
	defer sentry.WrapBinding("Projects_UpdateDescription")()
	return b.api.Projects().UpdateDescription(b.ctx(), id, description)
}
func (b *Bindings) Projects_Delete(id string, deleteSessions bool) error {
	defer sentry.WrapBinding("Projects_Delete")()
	return b.api.Projects().Delete(b.ctx(), id, deleteSessions)
}
func (b *Bindings) Projects_AddSession(projectID, sessionID string) error {
	defer sentry.WrapBinding("Projects_AddSession")()
	return b.api.Projects().AddSession(b.ctx(), projectID, sessionID)
}
func (b *Bindings) Projects_RemoveSession(sessionID string) error {
	defer sentry.WrapBinding("Projects_RemoveSession")()
	return b.api.Projects().RemoveSession(b.ctx(), sessionID)
}
func (b *Bindings) Projects_ListSessions(projectID string) ([]projectsview.Session, error) {
	defer sentry.WrapBinding("Projects_ListSessions")()
	return b.api.Projects().ListSessions(b.ctx(), projectID)
}

// ── artifacts (artifacts-storage WP02) ────────────────────────────────

func (b *Bindings) Artifacts_List(filter artifactsview.ArtifactFilter) ([]artifactsview.Artifact, error) {
	defer sentry.WrapBinding("Artifacts_List")()
	return b.api.Artifacts().List(b.ctx(), filter)
}
func (b *Bindings) Artifacts_Get(id string) (artifactsview.ArtifactWithBytes, error) {
	defer sentry.WrapBinding("Artifacts_Get")()
	return b.api.Artifacts().Get(b.ctx(), id)
}
func (b *Bindings) Artifacts_Promote(id, newScopeKind, newScopeID string) (artifactsview.Artifact, error) {
	defer sentry.WrapBinding("Artifacts_Promote")()
	return b.api.Artifacts().Promote(b.ctx(), id, newScopeKind, newScopeID)
}
func (b *Bindings) Artifacts_Delete(id string) error {
	defer sentry.WrapBinding("Artifacts_Delete")()
	return b.api.Artifacts().Delete(b.ctx(), id)
}

// Sessions_SaveAsArtifact is the user-facing manual-pin entry point
// (FR-006). The RPC routes through the artifacts view so the
// frontend's "Save as artifact" right-click action calls
// Sessions_SaveAsArtifact and receives the persisted artifact row.
func (b *Bindings) Sessions_SaveAsArtifact(sessionID, messageID, title string, sourceRangeStart, sourceRangeEnd int) (artifactsview.Artifact, error) {
	defer sentry.WrapBinding("Sessions_SaveAsArtifact")()
	return b.api.Artifacts().SaveFromMessage(b.ctx(), sessionID, messageID, title, sourceRangeStart, sourceRangeEnd)
}

// ── attachments (context_attachments — WP03) ──────────────────────────

func (b *Bindings) Attachments_List(scopeKind, scopeID string) ([]attachmentsview.Attachment, error) {
	defer sentry.WrapBinding("Attachments_List")()
	return b.api.Attachments().List(b.ctx(), scopeKind, scopeID)
}
func (b *Bindings) Attachments_ListResolved(sessionID string) ([]attachmentsview.Attachment, error) {
	defer sentry.WrapBinding("Attachments_ListResolved")()
	return b.api.Attachments().ListResolved(b.ctx(), sessionID)
}
func (b *Bindings) Attachments_Add(in attachmentsview.AddInput) (attachmentsview.Attachment, error) {
	defer sentry.WrapBinding("Attachments_Add")()
	return b.api.Attachments().Add(b.ctx(), in)
}
func (b *Bindings) Attachments_AddMedia(in attachmentsview.AddMediaInput) (attachmentsview.Attachment, error) {
	defer sentry.WrapBinding("Attachments_AddMedia")()
	return b.api.Attachments().AddMedia(b.ctx(), in)
}
func (b *Bindings) Attachments_Remove(id string) error {
	defer sentry.WrapBinding("Attachments_Remove")()
	return b.api.Attachments().Remove(b.ctx(), id)
}
func (b *Bindings) Attachments_Reorder(scopeKind, scopeID string, idsInOrder []string) error {
	defer sentry.WrapBinding("Attachments_Reorder")()
	return b.api.Attachments().Reorder(b.ctx(), scopeKind, scopeID, idsInOrder)
}
func (b *Bindings) Attachments_Refresh(id string) (attachmentsview.Attachment, error) {
	defer sentry.WrapBinding("Attachments_Refresh")()
	return b.api.Attachments().Refresh(b.ctx(), id)
}

// ── hooks ──────────────────────────────────────────────────────────────

func (b *Bindings) Hooks_List() ([]hooksview.Hook, error) {
	defer sentry.WrapBinding("Hooks_List")()
	return b.api.Hooks().List(b.ctx())
}
func (b *Bindings) Hooks_Get(id string) (hooksview.Hook, error) {
	defer sentry.WrapBinding("Hooks_Get")()
	return b.api.Hooks().Get(b.ctx(), id)
}
func (b *Bindings) Hooks_Add(in hooksview.HookInput) (hooksview.Hook, error) {
	defer sentry.WrapBinding("Hooks_Add")()
	return b.api.Hooks().Add(b.ctx(), in)
}
func (b *Bindings) Hooks_Update(in hooksview.HookInput) error {
	defer sentry.WrapBinding("Hooks_Update")()
	return b.api.Hooks().Update(b.ctx(), in)
}
func (b *Bindings) Hooks_Remove(id string) error {
	defer sentry.WrapBinding("Hooks_Remove")()
	return b.api.Hooks().Remove(b.ctx(), id)
}
func (b *Bindings) Hooks_AvailableBuiltins() ([]hooksview.BuiltinDescriptor, error) {
	defer sentry.WrapBinding("Hooks_AvailableBuiltins")()
	return b.api.Hooks().AvailableBuiltins(b.ctx())
}
func (b *Bindings) Hooks_InstallStarterMemory() error {
	defer sentry.WrapBinding("Hooks_InstallStarterMemory")()
	return b.api.Hooks().InstallStarterMemoryHooks(b.ctx())
}
func (b *Bindings) Hooks_RemoveStarterMemory() error {
	defer sentry.WrapBinding("Hooks_RemoveStarterMemory")()
	return b.api.Hooks().RemoveStarterMemoryHooks(b.ctx())
}
func (b *Bindings) Hooks_DryRun(hookID string, syntheticPayload string) (hooksview.DryRunResult, error) {
	defer sentry.WrapBinding("Hooks_DryRun")()
	return b.api.Hooks().DryRun(b.ctx(), hookID, syntheticPayload)
}

// ── tools (MCP recipes) ────────────────────────────────────────────────

func (b *Bindings) Tools_ListRecipes() ([]tools.RecipeListing, error) {
	defer sentry.WrapBinding("Tools_ListRecipes")()
	return b.api.Tools().ListRecipes(b.ctx())
}

func (b *Bindings) Tools_InstallRecipe(id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error) {
	defer sentry.WrapBinding("Tools_InstallRecipe")()
	return b.api.Tools().InstallRecipe(b.ctx(), id, env, config)
}

// Tools_SignInRecipe runs the MCP OAuth sign-in for a remote recipe (opens the
// system browser), persists the token, and respawns the recipe authenticated.
func (b *Bindings) Tools_SignInRecipe(id string) (stdio.RecipeStatus, error) {
	defer sentry.WrapBinding("Tools_SignInRecipe")()
	return b.api.Tools().SignInRecipe(b.ctx(), id)
}

func (b *Bindings) Tools_UninstallRecipe(id string) error {
	defer sentry.WrapBinding("Tools_UninstallRecipe")()
	return b.api.Tools().UninstallRecipe(b.ctx(), id)
}

func (b *Bindings) Tools_ForgetRecipeKey(id, envName string) error {
	defer sentry.WrapBinding("Tools_ForgetRecipeKey")()
	return b.api.Tools().ForgetRecipeKey(b.ctx(), id, envName)
}

func (b *Bindings) Tools_RecipeStatus(id string) (stdio.RecipeStatus, error) {
	defer sentry.WrapBinding("Tools_RecipeStatus")()
	return b.api.Tools().RecipeStatus(b.ctx(), id)
}

func (b *Bindings) Tools_RecipeConfig(id string) (map[string]any, error) {
	defer sentry.WrapBinding("Tools_RecipeConfig")()
	return b.api.Tools().RecipeConfig(b.ctx(), id)
}

// Tools_RequestAdditionalAllowedDir is the Wails binding for the runtime
// "expand filesystem access" flow. The model's kenaz__request_filesystem_access
// built-in calls this via its delegate; it can also be called directly from
// the frontend if a future UI surface needs it.
//
// Returns { granted, expanded, message } so the caller knows whether to
// retry its original filesystem operation using the canonicalised path.
func (b *Bindings) Tools_RequestAdditionalAllowedDir(recipeID, path, reason string) (tools.FSAccessResult, error) {
	defer sentry.WrapBinding("Tools_RequestAdditionalAllowedDir")()
	granted, expanded, err := b.api.Tools().RequestAdditionalAllowedDir(b.ctx(), recipeID, path, reason)
	msg := ""
	if err != nil {
		msg = err.Error()
	} else if !granted {
		msg = "access denied or timed out"
	} else {
		msg = "access granted"
	}
	return tools.FSAccessResult{Granted: granted, Expanded: expanded, Message: msg}, nil
}

// ── shell escape (chat input `!cmd` feature) ──────────────────────────

// BashExecResult mirrors the JSON the kenaz__bash tool returns.
// stdout/stderr are plain strings (UTF-8); the bash tool already caps
// at 64 KiB per stream and signals truncation via the `Truncated` flag.
type BashExecResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

// Bash_Exec runs a single command line through the kenaz__bash
// built-in tool and returns its result. Used by the chat input's
// `!cmd` shell-escape — typing `!ls -la ~/Desktop` dispatches here
// (not to the LLM) and the parent renders the result inline as a
// synthetic system message. The Cedar gate applies normally.
//
// sessionID is threaded through ctx so the bash tool's per-session
// run-id cache picks up the right slot. Empty sessionID is OK (the
// tool's run-id cache is best-effort).
func (b *Bindings) Bash_Exec(sessionID, command string) (BashExecResult, error) {
	defer sentry.WrapBinding("Bash_Exec")()
	type builtinsHolder interface{ Builtins() *toolloop.BuiltinRegistry }
	holder, ok := b.api.(builtinsHolder)
	if !ok || holder.Builtins() == nil {
		return BashExecResult{}, errors.New("rpc: bash tool registry not wired")
	}
	tool, ok := holder.Builtins().Lookup("kenaz__bash")
	if !ok {
		return BashExecResult{}, errors.New("rpc: kenaz__bash tool not registered (toggle on in Settings → Tools)")
	}
	args, err := json.Marshal(struct {
		Command string `json:"command"`
	}{Command: command})
	if err != nil {
		return BashExecResult{}, err
	}
	ctx := toolloop.WithSessionID(b.ctx(), sessionID)
	raw, err := tool.Call(ctx, args)
	if err != nil {
		return BashExecResult{}, err
	}
	var out BashExecResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return BashExecResult{}, err
	}
	return out, nil
}

// ── shell ──────────────────────────────────────────────────────────────

func (b *Bindings) Shell_OpenInOSBrowser(path string) error {
	defer sentry.WrapBinding("Shell_OpenInOSBrowser")()
	return b.api.Shell().OpenInOSBrowser(b.ctx(), path)
}

// ShellReadFileResult mirrors the (bytes, mediaType) return from
// ShellAPI.ReadFile. The bytes are base64-encoded at the binding so
// the Wails JSON wire shape stays string-only and the frontend can
// hand the data straight to Attachments_AddMedia.
type ShellReadFileResult struct {
	DataBase64 string `json:"dataBase64"`
	MediaType  string `json:"mediaType"`
}

func (b *Bindings) Shell_PathComplete(partial string) ([]string, error) {
	defer sentry.WrapBinding("Shell_PathComplete")()
	return b.api.Shell().PathComplete(b.ctx(), partial)
}

func (b *Bindings) Shell_ReadFile(path string) (ShellReadFileResult, error) {
	defer sentry.WrapBinding("Shell_ReadFile")()
	data, mt, err := b.api.Shell().ReadFile(b.ctx(), path)
	if err != nil {
		return ShellReadFileResult{}, err
	}
	return ShellReadFileResult{
		DataBase64: base64.StdEncoding.EncodeToString(data),
		MediaType:  mt,
	}, nil
}

// shellAPIType keeps the shell import alive — Wails-bound methods
// only return primitive errors here, so we anchor the import to the
// view-API interface so a future binding addition with a typed
// argument doesn't have to re-wire the input.
type shellAPIType = shell.ShellAPI

// ── slash commands ────────────────────────────────────────────────────

func (b *Bindings) Slash_Execute(sessionID, raw string) (slashview.ExecuteResult, error) {
	defer sentry.WrapBinding("Slash_Execute")()
	return b.api.Slash().Execute(b.ctx(), sessionID, raw)
}

func (b *Bindings) Slash_List() ([]slashview.CommandInfo, error) {
	defer sentry.WrapBinding("Slash_List")()
	return b.api.Slash().List(b.ctx())
}

// ── user command RPCs ─────────────────────────────────────────────────

func (b *Bindings) Slashcmd_List(projectID string) ([]slashview.UserCommandSummaryWire, error) {
	defer sentry.WrapBinding("Slashcmd_List")()
	return b.api.Slash().UserList(b.ctx(), projectID)
}

func (b *Bindings) Slashcmd_Get(name, projectID string) (slashview.UserCommandWire, error) {
	defer sentry.WrapBinding("Slashcmd_Get")()
	return b.api.Slash().UserGet(b.ctx(), name, projectID)
}

func (b *Bindings) Slashcmd_Save(cmd slashview.UserCommandWire) error {
	defer sentry.WrapBinding("Slashcmd_Save")()
	return b.api.Slash().UserSave(b.ctx(), cmd)
}

func (b *Bindings) Slashcmd_Delete(name, projectID string) error {
	defer sentry.WrapBinding("Slashcmd_Delete")()
	return b.api.Slash().UserDelete(b.ctx(), name, projectID)
}

func (b *Bindings) Slashcmd_Run(name string, args map[string]string, sessionID, projectID, cwd, selection string) (slashview.RunResultWire, error) {
	defer sentry.WrapBinding("Slashcmd_Run")()
	return b.api.Slash().UserRun(b.ctx(), name, args, sessionID, projectID, cwd, selection)
}

// ── fleet skill RPCs (fleet-skills-sync-01NDFSEX18 WP02/03/04) ───────

func (b *Bindings) Slashcmd_SkillList() ([]slashview.SkillItemWire, error) {
	defer sentry.WrapBinding("Slashcmd_SkillList")()
	return b.api.Slash().SkillList(b.ctx())
}

func (b *Bindings) Slashcmd_SkillPublish(name, projectID, visibility string) error {
	defer sentry.WrapBinding("Slashcmd_SkillPublish")()
	return b.api.Slash().SkillPublish(b.ctx(), name, projectID, visibility)
}

func (b *Bindings) Slashcmd_SkillInstall(catalogID, version string) error {
	defer sentry.WrapBinding("Slashcmd_SkillInstall")()
	return b.api.Slash().SkillInstall(b.ctx(), catalogID, version)
}

func (b *Bindings) Slashcmd_SkillUninstall(skillID string) error {
	defer sentry.WrapBinding("Slashcmd_SkillUninstall")()
	return b.api.Slash().SkillUninstall(b.ctx(), skillID)
}

func (b *Bindings) Slashcmd_SkillRenameLocalTrigger(skillID, newTrigger string) error {
	defer sentry.WrapBinding("Slashcmd_SkillRenameLocalTrigger")()
	return b.api.Slash().SkillRenameLocalTrigger(b.ctx(), skillID, newTrigger)
}

// ── feature flags (user-slash-commands-01KQ8TD9 WP09) ────────────────

// FeatureFlagInfo carries a single feature-flag name + enabled state
// for the frontend FeatureFlagsView.
type FeatureFlagInfo struct {
	Name        string `json:"name"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description"`
	EnvVar      string `json:"envVar"`
}

// Config_GetFlags returns the current state of all known feature flags.
// This RPC is read-only; flags are controlled via environment variables.
func (b *Bindings) Config_GetFlags() ([]FeatureFlagInfo, error) {
	defer sentry.WrapBinding("Config_GetFlags")()
	return []FeatureFlagInfo{
		{
			Name:        "user-slash-commands",
			Enabled:     coreslashcmd.UserSlashcmdEnabled(),
			Description: "User-defined / commands (text expansions, tool dispatch, prompt templates).",
			EnvVar:      "HARNESS_USER_SLASHCMD",
		},
		{
			Name:        "multimodal-out",
			Enabled:     llmcap.MultimodalOutEnabled(),
			Description: "Model-generated image output pipeline (DALL-E 3, gpt-image-1, Titan Image). When off, StreamGeneratedImage events are silently discarded regardless of the auto-capture dial.",
			EnvVar:      "HARNESS_MULTIMODAL_OUT",
		},
		{
			Name:        "google-gemini",
			Enabled:     gemini.IsEnabled(),
			Description: "Google Gemini adapter (AI Studio API key and Vertex AI service-account / ADC auth). Supports gemini-2.5-pro/flash with streaming, tool calling, vision, and reasoning.",
			EnvVar:      gemini.EnvFlag,
		},
	}, nil
}

// ── corpora (agent-kernel-graph; Bundle C WP10/WP11) ──────────────────

func (b *Bindings) Corpus_ListCorpora(scope string) ([]corpusview.Corpus, error) {
	defer sentry.WrapBinding("Corpus_ListCorpora")()
	return b.api.Corpus().ListCorpora(b.ctx(), scope)
}
func (b *Bindings) Corpus_CreateCorpus(req corpusview.CreateRequest) (corpusview.Corpus, error) {
	defer sentry.WrapBinding("Corpus_CreateCorpus")()
	return b.api.Corpus().CreateCorpus(b.ctx(), req)
}
func (b *Bindings) Corpus_GetCorpus(corpusID string) (corpusview.Corpus, error) {
	defer sentry.WrapBinding("Corpus_GetCorpus")()
	return b.api.Corpus().GetCorpus(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_DeleteCorpus(corpusID string) error {
	defer sentry.WrapBinding("Corpus_DeleteCorpus")()
	return b.api.Corpus().DeleteCorpus(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_ListFiles(corpusID string) ([]corpusview.CorpusFile, error) {
	defer sentry.WrapBinding("Corpus_ListFiles")()
	return b.api.Corpus().ListFiles(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_ListChunks(corpusID, fileID string) ([]corpusview.Chunk, error) {
	defer sentry.WrapBinding("Corpus_ListChunks")()
	return b.api.Corpus().ListChunks(b.ctx(), corpusID, fileID)
}
func (b *Bindings) Corpus_IngestPath(corpusID, path string, opts corpusview.IngestOptions) (corpusview.IngestStatus, error) {
	defer sentry.WrapBinding("Corpus_IngestPath")()
	return b.api.Corpus().IngestPath(b.ctx(), corpusID, path, opts)
}
func (b *Bindings) Corpus_JobStatus(jobID string) (corpusview.IngestStatus, error) {
	defer sentry.WrapBinding("Corpus_JobStatus")()
	return b.api.Corpus().JobStatus(b.ctx(), jobID)
}
func (b *Bindings) Corpus_Retrieve(corpusID string, req corpusview.RetrieveRequest) (corpusview.RetrieveResponse, error) {
	defer sentry.WrapBinding("Corpus_Retrieve")()
	return b.api.Corpus().Retrieve(b.ctx(), corpusID, req)
}

// ── agent graph (mission agent-kernel-graph; Bundle A WP06) ─────────

func (b *Bindings) Graph_ListGraphs(scope string) ([]graphview.GraphInfo, error) {
	defer sentry.WrapBinding("Graph_ListGraphs")()
	return b.api.Graph().ListGraphs(b.ctx(), scope)
}
func (b *Bindings) Graph_LoadGraph(id string) (graphview.GraphSpec, error) {
	defer sentry.WrapBinding("Graph_LoadGraph")()
	return b.api.Graph().LoadGraph(b.ctx(), id)
}
func (b *Bindings) Graph_SaveGraph(spec graphview.GraphSpec) error {
	defer sentry.WrapBinding("Graph_SaveGraph")()
	return b.api.Graph().SaveGraph(b.ctx(), spec)
}
func (b *Bindings) Graph_DeleteGraph(id string) error {
	defer sentry.WrapBinding("Graph_DeleteGraph")()
	return b.api.Graph().DeleteGraph(b.ctx(), id)
}
func (b *Bindings) Graph_Validate(yaml string) (graphview.ValidationResult, error) {
	defer sentry.WrapBinding("Graph_Validate")()
	return b.api.Graph().Validate(b.ctx(), yaml)
}
func (b *Bindings) Graph_StartRun(req graphview.StartRunRequest) (graphview.StartRunResponse, error) {
	defer sentry.WrapBinding("Graph_StartRun")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze graph execution during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return graphview.StartRunResponse{}, err
	}
	return b.api.Graph().StartRun(b.ctx(), req)
}
func (b *Bindings) Graph_GetRunStatus(runID string) (graphview.RunStatus, error) {
	defer sentry.WrapBinding("Graph_GetRunStatus")()
	return b.api.Graph().GetRunStatus(b.ctx(), runID)
}
func (b *Bindings) Graph_GetRunTrace(runID string, since int64) ([]graphview.RunTraceEvent, error) {
	defer sentry.WrapBinding("Graph_GetRunTrace")()
	return b.api.Graph().GetRunTrace(b.ctx(), runID, since)
}
func (b *Bindings) Graph_Resume(runID, askResponse string) error {
	defer sentry.WrapBinding("Graph_Resume")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze graph resume during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return err
	}
	return b.api.Graph().Resume(b.ctx(), runID, askResponse)
}
func (b *Bindings) Graph_CancelRun(runID string) error {
	defer sentry.WrapBinding("Graph_CancelRun")()
	return b.api.Graph().CancelRun(b.ctx(), runID)
}

// ── compaction (agent-kernel-graph; Bundle D WP12/WP13) ───────────────

func (b *Bindings) Compaction_GetConfig(layer compactionview.Layer, scopeID string) (compactionview.Config, error) {
	defer sentry.WrapBinding("Compaction_GetConfig")()
	return b.api.Compaction().GetConfig(b.ctx(), layer, scopeID)
}
func (b *Bindings) Compaction_GetEffective(scope compactionview.ScopeKey) (compactionview.EffectiveConfig, error) {
	defer sentry.WrapBinding("Compaction_GetEffective")()
	return b.api.Compaction().GetEffective(b.ctx(), scope)
}
func (b *Bindings) Compaction_SetConfig(layer compactionview.Layer, scopeID string, cfg compactionview.Config) error {
	defer sentry.WrapBinding("Compaction_SetConfig")()
	return b.api.Compaction().SetConfig(b.ctx(), layer, scopeID, cfg)
}
func (b *Bindings) Compaction_TriggerManual(sessionID string, opts compactionview.ManualOpts) (compactionview.ManualResult, error) {
	defer sentry.WrapBinding("Compaction_TriggerManual")()
	return b.api.Compaction().TriggerManualCompaction(b.ctx(), sessionID, opts)
}
func (b *Bindings) Compaction_ListCustomStrategies() ([]compactionview.CustomStrategy, error) {
	defer sentry.WrapBinding("Compaction_ListCustomStrategies")()
	return b.api.Compaction().ListCustomStrategies(b.ctx())
}

// Compaction_GetTierExplain returns the static tier-explain payload the
// Settings panel renders in the "What does this mean?" disclosure on
// the compaction-aggressiveness dial (mission
// compaction-strategy-ui-01KQ8TDI §2.2 / §2.9). The numerics come from
// core/compaction.Tier() so the engine and UI never drift.
func (b *Bindings) Compaction_GetTierExplain() ([]compactionview.TierExplain, error) {
	defer sentry.WrapBinding("Compaction_GetTierExplain")()
	return b.api.Compaction().GetTierExplain(b.ctx())
}

// ── branches (agent-kernel-graph; Bundle B WP07/08) ───────────────────

func (b *Bindings) Branches_List(parentSessionID string) ([]branchesview.Branch, error) {
	defer sentry.WrapBinding("Branches_List")()
	return b.api.Branches().ListBranches(b.ctx(), parentSessionID)
}
func (b *Bindings) Branches_Create(opts branchesview.CreateBranchOptions) (branchesview.Branch, error) {
	defer sentry.WrapBinding("Branches_Create")()
	return b.api.Branches().CreateBranch(b.ctx(), opts)
}
func (b *Bindings) Branches_GetStatus(branchID string) (branchesview.BranchStatus, error) {
	defer sentry.WrapBinding("Branches_GetStatus")()
	return b.api.Branches().GetBranchStatus(b.ctx(), branchID)
}
func (b *Bindings) Branches_Merge(branchID string) error {
	defer sentry.WrapBinding("Branches_Merge")()
	return b.api.Branches().MergeBranch(b.ctx(), branchID)
}
func (b *Bindings) Branches_Abandon(branchID string) error {
	defer sentry.WrapBinding("Branches_Abandon")()
	return b.api.Branches().AbandonBranch(b.ctx(), branchID)
}
func (b *Bindings) Branches_RecommendModel(parentSessionID, taskHint, preference string) (branchesview.RecommendedModel, error) {
	defer sentry.WrapBinding("Branches_RecommendModel")()
	return b.api.Branches().RecommendModel(b.ctx(), parentSessionID, taskHint, preference)
}
func (b *Bindings) Branches_ProposeReintegrationSummary(branchSessionID string) (branchesview.ReintegrationProposal, error) {
	defer sentry.WrapBinding("Branches_ProposeReintegrationSummary")()
	return b.api.Branches().ProposeReintegrationSummary(b.ctx(), branchSessionID)
}
func (b *Bindings) Branches_CommitReintegration(opts branchesview.CommitReintegrationOptions) error {
	defer sentry.WrapBinding("Branches_CommitReintegration")()
	return b.api.Branches().CommitReintegration(b.ctx(), opts)
}
func (b *Bindings) Branches_SetAdvisorDismissed(sessionID string, dismissed bool) error {
	defer sentry.WrapBinding("Branches_SetAdvisorDismissed")()
	return b.api.Branches().SetAdvisorDismissed(b.ctx(), sessionID, dismissed)
}

// Branches_ListWithBranchTree returns a flat list of sessions with parent
// pointers for all branches in a project. The frontend builds the visual
// tree by grouping on parentSessionId. BranchDepth is pre-computed.
// (branching-ux-polish-01KQ8TD7 WP02/WP03)
func (b *Bindings) Branches_ListWithBranchTree(projectID string) ([]branchesview.SessionWithBranchPointer, error) {
	defer sentry.WrapBinding("Branches_ListWithBranchTree")()
	return b.api.Branches().ListWithBranchTree(b.ctx(), projectID)
}

// ── workflows (mission workflows-01KQ8TDG, v0.3.0 beta) ───────────────

func (b *Bindings) Workflows_List() ([]workflowsview.Summary, error) {
	defer sentry.WrapBinding("Workflows_List")()
	return b.api.Workflows().List(b.ctx())
}
func (b *Bindings) Workflows_Get(id string) (workflowsview.Workflow, error) {
	defer sentry.WrapBinding("Workflows_Get")()
	return b.api.Workflows().Get(b.ctx(), id)
}
func (b *Bindings) Workflows_Run(id string, inputs map[string]string) (workflowsview.RunResult, error) {
	defer sentry.WrapBinding("Workflows_Run")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze workflow execution during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return workflowsview.RunResult{}, err
	}
	return b.api.Workflows().Run(b.ctx(), id, inputs)
}
func (b *Bindings) Workflows_Save(in workflowsview.SaveInput) (workflowsview.SaveOutput, error) {
	defer sentry.WrapBinding("Workflows_Save")()
	return b.api.Workflows().Save(b.ctx(), in)
}
func (b *Bindings) Workflows_Delete(id string) error {
	defer sentry.WrapBinding("Workflows_Delete")()
	return b.api.Workflows().Delete(b.ctx(), id)
}

// ── workflow scheduler (workflows-agentic-01KW2D3X WP02) ──────────────

func (b *Bindings) Workflows_ScheduleSet(in workflowsview.ScheduleSetInput) error {
	defer sentry.WrapBinding("Workflows_ScheduleSet")()
	return b.api.Workflows().ScheduleSet(b.ctx(), in)
}
func (b *Bindings) Workflows_ScheduleClear(workflowID string) error {
	defer sentry.WrapBinding("Workflows_ScheduleClear")()
	return b.api.Workflows().ScheduleClear(b.ctx(), workflowID)
}
func (b *Bindings) Workflows_ScheduleList() ([]workflowsview.ScheduleEntry, error) {
	defer sentry.WrapBinding("Workflows_ScheduleList")()
	return b.api.Workflows().ScheduleList(b.ctx())
}
func (b *Bindings) Workflows_RunNow(workflowID string) (workflowsview.RunSummary, error) {
	defer sentry.WrapBinding("Workflows_RunNow")()
	// fleet-emergency-lockdown-01NDFSEX12 WP04: freeze workflow execution during lockdown.
	if err := middleware.CheckLockdown(); err != nil {
		return workflowsview.RunSummary{}, err
	}
	return b.api.Workflows().RunNow(b.ctx(), workflowID)
}

// ── workflow scheduled-inbox (workflow-extensions-01KW2D3Y WP01) ──────

func (b *Bindings) Workflows_ScheduleRunHistory(workflowID string, limit int) ([]workflowsview.RunSummary, error) {
	defer sentry.WrapBinding("Workflows_ScheduleRunHistory")()
	return b.api.Workflows().ScheduleRunHistory(b.ctx(), workflowID, limit)
}
func (b *Bindings) Workflows_ScheduleNextFire(workflowID string) (string, error) {
	defer sentry.WrapBinding("Workflows_ScheduleNextFire")()
	t, err := b.api.Workflows().ScheduleNextFire(b.ctx(), workflowID)
	if err != nil || t.IsZero() {
		return "", err
	}
	return t.UTC().Format("2006-01-02T15:04:05Z"), nil
}
func (b *Bindings) Workflows_CancelRun(runID string) error {
	defer sentry.WrapBinding("Workflows_CancelRun")()
	return b.api.Workflows().CancelRun(b.ctx(), runID)
}

// ── workflow catalog bindings (p0-wiring-fixes WP02) ──────────────────────
// Workflows_CatalogList returns the full catalog of installable workflow
// templates. Delegates to the WorkflowsAPI catalog seam (WP03 of
// workflows-agentic-01KW2D3X). Returns ErrCatalogUnavailable when no
// catalog backend is wired.
func (b *Bindings) Workflows_CatalogList() ([]workflowsview.CatalogEntry, error) {
	defer sentry.WrapBinding("Workflows_CatalogList")()
	return b.api.Workflows().Catalog_List(b.ctx())
}

// Workflows_CatalogGet returns the full YAML source + entry metadata for
// the catalog item identified by id. The preview drawer uses this to render
// the install confirmation and the YAML diff.
func (b *Bindings) Workflows_CatalogGet(id string) (workflowsview.CatalogPreview, error) {
	defer sentry.WrapBinding("Workflows_CatalogGet")()
	return b.api.Workflows().Catalog_Get(b.ctx(), id)
}

// Workflows_CatalogInstall copies the catalog item identified by id into
// the user's workflow store and optionally arms a cron schedule. Returns
// ErrNotFound when id is unknown; returns ErrCatalogUnavailable when no
// catalog backend is wired.
func (b *Bindings) Workflows_CatalogInstall(id string) (workflowsview.CatalogInstallResult, error) {
	defer sentry.WrapBinding("Workflows_CatalogInstall")()
	return b.api.Workflows().Catalog_Install(b.ctx(), id)
}

// ── scheduled chat runs (scheduled-chat-runs-01KX5R8B, WP04) ──────────

func (b *Bindings) ScheduledChat_Create(in scheduledchatview.CreateInput) (scheduledchatview.ChatRunEntry, error) {
	defer sentry.WrapBinding("ScheduledChat_Create")()
	return b.api.ScheduledChat().Create(b.ctx(), in)
}
func (b *Bindings) ScheduledChat_Update(in scheduledchatview.UpdateInput) (scheduledchatview.ChatRunEntry, error) {
	defer sentry.WrapBinding("ScheduledChat_Update")()
	return b.api.ScheduledChat().Update(b.ctx(), in)
}
func (b *Bindings) ScheduledChat_Delete(id string) error {
	defer sentry.WrapBinding("ScheduledChat_Delete")()
	return b.api.ScheduledChat().Delete(b.ctx(), id)
}
func (b *Bindings) ScheduledChat_List() ([]scheduledchatview.ChatRunEntry, error) {
	defer sentry.WrapBinding("ScheduledChat_List")()
	return b.api.ScheduledChat().List(b.ctx())
}
func (b *Bindings) ScheduledChat_Get(id string) (scheduledchatview.ChatRunEntry, error) {
	defer sentry.WrapBinding("ScheduledChat_Get")()
	return b.api.ScheduledChat().Get(b.ctx(), id)
}
func (b *Bindings) ScheduledChat_RunNow(id string) (scheduledchatview.RunSummary, error) {
	defer sentry.WrapBinding("ScheduledChat_RunNow")()
	return b.api.ScheduledChat().RunNow(b.ctx(), id)
}
func (b *Bindings) ScheduledChat_History(id string, limit int) ([]scheduledchatview.RunSummary, error) {
	defer sentry.WrapBinding("ScheduledChat_History")()
	return b.api.ScheduledChat().History(b.ctx(), id, limit)
}
func (b *Bindings) ScheduledChat_SetEnabled(id string, enabled bool) error {
	defer sentry.WrapBinding("ScheduledChat_SetEnabled")()
	return b.api.ScheduledChat().SetEnabled(b.ctx(), id, enabled)
}

// ── update (mission auto-update, v0.4.0 WP03) ─────────────────────────
//
// TODO: regenerate via `wails generate module` once the WP04 + WP05 UI
// lands. Hand-added in WP03 so the frontend updateClient.ts has typed
// bindings to consume.

func (b *Bindings) Update_Status() (updateview.StatusOutput, error) {
	defer sentry.WrapBinding("Update_Status")()
	return b.api.Update().Status(b.ctx())
}
func (b *Bindings) Update_StartCheck() error {
	defer sentry.WrapBinding("Update_StartCheck")()
	return b.api.Update().StartCheck(b.ctx())
}
func (b *Bindings) Update_StartDownload() error {
	defer sentry.WrapBinding("Update_StartDownload")()
	return b.api.Update().StartDownload(b.ctx())
}
func (b *Bindings) Update_Apply() error {
	defer sentry.WrapBinding("Update_Apply")()
	return b.api.Update().Apply(b.ctx())
}
func (b *Bindings) Update_SkipVersion(version string) error {
	defer sentry.WrapBinding("Update_SkipVersion")()
	return b.api.Update().SkipVersion(b.ctx(), version)
}
func (b *Bindings) Update_ListSkippedVersions() ([]string, error) {
	defer sentry.WrapBinding("Update_ListSkippedVersions")()
	return b.api.Update().ListSkippedVersions(b.ctx())
}
func (b *Bindings) Update_UnskipVersion(version string) error {
	defer sentry.WrapBinding("Update_UnskipVersion")()
	return b.api.Update().UnskipVersion(b.ctx(), version)
}

// ── nodes (manifest-driven node catalog; WP07) ────────────────────────

func (b *Bindings) Nodes_Catalog() ([]nodesview.NodeManifestSummary, error) {
	defer sentry.WrapBinding("Nodes_Catalog")()
	return b.api.Nodes().Catalog(b.ctx())
}
func (b *Bindings) Nodes_Get(id string) (nodesview.NodeManifestDetail, error) {
	defer sentry.WrapBinding("Nodes_Get")()
	return b.api.Nodes().Get(b.ctx(), id)
}
func (b *Bindings) Nodes_ReloadOverrides() (nodesview.ReloadResult, error) {
	defer sentry.WrapBinding("Nodes_ReloadOverrides")()
	return b.api.Nodes().ReloadOverrides(b.ctx())
}
func (b *Bindings) Nodes_ListUserOverrides() ([]nodesview.UserOverrideInfo, error) {
	defer sentry.WrapBinding("Nodes_ListUserOverrides")()
	return b.api.Nodes().ListUserOverrides(b.ctx())
}
func (b *Bindings) Nodes_Doctor() (nodesview.DoctorReport, error) {
	defer sentry.WrapBinding("Nodes_Doctor")()
	return b.api.Nodes().Doctor(b.ctx())
}

// ── cedarpolicy (snippet writer/revoker; WP09) ────────────────────────

// CedarPolicy_WriteSnippet writes body to <DataDir>/policy/<name> after
// validating the filename. Triggers engine reload best-effort.
func (b *Bindings) CedarPolicy_WriteSnippet(name string, body string) error {
	defer sentry.WrapBinding("CedarPolicy_WriteSnippet")()
	return b.api.CedarPolicy().WritePolicySnippet(b.ctx(), name, body)
}

// CedarPolicy_RevokeSnippet deletes <DataDir>/policy/<name> after
// validating the filename. Triggers engine reload best-effort.
func (b *Bindings) CedarPolicy_RevokeSnippet(name string) error {
	defer sentry.WrapBinding("CedarPolicy_RevokeSnippet")()
	return b.api.CedarPolicy().RevokePolicySnippet(b.ctx(), name)
}

// CedarPolicy_ResolvePropose delivers a user decision (accept | reject)
// for a pending cedar-policy proposal surfaced by the CedarProposeModal.
// requestID came in on the "cedar:propose-pending" broker topic.
func (b *Bindings) CedarPolicy_ResolvePropose(requestID string, decision string) error {
	defer sentry.WrapBinding("CedarPolicy_ResolvePropose")()
	return b.api.CedarProposeResolve(requestID, decision)
}

// ── cedarpolicy editor (cedar-policy-editor-ui-01KQ8TD6 WP01) ────────────

// CedarPolicy_Get reads the source of a single policy file.
// For embedded defaults, reads from the embedded FS and returns ReadOnly=true.
// For on-disk user policies, reads from <DataDir>/policy/<name>.
func (b *Bindings) CedarPolicy_Get(name string) (cedarpolicyview.PolicyFileDetail, error) {
	defer sentry.WrapBinding("CedarPolicy_Get")()
	return b.api.CedarPolicy().GetPolicy(b.ctx(), name)
}

// CedarPolicy_Save validates source via the Cedar parser and, on success,
// atomically writes it to <DataDir>/policy/<name>. Returns ParseResult with
// diagnostics; never writes when parse fails.
func (b *Bindings) CedarPolicy_Save(name string, source string) (cedarpolicyview.ParseResult, error) {
	defer sentry.WrapBinding("CedarPolicy_Save")()
	return b.api.CedarPolicy().SavePolicy(b.ctx(), name, source)
}

// CedarPolicy_Delete removes <DataDir>/policy/<name>. Fails for protected
// embedded defaults.
func (b *Bindings) CedarPolicy_Delete(name string) error {
	defer sentry.WrapBinding("CedarPolicy_Delete")()
	return b.api.CedarPolicy().DeletePolicy(b.ctx(), name)
}

// CedarPolicy_Validate parses source without touching disk. Used by the
// editor's debounced live-validation indicator.
func (b *Bindings) CedarPolicy_Validate(source string) (cedarpolicyview.ParseResult, error) {
	defer sentry.WrapBinding("CedarPolicy_Validate")()
	return b.api.CedarPolicy().ValidatePolicy(b.ctx(), source)
}

// CedarPolicy_InstallTemplate copies a shipped Cedar template from the
// embedded policies/ directory to <DataDir>/policy/<destName>.
// Returns an error when the destination already exists.
func (b *Bindings) CedarPolicy_InstallTemplate(templateName string, destName string) (cedarpolicyview.PolicyFileDetail, error) {
	defer sentry.WrapBinding("CedarPolicy_InstallTemplate")()
	return b.api.CedarPolicy().InstallTemplate(b.ctx(), templateName, destName)
}

// CedarPolicy_ListPlanModeActions returns the canonical Cedar action
// strings denied while plan_mode posture is active. Used by the Cedar
// editor UI's plan-mode reference panel.
// (plan-mode-posture-01KZNP3F WP08)
func (b *Bindings) CedarPolicy_ListPlanModeActions() ([]string, error) {
	defer sentry.WrapBinding("CedarPolicy_ListPlanModeActions")()
	return b.api.CedarPolicy().ListPlanModeActions(b.ctx())
}

// ── search (cross-session-search mission) ─────────────────────────────

// Search_Sessions executes a full-text search across all session
// messages via the FTS5 messages_fts virtual table.
//
// query is sanitised (empty/whitespace → empty result). filters is
// optional; zero values mean "no filter".
func (b *Bindings) Search_Sessions(query string, filters searchview.SearchFilters) ([]searchview.SearchHit, error) {
	defer sentry.WrapBinding("Search_Sessions")()
	return b.api.Search().Search(b.ctx(), query, filters)
}

// Search_Unified fans out across all five corpora (messages, artifacts,
// memory, corpus, audit) in parallel and returns a merged, scored result
// list. filters.Corpora narrows which corpora are queried; an empty slice
// enables all sources.
//
// query is sanitised server-side; empty/whitespace-only returns an empty
// result without error. The raw query never appears in audit emission.
func (b *Bindings) Search_Unified(query string, filters searchview.SearchFilters) ([]searchview.SearchHit, error) {
	defer sentry.WrapBinding("Search_Unified")()
	return b.api.Search().UnifiedSearch(b.ctx(), query, filters)
}

// ── autonomy (autonomy-dial-01KR3M2A WP03) ────────────────────────────

// Settings_GetAutonomy returns the persisted global autonomy.Layer.
func (b *Bindings) Settings_GetAutonomy() (autonomy.Layer, error) {
	defer sentry.WrapBinding("Settings_GetAutonomy")()
	return b.api.Settings().LoadAutonomyProfile(b.ctx())
}

// Settings_SetAutonomy persists the global autonomy.Layer.
func (b *Bindings) Settings_SetAutonomy(layer autonomy.Layer) error {
	defer sentry.WrapBinding("Settings_SetAutonomy")()
	return b.api.Settings().SaveAutonomyProfile(b.ctx(), layer)
}

// Projects_GetAutonomy returns the project's persisted autonomy.Layer
// override.
func (b *Bindings) Projects_GetAutonomy(projectID string) (autonomy.Layer, error) {
	defer sentry.WrapBinding("Projects_GetAutonomy")()
	return b.api.Projects().LoadAutonomyProfile(b.ctx(), projectID)
}

// Projects_SetAutonomy persists the project's autonomy.Layer override.
func (b *Bindings) Projects_SetAutonomy(projectID string, layer autonomy.Layer) error {
	defer sentry.WrapBinding("Projects_SetAutonomy")()
	return b.api.Projects().SaveAutonomyProfile(b.ctx(), projectID, layer)
}

// Sessions_GetAutonomy returns the session's persisted autonomy.Layer
// override.
func (b *Bindings) Sessions_GetAutonomy(sessionID string) (autonomy.Layer, error) {
	defer sentry.WrapBinding("Sessions_GetAutonomy")()
	return b.api.Sessions().LoadAutonomyProfile(b.ctx(), sessionID)
}

// Sessions_SetAutonomy persists the session's autonomy.Layer override.
func (b *Bindings) Sessions_SetAutonomy(sessionID string, layer autonomy.Layer) error {
	defer sentry.WrapBinding("Sessions_SetAutonomy")()
	return b.api.Sessions().SaveAutonomyProfile(b.ctx(), sessionID, layer)
}

// Sessions_ResolveAutonomy folds global → project → session layers.
func (b *Bindings) Sessions_ResolveAutonomy(sessionID string) (sessions.ResolvedAutonomy, error) {
	defer sentry.WrapBinding("Sessions_ResolveAutonomy")()
	return b.api.Sessions().ResolveAutonomy(b.ctx(), sessionID)
}

// ── storage health (v0.5.1 migration-doctor) ───────────────────────────

// Storage_GetMigrationDriftReport reads the harness_migrations ledger and
// the registered migration set, compares them, and returns every
// discrepancy. Never modifies the database. Wired to the Settings → Health
// panel's MigrationDriftPanel.vue component.
func (b *Bindings) Storage_GetMigrationDriftReport() (storageview.DriftReport, error) {
	defer sentry.WrapBinding("Storage_GetMigrationDriftReport")()
	return b.api.Storage().GetMigrationDriftReport(b.ctx())
}

// Storage_ApplyDriftFix repairs an id_mismatch drift entry for the given
// version. It backs up data.db, then UPDATEs the ledger row's id to the
// expected value. Returns an error for ledger_only / code_only entries
// (those require manual intervention).
func (b *Bindings) Storage_ApplyDriftFix(version int) error {
	defer sentry.WrapBinding("Storage_ApplyDriftFix")()
	return b.api.Storage().ApplyDriftFix(b.ctx(), version)
}

// ── onboarding (harness-self-mcp-onboarding-01KQ8TDU WP08) ───────────────

// Onboarding_State returns the boot-time OnboardingState the frontend reads
// to decide whether to mount the dialog (firstRun) or show the
// "Reconfigure with assistant" entry in Settings.
func (b *Bindings) Onboarding_State() (onboardingview.OnboardingState, error) {
	defer sentry.WrapBinding("Onboarding_State")()
	return b.api.Onboarding().State(b.ctx())
}

// Onboarding_Begin returns the initial FSM card without consuming an event.
// The OnboardingDialog calls this when it mounts so the welcome screen
// renders immediately.
func (b *Bindings) Onboarding_Begin() (onboardingview.StepResponse, error) {
	defer sentry.WrapBinding("Onboarding_Begin")()
	return b.api.Onboarding().Begin(b.ctx())
}

// Onboarding_Step advances the Phase-1 FSM by one event (e.g. pick-provider,
// enter-api-key, test-connection). Returns the next Card descriptor.
func (b *Bindings) Onboarding_Step(req onboardingview.StepRequest) (onboardingview.StepResponse, error) {
	defer sentry.WrapBinding("Onboarding_Step")()
	return b.api.Onboarding().Step(b.ctx(), req)
}

// Onboarding_Dismiss marks onboarding as completed so the dialog will not
// auto-show again. Idempotent.
func (b *Bindings) Onboarding_Dismiss() error {
	defer sentry.WrapBinding("Onboarding_Dismiss")()
	return b.api.Onboarding().Dismiss(b.ctx())
}

// Onboarding_RestartPhase2 spawns a kind=onboarding session with the
// harness-self MCP enabled and the chosen starter's system prompt. Called
// when the user picks a starter from the dialog OR clicks "Reconfigure with
// assistant" in Settings. Returns the new session id.
func (b *Bindings) Onboarding_RestartPhase2(req onboardingview.RestartPhase2Request) (onboardingview.RestartPhase2Response, error) {
	defer sentry.WrapBinding("Onboarding_RestartPhase2")()
	return b.api.Onboarding().RestartPhase2(b.ctx(), req)
}

// Onboarding_ListStarters returns the curated starter prompts (title,
// description, recommended provider/model/recipes). The dialog renders
// these as cards; the system-prompt body is withheld and resolved
// server-side when RestartPhase2 fires.
func (b *Bindings) Onboarding_ListStarters() ([]onboardingview.StarterSummary, error) {
	defer sentry.WrapBinding("Onboarding_ListStarters")()
	return b.api.Onboarding().ListStarters(b.ctx())
}

// ── elicit (ask-user-question-interactive-01KZNP3G, WP04) ─────────────

// Elicit_SubmitAnswer resolves a pending ask-user-question elicitation.
// requestID was emitted on the "elicit:pending" broker topic when the
// model called kenaz__ask_user_question; answerJSON is the user's answer
// (a JSON-encoded value matching the question kind), or null when
// cancelled is true. The blocking OpenDialog call on the Go side returns
// after this method resolves the pending channel.
func (b *Bindings) Elicit_SubmitAnswer(requestID string, answerJSON json.RawMessage, cancelled bool) error {
	defer sentry.WrapBinding("Elicit_SubmitAnswer")()
	return b.api.Elicit().SubmitAnswer(b.ctx(), requestID, answerJSON, cancelled)
}

// Elicit_SubmitWizardStep records one question's answer in a multi-step wizard
// (WP05). requestID identifies the in-flight OpenWizard call; questionID is
// the id of the question being answered; answerJSON is the user's answer;
// dismissed is true when the user cancels the wizard mid-flow.
//
// The wizard resolves automatically when all visible questions are answered.
// When dismissed, the partial set of answers is returned to the model as
// answered_so_far in the WizardAnswer.
func (b *Bindings) Elicit_SubmitWizardStep(requestID string, questionID string, answerJSON json.RawMessage, dismissed bool) error {
	defer sentry.WrapBinding("Elicit_SubmitWizardStep")()
	return b.api.Elicit().SubmitWizardStep(b.ctx(), requestID, questionID, answerJSON, dismissed)
}

// Elicit_RegisterDeferred registers a deferred ask for a session and returns
// immediately with DeferredResult{Deferred:true, AskID:…} (WP06).
// The model can use AskID to retrieve the answer later via
// __ask_get_result(ask_id). A chat-header pill appears for the user.
func (b *Bindings) Elicit_RegisterDeferred(sessionID string, req elicitview.ElicitRequest) (elicitview.DeferredResult, error) {
	defer sentry.WrapBinding("Elicit_RegisterDeferred")()
	return b.api.Elicit().RegisterDeferred(b.ctx(), sessionID, req)
}

// Elicit_AnswerDeferred records the user's answer for a deferred ask (WP06).
// Returns the system_reminder text to inject into the next LLM turn.
func (b *Bindings) Elicit_AnswerDeferred(askID string, answer any) (string, error) {
	defer sentry.WrapBinding("Elicit_AnswerDeferred")()
	return b.api.Elicit().AnswerDeferred(b.ctx(), askID, answer)
}

// Elicit_ListPending returns in-flight elicitation request IDs so the
// frontend can reconcile its dialog queue on reconnect / hot reload.
func (b *Bindings) Elicit_ListPending() ([]elicitview.ElicitRequest, error) {
	defer sentry.WrapBinding("Elicit_ListPending")()
	return b.api.Elicit().ListPending(b.ctx())
}

// ── secrets (model-secret-references-01KW7M5A, WP10) ─────────────────

// Secrets_List returns all currently exposed secrets for the session.
// Plaintext is never included in the result (FR-005a). The Ref field
// contains the @secret:<locator> token the model writes in tool args.
func (b *Bindings) Secrets_List() ([]secretsview.SecretRow, error) {
	defer sentry.WrapBinding("Secrets_List")()
	return b.api.Secrets().ListSecrets(b.ctx())
}

// Secrets_Expose adds a new secret to the model-accessible exposure
// index. The plaintext in req is zeroed server-side before this method
// returns; it never re-enters the conversation context.
func (b *Bindings) Secrets_Expose(req secretsview.ExposeRequest) error {
	defer sentry.WrapBinding("Secrets_Expose")()
	return b.api.Secrets().ExposeSecret(b.ctx(), req)
}

// Secrets_Revoke removes a secret from the exposure index by locator.
// Returns an error when the locator is not currently exposed.
func (b *Bindings) Secrets_Revoke(locator string) error {
	defer sentry.WrapBinding("Secrets_Revoke")()
	return b.api.Secrets().RevokeSecret(b.ctx(), locator)
}

// ── local runtime settings (local-model-runtimes-01KQ8VMZ WP07) ─────────

// Settings_GetLocalRuntimeRAMOverrideGB returns the user-supplied RAM
// override in GiB (0 = use detected system RAM).
// (local-model-runtimes-01KQ8VMZ WP07)
func (b *Bindings) Settings_GetLocalRuntimeRAMOverrideGB() (float64, error) {
	defer sentry.WrapBinding("Settings_GetLocalRuntimeRAMOverrideGB")()
	if b.storeFn == nil {
		return 0, nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return 0, err
	}
	return s.LocalRuntimeRAMOverrideGB, nil
}

// Settings_SetLocalRuntimeRAMOverrideGB persists the RAM override. Zero
// disables the override (falls back to detected). Negative values are
// clamped to zero before save.
// (local-model-runtimes-01KQ8VMZ WP07)
func (b *Bindings) Settings_SetLocalRuntimeRAMOverrideGB(gb float64) error {
	defer sentry.WrapBinding("Settings_SetLocalRuntimeRAMOverrideGB")()
	if b.storeFn == nil {
		return nil
	}
	s, err := b.storeFn().LoadAll()
	if err != nil {
		return err
	}
	if gb < 0 {
		gb = 0
	}
	s.LocalRuntimeRAMOverrideGB = gb
	return b.storeFn().SaveAll(s)
}

// LLM_ListDetectedLocalRuntimes returns the current local runtime detection
// snapshot (cached for 5 min). Returns an empty slice when the feature flag
// is off (HARNESS_LOCAL_RUNTIMES=0).
// (local-model-runtimes-01KQ8VMZ WP04)
func (b *Bindings) LLM_ListDetectedLocalRuntimes() ([]llm.LocalRuntimeInfo, error) {
	defer sentry.WrapBinding("LLM_ListDetectedLocalRuntimes")()
	return b.api.LLMConnector().ListDetectedLocalRuntimes(b.ctx())
}

// LLM_AutoConfigureLocalRuntime detects the running runtime for the given
// kind and persists a personal provider profile using the custom-openai
// adapter. Returns an error when the runtime is not running or the feature
// flag is off.
// (local-model-runtimes-01KQ8VMZ WP04)
func (b *Bindings) LLM_AutoConfigureLocalRuntime(kind string) (llm.LocalRuntimeConfigResult, error) {
	defer sentry.WrapBinding("LLM_AutoConfigureLocalRuntime")()
	return b.api.LLMConnector().AutoConfigureLocalRuntime(b.ctx(), kind)
}

// LLM_RescanLocalRuntimes invalidates the detection cache and triggers a
// fresh scan. Returns the fresh snapshot.
// (local-model-runtimes-01KQ8VMZ WP04)
func (b *Bindings) LLM_RescanLocalRuntimes() ([]llm.LocalRuntimeInfo, error) {
	defer sentry.WrapBinding("LLM_RescanLocalRuntimes")()
	return b.api.LLMConnector().RescanLocalRuntimes(b.ctx())
}

// ── agents (branch-subagent-interactive-01KZNP3B, WP01) ──────────────────

// Agents_ListProfiles returns summary entries for all known sub-agent profiles
// (bundled + user-authored). The Settings → Agents panel calls this to
// populate the profile list.
func (b *Bindings) Agents_ListProfiles() ([]agentsview.ProfileSummaryWire, error) {
	defer sentry.WrapBinding("Agents_ListProfiles")()
	return b.api.Agents().ListProfiles(b.ctx())
}

// Agents_LoadProfile returns the full profile for the given id. The profile
// editor calls this to populate its form fields.
func (b *Bindings) Agents_LoadProfile(id string) (agentsview.ProfileWire, error) {
	defer sentry.WrapBinding("Agents_LoadProfile")()
	return b.api.Agents().LoadProfile(b.ctx(), id)
}

// Agents_SaveProfile creates or updates a user-authored profile. The profile
// editor calls this on Submit. Returns an error for bundled ids (the user
// must duplicate the profile first).
func (b *Bindings) Agents_SaveProfile(profile agentsview.ProfileWire) error {
	defer sentry.WrapBinding("Agents_SaveProfile")()
	return b.api.Agents().SaveProfile(b.ctx(), profile)
}

// Agents_DeleteProfile removes a user-authored profile by id. Returns an
// error for bundled ids or ids that do not exist.
func (b *Bindings) Agents_DeleteProfile(id string) error {
	defer sentry.WrapBinding("Agents_DeleteProfile")()
	return b.api.Agents().DeleteProfile(b.ctx(), id)
}

// ── Plan-mode approval bindings (plan-mode-posture-01KZNP3F WP05) ────────

// Planmode_Approve clears the session's plan_mode posture and signals
// approval to the toolloop. Called from PlanApprovalModal "Approve & continue".
func (b *Bindings) Planmode_Approve(req planmodeview.ApproveRequest) (planmodeview.ApproveResponse, error) {
	defer sentry.WrapBinding("Planmode_Approve")()
	return b.api.Planmode_Approve(b.ctx(), req)
}

// Planmode_Discard clears the session's plan_mode posture without approving
// the plan. The plan artifact is retained. Called from PlanApprovalModal "Discard".
func (b *Bindings) Planmode_Discard(req planmodeview.DiscardRequest) (planmodeview.DiscardResponse, error) {
	defer sentry.WrapBinding("Planmode_Discard")()
	return b.api.Planmode_Discard(b.ctx(), req)
}

// Planmode_Edit updates the plan artifact with edited content and then
// approves. Called from PlanApprovalModal "Save & approve" in the inline
// editor view.
func (b *Bindings) Planmode_Edit(req planmodeview.EditRequest) (planmodeview.EditResponse, error) {
	defer sentry.WrapBinding("Planmode_Edit")()
	return b.api.Planmode_Edit(b.ctx(), req)
}

// ── Sentry crash-reporting bindings (sentry-error-monitoring-01KX5R8G WP05)

// Sentry_GetLastFive returns the most-recent 5 (or fewer) captured events
// from the on-disk Last-5 cache. Oldest first.
func (b *Bindings) Sentry_GetLastFive() ([]sentryview.CachedEntry, error) {
	defer sentry.WrapBinding("Sentry_GetLastFive")()
	return b.api.Sentry().GetLastFive(b.ctx())
}

// Sentry_GenerateLocalReport builds a redacted JSON crash report at
// <DataDir>/crash-reports/YYYY-MM-DD-HHMMSS.json. Returns the path and
// byte count. Suitable for users who have tier=Off but want to capture
// a snapshot for manual support triage.
func (b *Bindings) Sentry_GenerateLocalReport() (sentryview.LocalReportResult, error) {
	defer sentry.WrapBinding("Sentry_GenerateLocalReport")()
	return b.api.Sentry().GenerateLocalReport(b.ctx())
}

// Sentry_TestDSN parses a Sentry DSN string and issues a HEAD request to
// the ingestion endpoint to verify reachability. Returns OK:true when the
// server responds 2xx/4xx (i.e. is reachable and accepts the project).
func (b *Bindings) Sentry_TestDSN(dsn string) (sentryview.DSNTestResult, error) {
	defer sentry.WrapBinding("Sentry_TestDSN")()
	return b.api.Sentry().TestDSN(b.ctx(), dsn)
}

// ── Fleet telemetry consent bindings (fleet-otel-archival-01NDFSEX11 WP07)

// Fleet_GetTelemetryConsent returns the device's effective telemetry consent
// level: "none" (default), "aggregate", or "full".
func (b *Bindings) Fleet_GetTelemetryConsent() (string, error) {
	defer sentry.WrapBinding("Fleet_GetTelemetryConsent")()
	return b.api.Fleet().GetTelemetryConsent(b.ctx())
}

// Fleet_SetTelemetryConsent persists the given consent level. Returns an error
// when the org tier is insufficient (aggregate requires Pro+, full requires
// Team+).
func (b *Bindings) Fleet_SetTelemetryConsent(level string) error {
	defer sentry.WrapBinding("Fleet_SetTelemetryConsent")()
	return b.api.Fleet().SetTelemetryConsent(b.ctx(), level)
}

// ── Phase-3 unit collaboration bindings (unified-context-artifacts-01NCTXU01) ─

// Unit_PromoteAsMergeRequest opens a merge request to promote a unit UP a
// classification level (personal→team→org) instead of writing the higher layer
// directly (WP16). toClassification is "team" | "org".
func (b *Bindings) Unit_PromoteAsMergeRequest(unitID, toClassification, title, body string) (fleetview.MergeRequestResult, error) {
	defer sentry.WrapBinding("Unit_PromoteAsMergeRequest")()
	return b.api.Fleet().Unit_PromoteAsMergeRequest(b.ctx(), unitID, toClassification, title, body)
}

// Unit_ListConflicts returns the unresolved same-unit pull conflicts surfaced
// by the syncer (the resolution worklist).
func (b *Bindings) Unit_ListConflicts() ([]fleetview.UnitConflictView, error) {
	defer sentry.WrapBinding("Unit_ListConflicts")()
	return b.api.Fleet().Unit_ListConflicts(b.ctx())
}

// Unit_ResolveMerge applies a whole-body MERGE resolution to a conflicted unit
// (WP17a): the resolved body is written as a new version and the conflict clears.
func (b *Bindings) Unit_ResolveMerge(unitID, resolvedBody string) error {
	defer sentry.WrapBinding("Unit_ResolveMerge")()
	return b.api.Fleet().Unit_ResolveMerge(b.ctx(), unitID, resolvedBody)
}

// Unit_ResolveEnshrine applies an ENSHRINE resolution (WP17b): a coexisting unit
// plus a conflicts_with marker edge so both sides load. Returns the new unit id.
func (b *Bindings) Unit_ResolveEnshrine(srcUnitID, enshrinedTitle, enshrinedBody, reason string) (string, error) {
	defer sentry.WrapBinding("Unit_ResolveEnshrine")()
	return b.api.Fleet().Unit_ResolveEnshrine(b.ctx(), srcUnitID, enshrinedTitle, enshrinedBody, reason)
}

// Unit_ResolveLoadable returns the precedence-ordered loadable set for a scope
// with enshrined conflicts flagged (WP18). scope is "" | "global" | "project" |
// "session"; scopeID narrows further.
func (b *Bindings) Unit_ResolveLoadable(scope, scopeID string) ([]fleetview.ResolvedUnitView, error) {
	defer sentry.WrapBinding("Unit_ResolveLoadable")()
	return b.api.Fleet().Unit_ResolveLoadable(b.ctx(), scope, scopeID)
}

// ── Catalog bindings (fleet-share-and-sync-01NDFSEX14 WP02) ──────────────────

// Catalog_Publish signs and publishes a workflow/agent-pack/bundle to the
// fleet catalog. Returns the canonical catalog item with its assigned ID.
func (b *Bindings) Catalog_Publish(input catalogview.PublishInput) (catalogview.CatalogItemView, error) {
	defer sentry.WrapBinding("Catalog_Publish")()
	return b.api.Catalog().Catalog_Publish(b.ctx(), input)
}

// Catalog_List returns items in the fleet catalog, optionally filtered by kind
// or visibility.
func (b *Bindings) Catalog_List(filter catalogview.CatalogFilter) ([]catalogview.CatalogItemView, error) {
	defer sentry.WrapBinding("Catalog_List")()
	return b.api.Catalog().Catalog_List(b.ctx(), filter)
}

// Catalog_Install downloads and installs the identified catalog item into the
// local DataDir. Returns an error when signature verification fails.
func (b *Bindings) Catalog_Install(catalogID, version string) error {
	defer sentry.WrapBinding("Catalog_Install")()
	return b.api.Catalog().Catalog_Install(b.ctx(), catalogID, version)
}

// Catalog_Uninstall removes a previously-installed catalog item from the local
// DataDir. Idempotent when the item is not installed.
func (b *Bindings) Catalog_Uninstall(kind, catalogID, version string) error {
	defer sentry.WrapBinding("Catalog_Uninstall")()
	return b.api.Catalog().Catalog_Uninstall(b.ctx(), kind, catalogID, version)
}

// Catalog_Installed lists all catalog items currently installed in the local
// DataDir, across all kinds.
func (b *Bindings) Catalog_Installed() ([]catalogview.CatalogItemView, error) {
	defer sentry.WrapBinding("Catalog_Installed")()
	return b.api.Catalog().Catalog_Installed(b.ctx())
}

// ── Sync bindings (fleet-share-and-sync-01NDFSEX14 WP05) ─────────────────────

// Sync_Toggle enables or disables sync for the given category string
// (e.g. "provider_profiles", "ui_theme"). On enable, triggers an immediate push.
func (b *Bindings) Sync_Toggle(category string, enabled bool) error {
	defer sentry.WrapBinding("Sync_Toggle")()
	return b.api.Sync().Sync_Toggle(b.ctx(), category, enabled)
}

// Sync_Status returns the sync status for all categories.
func (b *Bindings) Sync_Status() ([]syncview.SyncStatusView, error) {
	defer sentry.WrapBinding("Sync_Status")()
	return b.api.Sync().Sync_Status(b.ctx())
}

// Sync_ForcePush immediately pushes local state for the given category to fleet.
func (b *Bindings) Sync_ForcePush(category string) error {
	defer sentry.WrapBinding("Sync_ForcePush")()
	return b.api.Sync().Sync_ForcePush(b.ctx(), category)
}

// Sync_ForcePull immediately pulls remote state for the given category and applies it.
func (b *Bindings) Sync_ForcePull(category string) error {
	defer sentry.WrapBinding("Sync_ForcePull")()
	return b.api.Sync().Sync_ForcePull(b.ctx(), category)
}

// Sync_PendingMCPSecrets returns MCPs that arrived via sync but need credentials
// from the user before they can start. Shown in the SyncPanel "Provide credentials" banner.
func (b *Bindings) Sync_PendingMCPSecrets() ([]syncview.PendingMCPSecret, error) {
	defer sentry.WrapBinding("Sync_PendingMCPSecrets")()
	return b.api.Sync().Sync_PendingMCPSecrets(b.ctx())
}

// ── CedarPublish bindings (fleet-share-and-sync-01NDFSEX14 WP07) ─────────────

// Cedar_PublishToTeam publishes a Cedar rule source to the team via fleet.
// The current user must have the policy_admin role; returns ErrCapabilityNotInTier
// when the server responds 403.
func (b *Bindings) Cedar_PublishToTeam(ruleID, ruleSource string) error {
	defer sentry.WrapBinding("Cedar_PublishToTeam")()
	return b.api.CedarPublish().Cedar_PublishToTeam(b.ctx(), ruleID, ruleSource)
}
