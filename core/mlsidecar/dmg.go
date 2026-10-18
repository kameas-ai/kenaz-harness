package mlsidecar

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// DMG handling (laya-advisors-01LAYA001 WP13, design Amendment A3(1)):
// `stapler` cannot attach a notarization ticket to a zip or a bare
// onedir — only .app/.dmg/.pkg — so the real engine artifact is
// kenaz-ml CI's signed + notarized + stapled .dmg (volume name
// "kenaz-ml", containing the "kameas-ml" PyInstaller onedir whose
// launcher is kameas-ml/kameas-ml). The A-1 manifest verification wraps
// the DMG BYTES as published; Install therefore verifies the downloaded
// file BEFORE this file's mount/copy/detach ever runs (install.go's
// ordering; dmg_test.go proves it with a recording mounter).

// EngineVolumeName is the DMG volume name kenaz-ml CI builds (frozen
// cross-repo interface, recorded for diagnostics; the copy logic does not
// depend on the mount path, only on the onedir inside it).
const EngineVolumeName = "kenaz-ml"

// EngineOnedirName is the onedir directory at the DMG volume root — also
// the directory name under versions/<semver>/ (paths.go's
// pathUnderVersionsDir depends on it).
const EngineOnedirName = "kameas-ml"

// ErrDMGUnsupported is returned by the default mounter on platforms with
// no hdiutil (everything but macOS). The engine artifact is macOS-only
// today (design §6.3), so this is an honest "not available here", not a
// silent fallback.
var ErrDMGUnsupported = errors.New("mlsidecar: .dmg artifacts can only be mounted on macOS")

// DMGMounter attaches a disk image read-only and returns its mount point
// plus a detach func. Production uses hdiutil (DefaultDMGMounter); tests
// inject a recording fake so the verify-before-mount ordering is
// provable without a real mount.
type DMGMounter interface {
	Attach(ctx context.Context, dmgPath string) (mountPoint string, detach func() error, err error)
}

// isDMGArtifact reports whether the channel-relative artifact path names a
// disk image (case-insensitive .dmg suffix).
func isDMGArtifact(artifactPath string) bool {
	return strings.HasSuffix(strings.ToLower(artifactPath), ".dmg")
}

// extractDMG mounts dmgPath via mounter, copies the onedir found at the
// volume root into destDir/<EngineOnedirName>, and ALWAYS detaches before
// returning. Callers must have verified dmgPath's bytes already.
func extractDMG(ctx context.Context, mounter DMGMounter, dmgPath, destDir string) (err error) {
	if mounter == nil {
		mounter = DefaultDMGMounter()
	}
	mountPoint, detach, aerr := mounter.Attach(ctx, dmgPath)
	if aerr != nil {
		return fmt.Errorf("mlsidecar: attach dmg: %w", aerr)
	}
	defer func() {
		if derr := detach(); derr != nil && err == nil {
			err = fmt.Errorf("mlsidecar: detach dmg: %w", derr)
		}
	}()

	src := filepath.Join(mountPoint, EngineOnedirName)
	info, serr := os.Stat(src)
	if serr != nil || !info.IsDir() {
		return fmt.Errorf("mlsidecar: dmg volume has no %q onedir at its root", EngineOnedirName)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mlsidecar: mkdir dest: %w", err)
	}
	return copyTree(src, filepath.Join(destDir, EngineOnedirName))
}

// copyTree copies src to dst preserving regular-file modes (the launcher's
// exec bit matters) and symlinks. A PyInstaller macOS onedir contains
// relative symlinks; an absolute symlink, or a relative one that resolves
// outside the onedir, is refused — the destination must be
// self-contained. Anything that is not a dir, regular file or symlink is
// refused.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return werr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if filepath.IsAbs(link) {
				return fmt.Errorf("mlsidecar: dmg onedir symlink %s has an absolute target %q", rel, link)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(target), link))
			if !pathIsWithin(resolved, dst) {
				return fmt.Errorf("mlsidecar: dmg onedir symlink %s escapes the onedir (%q)", rel, link)
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			return copyRegular(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("mlsidecar: dmg onedir entry %s has unsupported type %v", rel, info.Mode().Type())
		}
	})
}

func copyRegular(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
