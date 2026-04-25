package trust

import (
	"context"
	"sync"
	"time"
)

// RevocationCache is the surface the verify pipeline uses to check whether
// a key id or identity is revoked (FR-006). v1.0 ships an in-memory
// implementation; WP08 swaps in the SQLite-backed cache with manual-pull
// distribution per FR-007.
type RevocationCache interface {
	// Ingest records a new revocation. Re-ingesting the same record id
	// is idempotent.
	Ingest(ctx context.Context, rec RevocationRecord) error

	// IsRevoked returns true when an effective revocation exists for
	// either the supplied keyID *or* identity (agent_id) at time `at`.
	// Per spec US4 acceptance scenario 2, revocation applies to the
	// identity, not just future-dated signatures.
	IsRevoked(ctx context.Context, keyID, identity string, at time.Time) (bool, RevocationRecord)
}

// memRevocationCache is the v1.0 in-memory revocation cache.
type memRevocationCache struct {
	mu        sync.RWMutex
	byKeyID   map[string]RevocationRecord
	byAgentID map[string]RevocationRecord
}

// newMemRevocationCache constructs an empty in-memory cache.
func newMemRevocationCache() *memRevocationCache {
	return &memRevocationCache{
		byKeyID:   make(map[string]RevocationRecord),
		byAgentID: make(map[string]RevocationRecord),
	}
}

// Ingest implements [RevocationCache].
func (c *memRevocationCache) Ingest(_ context.Context, rec RevocationRecord) error {
	if rec.RevocationID == "" {
		return ErrInvalidEnvelope
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if rec.IngestedAt.IsZero() {
		rec.IngestedAt = time.Now().UTC()
	}
	switch rec.SubjectKind {
	case RevocationByKeyID:
		c.byKeyID[rec.SubjectID] = rec
	case RevocationByIdentity:
		c.byAgentID[rec.SubjectID] = rec
	}
	return nil
}

// IsRevoked implements [RevocationCache].
func (c *memRevocationCache) IsRevoked(_ context.Context, keyID, identity string, at time.Time) (bool, RevocationRecord) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if keyID != "" {
		if r, ok := c.byKeyID[keyID]; ok && !r.EffectiveAt.After(at) {
			return true, r
		}
	}
	if identity != "" {
		if r, ok := c.byAgentID[identity]; ok && !r.EffectiveAt.After(at) {
			return true, r
		}
	}
	return false, RevocationRecord{}
}
