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
	ok, err := c.UploadState()
	if err != nil {
		logging.L().Warn("rpc.settings_sync.consent_read_failed", "err", err.Error())
		return false
	}
	return ok
}

// UploadState reads the persisted answer, reporting a read failure (or a
// missing store) as an error so Sync_Status can say why uploads are held.
func (c settingsUploadConsent) UploadState() (bool, error) {
	if c.store == nil {
		return false, syncview.ErrUploadConsentUnavailable
	}
	s, err := c.store.LoadAll()
	if err != nil {
		return false, err
	}
	return s.SettingsSyncUploadsConfirmed, nil
}

// ConfirmUploads persists the confirmation.
func (c settingsUploadConsent) ConfirmUploads() error {
	if c.store == nil {
		return syncview.ErrUploadConsentUnavailable
	}
	// Under the store lock (settings.Update): a concurrent whole-settings
	// save or sync apply cannot write back a copy read before this and
	// revert the confirmation.
	return settings.Update(c.store, func(s *settings.Settings) error {
		s.SettingsSyncUploadsConfirmed = true
		return nil
	})
}
