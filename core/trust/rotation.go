package trust

import (
	"context"
	"time"
)

// rotationManager owns FR-005 / FR-013 — applying overlap windows to an
// anchor and reporting whether a verification against PreviousKey is still
// inside the window. The manager is a small struct on the engine (no
// goroutines at v1.0); WP07 will lift it out into its own type if/when an
// auto-purge timer is needed.
type rotationManager struct {
	store AnchorStore
}

func newRotationManager(store AnchorStore) *rotationManager {
	return &rotationManager{store: store}
}

// Begin stamps PreviousKey + OverlapEnds onto the anchor. Returns
// [ErrAnchorNotFound] if the anchor does not exist or is tombstoned.
func (r *rotationManager) Begin(ctx context.Context, anchorID string, newKey PublicKey, overlap time.Duration) error {
	a, err := r.store.Get(ctx, anchorID)
	if err != nil {
		return err
	}
	if a.Removed {
		return ErrAnchorNotFound
	}
	prev := a.PublicKey
	a.PreviousKey = &prev
	end := time.Now().UTC().Add(overlap)
	a.OverlapEnds = &end
	a.PublicKey = newKey
	a.Algorithm = newKey.Algorithm
	return r.store.Update(ctx, a)
}

// Complete clears PreviousKey + OverlapEnds.
func (r *rotationManager) Complete(ctx context.Context, anchorID string) error {
	a, err := r.store.Get(ctx, anchorID)
	if err != nil {
		return err
	}
	a.PreviousKey = nil
	a.OverlapEnds = nil
	return r.store.Update(ctx, a)
}

// withinGrace reports whether the anchor's overlap window currently
// covers `at`. Used by the verify pipeline at step 8 (plan §4.2).
func withinGrace(a Anchor, at time.Time) bool {
	if a.PreviousKey == nil || a.OverlapEnds == nil {
		return false
	}
	return !at.After(*a.OverlapEnds)
}
