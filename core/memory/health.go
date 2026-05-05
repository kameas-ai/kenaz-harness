// Package memory — health snapshot computation for the inspector surface.
//
// HealthSnapshot is populated by indexed COUNT passes over the in-RAM
// chunk slice held by the store; there is no SQL or full-table scan.
// The Store is gob-backed, so "indexed" means a single slice walk with
// O(n) complexity — still well within the 200 ms p95 budget for a
// 100 k-chunk store on modern hardware.
package memory

import (
	"context"
	"time"
)

// EmbedderInfo surfaces the static properties of the currently-wired
// Embedder so the health panel can render "Provider / Model / Dimensions"
// without a network call.
type EmbedderInfo struct {
	// Kind is the short provider tag ("openai", "noop", "fake", …).
	Kind string
	// Model is the embedding model identifier (e.g. "text-embedding-3-small").
	// Empty for the noop embedder.
	Model string
	// Dimensions is the expected vector width. Zero for the noop embedder.
	Dimensions int
}

// ChunkCounts holds the per-kind breakdown of the total chunk count.
type ChunkCounts struct {
	// Total is the grand total number of chunks in the store.
	Total int
	// Raw is the count of chunks with ScopeKind "session".
	// Named "raw" in the UI to match the spec's §2.4 display.
	Raw int
	// Narrative is the count of chunks with ScopeKind "narrative"
	// (reserved for the future memory-narrative-layer; always 0 until
	// that ship).
	Narrative int
	// LongTermPromoted is the count of chunks with ScopeKind "long_term"
	// or "global". "global" is the current promotion target; long_term
	// is reserved for a future scope tier.
	LongTermPromoted int
	// Embedded is the count of chunks that have a non-empty embedding
	// vector (i.e. they can be retrieved by the k-NN search path).
	Embedded int
	// Unembedded is the count of chunks with an empty embedding vector.
	// These are unretrievable until the user triggers re-embedding.
	Unembedded int
}

// ActivityWindow summarises chunk activity over the last 7 days.
type ActivityWindow struct {
	// Captured is the number of chunks created in the last 7 days.
	Captured int
	// Pruned is always 0 at snapshot time — the prune sweep removes rows;
	// we cannot reconstruct the pre-prune count without a journal.
	// Reserved for a future audit-journal integration.
	Pruned int
	// Promoted is the number of chunks promoted to "global" scope in the
	// last 7 days (CreatedAt used as a proxy because promotion uses
	// move semantics — it deletes + re-inserts with a fresh CreatedAt).
	Promoted int
}

// HealthSnapshot is the wire-shape returned by HealthSnapshot(). All
// counts come from a single O(n) pass over the in-RAM store; there are
// no full-table scans or additional network calls.
type HealthSnapshot struct {
	Counts   ChunkCounts   `json:"counts"`
	Activity ActivityWindow `json:"activity"`
	Embedder EmbedderInfo  `json:"embedder"`
	// CapturedAt is when the snapshot was taken (UTC).
	CapturedAt time.Time `json:"capturedAt"`
}

// HealthSnapshotter is the optional capability the Store must expose for
// the health-snapshot path. The chromemStore satisfies it via the
// SnapshotHealth helper below; test stubs and future backends can
// implement it directly.
//
// The interface lives in core/memory (not core/rpc/views/memory) so the
// view layer never has to import core/memory internals beyond value
// types (DIRECTIVE_001 compliance).
type HealthSnapshotter interface {
	SnapshotHealth(ctx context.Context) (HealthSnapshot, error)
}

// SnapshotHealth builds a HealthSnapshot over the given chunk slice.
// Called by chromemStore.SnapshotHealth after it acquires a read lock.
// Pure function — no I/O, safe for benchmarking.
func SnapshotHealth(chunks []Chunk, embedder Embedder, now time.Time) HealthSnapshot {
	cutoff7d := now.Add(-7 * 24 * time.Hour)
	snap := HealthSnapshot{
		CapturedAt: now.UTC(),
	}
	if embedder != nil {
		snap.Embedder = EmbedderInfo{
			Kind:       embedder.Kind(),
			Dimensions: embedder.Dimensions(),
		}
		if namer, ok := embedder.(interface{ Model() string }); ok {
			snap.Embedder.Model = namer.Model()
		}
	}
	for i := range chunks {
		c := &chunks[i]
		snap.Counts.Total++

		switch c.ScopeKind {
		case ScopeKindSession:
			snap.Counts.Raw++
		case "narrative":
			snap.Counts.Narrative++
		case ScopeKindGlobal, "long_term":
			snap.Counts.LongTermPromoted++
		// ScopeKindProject counts as neither raw, narrative, nor long_term
		// in the spec's display — it is a mid-tier scope. We keep it in
		// Total only.
		}

		if len(c.Embedding) > 0 {
			snap.Counts.Embedded++
		} else {
			snap.Counts.Unembedded++
		}

		if c.CreatedAt.After(cutoff7d) {
			snap.Activity.Captured++
			if c.ScopeKind == ScopeKindGlobal || c.ScopeKind == "long_term" {
				snap.Activity.Promoted++
			}
		}
	}
	return snap
}

// SnapshotHealth implements HealthSnapshotter on chromemStore.
// It acquires a read lock, copies the slice reference, and delegates
// to the pure SnapshotHealth helper so the lock is held for only the
// copy phase — not the computation.
func (s *chromemStore) SnapshotHealth(ctx context.Context) (HealthSnapshot, error) {
	_ = ctx
	s.mu.RLock()
	chunks := make([]Chunk, len(s.chunks))
	copy(chunks, s.chunks)
	s.mu.RUnlock()

	return SnapshotHealth(chunks, nil, time.Now().UTC()), nil
}
