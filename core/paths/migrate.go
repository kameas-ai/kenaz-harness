package paths

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// MigrateResult describes the outcome of a MigrateLegacy attempt. Exactly one
// of Migrated / Skipped is meaningful: Migrated=true means data was adopted;
// otherwise Skipped explains why nothing was moved.
type MigrateResult struct {
	Migrated bool
	From     string
	To       string
	Skipped  string
}

// processAliveSameHost reports whether the lock holder is a live process on
// THIS host. It is a package var so tests can stub it deterministically.
var processAliveSameHost = func(pid int, hostname string) bool {
	host, _ := os.Hostname()
	if hostname != "" && host != "" && hostname != host {
		// Holder recorded a different machine (e.g. a shared NFS home). We
		// cannot verify liveness across hosts, so assume alive — never move a
		// data dir that another machine might be using.
		return true
	}
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// POSIX signal 0 probes for existence without delivering a signal.
	return p.Signal(syscall.Signal(0)) == nil
}

// legacyLocked reports whether legacy is currently held by a live harness
// process, via its data.db.harness-lock holder file (written by
// core/storage/internal/lockfile). Conservative: if a lock file exists but
// can't be read or parsed, we treat the dir as locked so we never move data
// out from under a running process.
func legacyLocked(legacy string) (bool, string) {
	lockPath := filepath.Join(legacy, "data.db.harness-lock")
	b, err := os.ReadFile(lockPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, ""
		}
		return true, "lock file unreadable: " + err.Error()
	}
	if len(b) == 0 {
		return false, ""
	}
	var h struct {
		PID      int    `json:"pid"`
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal(b, &h); err != nil {
		return true, "lock file unparseable"
	}
	if h.PID == 0 {
		return false, ""
	}
	if processAliveSameHost(h.PID, h.Hostname) {
		return true, fmt.Sprintf("held by live pid %d", h.PID)
	}
	return false, ""
}

// MigrateLegacy adopts a legacy single-profile data dir (e.g. ~/.harness) into
// a new per-env target (e.g. ~/.kenaz/harness/prod), exactly once and safely.
//
// It MOVES legacy → target only when ALL of these hold:
//   - target has no data.db (idempotent — never clobber an existing profile)
//   - legacy exists, is a directory, and contains a data.db
//   - legacy is NOT actively locked by a live harness process
//
// It tries an atomic os.Rename first. On a cross-filesystem rename it falls
// back to copy → verify → atomic-swap → remove-legacy, so a failure at any step
// leaves the legacy data fully intact (the legacy dir is only removed after a
// verified copy is atomically in place). The returned MigrateResult is for the
// caller to log; a non-nil error means a migration was attempted and failed.
func MigrateLegacy(legacy, target string) (MigrateResult, error) {
	res := MigrateResult{From: legacy, To: target}

	// Idempotent: a populated target always wins.
	if fi, err := os.Stat(filepath.Join(target, "data.db")); err == nil && !fi.IsDir() {
		res.Skipped = "target already has data.db"
		return res, nil
	}
	lfi, err := os.Stat(legacy)
	if err != nil || !lfi.IsDir() {
		res.Skipped = "no legacy data dir"
		return res, nil
	}
	if fi, err := os.Stat(filepath.Join(legacy, "data.db")); err != nil || fi.IsDir() {
		res.Skipped = "legacy has no data.db"
		return res, nil
	}
	if locked, why := legacyLocked(legacy); locked {
		res.Skipped = "legacy in use (" + why + ")"
		return res, nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return res, fmt.Errorf("paths.MigrateLegacy: mkdir parent: %w", err)
	}

	// Fast path: atomic same-filesystem rename.
	if err := os.Rename(legacy, target); err == nil {
		res.Migrated = true
		return res, nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return res, fmt.Errorf("paths.MigrateLegacy: rename: %w", err)
	}

	// Cross-filesystem fallback: copy → verify → swap → remove.
	tmp := target + ".migrating"
	_ = os.RemoveAll(tmp)
	if err := os.CopyFS(tmp, os.DirFS(legacy)); err != nil {
		_ = os.RemoveAll(tmp)
		return res, fmt.Errorf("paths.MigrateLegacy: copy: %w", err)
	}
	src, serr := os.Stat(filepath.Join(legacy, "data.db"))
	dst, derr := os.Stat(filepath.Join(tmp, "data.db"))
	if serr != nil || derr != nil || src.Size() != dst.Size() {
		_ = os.RemoveAll(tmp)
		return res, errors.New("paths.MigrateLegacy: verify failed (copied data.db missing or size mismatch)")
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.RemoveAll(tmp)
		return res, fmt.Errorf("paths.MigrateLegacy: swap: %w", err)
	}
	res.Migrated = true
	if err := os.RemoveAll(legacy); err != nil {
		// Data is safely at target; leaving the legacy copy behind is harmless.
		res.Skipped = "migrated; legacy copy left behind: " + err.Error()
	}
	return res, nil
}
