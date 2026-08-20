package trust

import (
	"context"
	"sync"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/trust/internal/algo"
)

// engine is the concrete v1.0 [TrustEngine] implementation.
//
// It composes the in-memory [AnchorStore] / [RevocationCache] /
// [IdentityCache] (swappable in WP06+) with the [BackendDispatcher]
// (provided by core/trust/backends/) and an [Emitter] for audit.
type engine struct {
	mu          sync.Mutex
	algo        *algo.Registry
	anchors     AnchorStore
	revocations RevocationCache
	identities  IdentityCache
	dispatcher  BackendDispatcher
	emitter     Emitter
	policy      AlgorithmPolicy
	clockSkew   ClockSkewPolicy
	chainDepth  ChainDepthPolicy
	rotation    *rotationManager
	cfg         Config
}

// NewEngineWithEmitter constructs the production v1.0 engine.
//
// dispatcher is typically *backends.Registry (core/trust/backends/) but
// tests can substitute any [BackendDispatcher]. emitter is the bridge
// to core/event/ — pass [NewMemoryEmitter] for unit tests.
//
// Verify-only callers (no signing) may pass dispatcher = nil; sign
// attempts will then return [ErrBackendNotRegistered].
func NewEngineWithEmitter(cfg Config, dispatcher BackendDispatcher, emitter Emitter) (TrustEngine, error) {
	policy := cfg.AlgorithmPolicy
	if len(policy.Allowed) == 0 {
		policy = DefaultAlgorithmPolicy()
	}
	clock := cfg.ClockSkew
	if clock.Tolerance == 0 {
		clock = DefaultClockSkewPolicy()
	}
	chain := cfg.ChainDepth
	if chain.Max == 0 {
		chain = DefaultChainDepthPolicy()
	}
	// UNIT-3: cfg.Anchors is the persistence seam (spec §1.7 F-1). A
	// nil Config.Anchors keeps the v1.0 in-memory default; a caller
	// that constructed a real store (typically NewSQLiteAnchorStore
	// against the harness's data.db) gets an engine whose anchors
	// survive across NewEngineWithEmitter calls — including a relaunch.
	store := cfg.Anchors
	if store == nil {
		store = newMemAnchorStore()
	}
	e := &engine{
		algo:        algo.New(),
		anchors:     store,
		revocations: newMemRevocationCache(),
		identities:  newMemIdentityCache(),
		dispatcher:  dispatcher,
		emitter:     emitter,
		policy:      policy,
		clockSkew:   clock,
		chainDepth:  chain,
		rotation:    newRotationManager(store),
		cfg:         cfg,
	}
	return e, nil
}

// Verify implements [TrustEngine].
func (e *engine) Verify(ctx context.Context, payload []byte, env Envelope, opts VerifyOptions) (VerificationResult, error) {
	deps := verifyDeps{
		algo:        e.algo,
		anchors:     e.anchors,
		revocations: e.revocations,
		identities:  e.identities,
		policy:      e.policy,
		clockSkew:   e.clockSkew,
		chainDepth:  e.chainDepth,
		emitter:     e.emitter,
	}
	return runVerify(ctx, deps, payload, env, opts)
}

// Sign implements [TrustEngine].
func (e *engine) Sign(ctx context.Context, payload []byte, ident IdentityRef, opts SignOptions) (Envelope, error) {
	return runSign(ctx, signDeps{dispatcher: e.dispatcher, emitter: e.emitter}, payload, ident, opts)
}

// InstallAnchor implements [TrustEngine].
func (e *engine) InstallAnchor(ctx context.Context, a Anchor) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if a.AnchorID == "" {
		return ErrInvalidEnvelope
	}
	if err := e.anchors.Install(ctx, a); err != nil {
		return err
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventAnchorInstalled,
		OccurredAt: time.Now().UTC(),
		AnchorID:   a.AnchorID,
		Algorithm:  a.Algorithm,
		Fields:     map[string]string{"kind": a.Kind.String()},
	})
	return nil
}

// RemoveAnchor implements [TrustEngine].
func (e *engine) RemoveAnchor(ctx context.Context, anchorID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.anchors.Tombstone(ctx, anchorID); err != nil {
		return err
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventAnchorRemoved,
		OccurredAt: time.Now().UTC(),
		AnchorID:   anchorID,
	})
	return nil
}

// ListAnchors implements [TrustEngine].
func (e *engine) ListAnchors(ctx context.Context) ([]Anchor, error) {
	return e.anchors.List(ctx)
}

// BeginRotation implements [TrustEngine].
func (e *engine) BeginRotation(ctx context.Context, anchorID string, newKey PublicKey, overlap time.Duration) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rotation.Begin(ctx, anchorID, newKey, overlap); err != nil {
		return err
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventKeyRotated,
		OccurredAt: time.Now().UTC(),
		AnchorID:   anchorID,
		Algorithm:  newKey.Algorithm,
		Fields:     map[string]string{"phase": "begin", "overlap": overlap.String()},
	})
	return nil
}

// CompleteRotation implements [TrustEngine].
func (e *engine) CompleteRotation(ctx context.Context, anchorID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.rotation.Complete(ctx, anchorID); err != nil {
		return err
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventKeyRotated,
		OccurredAt: time.Now().UTC(),
		AnchorID:   anchorID,
		Fields:     map[string]string{"phase": "complete"},
	})
	return nil
}

// IngestRevocation implements [TrustEngine].
func (e *engine) IngestRevocation(ctx context.Context, rec RevocationRecord) error {
	if err := e.revocations.Ingest(ctx, rec); err != nil {
		return err
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventRevocationIngested,
		OccurredAt: time.Now().UTC(),
		Fields: map[string]string{
			"revocation_id": rec.RevocationID,
			"subject_kind":  rec.SubjectKind.String(),
			"subject_id":    rec.SubjectID,
		},
	})
	return nil
}

// Preflight implements [TrustEngine] (FR-014).
func (e *engine) Preflight(ctx context.Context) ([]PreflightFinding, error) {
	return runPreflight(ctx, e.cfg, e.anchors)
}
