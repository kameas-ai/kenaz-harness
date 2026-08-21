// Package bundle's impl wires the cache + lockfile readers behind the
// BundleAPI surface for the /bundles view.
//
// Install (UNIT-5, bundle-download-and-verify-01PMZ909) routes entirely
// through the core/bundle/channels abstraction: the manifest itself and
// every declared artifact are fetched via Channel.Fetch, each artifact's
// bytes are verified against its ArtifactDescriptor.ContentHash, and
// only verified bytes are written into the CAS — never the raw return
// value alone (spec §11.2).
//
// Privacy CI invariant: this surface NEVER exposes credentials or
// signing keys. Source URLs and signature refs are reference-only;
// the typed Bundle struct's fields are the canonical boundary.
package bundle

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/kameas-ai/kenaz-harness/core/bundle/cache"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels"
	"github.com/kameas-ai/kenaz-harness/core/bundle/channels/localpath"
	"github.com/kameas-ai/kenaz-harness/core/bundle/integrity"
	"github.com/kameas-ai/kenaz-harness/core/bundle/lockfile"
	"github.com/kameas-ai/kenaz-harness/core/bundle/manifest"
	"github.com/kameas-ai/kenaz-harness/core/secrets"
	"github.com/kameas-ai/kenaz-harness/core/trust"
)

// manifestFileName is the well-known in-bundle relative path every
// channel resolves the manifest at — the same convention local_path
// always used, generalized to every channel kind (UNIT-5/6).
const manifestFileName = "kenaz.yaml"

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
//
// Put was added by UNIT-5 (bundle-download-and-verify-01PMZ909):
// before UNIT-5, Install never wrote artifact bytes anywhere, so Has
// was the only method List/Get needed for the "(uncached)" tier
// annotation. Install now streams every fetched artifact through Put,
// which performs its own SHA-256 verification against the digest the
// caller supplies (cache.CAS.Put's documented contract) and refuses to
// land bytes that fail it — this is where AC-005's "corrupt one byte
// in transit → refuse, no CAS entry" is enforced.
type CASLike interface {
	Has(digest string) bool
	Put(ctx context.Context, reader io.Reader, expected string) (cache.Receipt, error)
}

// API is the concrete BundleAPI implementation.
type API struct {
	mu     sync.RWMutex
	reader Reader
	writer Writer
	cas    CASLike

	// verifier, anchorsFunc and signingPolicyFunc are UNIT-4's
	// signature-verification seam (bundle-download-and-verify-01PMZ909,
	// spec §2). All three are optional; a nil verifier makes
	// VerifyManifestSignatures skip verification under any policy but
	// SigningRequired — matching pre-UNIT-4 behaviour for callers that
	// haven't wired a trust engine.
	verifier          trust.Verifier
	anchorsFunc       func(ctx context.Context) ([]trust.Anchor, error)
	signingPolicyFunc func() integrity.SigningPolicy

	// registry and creds are UNIT-5's channel-fetch seam
	// (bundle-download-and-verify-01PMZ909, spec §5.5). registry
	// defaults to NewDefaultRegistry() (local_path only) when nil —
	// every existing caller and test that never wired one keeps
	// working. creds defaults to secrets.NoopResolver{}, matching the
	// channel package's own established default
	// (core/bundle/channels/localpath wires the same façade).
	registry channels.Registry
	creds    secrets.ResolverAPI
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

// WithVerifier injects the trust.Verifier Install consults before
// writing a lockfile row (UNIT-4). A nil verifier (the zero value)
// means Install never verifies — VerifyManifestSignatures treats a nil
// verifier the same as "no signature checking configured", refusing
// only under SigningRequired.
func WithVerifier(v trust.Verifier) Option {
	return func(a *API) { a.verifier = v }
}

// WithAnchorsFunc injects a per-call anchor lookup (typically
// engine.ListAnchors, so a newly-installed anchor is visible on the
// very next Install without reconstructing the API).
func WithAnchorsFunc(f func(ctx context.Context) ([]trust.Anchor, error)) Option {
	return func(a *API) { a.anchorsFunc = f }
}

// WithSigningPolicyFunc injects a per-call signing-policy resolver
// (typically reading settings.Settings.EffectiveBundleSigningPolicy on
// every call so a Settings change takes effect on the next install
// without restarting). Defaults to SigningOptional when nil or when
// the function itself is nil.
func WithSigningPolicyFunc(f func() integrity.SigningPolicy) Option {
	return func(a *API) { a.signingPolicyFunc = f }
}

// WithChannelRegistry injects the channels.Registry Install routes
// every fetch through (UNIT-5). Nil (the default) makes Install lazily
// use NewDefaultRegistry() — local_path only. Production wiring
// (core/rpc/api.go) overrides this once UNIT-7 registers the
// http_mirror factory alongside local_path so the sequencing rule in
// plan.md ("UNIT-7 lands before UNIT-6 is enabled in production
// wiring") is enforced at the one call site that builds the
// production registry, not scattered across this package.
func WithChannelRegistry(r channels.Registry) Option {
	return func(a *API) { a.registry = r }
}

// WithSecretsResolver injects the secrets.ResolverAPI passed to
// Registry.Open for channels whose ChannelSpec.Auth needs resolving
// (e.g. UNIT-6's http_mirror). nil (the default) resolves to
// secrets.NoopResolver{} — safe for local_path, which never reads Auth.
func WithSecretsResolver(r secrets.ResolverAPI) Option {
	return func(a *API) { a.creds = r }
}

// NewDefaultRegistry returns a channels.Registry with only the
// always-available local_path factory registered. Callers that add
// further channels (e.g. UNIT-6's http_mirror) call Register on the
// returned registry themselves and pass the result to
// WithChannelRegistry — see core/rpc/api.go's UNIT-7 wiring, which is
// the only place the production http_mirror factory is registered
// (plan.md Rule 2: UNIT-7 must land first).
func NewDefaultRegistry() channels.Registry {
	r := channels.NewRegistry()
	// A fixed, package-owned kind string registered into a fresh
	// registry cannot collide; the error is structurally unreachable.
	_ = r.Register(localpath.Kind, localpath.Factory)
	return r
}

// NewAPI constructs the bundle view-scoped API.
func NewAPI(opts ...Option) *API {
	a := &API{}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// channelRegistry returns the configured registry, lazily defaulting
// to NewDefaultRegistry() so every pre-UNIT-5 caller (tests included)
// that never wired one keeps working unchanged.
func (a *API) channelRegistry() channels.Registry {
	if a.registry != nil {
		return a.registry
	}
	return NewDefaultRegistry()
}

// secretsResolver returns the configured credential resolver, lazily
// defaulting to secrets.NoopResolver{}.
func (a *API) secretsResolver() secrets.ResolverAPI {
	if a.creds != nil {
		return a.creds
	}
	return secrets.NoopResolver{}
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
// shape. Tier currently encodes the source channel + a REAL recorded
// verification result; once the resolver mission ships richer trust
// metadata this mapping moves to a typed function in core/bundle.
//
// UNIT-4 (spec FR-006, G-2): tier is derived from lb.Verified — set
// only by a positive VerifyManifestSignatures result at install time —
// never from lb.SignatureRef's mere presence. A pre-UNIT-4 lockfile row
// can carry a non-empty SignatureRef with Verified defaulting false
// (the field didn't exist yet), and must NOT render as "signed".
func lockedToBundle(lb lockfile.LockedBundle, cas CASLike, includeArtifacts bool) Bundle {
	tier := "channel"
	if lb.Verified {
		tier = "signed"
	}
	if cas != nil && !bundleIsCached(lb, cas) {
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

// bundleIsCached reports whether lb's bytes are actually present in
// cas. UNIT-5 (bundle-download-and-verify-01PMZ909): before UNIT-5,
// nothing ever fetched artifact bytes, so this check historically
// compared lb.ContentHash (the MANIFEST's canonical hash) against the
// CAS — a digest the CAS never stores anything under, because the CAS
// is keyed by ARTIFACT content hashes. That made "(uncached)" either
// always true (nothing installed real bytes) or, after UNIT-5 without
// this fix, permanently true even for a freshly, successfully cached
// install (AC-005's exact regression to avoid).
//
// A bundle with recorded per-artifact entries (every row UNIT-5+
// writes) is cached iff every one of those digests is in the CAS. A
// legacy row with no per-artifact entries (nothing UNIT-4-and-earlier
// ever wrote one) falls back to the old bundle-level content_hash
// check so TestList_UncachedTierAnnotation's pre-UNIT-5 fixture shape
// keeps its documented behaviour.
func bundleIsCached(lb lockfile.LockedBundle, cas CASLike) bool {
	if len(lb.Artifacts) > 0 {
		for _, art := range lb.Artifacts {
			if art.ContentHash == "" || !cas.Has(art.ContentHash) {
				return false
			}
		}
		return true
	}
	if lb.ContentHash == "" {
		return true
	}
	return cas.Has(lb.ContentHash)
}

// Install fetches and registers a bundle. It routes entirely through
// the core/bundle/channels abstraction (UNIT-5,
// bundle-download-and-verify-01PMZ909, spec §5.5): Registry.Open opens
// a Channel for req.Kind, the manifest itself is fetched via
// Channel.Fetch at the well-known manifestFileName path (generalizing
// what local_path always did — read a file at the channel root — to
// every channel kind), and every declared artifact is fetched and
// verified against its ArtifactDescriptor.ContentHash before being
// written into the CAS. A digest mismatch, an unreachable channel, or
// a rejected signature all refuse the install with NO lockfile row and
// NO CAS entry for the artifact that failed — the lockfile write is
// the last step, after every fetch has already succeeded.
//
// Before this unit, local_path only ever registered a lockfile row
// pointing at req.Path; nothing was copied or hashed, and removing the
// source directory silently orphaned the "installed" bundle. This is a
// visible behaviour change for anyone relying on that pointer
// semantics — local_path now copies and hashes, exactly like every
// other channel.
func (a *API) Install(ctx context.Context, req InstallRequest) (Bundle, error) {
	if a.reader == nil || a.writer == nil {
		return Bundle{}, fmt.Errorf("bundle: install requires a writable lockfile")
	}
	if req.Kind == "" {
		return Bundle{}, fmt.Errorf("bundle: install kind is required")
	}
	if req.Kind == "local_path" {
		if req.Path == "" {
			return Bundle{}, fmt.Errorf("bundle: install path is required")
		}
		if !filepath.IsAbs(req.Path) {
			return Bundle{}, fmt.Errorf("bundle: install path %q must be absolute", req.Path)
		}
	}

	spec := channels.ChannelSpec{Kind: req.Kind, Path: req.Path, URL: req.URL}
	ch, err := a.channelRegistry().Open(spec, a.secretsResolver())
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: open channel: %w", err)
	}
	if err := ch.Reachable(ctx); err != nil {
		return Bundle{}, fmt.Errorf("bundle: channel unreachable: %w", err)
	}

	var manifestBuf bytes.Buffer
	if _, err := ch.Fetch(ctx, channels.ArtifactCoord{Path: manifestFileName}, &manifestBuf); err != nil {
		return Bundle{}, fmt.Errorf("bundle: fetch manifest: %w", err)
	}
	m, err := manifest.Parse(manifestBuf.Bytes())
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: parse manifest: %w", err)
	}
	if err := m.Validate(manifest.ValidateOpts{}); err != nil {
		return Bundle{}, fmt.Errorf("bundle: validate manifest: %w", err)
	}

	// UNIT-4 (spec §2): verify signatures BEFORE any artifact fetch or
	// the lockfile lock, so a refused install leaves no lockfile row
	// and no CAS residue — "rollback on refusal" falls out of ordering
	// rather than needing an explicit undo.
	policy := integrity.SigningOptional
	if a.signingPolicyFunc != nil {
		policy = a.signingPolicyFunc()
	}
	var anchors []trust.Anchor
	if a.anchorsFunc != nil {
		anchors, err = a.anchorsFunc(ctx)
		if err != nil {
			return Bundle{}, fmt.Errorf("bundle: load trust anchors: %w", err)
		}
	}
	// local_path keeps the exact pre-UNIT-5 FileResolver (a direct
	// os.ReadFile against req.Path) so its already-tested signature
	// path — and FileResolver's own non-test caller, guarding it from
	// I10 orphaning — is unchanged. Every other channel resolves a
	// signature locator the same way it resolves an artifact: through
	// Channel.Fetch, so a channel that never has a local root (e.g.
	// UNIT-6's http_mirror) still works.
	resolve := integrity.FileResolver(req.Path)
	if req.Kind != "local_path" {
		resolve = channelSignatureResolver(ctx, ch)
	}
	verified, err := integrity.VerifyManifestSignatures(ctx, m, a.verifier, anchors, policy, resolve)
	if err != nil {
		return Bundle{}, fmt.Errorf("bundle: verify signatures: %w", err)
	}

	// UNIT-5: fetch + hash-verify + store every declared artifact BEFORE
	// touching the lockfile. A digest mismatch on any artifact refuses
	// the whole install; artifacts already streamed into the CAS by an
	// earlier iteration are untouched (they are individually
	// hash-verified, content-addressed, and harmless to leave behind —
	// what must never happen, and does not, is a lockfile row for a
	// bundle whose fetch did not fully succeed).
	if a.cas == nil && len(m.Artifacts) > 0 {
		return Bundle{}, fmt.Errorf("bundle: install requires a configured CAS to fetch %d artifact(s)", len(m.Artifacts))
	}
	for _, ad := range m.Artifacts {
		coord := channels.ArtifactCoord{
			BundleName:    m.Name,
			BundleVersion: m.Version,
			ArtifactName:  ad.Name,
			Path:          ad.Path,
			ContentHash:   ad.ContentHash,
		}
		if err := a.fetchArtifactToCAS(ctx, ch, coord); err != nil {
			return Bundle{}, fmt.Errorf("bundle: artifact %s: %w", ad.Name, err)
		}
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

	locator := req.Path
	if locator == "" {
		locator = req.URL
	}
	lb := lockfile.LockedBundle{
		Name:        m.Name,
		Version:     m.Version,
		Source:      req.Kind + ":" + locator,
		ContentHash: m.ContentHash(),
		// Verified is the real, positive verification result from
		// above — never derived from SignatureRef's mere presence
		// (G-2). false for an unsigned bundle under SigningOptional,
		// exactly as before UNIT-4.
		Verified: verified,
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

// fetchArtifactToCAS streams one artifact from ch straight into the
// CAS via an io.Pipe — no intermediate buffering, no temp file. The
// CAS's own Put verifies the streamed bytes' SHA-256 against
// coord.ContentHash and refuses (deleting its staging file) on
// mismatch; that refusal is this function's enforcement of "verify
// each artifact against its ArtifactDescriptor.ContentHash and refuse
// on mismatch" (spec UNIT-5 step 3) — channels themselves are
// documented to never verify content hashes (channel.go:37-39).
//
// io.Pipe is safe for concurrent use by design (it exists precisely
// for this producer/consumer shape); no shared mutable state crosses
// the goroutine boundary here, so this needs no CLAUDE.md
// mutex+snapshot fake pattern — that pattern is for TEST fakes the
// test body also reads, not for this production pipe.
func (a *API) fetchArtifactToCAS(ctx context.Context, ch channels.Channel, coord channels.ArtifactCoord) error {
	if coord.ContentHash == "" {
		return fmt.Errorf("no content_hash declared")
	}
	pr, pw := io.Pipe()
	fetchDone := make(chan error, 1)
	go func() {
		_, ferr := ch.Fetch(ctx, coord, pw)
		_ = pw.CloseWithError(ferr)
		fetchDone <- ferr
	}()
	_, putErr := a.cas.Put(ctx, pr, coord.ContentHash)
	if ferr := <-fetchDone; ferr != nil {
		return fmt.Errorf("fetch: %w", ferr)
	}
	if putErr != nil {
		return fmt.Errorf("store: %w", putErr)
	}
	return nil
}

// channelSignatureResolver resolves a manifest SignatureRef.Locator to
// bytes by fetching it through ch — the same channel the manifest and
// every artifact came from. This is the non-local_path counterpart to
// integrity.FileResolver: it has no notion of a filesystem bundleRoot,
// so it works for any channel kind, including one with no local root
// at all (UNIT-6's http_mirror, where a locator is a sibling URL path).
func channelSignatureResolver(ctx context.Context, ch channels.Channel) integrity.SignatureResolver {
	return func(locator string) ([]byte, error) {
		var buf bytes.Buffer
		if _, err := ch.Fetch(ctx, channels.ArtifactCoord{Path: locator}, &buf); err != nil {
			return nil, fmt.Errorf("fetch signature %s: %w", locator, err)
		}
		return buf.Bytes(), nil
	}
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

func (a casAdapter) Put(ctx context.Context, r io.Reader, expected string) (cache.Receipt, error) {
	return a.c.Put(ctx, r, expected)
}
