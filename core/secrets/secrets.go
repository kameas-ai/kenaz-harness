// Package secrets is the harness's cross-cutting credential-resolution
// layer. Every other consumer that needs a credential value at runtime
// (`llm-connector`, `a2a-signed-cards-trust`,
// `shared-context-distribution`, `bundle-format-resolver`,
// `storage-foundations`, `event-log`) calls into this package.
//
// Public types are re-exported from the subpackages where they are
// defined. Operationally:
//
//	resolver := secrets.NewResolver(secrets.ResolverConfig{
//	    Registry: reg,
//	    Cache:    cache,
//	    Logger:   evlog,
//	})
//	s, err := resolver.Resolve(ctx, ref, "llm:anthropic")
//	if err != nil { return err }
//	defer s.Destroy()
//	return s.Use(func(v []byte) error { ... })
//
// Per DIRECTIVE_001 / C-001, no other `core/` package imports a
// backend SDK directly. They depend on this package and its
// subpackages for the contract.
//
// Spec mapping (mission-wide): FR-001 through FR-017, NFR-001
// through NFR-007, C-001 through C-006, SC-001 through SC-006.
package secrets

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sigil-tech/kaneaz-harness/core/secrets/cache"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/events"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/preflight"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/ref"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/registry"
	"github.com/sigil-tech/kaneaz-harness/core/secrets/secret"
)

// Re-exports so consumers depend only on `core/secrets`.
type (
	// CredentialReference is the indirect pointer; see ref.CredentialReference.
	CredentialReference = ref.CredentialReference
	// Reference is a shorter alias for CredentialReference accepted by
	// consumers (some early WPs adopted the shorter form).
	Reference = ref.CredentialReference
	// RefKind enumerates supported reference kinds.
	RefKind = ref.RefKind
	// Backend is the resolution contract; see registry.Backend.
	Backend = registry.Backend
	// BackendKind is the canonical backend name.
	BackendKind = registry.BackendKind
	// BackendHealth is the structured health-probe result.
	BackendHealth = registry.BackendHealth
	// HealthStatus is the discrete health state.
	HealthStatus = registry.HealthStatus
	// Secret is the in-memory credential handle; see secret.Secret.
	Secret = secret.Secret
	// PreFlightResult is the per-reference startup outcome.
	PreFlightResult = preflight.Result
	// PreFlightStatus enumerates pre-flight outcomes.
	PreFlightStatus = preflight.Status
	// ResolutionEvent is the FR-012 payload; see events.ResolutionEvent.
	ResolutionEvent = events.ResolutionEvent
	// EventKind enumerates resolution-event kinds.
	EventKind = events.EventKind
	// EventLogger is the cross-mission emission contract.
	EventLogger = events.EventLogger
)

// RefKind constant re-exports.
const (
	RefUnknown   = ref.RefUnknown
	RefEnv       = ref.RefEnv
	RefKeychain  = ref.RefKeychain
	RefFile      = ref.RefFile
	RefAWSProfile = ref.RefAWSProfile
	RefAWSKMS    = ref.RefAWSKMS
	RefYubikeyPIV = ref.RefYubikeyPIV
	RefPKCS11    = ref.RefPKCS11
)

// Health status re-exports.
const (
	HealthOK          = registry.HealthOK
	HealthDegraded    = registry.HealthDegraded
	HealthUnavailable = registry.HealthUnavailable
)

// ErrUnknownKind is returned when a CredentialReference carries a Kind
// that is not registered with any backend. Re-exported here so consumers
// (llm/credref, acp/peers, etc.) can match it via errors.Is without
// importing the registry sub-package.
var ErrUnknownKind = errors.New("secrets: unknown reference kind or no backend configured")

// PreFlightStatus re-exports.
const (
	StatusResolvable           = preflight.StatusResolvable
	StatusUnresolvable         = preflight.StatusUnresolvable
	StatusOptionalUnresolvable = preflight.StatusOptionalUnresolvable
	StatusBackendDegraded      = preflight.StatusBackendDegraded
)

// EventKind re-exports.
const (
	EventResolutionRequested       = events.KindResolutionRequested
	EventResolutionCacheHit        = events.KindResolutionCacheHit
	EventResolutionCacheMiss       = events.KindResolutionCacheMiss
	EventResolutionBackendDispatch = events.KindResolutionBackendDispatch
	EventResolutionOK              = events.KindResolutionOK
	EventResolutionFailed          = events.KindResolutionFailed
	EventPreFlightOK               = events.KindPreFlightOK
	EventPreFlightFailed           = events.KindPreFlightFailed
	EventCacheInvalidated          = events.KindCacheInvalidated
	EventRotationDetected          = events.KindRotationDetected
)

// ErrPreflightFailed is returned by Resolver.PreFlight when at least
// one required reference is unresolvable.
var ErrPreflightFailed = preflight.ErrPreflightFailed

// ResolverConfig configures a Resolver. Zero values are filled with
// safe defaults at NewResolver.
type ResolverConfig struct {
	// Registry holds the backend dispatch table. Required.
	Registry *registry.Registry
	// Cache is the TTL cache. If nil, a default cache is created.
	Cache *cache.Cache
	// Logger receives ResolutionEvents. May be nil during the boot
	// cycle break; in that case events are dropped.
	Logger events.EventLogger
}

// ResolverAPI is the small interface most consumers need from the
// resolver. *Resolver satisfies it; NoopResolver is the stub used by
// tests and contexts where credentials aren't required (e.g. the
// local_path channel). Defined as an interface so callers can accept
// either without taking a hard dependency on *Resolver internals.
type ResolverAPI interface {
	// Resolve fetches a credential. See *Resolver.Resolve.
	Resolve(ctx context.Context, cred CredentialReference, consumerID string) (Secret, error)
	// ResolveFresh fetches a credential bypassing the cache but still
	// routing through the resolver for ResolutionEvent emission. Used
	// by spawn paths (MCP child processes) where stale cached values
	// would let a child outlive a credential rotation. Caches MUST NOT
	// be populated by ResolveFresh — the freshness contract is "every
	// call hits the backend." See *Resolver.ResolveFresh.
	ResolveFresh(ctx context.Context, cred CredentialReference, consumerID string) (Secret, error)
}

// NoopResolver is a ResolverAPI implementation that always reports a
// resolution failure. It is the safe placeholder for test channels and
// for runtime paths that intentionally never need to resolve a secret
// (e.g. local_path with no auth ref).
type NoopResolver struct{}

// Resolve always returns an error reporting that no resolver is wired.
func (NoopResolver) Resolve(_ context.Context, _ CredentialReference, _ string) (Secret, error) {
	return nil, errors.New("secrets: no resolver configured (NoopResolver)")
}

// ResolveFresh always returns an error reporting that no resolver is wired.
func (NoopResolver) ResolveFresh(_ context.Context, _ CredentialReference, _ string) (Secret, error) {
	return nil, errors.New("secrets: no resolver configured (NoopResolver)")
}

// Compile-time assertion that NoopResolver satisfies ResolverAPI.
var _ ResolverAPI = NoopResolver{}

// Resolver is the public entry point. It composes the registry,
// cache, pre-flight validator, and event emission.
type Resolver struct {
	reg   *registry.Registry
	cache *cache.Cache
	log   events.EventLogger
	pre   *preflight.Validator
}

// NewResolver assembles a Resolver from cfg.
func NewResolver(cfg ResolverConfig) (*Resolver, error) {
	if cfg.Registry == nil {
		return nil, errors.New("secrets: ResolverConfig.Registry is required")
	}
	c := cfg.Cache
	if c == nil {
		c = cache.New()
	}
	r := &Resolver{
		reg:   cfg.Registry,
		cache: c,
		log:   cfg.Logger,
	}
	r.pre = preflight.New(cfg.Registry, cfg.Logger)
	// Wire backend-driven invalidation: each backend's rotation
	// signal lands in Cache.Notify; the cache emits a
	// secret.rotation.detected event.
	c.Subscribe(func(rr ref.CredentialReference) {
		r.emit(context.Background(), events.KindCacheInvalidated, rr, "", "", false, time.Time{})
	})
	return r, nil
}

// Resolve fetches a credential. It checks cache first, dispatches to
// the registered backend on miss, and emits ResolutionEvents at every
// branch.
//
// The returned Secret is owned by the caller for the duration of its
// scope: caller MUST call Secret.Use(...) and Secret.Destroy().
func (r *Resolver) Resolve(ctx context.Context, cred ref.CredentialReference, consumerID string) (Secret, error) {
	if consumerID != "" {
		cred.ConsumerID = consumerID
	}
	t0 := time.Now()
	r.emit(ctx, events.KindResolutionRequested, cred, "", "", false, t0)

	if s, ok := r.cache.Get(cred); ok {
		r.emit(ctx, events.KindResolutionCacheHit, cred, "", "ok", true, t0)
		return s, nil
	}
	r.emit(ctx, events.KindResolutionCacheMiss, cred, "", "", false, t0)

	b, err := r.reg.Lookup(cred.Kind)
	if err != nil {
		r.emit(ctx, events.KindResolutionFailed, cred, "", "no_backend", false, t0)
		return nil, err
	}
	r.emit(ctx, events.KindResolutionBackendDispatch, cred, b.Kind(), "", false, t0)

	s, err := b.Resolve(ctx, cred)
	if err != nil {
		r.emit(ctx, events.KindResolutionFailed, cred, b.Kind(), "failed", false, t0)
		return nil, fmt.Errorf("secrets: backend %s: %w", b.Kind(), err)
	}
	r.cache.Put(cred, b.Kind(), s)
	r.emit(ctx, events.KindResolutionOK, cred, b.Kind(), "ok", false, t0)
	return s, nil
}

// ResolveFresh fetches a credential, bypassing the cache but still
// emitting ResolutionEvents at every branch. Use this on spawn paths
// (MCP child processes, exec'd subagents) where a cached value could
// outlive a rotation and be inherited by a forked process the resolver
// can no longer invalidate.
//
// Contract:
//   - Cache is NEVER read.
//   - Cache is NEVER written. The fresh secret returns to the caller
//     and dies with their scope; subsequent Resolve calls will still
//     pay a backend round-trip until the next cache-write path runs.
//   - Events emit identically to Resolve so observability stays
//     uniform: KindResolutionRequested, KindResolutionBackendDispatch,
//     KindResolutionOK / KindResolutionFailed. KindResolutionCacheHit /
//     KindResolutionCacheMiss are NOT emitted (no cache check happens).
//
// The returned Secret is owned by the caller for the duration of its
// scope: caller MUST call Secret.Use(...) and Secret.Destroy().
func (r *Resolver) ResolveFresh(ctx context.Context, cred ref.CredentialReference, consumerID string) (Secret, error) {
	if consumerID != "" {
		cred.ConsumerID = consumerID
	}
	t0 := time.Now()
	r.emit(ctx, events.KindResolutionRequested, cred, "", "", false, t0)

	b, err := r.reg.Lookup(cred.Kind)
	if err != nil {
		r.emit(ctx, events.KindResolutionFailed, cred, "", "no_backend", false, t0)
		return nil, err
	}
	r.emit(ctx, events.KindResolutionBackendDispatch, cred, b.Kind(), "", false, t0)

	s, err := b.Resolve(ctx, cred)
	if err != nil {
		r.emit(ctx, events.KindResolutionFailed, cred, b.Kind(), "failed", false, t0)
		return nil, fmt.Errorf("secrets: backend %s: %w", b.Kind(), err)
	}
	r.emit(ctx, events.KindResolutionOK, cred, b.Kind(), "ok", false, t0)
	return s, nil
}

// PreFlight runs the WP06 pre-flight validator over refs.
func (r *Resolver) PreFlight(ctx context.Context, refs []CredentialReference) ([]PreFlightResult, error) {
	return r.pre.Run(ctx, refs)
}

// Health returns each registered backend's current health probe.
func (r *Resolver) Health(ctx context.Context) map[BackendKind]BackendHealth {
	out := make(map[BackendKind]BackendHealth, 4)
	for _, b := range r.reg.List() {
		out[b.Kind()] = b.Health(ctx)
	}
	return out
}

// Invalidate evicts cred from the cache. Used by consumers that detect
// auth-rejection (FR-011) and by the operator CLI.
func (r *Resolver) Invalidate(cred CredentialReference) {
	r.cache.Invalidate(cred)
}

// NotifyRotation is the backend-side hook: a backend that detects a
// rotation signal calls this to evict the cached value.
func (r *Resolver) NotifyRotation(ctx context.Context, cred CredentialReference) {
	r.cache.Notify(cred)
	r.emit(ctx, events.KindRotationDetected, cred, "", "", false, time.Now())
}

// Close releases all cached secrets and stops the rotation goroutine.
func (r *Resolver) Close() error {
	r.cache.Close()
	return nil
}

func (r *Resolver) emit(ctx context.Context, kind EventKind, cred CredentialReference, b BackendKind, outcome string, cacheHit bool, t0 time.Time) {
	if r.log == nil {
		return
	}
	e := events.NewResolutionEvent(cred, cred.ConsumerID)
	e.BackendKind = b
	e.Outcome = outcome
	e.CacheHit = cacheHit
	if !t0.IsZero() {
		e.LatencyMS = time.Since(t0).Milliseconds()
	}
	_ = r.log.Append(ctx, kind, e)
}
