package settings

// Fleet learned-memory sync settings surface (memory-sync-01MEMSY01 WP08,
// contract H9): opt-in with consent, scope choice (long_term + global
// only), "also delete from Fleet" (forget-all), usage, sync_blocked count
// and lane health. The lane itself is core/fleet.MemorySync, built in
// core/rpc and installed here with SetMemorySync.

import (
	"context"
	"errors"

	"github.com/kameas-ai/kenaz-harness/core/fleet"
)

// ErrMemorySyncNotWired is returned when the harness booted without a
// memory store / sync lane.
var ErrMemorySyncNotWired = errors.New("settings: memory sync is not available on this install")

// MemorySyncStatusView is the wire shape of the memory-sync settings panel.
type MemorySyncStatusView struct {
	// Wired is false when this install has no memory store / lane.
	Wired bool `json:"wired"`
	// Entitled mirrors the memory_sync capability; the panel hides the
	// toggle without it.
	Entitled bool `json:"entitled"`
	// Enabled is this device's opt-in (follows Fleet's user-level setting
	// whenever the panel reads it).
	Enabled bool `json:"enabled"`
	// Scopes are Fleet's enabled sync scopes ([] when unknown).
	Scopes         []string `json:"scopes"`
	ConsentVersion string   `json:"consentVersion"`
	OptedInAt      string   `json:"optedInAt"`
	// CurrentConsentVersion is the disclosure version this build shows.
	CurrentConsentVersion string `json:"currentConsentVersion"`
	LiveRecords           int    `json:"liveRecords"`
	LiveBytes             int64  `json:"liveBytes"`
	MaxRecords            int    `json:"maxRecords"`
	MaxBytes              int64  `json:"maxBytes"`
	// BlockedCount is local chunks Fleet refused permanently (e.g.
	// secret_detected) — they stay on this device only.
	BlockedCount int `json:"blockedCount"`
	// PendingCount is local sync-scope chunks not yet accepted by Fleet.
	PendingCount int `json:"pendingCount"`
	// OlderNotes is unstamped notes held back until the one-time line is
	// answered (legal D164); OlderNotesAccount is who they would sync to.
	OlderNotes        int    `json:"olderNotes"`
	OlderNotesAccount string `json:"olderNotesAccount,omitempty"`
	// LocalErasePending: erased from Fleet, not yet from this device.
	LocalErasePending bool `json:"localErasePending,omitempty"`
	// UnresolvedNotes stay on this device: the keychain couldn't be read when
	// they were written.
	UnresolvedNotes int `json:"unresolvedNotes"`
	// InheritedErase, InheritedOptOut: an earlier version left that request
	// unfinished. Shown until dismissed.
	InheritedErase  bool `json:"inheritedErase,omitempty"`
	InheritedOptOut bool `json:"inheritedOptOut,omitempty"`
	// FleetError is the last settings-fetch error ("" on success).
	FleetError string `json:"fleetError,omitempty"`
	// Lane is the memory_sync lane health.
	Lane FleetSyncLaneView `json:"lane"`
	// OrgPaused: a staff "pause paid features" hold is on the org
	// (kenaz-fleet #206). The panel shows the paused copy instead of
	// hiding (entitled is false while paused) and keeps the data-rights
	// actions — turn off, delete from Fleet — enabled.
	OrgPaused      bool   `json:"orgPaused"`
	PausedCategory string `json:"pausedCategory,omitempty"`
	// NoticeHold: uploads wait until the memory-access notice is read in
	// this org (legal D168(2)). Never set while no text is registered.
	// NoticeHoldReason is unread, text_refused or check_failed.
	NoticeHold       bool   `json:"noticeHold"`
	NoticeHoldReason string `json:"noticeHoldReason,omitempty"`
}

// SetMemorySync installs the lane (nil uninstalls).
func (a *API) SetMemorySync(ms *fleet.MemorySync) {
	if a == nil {
		return
	}
	a.memorySync.Store(ms)
}

func memorySyncView(s fleet.MemorySyncStatus) MemorySyncStatusView {
	v := MemorySyncStatusView{
		Wired:                 true,
		Entitled:              s.Entitled,
		Enabled:               s.LocalEnabled,
		Scopes:                []string{},
		CurrentConsentVersion: fleet.MemoryConsentVersion,
		BlockedCount:          s.BlockedCount,
		PendingCount:          s.PendingPush,
		OlderNotes:            s.OlderNotes,
		OlderNotesAccount:     s.OlderNotesAccount,
		UnresolvedNotes:       s.UnresolvedNotes,
		LocalErasePending:     s.LocalErasePending,
		InheritedErase:        s.InheritedErase,
		InheritedOptOut:       s.InheritedOptOut,
		FleetError:            s.FleetError,
		Lane:                  laneToView(s.Lane),
		OrgPaused:             s.OrgPaused,
		PausedCategory:        s.PausedCategory,
		NoticeHold:            s.NoticeHold,
		NoticeHoldReason:      s.NoticeHoldReason,
	}
	if f := s.Fleet; f != nil {
		if f.Scopes != nil {
			v.Scopes = append(v.Scopes, f.Scopes...)
		}
		v.ConsentVersion, v.OptedInAt = f.ConsentVersion, f.OptedInAt
		v.LiveRecords, v.LiveBytes = f.Usage.LiveRecords, f.Usage.LiveBytes
		v.MaxRecords, v.MaxBytes = f.Usage.MaxRecords, f.Usage.MaxBytes
	}
	return v
}

// FleetMemorySyncStatus implements SettingsAPI.
func (a *API) FleetMemorySyncStatus(ctx context.Context) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{Scopes: []string{}, CurrentConsentVersion: fleet.MemoryConsentVersion}, nil
	}
	return memorySyncView(ms.Status(ctx)), nil
}

// FleetMemorySyncEnable implements SettingsAPI.
func (a *API) FleetMemorySyncEnable(ctx context.Context, scopes []string, consentVersion string) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{}, ErrMemorySyncNotWired
	}
	if _, err := ms.Enable(ctx, scopes, consentVersion); err != nil {
		return MemorySyncStatusView{}, err
	}
	return memorySyncView(ms.Status(ctx)), nil
}

// FleetMemorySyncContinueOlderNotes implements SettingsAPI.
func (a *API) FleetMemorySyncContinueOlderNotes(ctx context.Context) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{}, ErrMemorySyncNotWired
	}
	if err := ms.ContinueOlderNotes(ctx); err != nil {
		return MemorySyncStatusView{}, err
	}
	return memorySyncView(ms.Status(ctx)), nil
}

// FleetMemorySyncDismissNotice implements SettingsAPI.
func (a *API) FleetMemorySyncDismissNotice(ctx context.Context, notice string) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{}, ErrMemorySyncNotWired
	}
	if err := ms.DismissNotice(ctx, notice); err != nil {
		return MemorySyncStatusView{}, err
	}
	return memorySyncView(ms.Status(ctx)), nil
}

// FleetMemorySyncDisable implements SettingsAPI.
func (a *API) FleetMemorySyncDisable(ctx context.Context, deleteFromFleet bool, confirm string) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{}, ErrMemorySyncNotWired
	}
	if _, err := ms.Disable(ctx, deleteFromFleet, confirm); err != nil {
		return MemorySyncStatusView{}, err
	}
	return memorySyncView(ms.Status(ctx)), nil
}
