package rpc

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
	"github.com/sigil-tech/kaneaz-harness/core/autonomy"
	"github.com/sigil-tech/kaneaz-harness/core/toolloop"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	graphview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/agentgraph"
	artifactsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	branchesview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/branches"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	cedarpolicyview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/cedarpolicy"
	compactionview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/compaction"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	corpusview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/corpus"
	dialsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/dials"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	coremcp "github.com/sigil-tech/kaneaz-harness/core/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	nodesview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/nodes"
	onboardingview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/onboarding"
	permissionsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/permissions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	searchview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/search"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/shell"
	slashview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/slashcmd"
	coreslashcmd "github.com/sigil-tech/kaneaz-harness/core/slashcmd"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	updateview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/update"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
	workflowsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflows"
	storageview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/storage"
	elicitview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/elicit"
	"github.com/sigil-tech/kaneaz-harness/core/logging"
	"github.com/sigil-tech/kaneaz-harness/core/mcp/stdio"
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
	return b.api.ShellStatus(b.ctx())
}

func (b *Bindings) AppInfo() (AppInfo, error) {
	return b.api.AppInfo(b.ctx())
}

// ── settings (privacy CI invariant #5; WP13 fleshes out persistence) ───

func (b *Bindings) LoadRoute() (string, error) {
	if b.storeFn == nil {
		return "/sessions", nil
	}
	return b.storeFn().LoadRoute()
}

func (b *Bindings) SaveRoute(route string) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveRoute(route)
}

func (b *Bindings) LogRouteChange(from, to string) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().LogRouteChange(from, to)
}

func (b *Bindings) LoadTheme() (string, error) {
	if b.storeFn == nil {
		return "system", nil
	}
	return b.storeFn().LoadTheme()
}

func (b *Bindings) SaveTheme(theme string) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveTheme(theme)
}

// ── sessions ───────────────────────────────────────────────────────────

func (b *Bindings) Sessions_List() ([]sessions.Session, error) {
	return b.api.Sessions().List(b.ctx())
}
func (b *Bindings) Sessions_Get(id string) (sessions.Session, error) {
	return b.api.Sessions().Get(b.ctx(), id)
}
func (b *Bindings) Sessions_Create(name string) (sessions.Session, error) {
	return b.api.Sessions().Create(b.ctx(), name)
}
func (b *Bindings) Sessions_Rename(id, name string) error {
	return b.api.Sessions().Rename(b.ctx(), id, name)
}
func (b *Bindings) Sessions_Delete(id string) error {
	return b.api.Sessions().Delete(b.ctx(), id)
}
func (b *Bindings) Sessions_DeleteWithOptions(id string, opts sessions.DeleteOptions) error {
	return b.api.Sessions().DeleteWithOptions(b.ctx(), id, opts)
}
func (b *Bindings) Sessions_Reorder(ids []string) error {
	return b.api.Sessions().Reorder(b.ctx(), ids)
}
func (b *Bindings) Sessions_StartStream(id string) (string, error) {
	return b.api.Sessions().StartStream(b.ctx(), id)
}
func (b *Bindings) Sessions_StopStream(subID string) error {
	return b.api.Sessions().StopStream(b.ctx(), subID)
}
func (b *Bindings) Sessions_ListMessages(id string) ([]sessions.Message, error) {
	return b.api.Sessions().ListMessages(b.ctx(), id)
}
func (b *Bindings) Sessions_ListMessagesActive(id string) (sessions.ListMessagesResult, error) {
	return b.api.Sessions().ListMessagesActive(b.ctx(), id)
}
func (b *Bindings) Sessions_ListMessagesAll(id string) (sessions.ListMessagesResult, error) {
	return b.api.Sessions().ListMessagesAll(b.ctx(), id)
}
func (b *Bindings) Sessions_AppendMessage(id, role, content string) (sessions.Message, error) {
	return b.api.Sessions().AppendMessage(b.ctx(), id, role, content)
}
func (b *Bindings) Sessions_SendMessageWithBlocks(id string, contentBlocks []sessions.ContentBlock) (sessions.Message, error) {
	return b.api.Sessions().SendMessageWithBlocks(b.ctx(), id, contentBlocks)
}
func (b *Bindings) Sessions_SaveDraft(id, draft string) error {
	return b.api.Sessions().SaveDraft(b.ctx(), id, draft)
}
func (b *Bindings) Sessions_LoadDraft(id string) (string, error) {
	return b.api.Sessions().LoadDraft(b.ctx(), id)
}
func (b *Bindings) Sessions_SetSystemPrompt(id, content, kind string) error {
	return b.api.Sessions().SetSystemPrompt(b.ctx(), id, content, kind)
}
func (b *Bindings) Sessions_MoveToProject(id, projectID string) error {
	return b.api.Sessions().MoveToProject(b.ctx(), id, projectID)
}

// Sessions_SuggestTitle triggers a manual auto-title generation for the
// session identified by id. Returns the generated title string on success
// (session-auto-titling-01KQ8TDS WP04).
func (b *Bindings) Sessions_SuggestTitle(id string) (string, error) {
	return b.api.Sessions().SuggestTitle(b.ctx(), id)
}

// Sessions_GetUsage returns the cumulative token + cost aggregate for
// the session (token-cost-telemetry-01KQ8TD7 WP03). Returns a zeroed
// aggregate with costSource="unknown" for sessions with no usage data.
func (b *Bindings) Sessions_GetUsage(id string) (sessions.SessionUsage, error) {
	return b.api.Sessions().GetUsage(b.ctx(), id)
}

// Sessions_ClearTitle resets the session's name to "" and auto_titled=0,
// re-enabling future auto-title attempts
// (session-auto-titling-01KQ8TDS WP04).
func (b *Bindings) Sessions_ClearTitle(id string) error {
	return b.api.Sessions().ClearTitle(b.ctx(), id)
}

// ── llm ────────────────────────────────────────────────────────────────

func (b *Bindings) LLM_ListProviders() ([]llm.Provider, error) {
	return b.api.LLMConnector().ListProviders(b.ctx())
}
func (b *Bindings) LLM_StartStream(profileID, sessionID, modelOverride string) (string, error) {
	return b.api.LLMConnector().StartStream(b.ctx(), profileID, sessionID, modelOverride)
}
func (b *Bindings) LLM_StopStream(subID string) error {
	return b.api.LLMConnector().StopStream(b.ctx(), subID)
}
func (b *Bindings) LLM_AddProvider(input llm.AddProviderInput) error {
	return b.api.LLMConnector().AddProvider(b.ctx(), input)
}
func (b *Bindings) LLM_RemoveProvider(id string) error {
	return b.api.LLMConnector().RemoveProvider(b.ctx(), id)
}
func (b *Bindings) LLM_UpdateProvider(input llm.AddProviderInput) error {
	return b.api.LLMConnector().UpdateProvider(b.ctx(), input)
}

// Diag_LogClientEvent appends a structured record from the frontend
// into the harness log file at ~/.kenaz/harness.log. Used by the
// frontend's eventLog.ts to give support engineers a single trail
// when debugging send/stream issues.
func (b *Bindings) Diag_LogClientEvent(level, message string, attrs map[string]any) {
	logging.LogClientEvent(level, message, attrs)
}

// Diag_LogPath returns the resolved log path so the settings UI can
// surface "logging to ~/.kenaz/harness.log" or the fallback reason.
func (b *Bindings) Diag_LogPath() string {
	return logging.PathOrError()
}
func (b *Bindings) LLM_TestProvider(id string) (llm.TestResult, error) {
	return b.api.LLMConnector().TestProvider(b.ctx(), id)
}
func (b *Bindings) LLM_ListModels(kind, plaintextApiKey string) ([]llm.ModelInfo, error) {
	return b.api.LLMConnector().ListModels(b.ctx(), kind, plaintextApiKey)
}

// LLM_ResolveConfirm completes a pending confirm-each tool call
// (WP05). The frontend modal calls this with the request id surfaced
// on the `llm:tool-confirm-request` topic and one of the four
// canonical decisions ("allow" | "deny" | "always_allow" |
// "always_deny"). The toolloop goroutine waiting on the request id
// unblocks and continues / blocks accordingly.
func (b *Bindings) LLM_ResolveConfirm(requestID, decision string) error {
	return b.api.LLMConnector().ResolveConfirm(b.ctx(), requestID, decision)
}

// LLM_UpdateProviderCredential writes a new plaintext API key for
// profileID to the OS keychain and zeroes the in-memory buffer before
// returning (credential-store-01KQ8TDD WP05 / FR-007). The frontend
// ONLY calls this when the user has typed a new key value — the
// "leave blank to keep current" flow is preserved.
func (b *Bindings) LLM_UpdateProviderCredential(profileID, plaintext string) error {
	return b.api.LLMConnector().UpdateProviderCredential(b.ctx(), profileID, plaintext)
}

// LLM_GetAttachmentLimits returns the resolved per-provider attachment
// capability limits for the given provider kind + model. The frontend uses
// these to replace hard-coded byte caps in the attachment tray
// (multimodal-io-01KQ8TDF WP04 / FR-007).
func (b *Bindings) LLM_GetAttachmentLimits(provider, model string) (llm.AttachmentLimitsView, error) {
	return b.api.LLMConnector().GetAttachmentLimits(b.ctx(), provider, model)
}

// ── mcp ────────────────────────────────────────────────────────────────

func (b *Bindings) MCP_ListServers() ([]mcp.Server, error) {
	return b.api.MCP().ListServers(b.ctx())
}
func (b *Bindings) MCP_StartStream(id string) (string, error) {
	return b.api.MCP().StartStream(b.ctx(), id)
}
func (b *Bindings) MCP_StopStream(subID string) error {
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
	return b.api.MCP().HealthSnapshot(b.ctx())
}

// MCP_SubscribeHealthChanges registers a broker subscription for
// mcp:health-changed events. Returns a subscription id for use with
// MCP_StopStream.
// (mcp-server-health-ui-01KQ8TD6 WP02)
func (b *Bindings) MCP_SubscribeHealthChanges() (string, error) {
	return b.api.MCP().SubscribeHealthChanges(b.ctx())
}

// ── a2a ────────────────────────────────────────────────────────────────

func (b *Bindings) A2A_ListCards() ([]a2a.Card, error) {
	return b.api.A2A().ListCards(b.ctx())
}
func (b *Bindings) A2A_StartStream() (string, error) {
	return b.api.A2A().StartStream(b.ctx())
}
func (b *Bindings) A2A_StopStream(subID string) error {
	return b.api.A2A().StopStream(b.ctx(), subID)
}

// ── workflow ───────────────────────────────────────────────────────────

func (b *Bindings) Workflow_ListJobs() ([]workflow.Job, error) {
	return b.api.Workflow().ListJobs(b.ctx())
}
func (b *Bindings) Workflow_StartStream() (string, error) {
	return b.api.Workflow().StartStream(b.ctx())
}
func (b *Bindings) Workflow_StopStream(subID string) error {
	return b.api.Workflow().StopStream(b.ctx(), subID)
}

// ── trust ──────────────────────────────────────────────────────────────

func (b *Bindings) Trust_ListSecretReferences() ([]trust.SecretReference, error) {
	return b.api.Trust().ListSecretReferences(b.ctx())
}
func (b *Bindings) Trust_GetSecretReference(id string) (trust.SecretReference, error) {
	return b.api.Trust().GetSecretReference(b.ctx(), id)
}

// ── context ────────────────────────────────────────────────────────────

func (b *Bindings) Context_List() ([]contextview.ContextEntry, error) {
	return b.api.Context().List(b.ctx())
}
func (b *Bindings) Context_StartStream() (string, error) {
	return b.api.Context().StartStream(b.ctx())
}
func (b *Bindings) Context_StopStream(subID string) error {
	return b.api.Context().StopStream(b.ctx(), subID)
}

// ── contexts (library content pool — WP01) ────────────────────────────

func (b *Bindings) Contexts_List() (contextsview.Node, error) {
	return b.api.Contexts().List(b.ctx())
}
func (b *Bindings) Contexts_ListAll() (contextsview.Node, error) {
	return b.api.Contexts().ListAll(b.ctx())
}
func (b *Bindings) Contexts_Get(path string) (string, error) {
	return b.api.Contexts().Get(b.ctx(), path)
}
func (b *Bindings) Contexts_Save(path, content string) error {
	return b.api.Contexts().Save(b.ctx(), path, content)
}
func (b *Bindings) Contexts_CreateFolder(path string) error {
	return b.api.Contexts().CreateFolder(b.ctx(), path)
}
func (b *Bindings) Contexts_Rename(oldPath, newPath string) error {
	return b.api.Contexts().Rename(b.ctx(), oldPath, newPath)
}
func (b *Bindings) Contexts_Delete(path string) error {
	return b.api.Contexts().Delete(b.ctx(), path)
}
func (b *Bindings) Contexts_RecentlyApplied(limit int) ([]string, error) {
	return b.api.Contexts().RecentlyApplied(b.ctx(), limit)
}
func (b *Bindings) Contexts_RootPath() (string, error) {
	return b.api.Contexts().RootPath(b.ctx())
}

// ── bundle ─────────────────────────────────────────────────────────────

func (b *Bindings) Bundle_List() ([]bundle.Bundle, error) {
	return b.api.Bundle().List(b.ctx())
}
func (b *Bindings) Bundle_Get(id string) (bundle.Bundle, error) {
	return b.api.Bundle().Get(b.ctx(), id)
}
func (b *Bindings) Bundle_Install(req bundle.InstallRequest) (bundle.Bundle, error) {
	return b.api.Bundle().Install(b.ctx(), req)
}
func (b *Bindings) Bundle_Remove(id string) error {
	return b.api.Bundle().Remove(b.ctx(), id)
}

// ── policy ─────────────────────────────────────────────────────────────

func (b *Bindings) Policy_Explain(input map[string]any) (policy.Denial, error) {
	return b.api.Policy().Explain(b.ctx(), input)
}
func (b *Bindings) Policy_StartStream() (string, error) {
	return b.api.Policy().StartStream(b.ctx())
}
func (b *Bindings) Policy_StopStream(subID string) error {
	return b.api.Policy().StopStream(b.ctx(), subID)
}

// ── cedar policy panel (cedar-credential-policy-01KQ8TDE, WP02) ────────

// CedarPolicy_ListPolicies returns the per-file parse status for every
// .cedar source the engine has loaded. The frontend's Policy panel
// renders this list on mount and after a successful reload.
func (b *Bindings) CedarPolicy_ListPolicies() ([]cedarpolicyview.PolicyFile, error) {
	return b.api.CedarPolicy().ListPolicies(b.ctx())
}

// CedarPolicy_ReloadPolicies re-walks <DataDir>/policy/ and rebuilds
// the active policy bundle. Per-file parse failures are reported via
// the next CedarPolicy_ListPolicies call; errors do not abort reload.
func (b *Bindings) CedarPolicy_ReloadPolicies() error {
	return b.api.CedarPolicy().ReloadPolicies(b.ctx())
}

// CedarPolicy_RecentDecisions returns up to limit most-recent gate
// decisions, newest first. Used by the audit panel.
func (b *Bindings) CedarPolicy_RecentDecisions(limit int) ([]cedarpolicyview.Decision, error) {
	return b.api.CedarPolicy().RecentDecisions(b.ctx(), limit)
}

// ── permissions (cedar-credential-policy-01KQ8TDE, WP02) ───────────────

// Permissions_Resolve routes a modal decision (allow_once / allow_always
// / deny) back into the cedar prompt registry. requestID came in on one
// of the four `<family>:permission-pending` broker topics.
func (b *Bindings) Permissions_Resolve(requestID string, decision string) error {
	return b.api.Permissions().Resolve(b.ctx(), requestID, permissionsview.Decision(decision))
}

// Permissions_ListGrants enumerates accumulated grants — both persisted
// `<family>_allow_*.cedar` files in <DataDir>/policy/ and the per-process
// transient (Allow-once) cache. When family is non-empty ("bash" / "fs"
// / "cred" / "tool") only grants of that family are returned; empty
// string returns all four families.
func (b *Bindings) Permissions_ListGrants(family string) ([]permissionsview.Grant, error) {
	return b.api.Permissions().ListGrants(b.ctx(), family)
}

// Permissions_RevokeGrant removes a grant. Persisted grants delete the
// underlying .cedar file and trigger an engine reload; transient grants
// drop the in-memory cache entry.
func (b *Bindings) Permissions_RevokeGrant(grantID string) error {
	return b.api.Permissions().RevokeGrant(b.ctx(), grantID)
}

// Permissions_ListPending returns in-flight pending prompts. The
// frontend uses this to reconcile its modal queue on app start / after
// a hot reload.
func (b *Bindings) Permissions_ListPending() ([]permissionsview.PendingRequest, error) {
	return b.api.Permissions().ListPending(b.ctx())
}

// ── audit ──────────────────────────────────────────────────────────────

func (b *Bindings) Audit_ListEntries(filter audit.Filter) ([]audit.Entry, error) {
	return b.api.Audit().ListEntries(b.ctx(), filter)
}
func (b *Bindings) Audit_VerifyEntry(id string) (bool, error) {
	return b.api.Audit().VerifyEntry(b.ctx(), id)
}
func (b *Bindings) Audit_StartStream(filter audit.Filter) (string, error) {
	return b.api.Audit().StartStream(b.ctx(), filter)
}
func (b *Bindings) Audit_StopStream(subID string) error {
	return b.api.Audit().StopStream(b.ctx(), subID)
}

// ── settings ───────────────────────────────────────────────────────────

func (b *Bindings) Settings_Get() (settings.Settings, error) {
	return b.api.Settings().Get(b.ctx())
}
func (b *Bindings) Settings_Set(s settings.Settings) error {
	return b.api.Settings().Set(b.ctx(), s)
}

// Settings_GetMemory exposes the long-term-memory opt-in independently
// of the full settings round-trip. The frontend toggle reads / writes
// this so the privacy default (off) stays the cheap path.
func (b *Bindings) Settings_GetMemory() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadMemory()
}

func (b *Bindings) Settings_SetMemory(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveMemory(enabled)
}

// Settings_GetConfirmEach exposes the WP05 confirm-each tool-call
// modal opt-in flag (default true). The frontend toggle and the
// toolloop's per-Run flag check both read this.
func (b *Bindings) Settings_GetConfirmEach() (bool, error) {
	if b.storeFn == nil {
		return true, nil
	}
	return b.storeFn().LoadConfirmEach()
}

// Settings_SetConfirmEach persists the WP05 confirm-each opt-in flag.
func (b *Bindings) Settings_SetConfirmEach(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveConfirmEach(enabled)
}

// Settings_GetWebSearch exposes the local-first web-search built-in
// opt-in flag (default false). Surfaced as a toggle row in the Tools
// panel; toolloop reads this on every Run boundary so toggling takes
// effect on the next chat.
func (b *Bindings) Settings_GetWebSearch() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadWebSearch()
}

// Settings_SetWebSearch persists the web-search built-in opt-in flag.
func (b *Bindings) Settings_SetWebSearch(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveWebSearch(enabled)
}

// Settings_GetBash exposes the local-first bash built-in opt-in flag
// (default false). The bash tool is also gated by the per-command
// allowlist regardless of this toggle.
func (b *Bindings) Settings_GetBash() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadBash()
}

// Settings_SetBash persists the bash built-in opt-in flag.
func (b *Bindings) Settings_SetBash(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveBash(enabled)
}

// Settings_GetSaveArtifactEnabled exposes the kaneaz__save_artifact
// built-in opt-in flag. Default true (on) — saving deliverables is a
// low-risk primitive that should work on a fresh install. Surfaced as a
// toggle row in the Tools panel; toolloop reads this on every Run
// boundary so toggling takes effect on the next chat.
func (b *Bindings) Settings_GetSaveArtifactEnabled() (bool, error) {
	if b.storeFn == nil {
		return true, nil
	}
	return b.storeFn().LoadSaveArtifactEnabled()
}

// Settings_SetSaveArtifactEnabled persists the save_artifact built-in
// opt-in flag.
func (b *Bindings) Settings_SetSaveArtifactEnabled(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveSaveArtifactEnabled(enabled)
}

// Settings_GetFSRequestAccessEnabled exposes the
// kaneaz__request_filesystem_access built-in opt-in flag (default true).
// The toolloop EnabledFilter reads this on every Run boundary so toggling
// takes effect on the next chat.
func (b *Bindings) Settings_GetFSRequestAccessEnabled() (bool, error) {
	if b.storeFn == nil {
		return true, nil
	}
	return b.storeFn().LoadFSRequestAccessEnabled()
}

// Settings_SetFSRequestAccessEnabled persists the
// request_filesystem_access built-in opt-in flag.
func (b *Bindings) Settings_SetFSRequestAccessEnabled(enabled bool) error {
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
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadFSReadEnabled()
}

// Settings_SetFSReadEnabled persists the read-family filesystem tool opt-in.
func (b *Bindings) Settings_SetFSReadEnabled(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveFSReadEnabled(enabled)
}

// Settings_GetFSWriteEnabled exposes the write-family builtin filesystem
// tool opt-in (default false — write tools off until the user enables them).
func (b *Bindings) Settings_GetFSWriteEnabled() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadFSWriteEnabled()
}

// Settings_SetFSWriteEnabled persists the write-family filesystem tool opt-in.
func (b *Bindings) Settings_SetFSWriteEnabled(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveFSWriteEnabled(enabled)
}

// ── Todo tool dial (builtin-tools-search-and-elicitation-01KZNP3D WP07) ──

// Settings_GetTodoEnabled exposes the kaneaz__todo_write builtin opt-in
// (default false — tool off until the user enables it from the Tools panel).
// The toolloop's EnabledFilter consults this on every Run boundary so a
// toggle takes effect on the next chat turn.
func (b *Bindings) Settings_GetTodoEnabled() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadTodoEnabled()
}

// Settings_SetTodoEnabled persists the kaneaz__todo_write opt-in flag.
func (b *Bindings) Settings_SetTodoEnabled(enabled bool) error {
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
	if b.storeFn == nil {
		return 0, nil
	}
	return b.storeFn().LoadMaxAgentTurns()
}

// Settings_SetMaxAgentTurns persists the chat-graph LoopNode
// iteration cap. Zero clears the override (resets to the spec
// default); negatives are normalised to zero by the store.
func (b *Bindings) Settings_SetMaxAgentTurns(turns int) error {
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
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveMonthlyCostNotifyUSD(usd)
}

// ── WP08 permission dials ──────────────────────────────────────────

// Settings_GetPermissionMode returns the global permission posture.
// Default "normal" when unset.
func (b *Bindings) Settings_GetPermissionMode() (string, error) {
	if b.storeFn == nil {
		return "normal", nil
	}
	return b.storeFn().LoadPermissionMode()
}

// Settings_SetPermissionMode persists the global permission posture.
// Valid values: "strict", "normal", "permissive". Switching to
// "permissive" is gated by a confirm dialog on the frontend side.
func (b *Bindings) Settings_SetPermissionMode(mode string) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SavePermissionMode(mode)
}

// Settings_GetPermissionCacheDangerousOps returns the dangerous-ops
// override flag (default false).
func (b *Bindings) Settings_GetPermissionCacheDangerousOps() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadPermissionCacheDangerousOps()
}

// Settings_SetPermissionCacheDangerousOps persists the dangerous-ops
// override flag. Enabling requires a confirm dialog on the frontend.
func (b *Bindings) Settings_SetPermissionCacheDangerousOps(enabled bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SavePermissionCacheDangerousOps(enabled)
}

// Settings_GetBashAllowlistMigrated returns the WP10 migration
// marker. The UI reads this to suppress the one-time migration toast
// after WP10's first-boot migration has run.
func (b *Bindings) Settings_GetBashAllowlistMigrated() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadBashAllowlistMigrated()
}

// Settings_SetBashAllowlistMigrated marks the WP10 migration as done.
func (b *Bindings) Settings_SetBashAllowlistMigrated(migrated bool) error {
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveBashAllowlistMigrated(migrated)
}

// Settings_GetPermissionsMigrationToastShown returns the one-time toast
// shown marker.
func (b *Bindings) Settings_GetPermissionsMigrationToastShown() (bool, error) {
	if b.storeFn == nil {
		return false, nil
	}
	return b.storeFn().LoadPermissionsMigrationToastShown()
}

// Settings_SetPermissionsMigrationToastShown marks the migration toast
// as having been shown so it never appears again.
func (b *Bindings) Settings_SetPermissionsMigrationToastShown(shown bool) error {
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
	if b.storeFn == nil {
		return nil
	}
	return b.storeFn().SaveCedarStrictCredentialMode(enabled)
}

// Settings_GetShortcuts returns the full user-override keyboard shortcut
// map. Missing settings file returns an empty map (no error).
// (keyboard-shortcuts-settings-01KQ8TDR plan §2.7)
func (b *Bindings) Settings_GetShortcuts() (map[string]string, error) {
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
	return b.api.Settings().GetMCPAutoRestart(b.ctx())
}

// Settings_SetMCPAutoRestart persists the MCP auto-restart dial.
func (b *Bindings) Settings_SetMCPAutoRestart(enabled bool) error {
	return b.api.Settings().SetMCPAutoRestart(b.ctx(), enabled)
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
	id, model, err := b.api.Settings().GetEmbedderConfig(b.ctx())
	return EmbedderConfigResult{ProfileID: id, ModelOverride: model}, err
}

// Settings_SetEmbedderConfig persists the embedder provider selection
// and optional model override.  Empty strings reset to auto-pick /
// per-Kind-default behaviour.
func (b *Bindings) Settings_SetEmbedderConfig(profileID, modelOverride string) error {
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

// ── memory ─────────────────────────────────────────────────────────────

func (b *Bindings) Memory_ListChunks(filter memoryview.ListFilter) ([]memoryview.Chunk, error) {
	return b.api.Memory().ListChunks(b.ctx(), filter)
}

func (b *Bindings) Memory_RememberMessage(sessionID, messageID, scope string) (string, error) {
	return b.api.Memory().RememberMessage(b.ctx(), sessionID, messageID, scope)
}

func (b *Bindings) Memory_PromoteScope(chunkID, newScopeKind, newScopeID string) (string, error) {
	return b.api.Memory().PromoteScope(b.ctx(), chunkID, newScopeKind, newScopeID)
}

func (b *Bindings) Memory_Forget(id string) error {
	return b.api.Memory().Forget(b.ctx(), id)
}

func (b *Bindings) Memory_Pin(id string, pinned bool) error {
	return b.api.Memory().Pin(b.ctx(), id, pinned)
}

func (b *Bindings) Memory_JournalTail(scope string, sinceSeq int64, limit int) ([]memoryview.JournalEntry, error) {
	return b.api.Memory().JournalTail(b.ctx(), scope, sinceSeq, limit)
}

func (b *Bindings) Memory_PrunePreview(scope string) (memoryview.PrunePreview, error) {
	return b.api.Memory().PrunePreview(b.ctx(), scope)
}

func (b *Bindings) Memory_RunPruneNow(scope string) (memoryview.PruneStats, error) {
	return b.api.Memory().RunPruneNow(b.ctx(), scope)
}

// Memory_HealthSnapshot returns an at-a-glance health snapshot for the
// §2.4 memory health dashboard (memory-inspection-ui-01KX5R8E §2.4).
func (b *Bindings) Memory_HealthSnapshot() (memoryview.HealthSnapshot, error) {
	return b.api.Memory().HealthSnapshot(b.ctx())
}

// Memory_TestEmbedder probes the wired embedder against "hello world"
// and returns the resulting vector dimensions. Used by the §2.4
// "Test embedder" button.
func (b *Bindings) Memory_TestEmbedder() (int, error) {
	return b.api.Memory().TestEmbedder(b.ctx())
}

// Memory_CaptureRate returns a snapshot of the live memory capture
// velocity and embedder health for the §2.7 LegendBar pill.
func (b *Bindings) Memory_CaptureRate() (memoryview.CaptureRateSnapshot, error) {
	return b.api.Memory().CaptureRate(b.ctx())
}

// Memory_EmbedderEligibility inspects the configured provider profiles and
// returns eligibility metadata without constructing an Embedder. The
// frontend's Settings → Memory banner calls this on mount to determine
// whether to surface the "no memory provider" affordance.
func (b *Bindings) Memory_EmbedderEligibility() (memoryview.EmbedderEligibility, error) {
	return b.api.Memory().EmbedderEligibility(b.ctx())
}

// Memory_MarkImportant sets the user-pin counter for a chunk, making it
// a candidate for long-term scope promotion (memory-narrative-layer WP07).
func (b *Bindings) Memory_MarkImportant(chunkID string, pinned bool) error {
	return b.api.Memory().MarkImportant(b.ctx(), chunkID, pinned)
}

// Memory_NarrativeFailedCount returns the number of narrative synthesis
// jobs that have exhausted all retry attempts (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeFailedCount() (int, error) {
	return b.api.Memory().NarrativeFailedCount(b.ctx())
}

// Memory_NarrativeFailedList returns the narrative synthesis jobs that
// have exhausted all retry attempts (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeFailedList() ([]memoryview.NarrativeJobStatus, error) {
	return b.api.Memory().NarrativeFailedList(b.ctx())
}

// Memory_RetryFailedNarrative resets a failed narrative job so the
// Promoter worker will retry it (memory-narrative-layer WP07).
func (b *Bindings) Memory_RetryFailedNarrative(jobID string) error {
	return b.api.Memory().RetryFailedNarrative(b.ctx(), jobID)
}

// Memory_NarrativeMetricsForChunk returns the retrieval/citation/pin
// counters and computed promotion score for a single chunk
// (memory-narrative-layer WP07).
func (b *Bindings) Memory_NarrativeMetricsForChunk(chunkID string) (memoryview.NarrativeMetrics, error) {
	return b.api.Memory().NarrativeMetricsForChunk(b.ctx(), chunkID)
}

// ── Memory capstone (memory-inspection-ui-01KX5R8E) ───────────────────

// Memory_LastRetrieval returns the most recent retrieval report for the
// given session (§2.1 active-session retrieval inspector, FR-001).
func (b *Bindings) Memory_LastRetrieval(sessionID string) (memoryview.RetrievalReport, error) {
	return b.api.Memory().LastRetrieval(b.ctx(), sessionID)
}

// Memory_EmbeddingProbe embeds the given query and returns up to limit
// scored chunks ranked by cosine similarity (§2.2 embedding inspector,
// FR-003). limit is capped at 50 server-side.
func (b *Bindings) Memory_EmbeddingProbe(query string, limit int) ([]memoryview.ScoredChunk, error) {
	return b.api.Memory().EmbeddingProbe(b.ctx(), query, limit)
}

// Memory_ResummarizeChunk re-runs narrative synthesis on the chunk with
// the given ID (§2.3, FR-004). Rate-limited to one call per chunk per 60s.
func (b *Bindings) Memory_ResummarizeChunk(chunkID string) (memoryview.Chunk, error) {
	return b.api.Memory().ResummarizeChunk(b.ctx(), chunkID)
}

// Memory_GetChunkProvenance returns the full audit chain for a chunk
// (§2.6 provenance drawer, FR-007).
func (b *Bindings) Memory_GetChunkProvenance(chunkID string) (memoryview.ChunkProvenance, error) {
	return b.api.Memory().GetChunkProvenance(b.ctx(), chunkID)
}

// ── dials (Bundle E WP17) ──────────────────────────────────────────────

func (b *Bindings) Dials_Get(key dialsview.ScopeKey) (dialsview.DialConfig, error) {
	return b.api.Dials().GetDials(b.ctx(), key)
}

func (b *Bindings) Dials_Set(key dialsview.ScopeKey, cfg dialsview.DialConfig) error {
	return b.api.Dials().SetDials(b.ctx(), key, cfg)
}

func (b *Bindings) Dials_GetEffective(projectID, sessionID, graphID, runID string) (dialsview.EffectiveDials, error) {
	return b.api.Dials().GetEffective(b.ctx(), projectID, sessionID, graphID, runID)
}

func (b *Bindings) Dials_BumpAndResume(runID string, delta dialsview.DialDelta) error {
	return b.api.Dials().BumpAndResume(b.ctx(), runID, delta)
}

// ── projects ───────────────────────────────────────────────────────────

func (b *Bindings) Projects_List() ([]projectsview.Project, error) {
	return b.api.Projects().List(b.ctx())
}
func (b *Bindings) Projects_Get(id string) (projectsview.Project, error) {
	return b.api.Projects().Get(b.ctx(), id)
}
func (b *Bindings) Projects_Create(name, description string) (projectsview.Project, error) {
	return b.api.Projects().Create(b.ctx(), name, description)
}
func (b *Bindings) Projects_Rename(id, name string) error {
	return b.api.Projects().Rename(b.ctx(), id, name)
}
func (b *Bindings) Projects_UpdateDescription(id, description string) error {
	return b.api.Projects().UpdateDescription(b.ctx(), id, description)
}
func (b *Bindings) Projects_Delete(id string, deleteSessions bool) error {
	return b.api.Projects().Delete(b.ctx(), id, deleteSessions)
}
func (b *Bindings) Projects_AddSession(projectID, sessionID string) error {
	return b.api.Projects().AddSession(b.ctx(), projectID, sessionID)
}
func (b *Bindings) Projects_RemoveSession(sessionID string) error {
	return b.api.Projects().RemoveSession(b.ctx(), sessionID)
}
func (b *Bindings) Projects_ListSessions(projectID string) ([]projectsview.Session, error) {
	return b.api.Projects().ListSessions(b.ctx(), projectID)
}

// ── artifacts (artifacts-storage WP02) ────────────────────────────────

func (b *Bindings) Artifacts_List(filter artifactsview.ArtifactFilter) ([]artifactsview.Artifact, error) {
	return b.api.Artifacts().List(b.ctx(), filter)
}
func (b *Bindings) Artifacts_Get(id string) (artifactsview.ArtifactWithBytes, error) {
	return b.api.Artifacts().Get(b.ctx(), id)
}
func (b *Bindings) Artifacts_Promote(id, newScopeKind, newScopeID string) (artifactsview.Artifact, error) {
	return b.api.Artifacts().Promote(b.ctx(), id, newScopeKind, newScopeID)
}
func (b *Bindings) Artifacts_Delete(id string) error {
	return b.api.Artifacts().Delete(b.ctx(), id)
}

// Sessions_SaveAsArtifact is the user-facing manual-pin entry point
// (FR-006). The RPC routes through the artifacts view so the
// frontend's "Save as artifact" right-click action calls
// Sessions_SaveAsArtifact and receives the persisted artifact row.
func (b *Bindings) Sessions_SaveAsArtifact(sessionID, messageID, title string, sourceRangeStart, sourceRangeEnd int) (artifactsview.Artifact, error) {
	return b.api.Artifacts().SaveFromMessage(b.ctx(), sessionID, messageID, title, sourceRangeStart, sourceRangeEnd)
}

// ── attachments (context_attachments — WP03) ──────────────────────────

func (b *Bindings) Attachments_List(scopeKind, scopeID string) ([]attachmentsview.Attachment, error) {
	return b.api.Attachments().List(b.ctx(), scopeKind, scopeID)
}
func (b *Bindings) Attachments_ListResolved(sessionID string) ([]attachmentsview.Attachment, error) {
	return b.api.Attachments().ListResolved(b.ctx(), sessionID)
}
func (b *Bindings) Attachments_Add(in attachmentsview.AddInput) (attachmentsview.Attachment, error) {
	return b.api.Attachments().Add(b.ctx(), in)
}
func (b *Bindings) Attachments_AddMedia(in attachmentsview.AddMediaInput) (attachmentsview.Attachment, error) {
	return b.api.Attachments().AddMedia(b.ctx(), in)
}
func (b *Bindings) Attachments_Remove(id string) error {
	return b.api.Attachments().Remove(b.ctx(), id)
}
func (b *Bindings) Attachments_Reorder(scopeKind, scopeID string, idsInOrder []string) error {
	return b.api.Attachments().Reorder(b.ctx(), scopeKind, scopeID, idsInOrder)
}
func (b *Bindings) Attachments_Refresh(id string) (attachmentsview.Attachment, error) {
	return b.api.Attachments().Refresh(b.ctx(), id)
}

// ── hooks ──────────────────────────────────────────────────────────────

func (b *Bindings) Hooks_List() ([]hooksview.Hook, error) {
	return b.api.Hooks().List(b.ctx())
}
func (b *Bindings) Hooks_Get(id string) (hooksview.Hook, error) {
	return b.api.Hooks().Get(b.ctx(), id)
}
func (b *Bindings) Hooks_Add(in hooksview.HookInput) (hooksview.Hook, error) {
	return b.api.Hooks().Add(b.ctx(), in)
}
func (b *Bindings) Hooks_Update(in hooksview.HookInput) error {
	return b.api.Hooks().Update(b.ctx(), in)
}
func (b *Bindings) Hooks_Remove(id string) error {
	return b.api.Hooks().Remove(b.ctx(), id)
}
func (b *Bindings) Hooks_AvailableBuiltins() ([]hooksview.BuiltinDescriptor, error) {
	return b.api.Hooks().AvailableBuiltins(b.ctx())
}
func (b *Bindings) Hooks_InstallStarterMemory() error {
	return b.api.Hooks().InstallStarterMemoryHooks(b.ctx())
}
func (b *Bindings) Hooks_RemoveStarterMemory() error {
	return b.api.Hooks().RemoveStarterMemoryHooks(b.ctx())
}
func (b *Bindings) Hooks_DryRun(hookID string, syntheticPayload string) (hooksview.DryRunResult, error) {
	return b.api.Hooks().DryRun(b.ctx(), hookID, syntheticPayload)
}

// ── tools (MCP recipes) ────────────────────────────────────────────────

func (b *Bindings) Tools_ListRecipes() ([]tools.RecipeListing, error) {
	return b.api.Tools().ListRecipes(b.ctx())
}

func (b *Bindings) Tools_InstallRecipe(id string, env map[string]string, config map[string]any) (stdio.RecipeStatus, error) {
	return b.api.Tools().InstallRecipe(b.ctx(), id, env, config)
}

func (b *Bindings) Tools_UninstallRecipe(id string) error {
	return b.api.Tools().UninstallRecipe(b.ctx(), id)
}

func (b *Bindings) Tools_ForgetRecipeKey(id, envName string) error {
	return b.api.Tools().ForgetRecipeKey(b.ctx(), id, envName)
}

func (b *Bindings) Tools_RecipeStatus(id string) (stdio.RecipeStatus, error) {
	return b.api.Tools().RecipeStatus(b.ctx(), id)
}

func (b *Bindings) Tools_RecipeConfig(id string) (map[string]any, error) {
	return b.api.Tools().RecipeConfig(b.ctx(), id)
}

// Artifacts_SaveAs opens the OS-native save-file dialog and writes
// the artifact's bytes to the user-chosen path. Returns the absolute
// path written, or empty string if the user cancels. The bytes are
// resolved server-side from the media store so the round-trip never
// re-base64-encodes on the JS side (which is fragile in some webviews
// for large or binary blobs).
func (b *Bindings) Artifacts_SaveAs(id, suggestedName string) (string, error) {
	// Pull the artifact + its bytes through the existing view path.
	withBytes, err := b.api.Artifacts().Get(b.ctx(), id)
	if err != nil {
		return "", err
	}
	if suggestedName == "" {
		suggestedName = withBytes.Artifact.Title
	}
	if suggestedName == "" {
		suggestedName = id
	}
	dest, err := wruntime.SaveFileDialog(b.ctx(), wruntime.SaveDialogOptions{
		Title:           "Save artifact",
		DefaultFilename: suggestedName,
	})
	if err != nil {
		return "", err
	}
	if dest == "" {
		// User cancelled; soft return.
		return "", nil
	}
	if err := os.WriteFile(dest, withBytes.Bytes, 0o644); err != nil {
		return "", err
	}
	return dest, nil
}

// Tools_RequestAdditionalAllowedDir is the Wails binding for the runtime
// "expand filesystem access" flow. The model's kaneaz__request_filesystem_access
// built-in calls this via its delegate; it can also be called directly from
// the frontend if a future UI surface needs it.
//
// Returns { granted, expanded, message } so the caller knows whether to
// retry its original filesystem operation using the canonicalised path.
func (b *Bindings) Tools_RequestAdditionalAllowedDir(recipeID, path, reason string) (tools.FSAccessResult, error) {
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

// Tools_PickDirectory opens the OS-native folder-picker dialog and
// returns the absolute path the user selected. Returns the empty
// string when the user cancels (Wails surfaces cancel as a no-error
// empty result). The default-directory hint nudges the dialog to a
// useful starting point — pass "" to let the OS decide. Used by the
// recipe-install modal's allowed_directories chip list so the user
// can pick a folder graphically instead of typing an absolute path.
func (b *Bindings) Tools_PickDirectory(title, defaultDir string) (string, error) {
	if title == "" {
		title = "Choose a directory"
	}
	return wruntime.OpenDirectoryDialog(b.ctx(), wruntime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
	})
}

// ── shell escape (chat input `!cmd` feature) ──────────────────────────

// BashExecResult mirrors the JSON the kaneaz__bash tool returns.
// stdout/stderr are plain strings (UTF-8); the bash tool already caps
// at 64 KiB per stream and signals truncation via the `Truncated` flag.
type BashExecResult struct {
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	ExitCode  int    `json:"exitCode"`
	Truncated bool   `json:"truncated"`
}

// Bash_Exec runs a single command line through the kaneaz__bash
// built-in tool and returns its result. Used by the chat input's
// `!cmd` shell-escape — typing `!ls -la ~/Desktop` dispatches here
// (not to the LLM) and the parent renders the result inline as a
// synthetic system message. The Cedar gate applies normally.
//
// sessionID is threaded through ctx so the bash tool's per-session
// run-id cache picks up the right slot. Empty sessionID is OK (the
// tool's run-id cache is best-effort).
func (b *Bindings) Bash_Exec(sessionID, command string) (BashExecResult, error) {
	type builtinsHolder interface{ Builtins() *toolloop.BuiltinRegistry }
	holder, ok := b.api.(builtinsHolder)
	if !ok || holder.Builtins() == nil {
		return BashExecResult{}, errors.New("rpc: bash tool registry not wired")
	}
	tool, ok := holder.Builtins().Lookup("kaneaz__bash")
	if !ok {
		return BashExecResult{}, errors.New("rpc: kaneaz__bash tool not registered (toggle on in Settings → Tools)")
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
	return b.api.Shell().PathComplete(b.ctx(), partial)
}

func (b *Bindings) Shell_ReadFile(path string) (ShellReadFileResult, error) {
	data, mt, err := b.api.Shell().ReadFile(b.ctx(), path)
	if err != nil {
		return ShellReadFileResult{}, err
	}
	return ShellReadFileResult{
		DataBase64: base64.StdEncoding.EncodeToString(data),
		MediaType:  mt,
	}, nil
}

// Shell_PickFile opens the OS-native file-picker dialog and returns the
// absolute path of the file the user selected. Returns the empty string
// when the user cancels (Wails surfaces cancel as a no-error empty result).
//
// Parameters:
//   - title:      dialog window title; defaults to "Choose a file" if empty.
//   - defaultDir: starting directory hint; pass "" to let the OS decide.
//   - filters:    optional MIME / extension hints passed to the OS picker.
//     Each entry is "Display Name|*.ext1;*.ext2" (Wails format). Pass nil
//     for no filtering.
//
// Used by the AskUserQuestion dialog's "file" kind so the user can select a
// file path graphically (WP10) rather than typing the path manually.
func (b *Bindings) Shell_PickFile(title, defaultDir string, filters []string) (string, error) {
	if title == "" {
		title = "Choose a file"
	}
	var wailsFilters []wruntime.FileFilter
	for _, f := range filters {
		// Each filter string is "Display Name|*.ext1;*.ext2"
		pipe := strings.Index(f, "|")
		if pipe < 0 {
			wailsFilters = append(wailsFilters, wruntime.FileFilter{
				DisplayName: f,
				Pattern:     f,
			})
		} else {
			wailsFilters = append(wailsFilters, wruntime.FileFilter{
				DisplayName: f[:pipe],
				Pattern:     f[pipe+1:],
			})
		}
	}
	return wruntime.OpenFileDialog(b.ctx(), wruntime.OpenDialogOptions{
		Title:            title,
		DefaultDirectory: defaultDir,
		Filters:          wailsFilters,
	})
}

// shellAPIType keeps the shell import alive — Wails-bound methods
// only return primitive errors here, so we anchor the import to the
// view-API interface so a future binding addition with a typed
// argument doesn't have to re-wire the input.
type shellAPIType = shell.ShellAPI

// ── slash commands ────────────────────────────────────────────────────

func (b *Bindings) Slash_Execute(sessionID, raw string) (slashview.ExecuteResult, error) {
	return b.api.Slash().Execute(b.ctx(), sessionID, raw)
}

func (b *Bindings) Slash_List() ([]slashview.CommandInfo, error) {
	return b.api.Slash().List(b.ctx())
}

// ── user command RPCs ─────────────────────────────────────────────────

func (b *Bindings) Slashcmd_List(projectID string) ([]slashview.UserCommandSummaryWire, error) {
	return b.api.Slash().UserList(b.ctx(), projectID)
}

func (b *Bindings) Slashcmd_Get(name, projectID string) (slashview.UserCommandWire, error) {
	return b.api.Slash().UserGet(b.ctx(), name, projectID)
}

func (b *Bindings) Slashcmd_Save(cmd slashview.UserCommandWire) error {
	return b.api.Slash().UserSave(b.ctx(), cmd)
}

func (b *Bindings) Slashcmd_Delete(name, projectID string) error {
	return b.api.Slash().UserDelete(b.ctx(), name, projectID)
}

func (b *Bindings) Slashcmd_Run(name string, args map[string]string, sessionID, projectID, cwd, selection string) (slashview.RunResultWire, error) {
	return b.api.Slash().UserRun(b.ctx(), name, args, sessionID, projectID, cwd, selection)
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
	return []FeatureFlagInfo{
		{
			Name:        "user-slash-commands",
			Enabled:     coreslashcmd.UserSlashcmdEnabled(),
			Description: "User-defined / commands (text expansions, tool dispatch, prompt templates).",
			EnvVar:      "HARNESS_USER_SLASHCMD",
		},
	}, nil
}

// ── corpora (agent-kernel-graph; Bundle C WP10/WP11) ──────────────────

func (b *Bindings) Corpus_ListCorpora(scope string) ([]corpusview.Corpus, error) {
	return b.api.Corpus().ListCorpora(b.ctx(), scope)
}
func (b *Bindings) Corpus_CreateCorpus(req corpusview.CreateRequest) (corpusview.Corpus, error) {
	return b.api.Corpus().CreateCorpus(b.ctx(), req)
}
func (b *Bindings) Corpus_GetCorpus(corpusID string) (corpusview.Corpus, error) {
	return b.api.Corpus().GetCorpus(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_DeleteCorpus(corpusID string) error {
	return b.api.Corpus().DeleteCorpus(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_ListFiles(corpusID string) ([]corpusview.CorpusFile, error) {
	return b.api.Corpus().ListFiles(b.ctx(), corpusID)
}
func (b *Bindings) Corpus_ListChunks(corpusID, fileID string) ([]corpusview.Chunk, error) {
	return b.api.Corpus().ListChunks(b.ctx(), corpusID, fileID)
}
func (b *Bindings) Corpus_IngestPath(corpusID, path string, opts corpusview.IngestOptions) (corpusview.IngestStatus, error) {
	return b.api.Corpus().IngestPath(b.ctx(), corpusID, path, opts)
}
func (b *Bindings) Corpus_JobStatus(jobID string) (corpusview.IngestStatus, error) {
	return b.api.Corpus().JobStatus(b.ctx(), jobID)
}
func (b *Bindings) Corpus_Retrieve(corpusID string, req corpusview.RetrieveRequest) (corpusview.RetrieveResponse, error) {
	return b.api.Corpus().Retrieve(b.ctx(), corpusID, req)
}

// ── agent graph (mission agent-kernel-graph; Bundle A WP06) ─────────

func (b *Bindings) Graph_ListGraphs(scope string) ([]graphview.GraphInfo, error) {
	return b.api.Graph().ListGraphs(b.ctx(), scope)
}
func (b *Bindings) Graph_LoadGraph(id string) (graphview.GraphSpec, error) {
	return b.api.Graph().LoadGraph(b.ctx(), id)
}
func (b *Bindings) Graph_SaveGraph(spec graphview.GraphSpec) error {
	return b.api.Graph().SaveGraph(b.ctx(), spec)
}
func (b *Bindings) Graph_DeleteGraph(id string) error {
	return b.api.Graph().DeleteGraph(b.ctx(), id)
}
func (b *Bindings) Graph_Validate(yaml string) (graphview.ValidationResult, error) {
	return b.api.Graph().Validate(b.ctx(), yaml)
}
func (b *Bindings) Graph_StartRun(req graphview.StartRunRequest) (graphview.StartRunResponse, error) {
	return b.api.Graph().StartRun(b.ctx(), req)
}
func (b *Bindings) Graph_GetRunStatus(runID string) (graphview.RunStatus, error) {
	return b.api.Graph().GetRunStatus(b.ctx(), runID)
}
func (b *Bindings) Graph_GetRunTrace(runID string, since int64) ([]graphview.RunTraceEvent, error) {
	return b.api.Graph().GetRunTrace(b.ctx(), runID, since)
}
func (b *Bindings) Graph_Resume(runID, askResponse string) error {
	return b.api.Graph().Resume(b.ctx(), runID, askResponse)
}
func (b *Bindings) Graph_CancelRun(runID string) error {
	return b.api.Graph().CancelRun(b.ctx(), runID)
}

// ── compaction (agent-kernel-graph; Bundle D WP12/WP13) ───────────────

func (b *Bindings) Compaction_GetConfig(layer compactionview.Layer, scopeID string) (compactionview.Config, error) {
	return b.api.Compaction().GetConfig(b.ctx(), layer, scopeID)
}
func (b *Bindings) Compaction_GetEffective(scope compactionview.ScopeKey) (compactionview.EffectiveConfig, error) {
	return b.api.Compaction().GetEffective(b.ctx(), scope)
}
func (b *Bindings) Compaction_SetConfig(layer compactionview.Layer, scopeID string, cfg compactionview.Config) error {
	return b.api.Compaction().SetConfig(b.ctx(), layer, scopeID, cfg)
}
func (b *Bindings) Compaction_TriggerManual(sessionID string, opts compactionview.ManualOpts) (compactionview.ManualResult, error) {
	return b.api.Compaction().TriggerManualCompaction(b.ctx(), sessionID, opts)
}
func (b *Bindings) Compaction_ListCustomStrategies() ([]compactionview.CustomStrategy, error) {
	return b.api.Compaction().ListCustomStrategies(b.ctx())
}

// Compaction_GetTierExplain returns the static tier-explain payload the
// Settings panel renders in the "What does this mean?" disclosure on
// the compaction-aggressiveness dial (mission
// compaction-strategy-ui-01KQ8TDI §2.2 / §2.9). The numerics come from
// core/compaction.Tier() so the engine and UI never drift.
func (b *Bindings) Compaction_GetTierExplain() ([]compactionview.TierExplain, error) {
	return b.api.Compaction().GetTierExplain(b.ctx())
}

// ── branches (agent-kernel-graph; Bundle B WP07/08) ───────────────────

func (b *Bindings) Branches_List(parentSessionID string) ([]branchesview.Branch, error) {
	return b.api.Branches().ListBranches(b.ctx(), parentSessionID)
}
func (b *Bindings) Branches_Create(opts branchesview.CreateBranchOptions) (branchesview.Branch, error) {
	return b.api.Branches().CreateBranch(b.ctx(), opts)
}
func (b *Bindings) Branches_GetStatus(branchID string) (branchesview.BranchStatus, error) {
	return b.api.Branches().GetBranchStatus(b.ctx(), branchID)
}
func (b *Bindings) Branches_Merge(branchID string) error {
	return b.api.Branches().MergeBranch(b.ctx(), branchID)
}
func (b *Bindings) Branches_Abandon(branchID string) error {
	return b.api.Branches().AbandonBranch(b.ctx(), branchID)
}
func (b *Bindings) Branches_RecommendModel(parentSessionID, taskHint, preference string) (branchesview.RecommendedModel, error) {
	return b.api.Branches().RecommendModel(b.ctx(), parentSessionID, taskHint, preference)
}
func (b *Bindings) Branches_ProposeReintegrationSummary(branchSessionID string) (branchesview.ReintegrationProposal, error) {
	return b.api.Branches().ProposeReintegrationSummary(b.ctx(), branchSessionID)
}
func (b *Bindings) Branches_CommitReintegration(opts branchesview.CommitReintegrationOptions) error {
	return b.api.Branches().CommitReintegration(b.ctx(), opts)
}
func (b *Bindings) Branches_SetAdvisorDismissed(sessionID string, dismissed bool) error {
	return b.api.Branches().SetAdvisorDismissed(b.ctx(), sessionID, dismissed)
}

// Branches_ListWithBranchTree returns a flat list of sessions with parent
// pointers for all branches in a project. The frontend builds the visual
// tree by grouping on parentSessionId. BranchDepth is pre-computed.
// (branching-ux-polish-01KQ8TD7 WP02/WP03)
func (b *Bindings) Branches_ListWithBranchTree(projectID string) ([]branchesview.SessionWithBranchPointer, error) {
	return b.api.Branches().ListWithBranchTree(b.ctx(), projectID)
}

// ── workflows (mission workflows-01KQ8TDG, v0.3.0 beta) ───────────────

func (b *Bindings) Workflows_List() ([]workflowsview.Summary, error) {
	return b.api.Workflows().List(b.ctx())
}
func (b *Bindings) Workflows_Get(id string) (workflowsview.Workflow, error) {
	return b.api.Workflows().Get(b.ctx(), id)
}
func (b *Bindings) Workflows_Run(id string, inputs map[string]string) (workflowsview.RunResult, error) {
	return b.api.Workflows().Run(b.ctx(), id, inputs)
}
func (b *Bindings) Workflows_Save(in workflowsview.SaveInput) (workflowsview.SaveOutput, error) {
	return b.api.Workflows().Save(b.ctx(), in)
}
func (b *Bindings) Workflows_Delete(id string) error {
	return b.api.Workflows().Delete(b.ctx(), id)
}

// ── workflow scheduler (workflows-agentic-01KW2D3X WP02) ──────────────

func (b *Bindings) Workflows_ScheduleSet(in workflowsview.ScheduleSetInput) error {
	return b.api.Workflows().ScheduleSet(b.ctx(), in)
}
func (b *Bindings) Workflows_ScheduleClear(workflowID string) error {
	return b.api.Workflows().ScheduleClear(b.ctx(), workflowID)
}
func (b *Bindings) Workflows_ScheduleList() ([]workflowsview.ScheduleEntry, error) {
	return b.api.Workflows().ScheduleList(b.ctx())
}
func (b *Bindings) Workflows_RunNow(workflowID string) (workflowsview.RunSummary, error) {
	return b.api.Workflows().RunNow(b.ctx(), workflowID)
}

// ── workflow scheduled-inbox (workflow-extensions-01KW2D3Y WP01) ──────

func (b *Bindings) Workflows_ScheduleRunHistory(workflowID string, limit int) ([]workflowsview.RunSummary, error) {
	return b.api.Workflows().ScheduleRunHistory(b.ctx(), workflowID, limit)
}
func (b *Bindings) Workflows_ScheduleNextFire(workflowID string) (string, error) {
	t, err := b.api.Workflows().ScheduleNextFire(b.ctx(), workflowID)
	if err != nil || t.IsZero() {
		return "", err
	}
	return t.UTC().Format("2006-01-02T15:04:05Z"), nil
}
func (b *Bindings) Workflows_CancelRun(runID string) error {
	return b.api.Workflows().CancelRun(b.ctx(), runID)
}

// ── update (mission auto-update, v0.4.0 WP03) ─────────────────────────
//
// TODO: regenerate via `wails generate module` once the WP04 + WP05 UI
// lands. Hand-added in WP03 so the frontend updateClient.ts has typed
// bindings to consume.

func (b *Bindings) Update_Status() (updateview.StatusOutput, error) {
	return b.api.Update().Status(b.ctx())
}
func (b *Bindings) Update_StartCheck() error {
	return b.api.Update().StartCheck(b.ctx())
}
func (b *Bindings) Update_StartDownload() error {
	return b.api.Update().StartDownload(b.ctx())
}
func (b *Bindings) Update_Apply() error {
	return b.api.Update().Apply(b.ctx())
}
func (b *Bindings) Update_SkipVersion(version string) error {
	return b.api.Update().SkipVersion(b.ctx(), version)
}
func (b *Bindings) Update_ListSkippedVersions() ([]string, error) {
	return b.api.Update().ListSkippedVersions(b.ctx())
}
func (b *Bindings) Update_UnskipVersion(version string) error {
	return b.api.Update().UnskipVersion(b.ctx(), version)
}

// ── nodes (manifest-driven node catalog; WP07) ────────────────────────

func (b *Bindings) Nodes_Catalog() ([]nodesview.NodeManifestSummary, error) {
	return b.api.Nodes().Catalog(b.ctx())
}
func (b *Bindings) Nodes_Get(id string) (nodesview.NodeManifestDetail, error) {
	return b.api.Nodes().Get(b.ctx(), id)
}
func (b *Bindings) Nodes_ReloadOverrides() (nodesview.ReloadResult, error) {
	return b.api.Nodes().ReloadOverrides(b.ctx())
}
func (b *Bindings) Nodes_ListUserOverrides() ([]nodesview.UserOverrideInfo, error) {
	return b.api.Nodes().ListUserOverrides(b.ctx())
}
func (b *Bindings) Nodes_Doctor() (nodesview.DoctorReport, error) {
	return b.api.Nodes().Doctor(b.ctx())
}

// ── cedarpolicy (snippet writer/revoker; WP09) ────────────────────────

// CedarPolicy_WriteSnippet writes body to <DataDir>/policy/<name> after
// validating the filename. Triggers engine reload best-effort.
func (b *Bindings) CedarPolicy_WriteSnippet(name string, body string) error {
	return b.api.CedarPolicy().WritePolicySnippet(b.ctx(), name, body)
}

// CedarPolicy_RevokeSnippet deletes <DataDir>/policy/<name> after
// validating the filename. Triggers engine reload best-effort.
func (b *Bindings) CedarPolicy_RevokeSnippet(name string) error {
	return b.api.CedarPolicy().RevokePolicySnippet(b.ctx(), name)
}

// CedarPolicy_ResolvePropose delivers a user decision (accept | reject)
// for a pending cedar-policy proposal surfaced by the CedarProposeModal.
// requestID came in on the "cedar:propose-pending" broker topic.
func (b *Bindings) CedarPolicy_ResolvePropose(requestID string, decision string) error {
	return b.api.CedarProposeResolve(requestID, decision)
}

// ── cedarpolicy editor (cedar-policy-editor-ui-01KQ8TD6 WP01) ────────────

// CedarPolicy_Get reads the source of a single policy file.
// For embedded defaults, reads from the embedded FS and returns ReadOnly=true.
// For on-disk user policies, reads from <DataDir>/policy/<name>.
func (b *Bindings) CedarPolicy_Get(name string) (cedarpolicyview.PolicyFileDetail, error) {
	return b.api.CedarPolicy().GetPolicy(b.ctx(), name)
}

// CedarPolicy_Save validates source via the Cedar parser and, on success,
// atomically writes it to <DataDir>/policy/<name>. Returns ParseResult with
// diagnostics; never writes when parse fails.
func (b *Bindings) CedarPolicy_Save(name string, source string) (cedarpolicyview.ParseResult, error) {
	return b.api.CedarPolicy().SavePolicy(b.ctx(), name, source)
}

// CedarPolicy_Delete removes <DataDir>/policy/<name>. Fails for protected
// embedded defaults.
func (b *Bindings) CedarPolicy_Delete(name string) error {
	return b.api.CedarPolicy().DeletePolicy(b.ctx(), name)
}

// CedarPolicy_Validate parses source without touching disk. Used by the
// editor's debounced live-validation indicator.
func (b *Bindings) CedarPolicy_Validate(source string) (cedarpolicyview.ParseResult, error) {
	return b.api.CedarPolicy().ValidatePolicy(b.ctx(), source)
}

// CedarPolicy_InstallTemplate copies a shipped Cedar template from the
// embedded policies/ directory to <DataDir>/policy/<destName>.
// Returns an error when the destination already exists.
func (b *Bindings) CedarPolicy_InstallTemplate(templateName string, destName string) (cedarpolicyview.PolicyFileDetail, error) {
	return b.api.CedarPolicy().InstallTemplate(b.ctx(), templateName, destName)
}

// ── search (cross-session-search mission) ─────────────────────────────

// Search_Sessions executes a full-text search across all session
// messages via the FTS5 messages_fts virtual table.
//
// query is sanitised (empty/whitespace → empty result). filters is
// optional; zero values mean "no filter".
func (b *Bindings) Search_Sessions(query string, filters searchview.SearchFilters) ([]searchview.SearchHit, error) {
	return b.api.Search().Search(b.ctx(), query, filters)
}

// ── autonomy (autonomy-dial-01KR3M2A WP03) ────────────────────────────

// Settings_GetAutonomy returns the persisted global autonomy.Layer.
func (b *Bindings) Settings_GetAutonomy() (autonomy.Layer, error) {
	return b.api.Settings().LoadAutonomyProfile(b.ctx())
}

// Settings_SetAutonomy persists the global autonomy.Layer.
func (b *Bindings) Settings_SetAutonomy(layer autonomy.Layer) error {
	return b.api.Settings().SaveAutonomyProfile(b.ctx(), layer)
}

// Projects_GetAutonomy returns the project's persisted autonomy.Layer
// override.
func (b *Bindings) Projects_GetAutonomy(projectID string) (autonomy.Layer, error) {
	return b.api.Projects().LoadAutonomyProfile(b.ctx(), projectID)
}

// Projects_SetAutonomy persists the project's autonomy.Layer override.
func (b *Bindings) Projects_SetAutonomy(projectID string, layer autonomy.Layer) error {
	return b.api.Projects().SaveAutonomyProfile(b.ctx(), projectID, layer)
}

// Sessions_GetAutonomy returns the session's persisted autonomy.Layer
// override.
func (b *Bindings) Sessions_GetAutonomy(sessionID string) (autonomy.Layer, error) {
	return b.api.Sessions().LoadAutonomyProfile(b.ctx(), sessionID)
}

// Sessions_SetAutonomy persists the session's autonomy.Layer override.
func (b *Bindings) Sessions_SetAutonomy(sessionID string, layer autonomy.Layer) error {
	return b.api.Sessions().SaveAutonomyProfile(b.ctx(), sessionID, layer)
}

// Sessions_ResolveAutonomy folds global → project → session layers.
func (b *Bindings) Sessions_ResolveAutonomy(sessionID string) (sessions.ResolvedAutonomy, error) {
	return b.api.Sessions().ResolveAutonomy(b.ctx(), sessionID)
}

// ── storage health (v0.5.1 migration-doctor) ───────────────────────────

// Storage_GetMigrationDriftReport reads the harness_migrations ledger and
// the registered migration set, compares them, and returns every
// discrepancy. Never modifies the database. Wired to the Settings → Health
// panel's MigrationDriftPanel.vue component.
func (b *Bindings) Storage_GetMigrationDriftReport() (storageview.DriftReport, error) {
	return b.api.Storage().GetMigrationDriftReport(b.ctx())
}

// Storage_ApplyDriftFix repairs an id_mismatch drift entry for the given
// version. It backs up data.db, then UPDATEs the ledger row's id to the
// expected value. Returns an error for ledger_only / code_only entries
// (those require manual intervention).
func (b *Bindings) Storage_ApplyDriftFix(version int) error {
	return b.api.Storage().ApplyDriftFix(b.ctx(), version)
}

// ── onboarding (harness-self-mcp-onboarding-01KQ8TDU WP08) ───────────────

// Onboarding_State returns the boot-time OnboardingState the frontend reads
// to decide whether to mount the dialog (firstRun) or show the
// "Reconfigure with assistant" entry in Settings.
func (b *Bindings) Onboarding_State() (onboardingview.OnboardingState, error) {
	return b.api.Onboarding().State(b.ctx())
}

// Onboarding_Begin returns the initial FSM card without consuming an event.
// The OnboardingDialog calls this when it mounts so the welcome screen
// renders immediately.
func (b *Bindings) Onboarding_Begin() (onboardingview.StepResponse, error) {
	return b.api.Onboarding().Begin(b.ctx())
}

// Onboarding_Step advances the Phase-1 FSM by one event (e.g. pick-provider,
// enter-api-key, test-connection). Returns the next Card descriptor.
func (b *Bindings) Onboarding_Step(req onboardingview.StepRequest) (onboardingview.StepResponse, error) {
	return b.api.Onboarding().Step(b.ctx(), req)
}

// Onboarding_Dismiss marks onboarding as completed so the dialog will not
// auto-show again. Idempotent.
func (b *Bindings) Onboarding_Dismiss() error {
	return b.api.Onboarding().Dismiss(b.ctx())
}

// Onboarding_RestartPhase2 spawns a kind=onboarding session with the
// harness-self MCP enabled and the chosen starter's system prompt. Called
// when the user picks a starter from the dialog OR clicks "Reconfigure with
// assistant" in Settings. Returns the new session id.
func (b *Bindings) Onboarding_RestartPhase2(req onboardingview.RestartPhase2Request) (onboardingview.RestartPhase2Response, error) {
	return b.api.Onboarding().RestartPhase2(b.ctx(), req)
}

// Onboarding_ListStarters returns the curated starter prompts (title,
// description, recommended provider/model/recipes). The dialog renders
// these as cards; the system-prompt body is withheld and resolved
// server-side when RestartPhase2 fires.
func (b *Bindings) Onboarding_ListStarters() ([]onboardingview.StarterSummary, error) {
	return b.api.Onboarding().ListStarters(b.ctx())
}

// ── elicit (ask-user-question-interactive-01KZNP3G, WP04) ─────────────

// Elicit_SubmitAnswer resolves a pending ask-user-question elicitation.
// requestID was emitted on the "elicit:pending" broker topic when the
// model called kaneaz__ask_user_question; answerJSON is the user's answer
// (a JSON-encoded value matching the question kind), or null when
// cancelled is true. The blocking OpenDialog call on the Go side returns
// after this method resolves the pending channel.
func (b *Bindings) Elicit_SubmitAnswer(requestID string, answerJSON json.RawMessage, cancelled bool) error {
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
	return b.api.Elicit().SubmitWizardStep(b.ctx(), requestID, questionID, answerJSON, dismissed)
}

// Elicit_RegisterDeferred registers a deferred ask for a session and returns
// immediately with DeferredResult{Deferred:true, AskID:…} (WP06).
// The model can use AskID to retrieve the answer later via
// __ask_get_result(ask_id). A chat-header pill appears for the user.
func (b *Bindings) Elicit_RegisterDeferred(sessionID string, req elicitview.ElicitRequest) (elicitview.DeferredResult, error) {
	return b.api.Elicit().RegisterDeferred(b.ctx(), sessionID, req)
}

// Elicit_AnswerDeferred records the user's answer for a deferred ask (WP06).
// Returns the system_reminder text to inject into the next LLM turn.
func (b *Bindings) Elicit_AnswerDeferred(askID string, answer any) (string, error) {
	return b.api.Elicit().AnswerDeferred(b.ctx(), askID, answer)
}

// Elicit_ListPending returns in-flight elicitation request IDs so the
// frontend can reconcile its dialog queue on reconnect / hot reload.
func (b *Bindings) Elicit_ListPending() ([]elicitview.ElicitRequest, error) {
	return b.api.Elicit().ListPending(b.ctx())
}
