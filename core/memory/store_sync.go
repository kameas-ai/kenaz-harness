// Store-side operations for Fleet memory sync (memory-sync-01MEMSY01 WP07).
//
// The sync client (core/fleet/memory_sync.go) never edits chunks directly:
// every sync mutation is one of the batched, single-save operations below,
// applied under the store mutex. None of them ticks the HLC for a value it
// did not originate, marks a pulled change dirty, or records a forget —
// sync-originated deletes are local (Fleet already knows).
package memory

import (
	"context"
	"time"
)

// SyncScopes are the scope kinds Fleet memory sync carries in v1.
// session never syncs. project is present in the vocabulary but
// hard-disabled until Fleet's P5 prerequisites land (see
// ProjectScopeSyncEnabled).
var SyncScopes = []string{ScopeKindGlobal, ScopeKindLongTerm}

// ProjectScopeSyncEnabled is the single switch for project-scoped memory
// sync. BLOCKED on Fleet P5 (contract-harness-memory.md "P5
// prerequisites"): Fleet's project registry (#185, 0117) is live, but
// memory push still rejects scope=project (scope_not_supported_yet) and
// the pull feed does not yet carry scope_id. When Fleet opens it, flipping
// this constant is the unblock — plus mapping the local project id to the
// Fleet project id at the push/pull boundary (registry P4 scope_id
// translation), which the registry contract will dictate. Owner: the
// harness change that lands against Fleet P5.
const ProjectScopeSyncEnabled = false

// IsSyncScope reports whether chunks of scope kind may ever sync.
func IsSyncScope(kind string) bool {
	switch kind {
	case ScopeKindGlobal, ScopeKindLongTerm:
		return true
	case ScopeKindProject:
		return ProjectScopeSyncEnabled
	}
	return false
}

// SyncStore is the store capability the memory sync client drives.
type SyncStore interface {
	Store
	ChunkRemover
	// MarkSyncSent marks ids as riding a push request (from now on Fleet
	// may know them), stamps any unstamped LWW field with a fresh tick
	// (legacy chunks; Fleet keeps first-seen), and returns each present
	// chunk's state for building the request. Absent ids are omitted —
	// a chunk deleted before this call is never sent.
	MarkSyncSent(ctx context.Context, ids []string, at time.Time) (map[string]Chunk, error)
	// ApplySyncOutcomes applies a push response in one save.
	ApplySyncOutcomes(ctx context.Context, outs []SyncOutcome, at time.Time) error
	// ApplyRemote applies one pull page in one save.
	ApplyRemote(ctx context.Context, recs []RemoteRecord, at time.Time) error
	// LatestSyncedAt is the newest SyncedAt in the store — a reset epoch
	// must sort strictly after it so the post-reset sweep can tell rows the
	// snapshot touched from rows it did not.
	LatestSyncedAt(ctx context.Context) time.Time
	// DeleteSyncedBefore deletes every chunk Fleet had accepted or sent
	// to us (SyncedAt set) that the reset snapshot did not touch
	// (SyncedAt before epoch) — the reset snapshot rule, persisted across
	// cycles via the epoch instead of an in-memory keep set.
	DeleteSyncedBefore(ctx context.Context, epoch time.Time) (int, error)
	// DeleteCreatedBefore deletes every sync-scope chunk created before
	// the erased_before HLC (forget-all replay), synced or not — EXCEPT
	// rows the post-erase snapshot touched (SyncedAt >= epoch): anything
	// Fleet still holds after the erase is post-erase by definition, and a
	// pulled chunk's CreatedHLC is "" (Fleet does not carry it), which
	// would otherwise sort below every erased_before.
	DeleteCreatedBefore(ctx context.Context, hlc string, epoch time.Time) (int, error)
	// ClearSyncMarkers makes every chunk read as never-synced (this
	// device ran forget-all: nothing it pushed exists on Fleet anymore).
	ClearSyncMarkers(ctx context.Context) error
}

// SyncOutcomeKind is the local consequence of one push result.
type SyncOutcomeKind int

const (
	// OutcomeSynced: accepted / unchanged — Fleet holds this state.
	OutcomeSynced SyncOutcomeKind = iota
	// OutcomeLeftScope: a demotion was accepted — Fleet tombstoned the
	// record left_sync_scope; this device keeps its chunk and clears its
	// synced markers (ruling OQ-B).
	OutcomeLeftScope
	// OutcomeBlocked: rejected retry:false — the chunk stays local,
	// marked SyncBlocked with the code, and is not retried.
	OutcomeBlocked
	// OutcomeDelete: superseded / forgotten — delete locally.
	OutcomeDelete
	// OutcomeRekey: merged — this id is an alias of CanonicalID.
	OutcomeRekey
	// OutcomeResendContent: Fleet has no live record for the id but the
	// push omitted content — clear the synced marker so the next push
	// carries it.
	OutcomeResendContent
)

// SyncOutcome is one push result, resolved to its local action. Gen is
// the chunk's SyncGen as of MarkSyncSent: SyncDirty is cleared only when
// nothing changed while the request was in flight.
type SyncOutcome struct {
	ID          string
	Gen         int64
	Kind        SyncOutcomeKind
	Code        string
	CanonicalID string
}

// RemoteRecord is one pull record. Live records carry the chunk content
// and Fleet's merged field state; OwnRecall is this device's counter as
// Fleet holds it (recall_count - recall_count_others).
type RemoteRecord struct {
	ID        string
	Tombstone bool
	Reason    string
	Chunk     Chunk
	OwnRecall int
	Aliases   []string
}

// Tombstone reasons on the pull feed.
const (
	TombstoneForgotten     = "forgotten"
	TombstoneSuperseded    = "superseded"
	TombstoneLeftSyncScope = "left_sync_scope"
)

func (s *chromemStore) indexLocked() map[string]int {
	idx := make(map[string]int, len(s.chunks))
	for i, c := range s.chunks {
		idx[c.ID] = i
	}
	return idx
}

func (s *chromemStore) removeIdxLocked(drop map[int]bool) {
	if len(drop) == 0 {
		return
	}
	kept := make([]Chunk, 0, len(s.chunks)-len(drop))
	for i, c := range s.chunks {
		if !drop[i] {
			kept = append(kept, c)
		}
	}
	s.chunks = kept
}

// MarkSyncSent implements SyncStore.
func (s *chromemStore) MarkSyncSent(_ context.Context, ids []string, at time.Time) (map[string]Chunk, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := s.indexLocked()
	out := make(map[string]Chunk, len(ids))
	changed := false
	for _, id := range ids {
		i, ok := idx[id]
		if !ok {
			continue
		}
		c := &s.chunks[i]
		if c.CreatedHLC == "" {
			c.CreatedHLC = s.tickLocked()
			changed = true
		}
		if c.ScopeHLC == "" {
			c.ScopeHLC = s.tickLocked()
			changed = true
		}
		if c.TitleHLC == "" && c.Title != "" {
			c.TitleHLC = s.tickLocked()
			changed = true
		}
		if c.PinnedHLC == "" && c.Pinned {
			c.PinnedHLC = s.tickLocked()
			changed = true
		}
		if c.SyncSentAt.IsZero() {
			c.SyncSentAt = at
			changed = true
		}
		out[id] = *c
	}
	if changed {
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ApplySyncOutcomes implements SyncStore.
func (s *chromemStore) ApplySyncOutcomes(_ context.Context, outs []SyncOutcome, at time.Time) error {
	if len(outs) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := map[int]bool{}
	for _, o := range outs {
		idx := s.indexLocked()
		i, ok := idx[o.ID]
		if !ok || drop[i] {
			continue // deleted locally while in flight: its forget is queued
		}
		c := &s.chunks[i]
		settled := c.SyncGen == o.Gen
		switch o.Kind {
		case OutcomeSynced:
			c.SyncedAt, c.SyncBlocked = at, ""
			if settled {
				c.SyncDirty = false
			}
		case OutcomeLeftScope:
			c.SyncedAt, c.SyncSentAt = time.Time{}, time.Time{}
			if settled {
				c.SyncDirty = false
			}
		case OutcomeBlocked:
			c.SyncBlocked = o.Code
			if settled {
				c.SyncDirty = false
			}
		case OutcomeResendContent:
			c.SyncedAt = time.Time{}
		case OutcomeDelete:
			drop[i] = true
		case OutcomeRekey:
			if o.CanonicalID == "" || o.CanonicalID == o.ID {
				continue
			}
			if j, has := idx[o.CanonicalID]; has && !drop[j] {
				mergeAliasInto(&s.chunks[j], *c)
				drop[i] = true
				continue
			}
			c.ID = o.CanonicalID
			c.SyncedAt, c.SyncBlocked = at, ""
			if settled {
				c.SyncDirty = false
			}
		}
	}
	s.removeIdxLocked(drop)
	return s.saveLocked()
}

// mergeAliasInto folds a local alias row into its canonical row: the
// canonical keeps its identity; it takes the alias's embedding when it has
// none (re-embedding is the expensive part), the newer value of each LWW
// field, and the larger own-recall counter (Fleet applied max per device).
func mergeAliasInto(canon *Chunk, alias Chunk) {
	if len(canon.Embedding) == 0 && len(alias.Embedding) > 0 {
		canon.Embedding, canon.EmbedPending = alias.Embedding, false
	}
	if HLCAfter(alias.TitleHLC, canon.TitleHLC) {
		canon.Title, canon.TitleHLC = alias.Title, alias.TitleHLC
	}
	if HLCAfter(alias.PinnedHLC, canon.PinnedHLC) {
		canon.Pinned, canon.PinnedHLC = alias.Pinned, alias.PinnedHLC
	}
	if HLCAfter(alias.ScopeHLC, canon.ScopeHLC) {
		canon.ScopeKind, canon.ScopeID, canon.ScopeHLC = alias.ScopeKind, alias.ScopeID, alias.ScopeHLC
	}
	canon.RecallOwn = max(canon.RecallOwn, alias.RecallOwn)
	canon.RecallFolded += alias.RecallFolded
	recomputeRecall(canon)
	if alias.LastAccessed.After(canon.LastAccessed) {
		canon.LastAccessed = alias.LastAccessed
	}
}

// ApplyRemote implements SyncStore. Gate denials (a local Cedar
// memory_write policy refusing the scope) skip the record: pulled memory
// is a local write like any other.
func (s *chromemStore) ApplyRemote(ctx context.Context, recs []RemoteRecord, at time.Time) error {
	if len(recs) == 0 {
		return nil
	}
	s.mu.RLock()
	gate := s.gate
	s.mu.RUnlock()
	allowed := make([]bool, len(recs))
	for i, r := range recs {
		allowed[i] = r.Tombstone || gate == nil || gate.CheckWrite(ctx, r.Chunk.ScopeKind) == nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for n, r := range recs {
		idx := s.indexLocked()
		if r.Tombstone {
			i, ok := idx[r.ID]
			if !ok {
				continue
			}
			c := &s.chunks[i]
			if r.Reason == TombstoneLeftSyncScope && (!IsSyncScope(c.ScopeKind) || !c.FleetMayKnow()) {
				// The demoting device keeps its chunk (ruling OQ-B). It is
				// recognisable two ways: its local scope is outside the
				// sync scopes (it demoted), or its synced markers were
				// cleared when Fleet accepted the demotion — which also
				// covers "demoted, then re-promoted before this pull":
				// that re-promotion has not been sent yet, will push with
				// content and revive the record. Every other device's
				// copy was pulled (FleetMayKnow) and is deleted.
				c.SyncedAt, c.SyncSentAt = time.Time{}, time.Time{}
				continue
			}
			s.removeIdxLocked(map[int]bool{i: true})
			continue
		}
		if !allowed[n] {
			continue
		}
		// Aliases: local rows under an alias id fold into the canonical.
		for _, a := range r.Aliases {
			if j, ok := idx[a]; ok && a != r.ID {
				alias := s.chunks[j]
				if k, has := idx[r.ID]; has {
					mergeAliasInto(&s.chunks[k], alias)
					s.removeIdxLocked(map[int]bool{j: true})
				} else {
					s.chunks[j].ID = r.ID
				}
				idx = s.indexLocked()
			}
		}
		in := r.Chunk
		if i, ok := idx[r.ID]; ok {
			c := &s.chunks[i]
			// Content is immutable on Fleet; only the LWW fields, the
			// counters and last_accessed merge.
			if HLCAfter(in.TitleHLC, c.TitleHLC) {
				c.Title, c.TitleHLC = in.Title, in.TitleHLC
			}
			if HLCAfter(in.PinnedHLC, c.PinnedHLC) {
				c.Pinned, c.PinnedHLC = in.Pinned, in.PinnedHLC
			}
			if HLCAfter(in.ScopeHLC, c.ScopeHLC) {
				c.ScopeKind, c.ScopeID, c.ScopeHLC = in.ScopeKind, "", in.ScopeHLC
			}
			c.RecallOthers = in.RecallOthers
			if c.RecallOwn > r.OwnRecall {
				// Fleet holds less of THIS device's counter than we do
				// (a recall pushed under a since-revived id, a lost
				// push): mark for push or the G-counter never converges.
				touchLocked(c)
			}
			c.RecallOwn = max(c.RecallOwn, r.OwnRecall)
			recomputeRecall(c)
			if in.LastAccessed.After(c.LastAccessed) {
				c.LastAccessed = in.LastAccessed
			}
			c.SyncedAt = at
			continue
		}
		in.ID = r.ID
		in.ScopeID = ""
		in.Embedding, in.EmbedPending = nil, true
		in.RecallOwn = r.OwnRecall
		in.RecallFolded = 0
		recomputeRecall(&in)
		if in.ContentHash == "" {
			in.ContentHash = HashContent(in.Content)
		}
		if in.Kind == "" {
			in.Kind = "raw"
		}
		if in.RetrievalWeight == 0 {
			in.RetrievalWeight = 1.0
		}
		if in.LastAccessed.IsZero() {
			in.LastAccessed = in.CreatedAt
		}
		in.SyncedAt, in.SyncSentAt, in.SyncDirty, in.SyncBlocked, in.SyncGen = at, time.Time{}, false, "", 0
		s.chunks = append(s.chunks, in)
	}
	return s.saveLocked()
}

// LatestSyncedAt implements SyncStore.
func (s *chromemStore) LatestSyncedAt(_ context.Context) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var t time.Time
	for _, c := range s.chunks {
		if c.SyncedAt.After(t) {
			t = c.SyncedAt
		}
	}
	return t
}

// DeleteSyncedBefore implements SyncStore.
func (s *chromemStore) DeleteSyncedBefore(_ context.Context, epoch time.Time) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := map[int]bool{}
	for i, c := range s.chunks {
		if !c.SyncedAt.IsZero() && c.SyncedAt.Before(epoch) {
			drop[i] = true
		}
	}
	if len(drop) == 0 {
		return 0, nil
	}
	s.removeIdxLocked(drop)
	return len(drop), s.saveLocked()
}

// DeleteCreatedBefore implements SyncStore. Only sync-scope chunks are
// considered: session (and, while disabled, project) memory never reached
// Fleet, so a Fleet erase has nothing to say about it. An unstamped legacy
// chunk ("" sorts first) predates every erase — unless the post-erase
// snapshot touched it.
func (s *chromemStore) DeleteCreatedBefore(_ context.Context, hlc string, epoch time.Time) (int, error) {
	if hlc == "" {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	drop := map[int]bool{}
	for i, c := range s.chunks {
		if !c.SyncedAt.IsZero() && !c.SyncedAt.Before(epoch) {
			continue // in the post-erase snapshot ⇒ post-erase
		}
		if IsSyncScope(c.ScopeKind) && c.CreatedHLC < hlc {
			drop[i] = true
		}
	}
	if len(drop) == 0 {
		return 0, nil
	}
	s.removeIdxLocked(drop)
	return len(drop), s.saveLocked()
}

// ClearSyncMarkers implements SyncStore.
func (s *chromemStore) ClearSyncMarkers(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.chunks {
		s.chunks[i].SyncedAt, s.chunks[i].SyncSentAt = time.Time{}, time.Time{}
	}
	return s.saveLocked()
}
