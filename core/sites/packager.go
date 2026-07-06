// Package sites — packager.go
//
// Pack produces a deterministic gzip tarball from a site root directory.
// Determinism guarantee (NFR-001): the same directory tree always produces
// the same sha256 on macOS and Linux, regardless of file mtime, inode order,
// or file-system traversal ordering differences between platforms.
//
// Protocol:
//  1. Walk the tree, collecting (path, FileInfo) pairs.
//  2. Apply IgnoreSet (default globs always + manifest.Ignore globs).
//  3. Reject symlinks that resolve outside the root (FR-103).
//  4. Sort entries lexicographically by slash-normalised path (FR-101).
//  5. Write tar entries with zeroed mtime/uid/gid/uname/gname and
//     normalised modes (0644 files / 0755 directories) (FR-101).
//  6. gzip with fixed header fields (OS=255, mtime=0) for reproducibility.
//  7. Return hex sha256 of the compressed output (NFR-001).
//
// OSS-boundary: no imports from core/fleet.
// Mission: sites-foundation-01NSITE04, WP03.
package sites

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// defaultIgnoreGlobs are always applied, regardless of manifest settings.
// They guard against accidentally bundling credentials or git history.
var defaultIgnoreGlobs = []string{
	".git",
	"node_modules",
	".env",
	".env.*",
	"*.env",
	"*.pem",
	"*.key",
}

// ErrSymlinkEscape is returned when a symlink inside the bundle root resolves
// to a path outside the root directory (zip-slip variant).
type ErrSymlinkEscape struct {
	Link   string // relative path of the symlink inside the root
	Target string // absolute path the symlink resolves to
	Root   string // the site root
}

func (e *ErrSymlinkEscape) Error() string {
	return fmt.Sprintf(
		"sites/packager: symlink %q resolves to %q which is outside the site root %q; "+
			"symlinks that escape the bundle are rejected to prevent path-traversal attacks",
		e.Link, e.Target, e.Root,
	)
}

// PackResult is returned by Pack and contains the hex-encoded sha256 of the
// gzipped tarball and a list of files that were excluded by the ignore set.
type PackResult struct {
	// SHA256Hex is the hex-encoded sha256 of the gzipped tarball bytes written
	// to the io.Writer (identical across platforms for the same tree).
	SHA256Hex string
	// Excluded is the list of paths (relative to the root, slash-separated)
	// that were skipped by the default or manifest ignore globs. Callers should
	// surface this list so users can verify that no unintended files were dropped.
	Excluded []string
}

// WarnFunc is called once per excluded file/directory so callers can surface
// the exclusion to the user (FR-102). The path is relative to the root.
type WarnFunc func(path string)

// PackOptions configures optional behaviour for Pack.
type PackOptions struct {
	// WarnFn is called for each excluded path (FR-102). If nil, exclusions are
	// recorded in PackResult.Excluded but no callback fires.
	WarnFn WarnFunc
}

// Pack walks root, applies the IgnoreSet derived from manifest ignore globs
// plus the always-on default ignore set, rejects symlink escapes, then writes
// a deterministic gzip tarball to w. The sha256 of the compressed output is
// returned in PackResult.SHA256Hex.
//
// The caller is responsible for closing w; Pack does not close it.
func Pack(ctx context.Context, root string, m Manifest, w io.Writer, opts PackOptions) (PackResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return PackResult{}, fmt.Errorf("sites/packager: resolve root %q: %w", root, err)
	}
	// EvalSymlinks resolves any OS-level symlinks in the root path itself
	// (e.g. macOS /var → /private/var). This ensures isContained works when
	// symlink targets are compared against an EvalSymlinks-resolved root.
	if resolved, rerr := filepath.EvalSymlinks(absRoot); rerr == nil {
		absRoot = resolved
	}

	// Build the combined ignore set.
	ignoreGlobs := append([]string(nil), defaultIgnoreGlobs...)
	ignoreGlobs = append(ignoreGlobs, m.Ignore...)

	// Collect all entries (not yet sorted).
	type entry struct {
		relPath string // slash-normalised, relative to root
		absPath string
		info    fs.FileInfo
	}
	var entries []entry
	var excluded []string

	walkErr := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("sites/packager: walk %q: %w", path, err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}

		rel, _ := filepath.Rel(absRoot, path)
		rel = filepath.ToSlash(rel)

		// Never include the root itself.
		if rel == "." {
			return nil
		}

		// Check against ignore globs.
		base := filepath.Base(path)
		if isIgnored(base, rel, ignoreGlobs) {
			excluded = append(excluded, rel)
			if opts.WarnFn != nil {
				opts.WarnFn(rel)
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Use Lstat (do not follow symlinks) to detect symlinks.
		linfo, lerr := os.Lstat(path) // #nosec G304 -- path derived from absRoot Walk
		if lerr != nil {
			return fmt.Errorf("sites/packager: lstat %q: %w", path, lerr)
		}

		if linfo.Mode()&os.ModeSymlink != 0 {
			// Resolve the symlink and check containment.
			resolved, rerr := filepath.EvalSymlinks(path)
			if rerr != nil {
				return fmt.Errorf("sites/packager: resolve symlink %q: %w", rel, rerr)
			}
			absResolved, aerr := filepath.Abs(resolved)
			if aerr != nil {
				return fmt.Errorf("sites/packager: abs symlink target %q: %w", resolved, aerr)
			}
			if !isContained(absResolved, absRoot) {
				return &ErrSymlinkEscape{
					Link:   rel,
					Target: absResolved,
					Root:   absRoot,
				}
			}
			// Symlink is internal — follow and re-stat so we include the
			// content at its real type. WalkDir already handles this, but
			// we need the FileInfo after following.
			linfo, lerr = os.Stat(path) // #nosec G304
			if lerr != nil {
				return fmt.Errorf("sites/packager: stat symlink target %q: %w", path, lerr)
			}
		}

		entries = append(entries, entry{relPath: rel, absPath: path, info: linfo})
		return nil
	})
	if walkErr != nil {
		return PackResult{}, walkErr
	}

	// Sort lexicographically (FR-101).
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].relPath < entries[j].relPath
	})

	// Write the tarball.
	h := sha256.New()
	mw := io.MultiWriter(w, h)

	gz, err := newDeterministicGzip(mw)
	if err != nil {
		return PackResult{}, fmt.Errorf("sites/packager: create gzip writer: %w", err)
	}
	tw := tar.NewWriter(gz)

	zeroTime := time.Time{} // epoch — zeroed mtime for determinism

	for _, e := range entries {
		if ctx.Err() != nil {
			return PackResult{}, ctx.Err()
		}

		var mode int64
		if e.info.IsDir() {
			mode = 0o755
		} else {
			mode = 0o644
		}

		hdr := &tar.Header{
			Name:     e.relPath,
			Mode:     mode,
			ModTime:  zeroTime,
			Uid:      0,
			Gid:      0,
			Uname:    "",
			Gname:    "",
			Typeflag: tarTypeFlag(e.info),
		}
		if e.info.IsDir() {
			hdr.Name = e.relPath + "/"
		} else {
			hdr.Size = e.info.Size()
		}

		if err := tw.WriteHeader(hdr); err != nil {
			return PackResult{}, fmt.Errorf("sites/packager: write tar header for %q: %w", e.relPath, err)
		}

		if !e.info.IsDir() {
			f, ferr := os.Open(e.absPath) // #nosec G304
			if ferr != nil {
				return PackResult{}, fmt.Errorf("sites/packager: open %q: %w", e.relPath, ferr)
			}
			if _, cerr := io.Copy(tw, f); cerr != nil {
				_ = f.Close()
				return PackResult{}, fmt.Errorf("sites/packager: copy %q: %w", e.relPath, cerr)
			}
			_ = f.Close()
		}
	}

	if err := tw.Close(); err != nil {
		return PackResult{}, fmt.Errorf("sites/packager: close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return PackResult{}, fmt.Errorf("sites/packager: close gzip: %w", err)
	}

	return PackResult{
		SHA256Hex: hex.EncodeToString(h.Sum(nil)),
		Excluded:  excluded,
	}, nil
}

// newDeterministicGzip creates a gzip.Writer with all header fields that could
// vary between runs zeroed or set to fixed values, ensuring the compressed
// output is byte-identical across runs and platforms for the same input.
//
// Fields set:
//   - Header.Name = ""  (no original filename)
//   - Header.OS   = 255 (unknown — not platform-specific; gzip default is 0=FAT or 255=unknown)
//   - Header.ModTime = time.Time{} (zero — no timestamp)
//   - CompressionLevel = gzip.DefaultCompression (consistent across Go versions)
func newDeterministicGzip(w io.Writer) (*gzip.Writer, error) {
	gz, err := gzip.NewWriterLevel(w, gzip.DefaultCompression)
	if err != nil {
		return nil, err
	}
	gz.Header = gzip.Header{
		OS:       255, // unknown OS — avoids platform-specific 0 (FAT) vs 3 (Unix)
		ModTime:  time.Time{},
		Name:     "",
		Comment:  "",
		Extra:    nil,
	}
	return gz, nil
}

// tarTypeFlag returns the tar header Typeflag for the given FileInfo.
func tarTypeFlag(info fs.FileInfo) byte {
	if info.IsDir() {
		return tar.TypeDir
	}
	return tar.TypeReg
}

// isContained reports whether path is inside (or equal to) root.
// Both paths must be absolute.
func isContained(path, root string) bool {
	// Ensure root ends with separator for prefix check.
	if root != "/" {
		root = root + string(filepath.Separator)
	}
	return path == strings.TrimSuffix(root, string(filepath.Separator)) ||
		strings.HasPrefix(path, root)
}

// isIgnored reports whether a file at relPath with base name base
// should be excluded by the given set of glob patterns.
//
// The glob is matched against:
//  1. The base name (for patterns like "*.pem", ".env").
//  2. The full relative (slash-normalised) path (for directory patterns
//     like "node_modules" or "notebooks/").
func isIgnored(base, relPath string, globs []string) bool {
	for _, g := range globs {
		if matchGlob(g, base) {
			return true
		}
		if matchGlob(g, relPath) {
			return true
		}
		// Check each path segment against the glob (for directory name matches).
		for _, seg := range strings.Split(relPath, "/") {
			if matchGlob(g, seg) {
				return true
			}
		}
	}
	return false
}

// matchGlob wraps filepath.Match with a normalised separator.
func matchGlob(pattern, name string) bool {
	matched, _ := filepath.Match(pattern, name)
	return matched
}
