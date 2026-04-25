package rpc

import (
	"context"

	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/a2a"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/audit"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/bundle"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/contextview"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/llm"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/mcp"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/policy"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/sessions"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/settings"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/trust"
	"github.com/sigil-tech/kaneaz-harness/core/rpc/views/workflow"
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
func (b *Bindings) Sessions_Reorder(ids []string) error {
	return b.api.Sessions().Reorder(b.ctx(), ids)
}
func (b *Bindings) Sessions_StartStream(id string) (string, error) {
	return b.api.Sessions().StartStream(b.ctx(), id)
}
func (b *Bindings) Sessions_StopStream(subID string) error {
	return b.api.Sessions().StopStream(b.ctx(), subID)
}

// ── llm ────────────────────────────────────────────────────────────────

func (b *Bindings) LLM_ListProviders() ([]llm.Provider, error) {
	return b.api.LLMConnector().ListProviders(b.ctx())
}
func (b *Bindings) LLM_StartStream(id string) (string, error) {
	return b.api.LLMConnector().StartStream(b.ctx(), id)
}
func (b *Bindings) LLM_StopStream(subID string) error {
	return b.api.LLMConnector().StopStream(b.ctx(), subID)
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
