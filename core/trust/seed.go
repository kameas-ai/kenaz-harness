package trust

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// SeedOutcome reports what [SeedAnchor] did. Only SeedInstalled wrote
// anything; every other outcome is a deliberate no-op.
type SeedOutcome string

const (
	// SeedInstalled — no row knew this key or anchor id; the anchor was
	// installed.
	SeedInstalled SeedOutcome = "installed"
	// SeedAlreadyPresent — a LIVE anchor already carries this key (as its
	// current or rotation-previous key), whoever installed it: an
	// earlier boot's seed, an operator, or fleet distribution. Left
	// untouched.
	SeedAlreadyPresent SeedOutcome = "already_present"
	// SeedRevoked — a TOMBSTONED row carries this key or this anchor id.
	// Revocation wins over the baked default: the seed never resurrects
	// a removed anchor (Install would otherwise flip removed back to 0).
	SeedRevoked SeedOutcome = "revoked"
	// SeedIDTaken — a live anchor already uses this anchor id with a
	// DIFFERENT key (e.g. an operator or fleet anchor that rotated it).
	// Never overwritten.
	SeedIDTaken SeedOutcome = "id_taken"
)

// ErrSeedUnsupported is returned by [SeedAnchor] for a TrustEngine that
// is not this package's engine (a test double): seeding needs the
// tombstone-aware store lookups ListAnchors does not expose.
var ErrSeedUnsupported = errors.New("trust: engine does not support anchor seeding")

// anchorSeeder is implemented by *engine.
type anchorSeeder interface {
	seedAnchor(ctx context.Context, a Anchor) (SeedOutcome, error)
}

// SeedAnchor installs a build-time default anchor (e.g. the baked-in
// Kameas release public key — engine-publication-01ENPUB01 WP-H2) ONLY
// when nothing in the store already knows it. It is the boot-time
// counterpart of [TrustEngine.InstallAnchor] with the opposite
// precedence: InstallAnchor is an operator/fleet WRITE and replaces;
// SeedAnchor is a DEFAULT and yields to every existing row —
//
//   - a live anchor with the same key → untouched (SeedAlreadyPresent);
//   - a tombstoned anchor with the same key or id → stays removed
//     (SeedRevoked) — a revoked baked key does not come back on the next
//     boot;
//   - a live anchor with the same id but another key → untouched
//     (SeedIDTaken).
//
// The check and the install run under the engine's mutation lock, so a
// concurrent InstallAnchor/RemoveAnchor cannot interleave.
func SeedAnchor(ctx context.Context, eng TrustEngine, a Anchor) (SeedOutcome, error) {
	s, ok := eng.(anchorSeeder)
	if !ok {
		return "", ErrSeedUnsupported
	}
	return s.seedAnchor(ctx, a)
}

func (e *engine) seedAnchor(ctx context.Context, a Anchor) (SeedOutcome, error) {
	if a.AnchorID == "" || a.PublicKey.Fingerprint == "" || len(a.PublicKey.Bytes) == 0 {
		return "", fmt.Errorf("trust: seed anchor: id, key bytes and fingerprint are required")
	}
	if got := ComputeFingerprint(a.PublicKey.Bytes); got != a.PublicKey.Fingerprint {
		return "", fmt.Errorf("trust: seed anchor %s: fingerprint does not match key bytes", a.AnchorID)
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	existing, err := e.anchors.FindByKeyID(ctx, a.PublicKey.Fingerprint)
	switch {
	case err == nil && existing.Removed:
		return SeedRevoked, nil
	case err == nil:
		return SeedAlreadyPresent, nil
	case !errors.Is(err, ErrAnchorNotFound):
		return "", fmt.Errorf("trust: seed anchor %s: lookup by key: %w", a.AnchorID, err)
	}
	byID, err := e.anchors.Get(ctx, a.AnchorID)
	switch {
	case err == nil && byID.Removed:
		return SeedRevoked, nil
	case err == nil:
		return SeedIDTaken, nil
	case !errors.Is(err, ErrAnchorNotFound):
		return "", fmt.Errorf("trust: seed anchor %s: lookup by id: %w", a.AnchorID, err)
	}

	if err := e.anchors.Install(ctx, a); err != nil {
		return "", err
	}
	fields := map[string]string{"kind": a.Kind.String(), "seeded": "true"}
	if origin := a.Metadata["origin"]; origin != "" {
		fields["origin"] = origin
	}
	emitAuditOrLog(ctx, e.emitter, Event{
		Kind:       EventAnchorInstalled,
		OccurredAt: time.Now().UTC(),
		AnchorID:   a.AnchorID,
		Algorithm:  a.Algorithm,
		Fields:     fields,
	})
	return SeedInstalled, nil
}
