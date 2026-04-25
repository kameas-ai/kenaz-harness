package trust

import (
	"context"
	"sort"
	"sync"
	"time"
)

// AnchorStore is the persistence-agnostic surface the engine uses to
// resolve and mutate anchors. WP06 will swap the in-memory implementation
// for a storage-foundations-backed SQLite store; the interface remains
// stable.
type AnchorStore interface {
	// Get returns the anchor by id. Returns [ErrAnchorNotFound] when
	// no row exists; returns the anchor with Removed=true when the
	// anchor has been tombstoned (lookup distinguishes "missing" vs
	// "removed" per spec edge case).
	Get(ctx context.Context, anchorID string) (Anchor, error)

	// FindByKeyID resolves an anchor by the public-key fingerprint that
	// signed an envelope. The verify pipeline calls this when the
	// envelope's KeyID is set. Returns [ErrAnchorNotFound] if no live
	// or tombstoned anchor matches.
	FindByKeyID(ctx context.Context, keyID string) (Anchor, error)

	// Install writes (or replaces) an anchor. Returns
	// [ErrIdentityCollision] when a different fingerprint is already
	// bound to the agent_id (FR-015).
	Install(ctx context.Context, a Anchor) error

	// Tombstone marks the anchor Removed without deleting the row.
	// Subsequent FindByKeyID lookups return the anchor with
	// Removed=true so verification can produce [RejAnchorRemoved].
	Tombstone(ctx context.Context, anchorID string) error

	// Update mutates rotation state (PreviousKey / OverlapEnds) for an
	// anchor. Used by [TrustEngine.BeginRotation] /
	// [TrustEngine.CompleteRotation].
	Update(ctx context.Context, a Anchor) error

	// List returns every live anchor (Removed = false).
	List(ctx context.Context) ([]Anchor, error)
}

// memAnchorStore is the in-memory [AnchorStore] used by the v1.0 engine
// until WP06 lands the SQLite-backed implementation. It is thread-safe
// and satisfies the verify pipeline's read-mostly access pattern.
type memAnchorStore struct {
	mu      sync.RWMutex
	anchors map[string]Anchor // anchor_id -> Anchor (Removed flag preserved)
	byKeyID map[string]string // public-key fingerprint -> anchor_id
}

// newMemAnchorStore constructs an empty in-memory anchor store.
func newMemAnchorStore() *memAnchorStore {
	return &memAnchorStore{
		anchors: make(map[string]Anchor),
		byKeyID: make(map[string]string),
	}
}

// Get implements [AnchorStore].
func (s *memAnchorStore) Get(_ context.Context, anchorID string) (Anchor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.anchors[anchorID]
	if !ok {
		return Anchor{}, ErrAnchorNotFound
	}
	return a, nil
}

// FindByKeyID implements [AnchorStore].
func (s *memAnchorStore) FindByKeyID(_ context.Context, keyID string) (Anchor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byKeyID[keyID]
	if !ok {
		return Anchor{}, ErrAnchorNotFound
	}
	a, ok := s.anchors[id]
	if !ok {
		return Anchor{}, ErrAnchorNotFound
	}
	return a, nil
}

// Install implements [AnchorStore].
func (s *memAnchorStore) Install(_ context.Context, a Anchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Identity-collision: if another *live* anchor binds the same
	// agent_id (peer_id) to a different fingerprint, refuse.
	if a.PeerID != "" {
		for _, existing := range s.anchors {
			if existing.Removed || existing.AnchorID == a.AnchorID {
				continue
			}
			if existing.PeerID == a.PeerID && existing.PublicKey.Fingerprint != a.PublicKey.Fingerprint {
				return ErrIdentityCollision
			}
		}
	}
	if a.InstalledAt.IsZero() {
		a.InstalledAt = time.Now().UTC()
	}
	a.Removed = false
	s.anchors[a.AnchorID] = a
	if a.PublicKey.Fingerprint != "" {
		s.byKeyID[a.PublicKey.Fingerprint] = a.AnchorID
	}
	if a.PreviousKey != nil && a.PreviousKey.Fingerprint != "" {
		s.byKeyID[a.PreviousKey.Fingerprint] = a.AnchorID
	}
	return nil
}

// Tombstone implements [AnchorStore]. The row is preserved with
// Removed=true so subsequent lookups can distinguish "missing" from
// "removed" per spec edge case.
func (s *memAnchorStore) Tombstone(_ context.Context, anchorID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.anchors[anchorID]
	if !ok {
		return ErrAnchorNotFound
	}
	a.Removed = true
	s.anchors[anchorID] = a
	return nil
}

// Update implements [AnchorStore].
func (s *memAnchorStore) Update(_ context.Context, a Anchor) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.anchors[a.AnchorID]; !ok {
		return ErrAnchorNotFound
	}
	s.anchors[a.AnchorID] = a
	if a.PublicKey.Fingerprint != "" {
		s.byKeyID[a.PublicKey.Fingerprint] = a.AnchorID
	}
	if a.PreviousKey != nil && a.PreviousKey.Fingerprint != "" {
		s.byKeyID[a.PreviousKey.Fingerprint] = a.AnchorID
	}
	return nil
}

// List implements [AnchorStore].
func (s *memAnchorStore) List(_ context.Context) ([]Anchor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Anchor, 0, len(s.anchors))
	for _, a := range s.anchors {
		if a.Removed {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AnchorID < out[j].AnchorID })
	return out, nil
}
