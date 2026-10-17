// sync_optins.go — persists the per-category settings-sync opt-ins
// (Settings.SettingsSyncEnabledCategories).
//
// fleet.Syncer kept each category's enabled flag in memory only, so every
// launch turned every category off again while the Sync panel just showed
// them off: a toggle that silently forgot the user's choice. Sync_Toggle now
// writes the choice here, and rpc.New re-applies it at boot through
// syncview.API.RestoreOptIns. Restoring is not upload consent: uploads stay
// behind Settings.SettingsSyncUploadsConfirmed (sync_upload_consent.go).
package rpc

import (
	"sort"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
)

// settingsSyncOptIns implements syncview.OptInStore over the settings store.
type settingsSyncOptIns struct {
	store settings.SettingsStore
}

var _ syncview.OptInStore = settingsSyncOptIns{}

func newSettingsSyncOptIns(store settings.SettingsStore) settingsSyncOptIns {
	return settingsSyncOptIns{store: store}
}

// EnabledCategories returns the persisted opt-ins. No store means none.
func (o settingsSyncOptIns) EnabledCategories() ([]string, error) {
	if o.store == nil {
		return nil, nil
	}
	s, err := o.store.LoadAll()
	if err != nil {
		return nil, err
	}
	return append([]string(nil), s.SettingsSyncEnabledCategories...), nil
}

// SetCategoryEnabled adds or removes category from the persisted set.
func (o settingsSyncOptIns) SetCategoryEnabled(category string, enabled bool) error {
	if o.store == nil {
		return nil
	}
	// Under the store lock (settings.Update), so concurrent toggles and
	// other settings writes cannot lose each other's changes.
	return settings.Update(o.store, func(s *settings.Settings) error {
		set := make(map[string]struct{}, len(s.SettingsSyncEnabledCategories)+1)
		for _, c := range s.SettingsSyncEnabledCategories {
			set[c] = struct{}{}
		}
		if enabled {
			set[category] = struct{}{}
		} else {
			delete(set, category)
		}
		out := make([]string, 0, len(set))
		for c := range set {
			out = append(out, c)
		}
		sort.Strings(out)
		s.SettingsSyncEnabledCategories = out
		return nil
	})
}

// wireSyncOptIns is rpc.New's boot step: install the opt-in store on the
// Sync view and re-apply the persisted choices. No store, no persistence
// (toggles then last only for the session, as before).
func wireSyncOptIns(syncAPI *syncview.API, store settings.SettingsStore) {
	if syncAPI == nil || store == nil {
		return
	}
	syncAPI.SetOptInStore(newSettingsSyncOptIns(store))
	restored, err := syncAPI.RestoreOptIns()
	if err != nil {
		logging.L().Warn("rpc.settings_sync.optins_restore_failed", "err", err.Error())
		return
	}
	if len(restored) > 0 {
		logging.L().Info("rpc.settings_sync.optins_restored", "categories", restored)
	}
}
