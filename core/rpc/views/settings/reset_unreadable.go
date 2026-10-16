package settings

// reset_unreadable.go — recovery from a settings.json that cannot be read.
//
// Set (and every Update) refuses to save over a file it cannot read, so a
// corrupt file is never silently replaced and the upload consent it records
// is never guessed. That leaves the user stuck: nothing repaired the file,
// and settings sync stayed paused until someone edited it by hand. Reset is
// the explicit way out. It moves the unreadable file aside, untouched, to
// settings.json.corrupt-<UTC timestamp> (owner-only), writes the defaults,
// and the store reads them from then on. It refuses when the file reads
// fine: it is a recovery action, not a "restore defaults" button.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/logging"
)

// ErrSettingsReadable is returned by ResetUnreadable when the settings file
// reads fine, so there is nothing to recover and nothing was changed.
var ErrSettingsReadable = errors.New("settings: the settings file can be read; nothing was reset")

// ErrResetUnsupported is returned when the settings store is not file
// backed, so there is no file to recover.
var ErrResetUnsupported = errors.New("settings: this settings store has no file to reset")

// ResetUnreadableResult reports where the unreadable file was kept.
type ResetUnreadableResult struct {
	// BackupPath is the full path of the moved-aside file.
	BackupPath string `json:"backupPath"`
}

// corruptBackupSuffix is the timestamp layout in the backup's name.
const corruptBackupSuffix = "20060102T150405Z"

// ResetUnreadable moves an unreadable settings file to
// <path>.corrupt-<UTC timestamp> (mode 0600), then writes the defaults. It
// returns ErrSettingsReadable, changing nothing, when the file reads (a
// missing file reads as the defaults). Runs under the store lock, so no
// save can land between the check and the reset.
func (s *FileStore) ResetUnreadable(now time.Time) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.loadLocked(); err == nil {
		return "", ErrSettingsReadable
	}
	stamp := now.UTC().Format(corruptBackupSuffix)
	backup := s.path + ".corrupt-" + stamp
	for i := 2; ; i++ {
		if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
			break
		}
		backup = fmt.Sprintf("%s.corrupt-%s-%d", s.path, stamp, i)
	}
	// Rename, not copy: the bytes are kept exactly, even when the file
	// cannot be read at all (a permission problem rather than bad JSON).
	if err := os.Rename(s.path, backup); err != nil {
		return "", fmt.Errorf("settings: move the unreadable file aside: %w", err)
	}
	if err := os.Chmod(backup, 0o600); err != nil {
		logging.L().Warn("settings.reset_unreadable.chmod_failed", "err", err.Error())
	}
	if err := s.saveLocked(defaultSettings()); err != nil {
		// settings.json is now absent, which reads as the defaults anyway.
		return backup, fmt.Errorf("settings: write defaults (your old file is at %s): %w", backup, err)
	}
	return backup, nil
}

// unreadableResetter is a store that can recover from an unreadable file.
type unreadableResetter interface {
	ResetUnreadable(now time.Time) (string, error)
}

// ResetUnreadable is the "Reset settings" recovery action: see
// FileStore.ResetUnreadable. Afterwards every read returns the defaults,
// so the live Fleet model-call sharing is set to the default too. Nothing
// is announced to settings sync: defaults must never be uploaded over the
// user's other devices (and the reset also clears the upload consent).
func (a *API) ResetUnreadable(_ context.Context) (ResetUnreadableResult, error) {
	r, ok := a.store.(unreadableResetter)
	if !ok {
		return ResetUnreadableResult{}, ErrResetUnsupported
	}
	backup, err := r.ResetUnreadable(time.Now())
	if err != nil {
		return ResetUnreadableResult{BackupPath: backup}, err
	}
	a.applyModelCallSharing(defaultSettings().ShareModelCallTelemetryWithFleet)
	logging.L().Warn("settings.reset_unreadable.done")
	return ResetUnreadableResult{BackupPath: backup}, nil
}
