package sync

import (
	"context"
	"errors"
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
	consent  UploadConsent
	optIns   OptInStore
}

// SetOptInStore wires persistence for the per-category opt-ins
// (Settings.SettingsSyncEnabledCategories). Called once from rpc.New(),
// followed by RestoreOptIns.
func (a *API) SetOptInStore(o OptInStore) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.optIns = o
}

func (a *API) optInStore() OptInStore {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.optIns
}

// RestoreOptIns re-applies the persisted opt-ins to the syncer. rpc.New
// calls it at boot, after every category is registered and the upload gate
// is installed, and before the first poll (StartPolling's first tick is
// syncBaseInterval away). It never uploads (fleet.Syncer.RestoreEnabled):
// a restored opt-in is not upload consent, so held categories keep waiting
// for Sync_ConfirmUploads. A persisted category this build does not
// register (e.g. slash_commands with its feature gate off) is skipped and
// kept on disk, so it comes back when the category does.
func (a *API) RestoreOptIns() (restored []string, err error) {
	o := a.optInStore()
	if o == nil || a.syncer == nil {
		return nil, nil
	}
	cats, err := o.EnabledCategories()
	if err != nil {
		return nil, err
	}
	for _, c := range cats {
		cat := corefleet.SyncCategory(c)
		if !a.syncer.HasCategory(cat) {
			continue
		}
		if err := a.syncer.RestoreEnabled(cat, true); err != nil {
			return restored, err
		}
		restored = append(restored, c)
	}
	return restored, nil
}

// SetUploadConsent wires the persisted upload confirmation
// (Settings.SettingsSyncUploadsConfirmed) and installs it as the syncer's
// upload gate, so Push holds every category until the user confirms.
// Called once from rpc.New(). A nil consent keeps uploads closed (the Syncer default).
func (a *API) SetUploadConsent(c UploadConsent) {
	a.mu.Lock()
	a.consent = c
	a.mu.Unlock()
	if a.syncer == nil {
		return
	}
	if c == nil {
		a.syncer.SetUploadGate(nil)
		return
	}
	a.syncer.SetUploadGate(c.UploadsConfirmed)
}

func (a *API) uploadConsent() UploadConsent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.consent
}

// Sync_ConfirmUploads implements SyncAPI.
func (a *API) Sync_ConfirmUploads(ctx context.Context) error {
	c := a.uploadConsent()
	if c == nil {
		return ErrUploadConsentUnavailable
	}
	if err := c.ConfirmUploads(); err != nil {
		return err
	}
	if a.syncer == nil {
		return nil
	}
	// Best effort: the confirmation is saved; an upload that fails now
	// (offline, signed out) shows in that category's Sync_Status error and
	// retries on the next change or Push.
	_ = a.syncer.PushHeld(ctx)
	return nil
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
	cat := corefleet.SyncCategory(category)
	if !a.syncer.HasCategory(cat) {
		return fmt.Errorf("fleet/sync: unknown category %q", category)
	}
	// Persist first: the choice must survive a restart even when the
	// immediate push below fails (offline, signed out, uploads held), and a
	// choice that could not be saved must not take effect, or the panel
	// would show a state the next launch silently drops.
	if o := a.optInStore(); o != nil {
		if err := o.SetCategoryEnabled(category, enabled); err != nil {
			return fmt.Errorf("sync: save %s choice: %w", category, err)
		}
	}
	return a.syncer.SetEnabled(ctx, cat, enabled)
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
			out[i] = enrichWithRegistry(SyncStatusView{
				Category:  cat,
				LocalOnly: !corefleet.FleetAcceptsSyncCategory(corefleet.SyncCategory(cat)),
			}, registry, cat)
		}
		return out, nil
	}
	raw := a.syncer.Status()
	out := make([]SyncStatusView, len(raw))
	blocked, unreadable := a.uploadsBlockedReason()
	for i, s := range raw {
		v := enrichWithRegistry(syncStatusToView(s), registry, string(s.Category))
		if blocked != "" && v.Enabled && !v.LocalOnly && !v.NotSyncingYet {
			v.UploadsBlockedReason = blocked
			v.SettingsUnreadable = unreadable
			v.AwaitingConfirmation = false
		}
		out[i] = v
	}
	return out, nil
}

// uploadsBlockedReason is non-empty when the upload confirmation cannot be
// read, so uploads are held for a reason the user cannot fix by
// confirming. Without this the gate failed closed silently: the panel
// asked the user to "Start syncing", and confirming could not take effect.
// unreadable is true when the reason is the settings file itself, which
// "Reset settings" can recover from.
func (a *API) uploadsBlockedReason() (reason string, unreadable bool) {
	c := a.uploadConsent()
	if c == nil {
		return "Settings sync cannot upload: this build has no settings store to record your confirmation.", false
	}
	if _, err := c.UploadState(); err != nil {
		if errors.Is(err, ErrUploadConsentUnavailable) {
			return "Settings sync cannot upload: this build has no settings store to record your confirmation.", false
		}
		return "Settings sync is paused: Kenaz could not read your settings file, so it cannot tell whether you confirmed uploads (" +
			err.Error() + "). Uploads resume as soon as the file can be read again.", true
	}
	return "", false
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
		if kind.UserSyncNotBuilt {
			v.NotSyncingYet = true
		}
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
	// The user's explicit Push: also resolves a conflict in favour of
	// this device (fleet.Syncer.ForcePush).
	return a.syncer.ForcePush(ctx, corefleet.SyncCategory(category))
}

// Sync_ForcePull implements SyncAPI.
func (a *API) Sync_ForcePull(ctx context.Context, category string) error {
	if a.syncer == nil {
		return corefleet.ErrFleetDisabled
	}
	// The user's explicit Pull: also resolves a conflict in favour of
	// the other device (fleet.Syncer.ForcePull).
	return a.syncer.ForcePull(ctx, corefleet.SyncCategory(category))
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
		Category:             string(s.Category),
		Enabled:              s.Enabled,
		LastError:            s.LastError,
		AwaitingConfirmation: s.AwaitingConfirmation,
		LocalOnly:            s.LocalOnly,
		Conflict:             s.Conflict,
		NotSyncingYet:        s.NotSyncingYet,
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
