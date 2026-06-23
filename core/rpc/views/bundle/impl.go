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

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/bundle/lockfile"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
)

// Reader is the minimal interface this impl needs — backed in
// production by os.ReadFile against the data-dir lockfile, and by an
// in-memory blob in tests.
type Reader interface {
	ReadLockfile() ([]byte, error)
}

// Writer mirrors Reader for the install/remove path. Implementations
// are responsible for atomic on-disk replacement; the fsReader/Writer
// in this package uses a tmp-file + os.Rename to satisfy that.
type Writer interface {
	WriteLockfile(data []byte) error
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
	writer Writer
	cas    CASLike
}

// Option configures NewAPI.
type Option func(*API)

// WithReader injects a lockfile reader.
func WithReader(r Reader) Option {
	return func(a *API) { a.reader = r }
}

// WithWriter injects a lockfile writer. Required for Install/Remove.
func WithWriter(w Writer) Option {
	return func(a *API) { a.writer = w }
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

// fsReader resolves <dataDir>/kenaz.lock (the canonical lockfile path
// for v1; the resolver mission may move this once it lands).
type fsReader struct {
	path string
}

// NewFSReader returns a reader anchored at the harness data dir.
func NewFSReader(dataDir string) Reader {
	return &fsReader{path: filepath.Join(dataDir, "kenaz.lock")}
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

// fsWriter writes the lockfile back atomically (tmp + rename).
type fsWriter struct {
	path string
}

// NewFSWriter returns a writer anchored at the harness data dir.
func NewFSWriter(dataDir string) Writer {
	return &fsWriter{path: filepath.Join(dataDir, "kenaz.lock")}
}

func (w *fsWriter) WriteLockfile(data []byte) error {
	dir := filepath.Dir(w.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("bundle: mkdir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".kenaz.lock.tmp-*")
	if err != nil {
		return fmt.Errorf("bundle: create tmp lockfile: %w", err)
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("bundle: write tmp lockfile: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("bundle: sync tmp lockfile: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("bundle: close tmp lockfile: %w", err)
	}
	if err := os.Rename(tmpPath, w.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("bundle: rename lockfile: %w", err)
	}
	return nil
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

// Install registers a bundle in the lockfile. Beta-scoped to the
// local_path channel: req.Path must point at a directory that contains
// a kenaz.yaml manifest at its root. The manifest is parsed, validated,
// and converted into a LockedBundle entry that's appended to the
// lockfile (replacing any existing entry of the same name). Artifact
// bytes are NOT fetched into the CAS by this minimal install path —
// the resolver mission ships the byte-fetch pipeline; this beta surface
// surfaces "uncached" tier annotations on the resulting list rows so
// the operator knows the bundle is registered but not yet materialized.
func (a *API) Install(_ context.Context, req InstallRequest) (Bundle, error) {
	if a.reader == nil || a.writer == nil {
		return Bundle{}, fmt.Errorf("bundle: install requires a writable lockfile")
	}
	if req.Kind != "local_path" {
		return Bundle{}, fmt.Errorf("bundle: install kind %q unsupported (v0.3.0 beta: local_path only)", req.Kind)
	}
	if req.Path == "" {
		return Bundle{}, fmt.Errorf("bundle: install path is required")
	}
	if !filepath.IsAbs(req.Path) {
		return Bundle{}, fmt.Errorf("bundle: install path %q must be absolute", req.Path)
	}
	manifestPath := filepath.Join(req.Path, "kenaz.yaml")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: read manifest %s: %w", manifestPath, err)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: parse manifest: %w", err)
	}
	if err := m.Validate(manifest.ValidateOpts{}); err != nil {
		return Bundle{}, fmt.Errorf("bundle: validate manifest: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	existing, err := a.reader.ReadLockfile()
	if err != nil {
		return Bundle{}, err
	}
	lf, err := lockfile.Read(existing)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: parse lockfile: %w", err)
	}

	lb := lockfile.LockedBundle{
		Name:        m.Name,
		Version:     m.Version,
		Source:      "local_path:" + req.Path,
		ContentHash: m.ContentHash(),
	}
	for _, ad := range m.Artifacts {
		lb.Artifacts = append(lb.Artifacts, lockfile.LockedArtifact{
			Name:        ad.Name,
			Kind:        ad.Kind,
			ContentHash: ad.ContentHash,
		})
	}
	if len(m.Signatures) > 0 {
		// Lockfile carries a single signature ref — surface the first one
		// as a presence marker; richer trust metadata is the resolver
		// mission's responsibility.
		lb.SignatureRef = m.Signatures[0].Locator
	}

	// Replace any existing entry with the same name.
	replaced := false
	for i := range lf.Bundles {
		if lf.Bundles[i].Name == lb.Name {
			lf.Bundles[i] = lb
			replaced = true
			break
		}
	}
	if !replaced {
		lf.Bundles = append(lf.Bundles, lb)
	}

	out, err := lockfile.Write(lf)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: write lockfile: %w", err)
	}
	if err := a.writer.WriteLockfile(out); err != nil {
		return Bundle{}, err
	}
	return lockedToBundle(lb, a.cas, true), nil
}

// Remove drops the bundle whose name matches id from the lockfile and
// writes the result back. Removing an unknown id is a no-op (returns
// nil) so the UI can surface "remove" without first verifying presence.
func (a *API) Remove(_ context.Context, id string) error {
	if a.reader == nil || a.writer == nil {
		return fmt.Errorf("bundle: remove requires a writable lockfile")
	}
	if id == "" {
		return fmt.Errorf("bundle: remove id is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	existing, err := a.reader.ReadLockfile()
	if err != nil {
		return err
	}
	lf, err := lockfile.Read(existing)
	if err != nil {
		return fmt.Errorf("bundle: parse lockfile: %w", err)
	}
	kept := make([]lockfile.LockedBundle, 0, len(lf.Bundles))
	removed := false
	for _, b := range lf.Bundles {
		if b.Name == id {
			removed = true
			continue
		}
		kept = append(kept, b)
	}
	if !removed {
		// Idempotent: nothing to do.
		return nil
	}
	lf.Bundles = kept
	out, err := lockfile.Write(lf)
	if err != nil {
		return fmt.Errorf("bundle: write lockfile: %w", err)
	}
	return a.writer.WriteLockfile(out)
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
