// sync_upload_consent.go — the persisted one-time confirmation that gates
// settings-sync uploads (provider-credentials-client-01PCCLI01 A2, owner
// ruling H1).
//
// Until A2, settings sync never reached Fleet: the harness spoke a wire
// Fleet does not route (POST, int64 lww_ts). A user who had enabled a
// category had therefore never uploaded anything. Fixing the wire would
// start those uploads silently, so the owner ruled that the first upload
// waits for a one-time confirmation. That answer is
// Settings.SettingsSyncUploadsConfirmed; this adapter reads it for
// fleet.Syncer's upload gate (the consumer branch is in Syncer.Push) and
// writes it from Sync_ConfirmUploads. Pulls are not gated (see Syncer.Pull).
package rpc

import (
	"github.com/kameas-ai/kenaz-harness/core/logging"
	"github.com/kameas-ai/kenaz-harness/core/rpc/views/settings"
	syncview "github.com/kameas-ai/kenaz-harness/core/rpc/views/sync"
)

// settingsUploadConsent implements syncview.UploadConsent over the settings
// store.
type settingsUploadConsent struct {
	store settings.SettingsStore
}

var _ syncview.UploadConsent = settingsUploadConsent{}

// UploadsConfirmed reads the persisted answer. It fails closed: a store that
// cannot be read means no upload.
func (c settingsUploadConsent) UploadsConfirmed() bool {
	if c.store == nil {
		return false
	}
	s, err := c.store.LoadAll()
	if err != nil {
		logging.L().Warn("rpc.settings_sync.consent_read_failed", "err", err.Error())
		return false
	}
	return s.SettingsSyncUploadsConfirmed
}

// ConfirmUploads persists the confirmation.
func (c settingsUploadConsent) ConfirmUploads() error {
	if c.store == nil {
		return syncview.ErrUploadConsentUnavailable
	}
	s, err := c.store.LoadAll()
	if err != nil {
		return err
	}
	if s.SettingsSyncUploadsConfirmed {
		return nil
	}
	s.SettingsSyncUploadsConfirmed = true
	return c.store.SaveAll(s)
}
