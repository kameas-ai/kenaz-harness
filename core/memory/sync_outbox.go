// Forget outbox for learned-memory sync (memory-sync-01MEMSY01 WP04/WP07).
//
// Dirty chunks need no outbox entry — the chunk row itself carries
// SyncDirty / SyncSentAt, so "what to push" is derived from the store. A
// forget, though, outlives its chunk: once the row is gone nothing but this
// file remembers that Fleet must tombstone the id. The outbox is therefore
// the one piece of sync state that must persist on its own.
//
// Coalescing rule (fleet-live-notes 2026-10-07, required): a local delete
// of a chunk Fleet cannot know — never pulled, never sent — sends NOTHING.
// The unpushed create disappears with the row, and no forget is queued
// (Fleet records no forgets for ids it has never seen). RecordDelete is
// where that rule lives; every user-intent delete path reports through it.
// Automatic prune never does (ruling: prune is device-local).
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ForgetRecorder is the narrow seam user-intent delete paths report to.
// RecordDelete receives the deleted row's FINAL state (see ChunkRemover)
// and queues a forget when Fleet may know the id. nil recorders are
// allowed everywhere and mean "no sync".
type ForgetRecorder interface {
	RecordDelete(deleted Chunk) error
}

// ChunkRemover is the optional store capability that deletes a chunk and
// returns its final state atomically. Sync needs the state as of the
// delete, not as of an earlier read: a push may mark the chunk sent
// between the caller's read and its delete, and that chunk must then be
// forgotten on Fleet.
type ChunkRemover interface {
	Remove(ctx context.Context, id string) (Chunk, error)
}

// ForgetOp is one queued forget.
type ForgetOp struct {
	ID       string    `json:"id"`
	HLC      string    `json:"hlc,omitempty"`
	QueuedAt time.Time `json:"queued_at"`
}

// ForgetOutboxPath is the canonical outbox location under dataDir.
func ForgetOutboxPath(dataDir string) string {
	return filepath.Join(dataDir, "fleet", "memory_outbox.json")
}

// ForgetOutbox is the persisted forget queue. Safe for concurrent use.
type ForgetOutbox struct {
	mu    sync.Mutex
	path  string // "" = in-memory (tests)
	clock *HLC
	now   func() time.Time
	ops   []ForgetOp
}

type outboxFile struct {
	Forgets []ForgetOp `json:"forgets"`
}

// OpenForgetOutbox loads (or starts) the outbox at path. A corrupt file is
// an error: dropping it would silently resurrect forgotten memories on the
// next pull.
func OpenForgetOutbox(path string, clock *HLC) (*ForgetOutbox, error) {
	o := &ForgetOutbox{path: path, clock: clock, now: time.Now}
	if path == "" {
		return o, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return o, nil
		}
		return nil, fmt.Errorf("memory: read outbox: %w", err)
	}
	var f outboxFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("memory: decode outbox %s: %w", path, err)
	}
	o.ops = f.Forgets
	return o, nil
}

// RecordDelete applies the coalescing rule: queue a forget iff Fleet may
// know the deleted chunk's id.
func (o *ForgetOutbox) RecordDelete(deleted Chunk) error {
	if o == nil || deleted.ID == "" || !deleted.FleetMayKnow() {
		return nil
	}
	return o.Enqueue(deleted.ID)
}

// Enqueue queues a forget for id (idempotent).
func (o *ForgetOutbox) Enqueue(id string) error {
	if o == nil || id == "" {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, op := range o.ops {
		if op.ID == id {
			return nil
		}
	}
	o.ops = append(o.ops, ForgetOp{ID: id, HLC: o.clock.Tick(), QueuedAt: o.now().UTC()})
	return o.saveLocked()
}

// Pending returns a copy of the queued forgets, oldest first.
func (o *ForgetOutbox) Pending() []ForgetOp {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]ForgetOp(nil), o.ops...)
}

// Has reports whether a forget for id is queued — the puller must not
// resurrect such an id from a pull page that predates the forget.
func (o *ForgetOutbox) Has(id string) bool {
	if o == nil {
		return false
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for _, op := range o.ops {
		if op.ID == id {
			return true
		}
	}
	return false
}

// Ack removes the given ids (Fleet answered them).
func (o *ForgetOutbox) Ack(ids ...string) error {
	if o == nil || len(ids) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		drop[id] = struct{}{}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	kept := o.ops[:0]
	for _, op := range o.ops {
		if _, ok := drop[op.ID]; !ok {
			kept = append(kept, op)
		}
	}
	o.ops = kept
	return o.saveLocked()
}

func (o *ForgetOutbox) saveLocked() error {
	if o.path == "" {
		return nil
	}
	ops := o.ops
	if ops == nil {
		ops = []ForgetOp{}
	}
	return writeFileAtomic(o.path, mustJSON(outboxFile{Forgets: ops}))
}

// ReplaceForSync is the one seam every "rewrite content ⇒ new id" path uses
// (contract §3 "Resummarize / promote guidance": both the inline
// resummarize fallback and the narrative promoter's synthesised write).
// Content is immutable on Fleet, so the replacement is a NEW record (next,
// carrying its turn_id) plus a forget for each replaced id Fleet may know.
// next is added first, so a failed add removes nothing. A replaced id that
// is already gone is skipped.
func ReplaceForSync(ctx context.Context, store Store, rec ForgetRecorder, next Chunk, oldIDs ...string) error {
	if err := store.Add(ctx, next); err != nil {
		return err
	}
	for _, id := range oldIDs {
		if id == "" || id == next.ID {
			continue
		}
		if err := RemoveForSync(ctx, store, rec, id); err != nil {
			return err
		}
	}
	return nil
}

// RemoveForSync deletes id as a user-intent removal: the chunk's final
// state is handed to rec, which queues a Fleet forget when Fleet may know
// the id. A store without ChunkRemover falls back to Delete and records
// nothing (it cannot carry sync state either).
func RemoveForSync(ctx context.Context, store Store, rec ForgetRecorder, id string) error {
	if rm, ok := store.(ChunkRemover); ok {
		gone, err := rm.Remove(ctx, id)
		if err != nil {
			return err
		}
		if rec != nil {
			return rec.RecordDelete(gone)
		}
		return nil
	}
	return store.Delete(ctx, id)
}

// FreshIdentity returns c as a brand-new record for sync: every Fleet
// identity field (HLC stamps, sent/synced markers, rejection) is cleared
// so the store stamps it on Add, and recall is re-based — this device's
// own count travels with it (it is this device's history), while recalls
// other devices reported for the OLD id become display-only (RecallFolded)
// so they are never re-pushed under the new id.
func FreshIdentity(c Chunk, newID string) Chunk {
	out := c
	out.ID = newID
	out.TitleHLC, out.PinnedHLC, out.ScopeHLC, out.CreatedHLC = "", "", "", ""
	out.SyncDirty, out.SyncSentAt, out.SyncedAt, out.SyncBlocked, out.SyncGen = false, time.Time{}, time.Time{}, "", 0
	out.RecallFolded = c.RecallFolded + c.RecallOthers
	out.RecallOthers = 0
	normalizeRecall(&out)
	return out
}
