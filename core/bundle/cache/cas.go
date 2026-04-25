// Package cache implements the content-addressable storage that backs
// the bundle layer's local-first invariant. The CAS lives under
// <data_dir>/cache/sha256/<aa>/<bb>/<digest> and is the long-lived store
// of every artifact byte the resolver has ever fetched and verified.
//
// Atomicity contract: bytes only land at the canonical sha256/.../ path
// AFTER hash verification matches the expected digest. Until then they
// live under <data_dir>/cache/staging/<random> and are deleted on
// mismatch or cancellation.
//
// The package depends on stdlib + parent core/bundle for typed errors only.
// (DIRECTIVE_001 isolation; this is the only consumer of the data-dir
// path requested from storage-foundations.)
package cache

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	bundle "github.com/sigil-tech/kaneaz-harness/core/bundle"
)

// CAS is the content-addressable storage interface.
type CAS interface {
	// Has returns true if the canonical path for digest exists.
	Has(digest string) bool
	// Get returns a streaming reader over the canonical bytes. Callers
	// must Close the reader.
	Get(digest string) (io.ReadCloser, error)
	// Put streams reader to a staging path, verifies its SHA-256 against
	// expected, and atomically renames into the canonical path on match.
	// On mismatch the staging file is deleted and ErrIntegrityMismatch is
	// returned. On context cancellation the staging file is deleted and
	// ErrCancelled is returned.
	Put(ctx context.Context, reader io.Reader, expected string) (Receipt, error)
	// Evict removes a digest from the canonical store. Returns nil if the
	// digest is already absent.
	Evict(digest string) error
	// Root returns the data-dir root the CAS is operating under.
	Root() string
}

// Receipt records a successful Put.
type Receipt struct {
	Digest string // sha256:<hex>
	Size   int64
	Path   string // canonical path on disk
}

// New returns a CAS rooted at dataDir. The directory is created if
// missing. The CAS makes no other assumptions about dataDir layout —
// it owns only `sha256/`, `staging/`, and `manifests/` subtrees.
func New(dataDir string) (CAS, error) {
	if dataDir == "" {
		return nil, errors.New("cache: empty data dir")
	}
	cas := &fsCAS{root: dataDir}
	for _, sub := range []string{"sha256", "staging", "manifests"} {
		if err := os.MkdirAll(filepath.Join(dataDir, sub), 0o755); err != nil {
			return nil, fmt.Errorf("cache: mkdir %s: %w", sub, err)
		}
	}
	return cas, nil
}

type fsCAS struct {
	root string
}

func (c *fsCAS) Root() string { return c.root }

func (c *fsCAS) Has(digest string) bool {
	p, err := canonicalPath(c.root, digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (c *fsCAS) Get(digest string) (io.ReadCloser, error) {
	p, err := canonicalPath(c.root, digest)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (c *fsCAS) Put(ctx context.Context, reader io.Reader, expected string) (Receipt, error) {
	if !validDigest(expected) {
		return Receipt{}, fmt.Errorf("cache: invalid expected digest %q", expected)
	}
	stagingDir := filepath.Join(c.root, "staging")
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return Receipt{}, fmt.Errorf("cache: mkdir staging: %w", err)
	}

	// Random staging filename — never under canonical sha256/ path.
	var nameBuf [16]byte
	if _, err := rand.Read(nameBuf[:]); err != nil {
		return Receipt{}, fmt.Errorf("cache: random: %w", err)
	}
	stagingName := hex.EncodeToString(nameBuf[:])
	stagingPath := filepath.Join(stagingDir, stagingName)

	f, err := os.OpenFile(stagingPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return Receipt{}, fmt.Errorf("cache: open staging: %w", err)
	}
	// Cleanup is the caller's promise on every error path.
	cleanup := func() {
		_ = f.Close()
		_ = os.Remove(stagingPath)
	}

	h := sha256.New()
	tr := &cancelReader{ctx: ctx, r: io.TeeReader(reader, h)}
	n, err := io.Copy(f, tr)
	if err != nil {
		cleanup()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errCancelled) {
			return Receipt{}, fmt.Errorf("%w: %v", bundle.ErrCancelled, err)
		}
		return Receipt{}, fmt.Errorf("cache: copy: %w", err)
	}
	if err := f.Sync(); err != nil {
		cleanup()
		return Receipt{}, fmt.Errorf("cache: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(stagingPath)
		return Receipt{}, fmt.Errorf("cache: close: %w", err)
	}

	got := "sha256:" + hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		_ = os.Remove(stagingPath)
		return Receipt{}, fmt.Errorf("%w: expected=%s got=%s", bundle.ErrIntegrityMismatch, expected, got)
	}

	canonical, err := canonicalPath(c.root, expected)
	if err != nil {
		_ = os.Remove(stagingPath)
		return Receipt{}, err
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o755); err != nil {
		_ = os.Remove(stagingPath)
		return Receipt{}, fmt.Errorf("cache: mkdir canonical parent: %w", err)
	}
	// If canonical already exists (concurrent Put for same digest),
	// the rename is still safe — bytes are identical by definition.
	if err := os.Rename(stagingPath, canonical); err != nil {
		// On some filesystems Rename across same-FS still works; if it
		// fails attempt a copy fallback then remove staging.
		if cerr := copyFile(stagingPath, canonical); cerr != nil {
			_ = os.Remove(stagingPath)
			return Receipt{}, fmt.Errorf("cache: rename canonical: %w (copy fallback: %v)", err, cerr)
		}
		_ = os.Remove(stagingPath)
	}

	return Receipt{Digest: expected, Size: n, Path: canonical}, nil
}

func (c *fsCAS) Evict(digest string) error {
	p, err := canonicalPath(c.root, digest)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// canonicalPath returns <root>/sha256/<aa>/<bb>/<digest-hex>.
// digest is required to be of the form sha256:<64-hex-chars>.
func canonicalPath(root, digest string) (string, error) {
	if !validDigest(digest) {
		return "", fmt.Errorf("cache: bad digest %q", digest)
	}
	hexPart := digest[len("sha256:"):]
	aa := hexPart[0:2]
	bb := hexPart[2:4]
	return filepath.Join(root, "sha256", aa, bb, hexPart), nil
}

func validDigest(d string) bool {
	if !strings.HasPrefix(d, "sha256:") {
		return false
	}
	h := d[len("sha256:"):]
	if len(h) != 64 {
		return false
	}
	for i := 0; i < len(h); i++ {
		c := h[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// cancelReader wraps a Reader and aborts the next Read with errCancelled
// once ctx.Done fires. We avoid spawning a goroutine — a per-Read poll is
// sufficient for the typical fetch sizes (KiB to GB) and is simpler.
type cancelReader struct {
	ctx context.Context
	r   io.Reader
}

var errCancelled = errors.New("cancelled")

func (c *cancelReader) Read(p []byte) (int, error) {
	select {
	case <-c.ctx.Done():
		return 0, errCancelled
	default:
	}
	return c.r.Read(p)
}

// copyFile is a fallback used when Rename fails (e.g., crossing
// filesystems on some test setups). Streaming copy.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
