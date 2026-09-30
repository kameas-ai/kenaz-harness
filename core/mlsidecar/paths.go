package mlsidecar

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Layout is the shared install root's directory shape (design §3.4):
//
//	kameas/ml/
//	  versions/<semver>/kameas-ml/…    # verified engine onedirs, immutable
//	  checkpoints/<sha256-prefix>/…    # verified packs, content-addressed
//	  current -> versions/<semver>     # atomic symlink, rename-swap
//	  lease/                           # lease files + spawn lock + local token
//	  install.json                     # who installed what, when, from which manifest
//
// Root is always caller-supplied. Production wiring (a later WP, not
// this one) resolves the real per-OS path via DefaultRootFor; every test
// in this package uses t.TempDir() instead — the WP12 brief's hard
// constraint is "never write to ~/.kenaz", and this package has no
// production call site yet that would resolve DefaultRootFor for real.
type Layout struct {
	Root string
}

// NewLayout returns a Layout rooted at root. root must be an absolute
// path; callers (tests, and eventually production wiring) are
// responsible for choosing it.
func NewLayout(root string) Layout { return Layout{Root: root} }

// DefaultRootFor returns the conventional per-OS shared install root
// (design §3.4): $XDG_DATA_HOME/kameas/ml on Linux, ~/Library/Application
// Support/kameas/ml on macOS. Exposed for the production wiring WP that
// eventually calls it; NOT called by anything in this package or its
// tests today (see doc.go's "what this package does NOT do").
func DefaultRootFor(goos string, homeDir string, xdgDataHome string) (string, error) {
	switch goos {
	case "darwin":
		if homeDir == "" {
			return "", fmt.Errorf("mlsidecar: darwin default root requires a home dir")
		}
		return filepath.Join(homeDir, "Library", "Application Support", "kameas", "ml"), nil
	case "linux":
		base := xdgDataHome
		if base == "" {
			if homeDir == "" {
				return "", fmt.Errorf("mlsidecar: linux default root requires XDG_DATA_HOME or a home dir")
			}
			base = filepath.Join(homeDir, ".local", "share")
		}
		return filepath.Join(base, "kameas", "ml"), nil
	default:
		return "", fmt.Errorf("mlsidecar: no default install root for GOOS=%q (design §6.3: Windows has no freeze target yet)", goos)
	}
}

// VersionsDir is the parent of every installed engine version.
func (l Layout) VersionsDir() string { return filepath.Join(l.Root, "versions") }

// VersionDir is one engine version's immutable install directory.
func (l Layout) VersionDir(semver string) string { return filepath.Join(l.VersionsDir(), semver) }

// CheckpointsDir is the content-addressed root for laya checkpoint
// packs (design §3.4). Phase-3 work (§9); only the path is defined here
// so the layout is documented in one place.
func (l Layout) CheckpointsDir() string { return filepath.Join(l.Root, "checkpoints") }

// CurrentLink is the atomic symlink update.go rename-swaps to point at
// the active version directory.
func (l Layout) CurrentLink() string { return filepath.Join(l.Root, "current") }

// LeaseDir holds lease heartbeat files, the spawn lock, and the local
// shutdown-authorization token (design §3.4/§3.5).
func (l Layout) LeaseDir() string { return filepath.Join(l.Root, "lease") }

// LeaseFile is one client's own heartbeat file, keyed by a stable client
// id ("harness", "kenaz").
func (l Layout) LeaseFile(client string) string {
	return filepath.Join(l.LeaseDir(), client+".lease")
}

// SpawnLockFile is the O_EXCL lock every client races on before
// spawning the engine (design §3.5: "O_EXCL spawn-lock in lease/").
func (l Layout) SpawnLockFile() string { return filepath.Join(l.LeaseDir(), "spawn.lock") }

// ShutdownTokenFilename is the admin-shutdown token's name inside lease/
// — a CROSS-REPO frozen value: kenaz-ml reads exactly
// <install_root>/lease/shutdown.token (config.SHUTDOWN_TOKEN_FILENAME) on
// every POST /v1/admin/shutdown and fails closed when it is absent. It was
// "admin.token" until the 2026-09-30 interop review, which meant every
// token-authorized stop would have been refused (401) by the real engine.
const ShutdownTokenFilename = "shutdown.token"

// TokenFile is the local, user-read-only token authorizing
// POST /v1/admin/shutdown (design §3.5's flip-and-respawn update flow).
func (l Layout) TokenFile() string { return filepath.Join(l.LeaseDir(), ShutdownTokenFilename) }

// InstallJSONPath is install.json's location.
func (l Layout) InstallJSONPath() string { return filepath.Join(l.Root, "install.json") }

// EnsureDirs creates every directory this layout needs, idempotently.
func (l Layout) EnsureDirs() error {
	for _, d := range []string{l.Root, l.VersionsDir(), l.CheckpointsDir(), l.LeaseDir()} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return fmt.Errorf("mlsidecar: mkdir %s: %w", d, err)
		}
	}
	return nil
}

// CurrentVersionDir resolves the `current` symlink to its target
// version directory. Returns an error if `current` does not exist or is
// not a symlink pointing inside VersionsDir() — a plain directory or a
// dangling/foreign link is never trusted.
func (l Layout) CurrentVersionDir() (string, error) {
	link := l.CurrentLink()
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("mlsidecar: read `current` symlink: %w", err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(l.Root, target)
	}
	target = filepath.Clean(target)
	rel, err := filepath.Rel(l.VersionsDir(), target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("mlsidecar: `current` target %q escapes versions dir", target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return "", fmt.Errorf("mlsidecar: `current` target %q is not a directory: %v", target, err)
	}
	return target, nil
}

// SetCurrent atomically points `current` at versionDir (design §3.5/R6:
// "atomically rename-swaps `current`"). It writes a new symlink under a
// temp name and renames it over the old one — rename of a symlink is
// atomic on every platform this harness ships (matches core/update's
// rename-based swap discipline).
func (l Layout) SetCurrent(semver string) error {
	target := filepath.Join("versions", semver)
	tmp := l.CurrentLink() + ".tmp"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return fmt.Errorf("mlsidecar: create temp `current` symlink: %w", err)
	}
	if err := os.Rename(tmp, l.CurrentLink()); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("mlsidecar: rename `current` symlink: %w", err)
	}
	return nil
}

// EngineExecutableName is the frozen onedir's entry-point binary name
// within a version directory, per-platform. The onedir name itself
// ("kameas-ml") is a frozen cross-repo interface (design §3.4); the
// executable inside it is platform-suffixed for Windows only (which has
// no freeze target yet per design §6.3, so this is forward-documentation
// more than a live path).
func EngineExecutableName(goos string) string {
	if goos == "" {
		goos = runtime.GOOS
	}
	if goos == "windows" {
		return "kameas-ml.exe"
	}
	return "kameas-ml"
}
