package sync

import (
	"context"
	"fmt"
	"sync"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// API implements SyncAPI backed by fleet.Syncer.
type API struct {
	syncer  *corefleet.Syncer
	pending *corefleet.SecretPromptQueue

	mu       sync.RWMutex
	registry *corefleet.KindRegistry
}

var _ SyncAPI = (*API)(nil)

// NewAPI constructs a SyncAPI. syncer and pending may be nil; all methods
// degrade gracefully when fleet is disabled or not yet wired.
func NewAPI(syncer *corefleet.Syncer, pending *corefleet.SecretPromptQueue) *API {
	return &API{syncer: syncer, pending: pending}
}

// SetSyncKindRegistry wires the SyncKind registry into the API so
// Sync_Status can enrich each row with its declared Scopes and org
// provenance (fleet-generic-sync-framework-01NSYNC02 WP06, FR-007).
//
// Called from rpc.New() AFTER registerSyncCategories / registerSlash
// CommandsSyncKind have populated the registry — mirrors settingsImpl's
// SetSyncKindRegistry (core/rpc/views/settings/fleet.go), which holds the
// same pointer for the org_config apply path. Safe to skip: a nil
// registry just means Scopes/OrgAppliedAt stay empty on every row,
// preserving the pre-WP06 SyncStatusView shape.
func (a *API) SetSyncKindRegistry(registry *corefleet.KindRegistry) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.registry = registry
}

func (a *API) kindRegistry() *corefleet.KindRegistry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.registry
}

// Sync_Toggle implements SyncAPI.
func (a *API) Sync_Toggle(ctx context.Context, category string, enabled bool) error {
	if a.syncer == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.syncer.SetEnabled(ctx, corefleet.SyncCategory(category), enabled)
}

// Sync_Status implements SyncAPI.
//
// fleet-generic-sync-framework-01NSYNC02 WP06: every row is enriched with
// its registered Scopes and org provenance when a KindRegistry is wired
// (SetSyncKindRegistry). Before this, the fleet-DISABLED fallback branch
// below iterated the hardcoded five-category AllSyncCategories() list —
// syncer.Status() itself already enumerated every REGISTERED category
// generically (fleet.Syncer.RegisterCategory lazily creates state for any
// category, WP01's genericity fix), so a kind registered after the five
// built-ins (e.g. slash_commands, WP05) was already present in the
// syncer!=nil path; it just carried no Scopes/provenance, and the
// frontend's own hardcoded CATEGORIES array (SyncPanel.vue, fixed in this
// same WP) never rendered a row for it regardless.
func (a *API) Sync_Status(_ context.Context) ([]SyncStatusView, error) {
	registry := a.kindRegistry()
	if a.syncer == nil {
		// Fleet disabled: no *fleet.Syncer to enumerate live category
		// state from. Prefer the registry's ID list when one is wired
		// (it reflects every kind actually registered, including
		// non-built-in ones), falling back to the historical hardcoded
		// five-category list only when no registry is available either.
		var cats []string
		if registry != nil && len(registry.IDs()) > 0 {
			cats = registry.IDs()
		} else {
			for _, c := range corefleet.AllSyncCategories() {
				cats = append(cats, string(c))
			}
		}
		out := make([]SyncStatusView, len(cats))
		for i, cat := range cats {
			out[i] = enrichWithRegistry(SyncStatusView{Category: cat}, registry, cat)
		}
		return out, nil
	}
	raw := a.syncer.Status()
	out := make([]SyncStatusView, len(raw))
	for i, s := range raw {
		out[i] = enrichWithRegistry(syncStatusToView(s), registry, string(s.Category))
	}
	return out, nil
}

// enrichWithRegistry adds Scopes + OrgAppliedAt to v from registry, when a
// registry is wired and has a registration for category. A nil registry
// or an unregistered category id (a stale/removed kind) leaves v
// unchanged — the pre-WP06 SyncStatusView shape.
func enrichWithRegistry(v SyncStatusView, registry *corefleet.KindRegistry, category string) SyncStatusView {
	if registry == nil {
		return v
	}
	if kind, ok := registry.Kind(category); ok {
		scopes := make([]string, 0, len(kind.Scopes))
		for _, s := range kind.Scopes {
			scopes = append(scopes, string(s))
		}
		v.Scopes = scopes
	}
	if at, ok := registry.OrgAppliedAt(category); ok {
		v.OrgAppliedAt = at.UTC().Format(time.RFC3339)
	}
	return v
}

// Sync_ForcePush implements SyncAPI.
func (a *API) Sync_ForcePush(ctx context.Context, category string) error {
	if a.syncer == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.syncer.Push(ctx, corefleet.SyncCategory(category))
}

// Sync_ForcePull implements SyncAPI.
func (a *API) Sync_ForcePull(ctx context.Context, category string) error {
	if a.syncer == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.syncer.Pull(ctx, corefleet.SyncCategory(category))
}

// Sync_PendingMCPSecrets implements SyncAPI.
func (a *API) Sync_PendingMCPSecrets(_ context.Context) ([]PendingMCPSecret, error) {
	if a.pending == nil {
		return nil, nil
	}
	items := a.pending.Snapshot()
	out := make([]PendingMCPSecret, len(items))
	for i, it := range items {
		out[i] = PendingMCPSecret{
			MCPID:              it.ID,
			RecipeID:           it.RecipeID,
			RequiresSecretKeys: it.RequiresSecretKeys,
		}
	}
	return out, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func syncStatusToView(s corefleet.SyncStatus) SyncStatusView {
	v := SyncStatusView{
		Category:  string(s.Category),
		Enabled:   s.Enabled,
		LastError: s.LastError,
	}
	if !s.LastPushAt.IsZero() {
		v.LastPushAt = s.LastPushAt.UTC().Format(time.RFC3339)
	}
	if !s.LastPullAt.IsZero() {
		v.LastPullAt = s.LastPullAt.UTC().Format(time.RFC3339)
	}
	return v
}

// ErrUnknownCategory is a sentinel for unknown sync category names.
var ErrUnknownCategory = fmt.Errorf("sync: unknown category")
