package paths

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// The harness's personal config (providers.json, settings.json) lives under
// os.UserConfigDir()/<dir>. The directory name was misspelled "kaneaz-harness"
// before the kaneaz->kenaz rename; it is now "kenaz-harness".
const (
	legacyConfigDirName  = "kaneaz-harness"
	currentConfigDirName = "kenaz-harness"
)

var migrateConfigOnce sync.Once

// MigrateLegacyConfigDir renames os.UserConfigDir()/kaneaz-harness to
// .../kenaz-harness exactly once per process, but only when the new
// directory does not yet exist and the legacy one does. It keeps
// providers.json / settings.json reachable after the kaneaz->kenaz rename.
//
// It never clobbers an existing new directory (new always wins) and never
// fails startup — a rename error is logged and ignored. Safe to call from
// multiple read paths; the sync.Once makes all but the first call a no-op.
func MigrateLegacyConfigDir() {
	migrateConfigOnce.Do(func() {
		base, err := os.UserConfigDir()
		if err != nil {
			return
		}
		newPath := filepath.Join(base, currentConfigDirName)
		oldPath := filepath.Join(base, legacyConfigDirName)

		// If the new dir already exists, do nothing — never clobber.
		if _, err := os.Stat(newPath); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			return
		}
		// Only migrate when a legacy directory is actually present.
		if info, err := os.Stat(oldPath); err != nil || !info.IsDir() {
			return
		}
		if err := os.Rename(oldPath, newPath); err != nil {
			slog.Warn("paths: legacy config dir migration failed",
				"from", oldPath, "to", newPath, "err", err)
		}
	})
}
