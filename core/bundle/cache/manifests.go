package cache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ManifestCAS is a parallel sub-CAS keyed by manifest content hash. It is
// used to memo-ize parsed-and-validated manifest bytes so a re-resolve
// does not re-parse manifests already verified. Layout: same two-hex-char
// nesting under <data_dir>/cache/manifests/sha256/<aa>/<bb>/<digest>.
type ManifestCAS struct {
	root string
}

// Manifests returns a ManifestCAS rooted under the same data_dir as the
// CAS. The two stores are independent (eviction of an artifact does not
// evict its manifest, and vice versa).
func (c *fsCAS) Manifests() *ManifestCAS {
	return &ManifestCAS{root: c.root}
}

// Has reports whether the parsed-manifest bytes for digest are cached.
func (m *ManifestCAS) Has(digest string) bool {
	p, err := manifestPath(m.root, digest)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Get streams a parsed manifest's bytes.
func (m *ManifestCAS) Get(digest string) (io.ReadCloser, error) {
	p, err := manifestPath(m.root, digest)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// Put atomically stores manifest bytes under digest. The caller is
// responsible for ensuring digest matches sha256(bytes); this CAS is a
// memo cache, not a verification boundary.
func (m *ManifestCAS) Put(_ context.Context, digest string, data []byte) error {
	if !validDigest(digest) {
		return errors.New("manifests: bad digest")
	}
	p, err := manifestPath(m.root, digest)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("manifests: mkdir: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

func manifestPath(root, digest string) (string, error) {
	if !validDigest(digest) {
		return "", fmt.Errorf("manifests: bad digest %q", digest)
	}
	hexPart := digest[len("sha256:"):]
	aa := hexPart[0:2]
	bb := hexPart[2:4]
	return filepath.Join(root, "manifests", "sha256", aa, bb, hexPart), nil
}
