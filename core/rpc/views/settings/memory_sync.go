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
	// FleetError is the last settings-fetch error ("" on success).
	FleetError string `json:"fleetError,omitempty"`
	// Lane is the memory_sync lane health.
	Lane FleetSyncLaneView `json:"lane"`
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
		FleetError:            s.FleetError,
		Lane:                  laneToView(s.Lane),
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

// FleetMemorySyncDisable implements SettingsAPI.
func (a *API) FleetMemorySyncDisable(ctx context.Context, deleteFromFleet bool, confirm string) (MemorySyncStatusView, error) {
	ms := a.memorySync.Load()
	if ms == nil {
		return MemorySyncStatusView{}, ErrMemorySyncNotWired
	}
	if deleteFromFleet {
		if _, err := ms.ForgetAll(ctx, confirm); err != nil {
			return MemorySyncStatusView{}, err
		}
	}
	if err := ms.Disable(ctx); err != nil {
		return MemorySyncStatusView{}, err
	}
	return memorySyncView(ms.Status(ctx)), nil
}
