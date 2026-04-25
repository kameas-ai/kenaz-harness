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
type Bindings struct {
	api     HarnessAPI
	storeFn func() settings.SettingsStore // injected; nil until WP13 wires
}

// NewBindings constructs the Wails-reflected surface.
func NewBindings(api HarnessAPI) *Bindings {
	return &Bindings{api: api}
}

// ── top-level cross-cutting ────────────────────────────────────────────

func (b *Bindings) ShellStatus(ctx context.Context) (ShellStatus, error) {
	return b.api.ShellStatus(ctx)
}

func (b *Bindings) AppInfo(ctx context.Context) (AppInfo, error) {
	return b.api.AppInfo(ctx)
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

func (b *Bindings) Sessions_List(ctx context.Context) ([]sessions.Session, error) {
	return b.api.Sessions().List(ctx)
}
func (b *Bindings) Sessions_Get(ctx context.Context, id string) (sessions.Session, error) {
	return b.api.Sessions().Get(ctx, id)
}
func (b *Bindings) Sessions_Create(ctx context.Context, name string) (sessions.Session, error) {
	return b.api.Sessions().Create(ctx, name)
}
func (b *Bindings) Sessions_Rename(ctx context.Context, id, name string) error {
	return b.api.Sessions().Rename(ctx, id, name)
}
func (b *Bindings) Sessions_Delete(ctx context.Context, id string) error {
	return b.api.Sessions().Delete(ctx, id)
}
func (b *Bindings) Sessions_Reorder(ctx context.Context, ids []string) error {
	return b.api.Sessions().Reorder(ctx, ids)
}
func (b *Bindings) Sessions_StartStream(ctx context.Context, id string) (string, error) {
	return b.api.Sessions().StartStream(ctx, id)
}
func (b *Bindings) Sessions_StopStream(ctx context.Context, subID string) error {
	return b.api.Sessions().StopStream(ctx, subID)
}

// ── llm ────────────────────────────────────────────────────────────────

func (b *Bindings) LLM_ListProviders(ctx context.Context) ([]llm.Provider, error) {
	return b.api.LLMConnector().ListProviders(ctx)
}
func (b *Bindings) LLM_StartStream(ctx context.Context, id string) (string, error) {
	return b.api.LLMConnector().StartStream(ctx, id)
}
func (b *Bindings) LLM_StopStream(ctx context.Context, subID string) error {
	return b.api.LLMConnector().StopStream(ctx, subID)
}

// ── mcp ────────────────────────────────────────────────────────────────

func (b *Bindings) MCP_ListServers(ctx context.Context) ([]mcp.Server, error) {
	return b.api.MCP().ListServers(ctx)
}
func (b *Bindings) MCP_StartStream(ctx context.Context, id string) (string, error) {
	return b.api.MCP().StartStream(ctx, id)
}
func (b *Bindings) MCP_StopStream(ctx context.Context, subID string) error {
	return b.api.MCP().StopStream(ctx, subID)
}

// ── a2a ────────────────────────────────────────────────────────────────

func (b *Bindings) A2A_ListCards(ctx context.Context) ([]a2a.Card, error) {
	return b.api.A2A().ListCards(ctx)
}
func (b *Bindings) A2A_StartStream(ctx context.Context) (string, error) {
	return b.api.A2A().StartStream(ctx)
}
func (b *Bindings) A2A_StopStream(ctx context.Context, subID string) error {
	return b.api.A2A().StopStream(ctx, subID)
}

// ── workflow ───────────────────────────────────────────────────────────

func (b *Bindings) Workflow_ListJobs(ctx context.Context) ([]workflow.Job, error) {
	return b.api.Workflow().ListJobs(ctx)
}
func (b *Bindings) Workflow_StartStream(ctx context.Context) (string, error) {
	return b.api.Workflow().StartStream(ctx)
}
func (b *Bindings) Workflow_StopStream(ctx context.Context, subID string) error {
	return b.api.Workflow().StopStream(ctx, subID)
}

// ── trust ──────────────────────────────────────────────────────────────

func (b *Bindings) Trust_ListSecretReferences(ctx context.Context) ([]trust.SecretReference, error) {
	return b.api.Trust().ListSecretReferences(ctx)
}
func (b *Bindings) Trust_GetSecretReference(ctx context.Context, id string) (trust.SecretReference, error) {
	return b.api.Trust().GetSecretReference(ctx, id)
}

// ── context ────────────────────────────────────────────────────────────

func (b *Bindings) Context_List(ctx context.Context) ([]contextview.ContextEntry, error) {
	return b.api.Context().List(ctx)
}
func (b *Bindings) Context_StartStream(ctx context.Context) (string, error) {
	return b.api.Context().StartStream(ctx)
}
func (b *Bindings) Context_StopStream(ctx context.Context, subID string) error {
	return b.api.Context().StopStream(ctx, subID)
}

// ── bundle ─────────────────────────────────────────────────────────────

func (b *Bindings) Bundle_List(ctx context.Context) ([]bundle.Bundle, error) {
	return b.api.Bundle().List(ctx)
}
func (b *Bindings) Bundle_Get(ctx context.Context, id string) (bundle.Bundle, error) {
	return b.api.Bundle().Get(ctx, id)
}

// ── policy ─────────────────────────────────────────────────────────────

func (b *Bindings) Policy_Explain(ctx context.Context, input map[string]any) (policy.Denial, error) {
	return b.api.Policy().Explain(ctx, input)
}
func (b *Bindings) Policy_StartStream(ctx context.Context) (string, error) {
	return b.api.Policy().StartStream(ctx)
}
func (b *Bindings) Policy_StopStream(ctx context.Context, subID string) error {
	return b.api.Policy().StopStream(ctx, subID)
}

// ── audit ──────────────────────────────────────────────────────────────

func (b *Bindings) Audit_ListEntries(ctx context.Context, filter audit.Filter) ([]audit.Entry, error) {
	return b.api.Audit().ListEntries(ctx, filter)
}
func (b *Bindings) Audit_VerifyEntry(ctx context.Context, id string) (bool, error) {
	return b.api.Audit().VerifyEntry(ctx, id)
}
func (b *Bindings) Audit_StartStream(ctx context.Context, filter audit.Filter) (string, error) {
	return b.api.Audit().StartStream(ctx, filter)
}
func (b *Bindings) Audit_StopStream(ctx context.Context, subID string) error {
	return b.api.Audit().StopStream(ctx, subID)
}

// ── settings ───────────────────────────────────────────────────────────

func (b *Bindings) Settings_Get(ctx context.Context) (settings.Settings, error) {
	return b.api.Settings().Get(ctx)
}
func (b *Bindings) Settings_Set(ctx context.Context, s settings.Settings) error {
	return b.api.Settings().Set(ctx, s)
}
