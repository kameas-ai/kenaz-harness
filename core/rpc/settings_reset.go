// settings_reset.go — the "Reset settings" recovery for an unreadable
// settings.json, as offered by the "Settings sync is paused" notice
// (frontend/src/lib/settingsIssues.ts).
//
// settings.API.ResetUnreadable moves the bad file aside and writes the
// defaults. The defaults have no settings-sync opt-ins and no upload
// consent, so the live Syncer is brought in line here: every category is
// turned off, exactly as if the user had switched each one off. Otherwise
// the Sync panel would show categories enabled that the file, and the next
// launch, say are off. The user turns sync back on, and confirms uploads
// again, from the Sync panel; turning a category on pulls first, so the
// settings their other devices hold come back down rather than these
// defaults going up.
package rpc

import (
	"context"

	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
)

// unreadableSettingsResetter is the optional HarnessAPI capability behind
// Bindings.Settings_ResetUnreadable.
type unreadableSettingsResetter interface {
	ResetUnreadableSettings(ctx context.Context) (settings.ResetUnreadableResult, error)
}

var _ unreadableSettingsResetter = (*API)(nil)

// ResetUnreadableSettings runs settings.API.ResetUnreadable, then turns the
// live settings-sync opt-ins off to match the defaults it wrote.
func (a *API) ResetUnreadableSettings(ctx context.Context) (settings.ResetUnreadableResult, error) {
	if a.settingsImpl == nil {
		return settings.ResetUnreadableResult{}, settings.ErrResetUnsupported
	}
	res, err := a.settingsImpl.ResetUnreadable(ctx)
	if err != nil {
		return res, err
	}
	if a.settingsSyncer != nil {
		for _, cat := range a.settingsSyncer.RegisteredCategories() {
			if !a.settingsSyncer.IsEnabled(cat) {
				continue
			}
			if err := a.settingsSyncer.SetEnabled(ctx, cat, false); err != nil {
				logging.L().Warn("rpc.settings_reset.sync_disable_failed", "category", string(cat), "err", err.Error())
			}
		}
	}
	return res, nil
}
