package sync

import (
	"context"
	"fmt"
	"time"

	corefleet "github.com/kameas-ai/kenaz-harness/core/fleet"
)

// API implements SyncAPI backed by fleet.Syncer.
type API struct {
	syncer  *corefleet.Syncer
	pending *corefleet.SecretPromptQueue
}

var _ SyncAPI = (*API)(nil)

// NewAPI constructs a SyncAPI. syncer and pending may be nil; all methods
// degrade gracefully when fleet is disabled or not yet wired.
func NewAPI(syncer *corefleet.Syncer, pending *corefleet.SecretPromptQueue) *API {
	return &API{syncer: syncer, pending: pending}
}

// Sync_Toggle implements SyncAPI.
func (a *API) Sync_Toggle(ctx context.Context, category string, enabled bool) error {
	if a.syncer == nil {
		return corefleet.ErrFleetDisabled
	}
	return a.syncer.SetEnabled(ctx, corefleet.SyncCategory(category), enabled)
}

// Sync_Status implements SyncAPI.
func (a *API) Sync_Status(_ context.Context) ([]SyncStatusView, error) {
	if a.syncer == nil {
		// Return empty status for all categories when fleet is disabled.
		cats := corefleet.AllSyncCategories()
		out := make([]SyncStatusView, len(cats))
		for i, cat := range cats {
			out[i] = SyncStatusView{Category: string(cat)}
		}
		return out, nil
	}
	raw := a.syncer.Status()
	out := make([]SyncStatusView, len(raw))
	for i, s := range raw {
		out[i] = syncStatusToView(s)
	}
	return out, nil
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
