package mlsidecar

// Whole-tree digest (design Amendment A5(3)). The launcher-only
// engine_sha256 left the ~150 MB _internal/ payload (the interpreter, the
// dylibs, the engine's own Python) covered by nothing at adoption time.
// install.json now records a digest over the WHOLE onedir, and adoption
// and spawn re-verify it.
//
// The recipe is Kenaz's shipped implementation (internal/ml/tree.go on
// kenaz feat/ml-shared-engine-phase2, ruled canonical 2026-09-30 — both
// clients must compute the identical value or neither can adopt the
// other's install). It is the FR-011 directory-artifact recipe:
//
//	for every member under <version>/kameas-ml/ (directories themselves
//	are not members):
//	  regular file  -> member hash = hex(sha256(file bytes))
//	  symlink       -> member hash = hex(sha256("symlink:" + link target))
//	  anything else -> error (not a valid engine tree)
//	path     = the member's path relative to kameas-ml/, '/'-separated
//	manifest = concat over members sorted bytewise by path of
//	           "<member hash>  <path>\n"          (sha256sum format)
//	digest   = "sha256:" + hex(sha256(manifest))
//
// TestTreeDigest_CanonicalVector pins it against a vector computed
// independently of this code.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
)

type treeMember struct {
	path string
	hash string
}

// TreeDigest computes the whole-tree digest of dir (a kameas-ml onedir).
func TreeDigest(dir string) (string, error) {
	var members []treeMember
	err := walkTree(dir, func(rel, abs string, info fs.FileInfo) error {
		var h string
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(abs)
			if err != nil {
				return err
			}
			sum := sha256.Sum256([]byte("symlink:" + target))
			h = hex.EncodeToString(sum[:])
		} else {
			f, err := os.Open(abs)
			if err != nil {
				return err
			}
			hh := sha256.New()
			_, cerr := io.Copy(hh, f)
			_ = f.Close()
			if cerr != nil {
				return cerr
			}
			h = hex.EncodeToString(hh.Sum(nil))
		}
		members = append(members, treeMember{path: rel, hash: h})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("mlsidecar: tree digest of %s: %w", dir, err)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].path < members[j].path })
	m := sha256.New()
	for _, mem := range members {
		_, _ = io.WriteString(m, mem.hash+"  "+mem.path+"\n")
	}
	return "sha256:" + hex.EncodeToString(m.Sum(nil)), nil
}

// treeFingerprint is a cheap stat-only summary (path, size, mtime, mode of
// every member) that lets TreeVerifier skip a full re-hash when nothing
// changed on disk since the last successful verification.
func treeFingerprint(dir string) (string, error) {
	var lines []string
	err := walkTree(dir, func(rel, _ string, info fs.FileInfo) error {
		lines = append(lines, rel+"\x00"+strconv.FormatInt(info.Size(), 10)+"\x00"+
			strconv.FormatInt(info.ModTime().UnixNano(), 10)+"\x00"+info.Mode().String())
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		_, _ = io.WriteString(h, l+"\n")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// walkTree visits every non-directory member of dir with its '/'-separated
// relative path, refusing member types a frozen onedir never contains.
func walkTree(dir string, visit func(rel, abs string, info fs.FileInfo) error) error {
	root := filepath.Clean(dir)
	if info, err := os.Stat(root); err != nil {
		return err
	} else if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", root)
	}
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return fmt.Errorf("unsupported member %s (%s)", path, info.Mode().Type())
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return visit(filepath.ToSlash(rel), path, info)
	})
}

// TreeVerifier re-verifies onedir trees against recorded digests with a
// stat (size/mtime/mode) cache. It is per-process: the first verification
// of a directory in a process is always a full re-hash — so every boot
// re-hashes on first adoption or spawn — and later ones re-hash only when
// the stat fingerprint changed. A nil *TreeVerifier always re-hashes.
type TreeVerifier struct {
	mu sync.Mutex
	ok map[string]treeOK
}

type treeOK struct{ digest, fingerprint string }

// NewTreeVerifier returns an empty, per-process verifier.
func NewTreeVerifier() *TreeVerifier { return &TreeVerifier{ok: map[string]treeOK{}} }

// Verify checks that dir's tree digest equals want.
func (v *TreeVerifier) Verify(dir, want string) error {
	if normalizeDigest(want) == "" {
		return fmt.Errorf("%w: no tree digest recorded for %s", ErrDigestMismatch, dir)
	}
	fp, err := treeFingerprint(dir)
	if err != nil {
		return fmt.Errorf("mlsidecar: tree fingerprint of %s: %w", dir, err)
	}
	if v != nil {
		v.mu.Lock()
		e, hit := v.ok[dir]
		v.mu.Unlock()
		if hit && digestsEqual(e.digest, want) && e.fingerprint == fp {
			return nil
		}
	}
	got, err := TreeDigest(dir)
	if err != nil {
		return err
	}
	if !digestsEqual(got, want) {
		v.forget(dir)
		return fmt.Errorf("%w: tree %s is %s, record says %s", ErrDigestMismatch, dir, got, want)
	}
	v.remember(dir, got, fp)
	return nil
}

func (v *TreeVerifier) remember(dir, digest, fp string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	v.ok[dir] = treeOK{digest: digest, fingerprint: fp}
	v.mu.Unlock()
}

func (v *TreeVerifier) forget(dir string) {
	if v == nil {
		return
	}
	v.mu.Lock()
	delete(v.ok, dir)
	v.mu.Unlock()
}
