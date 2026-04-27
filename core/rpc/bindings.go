package rpc

import (
	"context"
	"encoding/base64"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	graphview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/agentgraph"
	artifactsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/artifacts"
	attachmentsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/attachments"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	contextsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/contexts"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	corpusview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/corpus"
	hooksview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/hooks"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	memoryview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/memory"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	projectsview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/projects"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/shell"
	slashview "github.com/sigil-tech/kaneaz-harness/core/rpc/views/slashcmd"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/tools"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
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

// shellAPIType keeps the shell import alive — Wails-bound methods
// only return primitive errors here, so we anchor the import to the
// view-API interface so a future binding addition with a typed
// argument doesn't have to re-wire the import.
type shellAPIType = shell.ShellAPI

// ── slash commands ────────────────────────────────────────────────────

func (b *Bindings) Slash_Execute(sessionID, raw string) (slashview.ExecuteResult, error) {
	return b.api.Slash().Execute(b.ctx(), sessionID, raw)
}

func (b *Bindings) Slash_List() ([]slashview.CommandInfo, error) {
	return b.api.Slash().List(b.ctx())
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
