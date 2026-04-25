// Package bundle's impl wires the cache + lockfile readers behind the
// BundleAPI surface. Read-only by construction — installation is the
// resolver mission's responsibility; this surface answers "what is
// installed today?" for the /bundles view.
//
// Privacy CI invariant: this surface NEVER exposes credentials or
// signing keys. Source URLs and signature refs are reference-only;
// the typed Bundle struct's fields are the canonical boundary.
package bundle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/sigil-tech/kaneaz-harness/core/bundle/cache"
	"github.com/sigil-tech/kaneaz-harness/core/bundle/lockfile"
)

// Reader is the minimal interface this impl needs — backed in
// production by os.ReadFile against the data-dir lockfile, and by an
// in-memory blob in tests.
type Reader interface {
	ReadLockfile() ([]byte, error)
}

// CASLike is the slimmed view of cache.CAS used here. Lets tests
// supply a fake without pulling in the filesystem.
type CASLike interface {
	Has(digest string) bool
}

// API is the concrete BundleAPI implementation.
type API struct {
	mu     sync.RWMutex
	reader Reader
	cas    CASLike
}

// Option configures NewAPI.
type Option func(*API)

// WithReader injects a lockfile reader.
func WithReader(r Reader) Option {
	return func(a *API) { a.reader = r }
}

// WithCAS injects the bundle cache (cache.CAS satisfies CASLike).
func WithCAS(c CASLike) Option {
	return func(a *API) { a.cas = c }
}

// NewAPI constructs the bundle view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// fsReader resolves <dataDir>/kaneaz.lock (the canonical lockfile path
// for v1; the resolver mission may move this once it lands).
type fsReader struct {
	path string
}

// NewFSReader returns a reader anchored at the harness data dir.
func NewFSReader(dataDir string) Reader {
	return &fsReader{path: filepath.Join(dataDir, "kaneaz.lock")}
}

func (r *fsReader) ReadLockfile() ([]byte, error) {
	b, err := os.ReadFile(r.path)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty lockfile shape: zero-bundle, schema v1.
			return []byte("schema_version = 1\n"), nil
		}
		return nil, fmt.Errorf("bundle: read lockfile: %w", err)
	}
	return b, nil
}

// List returns every bundle in the lockfile. Sorted by name for a
// stable UI ordering. Missing-lockfile is not an error: an empty list
// is returned so the frontend can render its empty state. The returned
// Bundle structs omit the per-artifact slice — call Get to expand a
// single bundle's artifacts.
func (a *API) List(_ context.Context) ([]Bundle, error) {
	if a.reader == nil {
		return []Bundle{}, nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	raw, err := a.reader.ReadLockfile()
	if err != nil {
		return nil, err
	}
	lf, err := lockfile.Read(raw)
	if err != nil {
		return nil, fmt.Errorf("bundle: parse lockfile: %w", err)
	}
	out := make([]Bundle, 0, len(lf.Bundles))
	for _, lb := range lf.Bundles {
		out = append(out, lockedToBundle(lb, a.cas, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Get returns one bundle by id. Id matches against the bundle name.
// The returned Bundle includes the full artifact slice.
func (a *API) Get(_ context.Context, id string) (Bundle, error) {
	if a.reader == nil {
		return Bundle{}, fmt.Errorf("bundle: %q not found (no lockfile reader)", id)
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	raw, err := a.reader.ReadLockfile()
	if err != nil {
		return Bundle{}, err
	}
	lf, err := lockfile.Read(raw)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: parse lockfile: %w", err)
	}
	for _, lb := range lf.Bundles {
		if lb.Name == id {
			return lockedToBundle(lb, a.cas, true), nil
		}
	}
	return Bundle{}, fmt.Errorf("bundle: %q not found", id)
}

// lockedToBundle maps a lockfile.LockedBundle into the public Bundle
// shape. Tier currently encodes the source channel + signature
// presence; once the resolver mission ships richer trust metadata
// this mapping moves to a typed function in core/bundle.
func lockedToBundle(lb lockfile.LockedBundle, cas CASLike, includeArtifacts bool) Bundle {
	tier := "channel"
	if lb.SignatureRef != "" {
		tier = "signed"
	}
	if cas != nil && lb.ContentHash != "" && !cas.Has(lb.ContentHash) {
		tier = tier + " (uncached)"
	}
	out := Bundle{
		ID:            lb.Name,
		Name:          lb.Name,
		Version:       lb.Version,
		Tier:          tier,
		Source:        lb.Source,
		Signature:     lb.SignatureRef,
		ArtifactCount: len(lb.Artifacts),
	}
	if includeArtifacts && len(lb.Artifacts) > 0 {
		arts := make([]Artifact, 0, len(lb.Artifacts))
		for _, la := range lb.Artifacts {
			arts = append(arts, Artifact{
				Name:        la.Name,
				Kind:        la.Kind,
				ContentHash: la.ContentHash,
			})
		}
		out.Artifacts = arts
	}
	return out
}

// CASFromCache adapts a cache.CAS into the CASLike interface. Lets
// callers pass cache.CAS directly without coupling the impl package
// to the *fsCAS type.
func CASFromCache(c cache.CAS) CASLike {
	if c == nil {
		return nil
	}
	return casAdapter{c: c}
}

type casAdapter struct{ c cache.CAS }

func (a casAdapter) Has(d string) bool { return a.c.Has(d) }
