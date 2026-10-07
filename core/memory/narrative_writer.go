package memory

// NarrativeStoreWriter is the production narrative.NarrativeWriter over a
// memory Store (memory-sync-01MEMSY01 F10). It exists so the promoter's
// write seam carries the Fleet sync obligation (contract H2) by
// construction rather than by a doc comment: a synthesised write that
// replaces a turn's earlier narrative mints a new id and goes through
// ReplaceForSync (new record with turn_id + forget of each replaced id
// Fleet may know), and DeleteByTurnFallback goes through RemoveForSync.
//
// Wiring status: the Promoter itself is still not constructed outside tests
// (ruling A-4 retired the narrative subsystem's runtime). Whoever wires it
// passes this writer; nothing else satisfies the interface in production.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/kameas-ai/kenaz-harness/core/memory/narrative"
)

// NarrativeStoreWriter implements narrative.NarrativeWriter.
type NarrativeStoreWriter struct {
	store    Store
	forgets  ForgetRecorder
	embedder Embedder
	now      func() time.Time
}

var _ narrative.NarrativeWriter = (*NarrativeStoreWriter)(nil)

// NewNarrativeStoreWriter builds the writer. forgets and embedder may be
// nil (no sync / no vectors: the chunk is stored EmbedPending).
func NewNarrativeStoreWriter(store Store, forgets ForgetRecorder, embedder Embedder) *NarrativeStoreWriter {
	return &NarrativeStoreWriter{store: store, forgets: forgets, embedder: embedder, now: time.Now}
}

// turnChunks lists the (session, turn) chunks of the given kinds,
// wherever they have been promoted since (the id survives promotion).
func (w *NarrativeStoreWriter) turnChunks(ctx context.Context, sessionID, turnID string, kinds ...string) ([]string, error) {
	all, err := w.store.List(ctx)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range all {
		if c.SessionID != sessionID || c.TurnID != turnID {
			continue
		}
		for _, k := range kinds {
			if c.Kind == k {
				ids = append(ids, c.ID)
				break
			}
		}
	}
	return ids, nil
}

// WriteNarrative implements narrative.NarrativeWriter. A write replaces the
// turn's earlier chunks of the SAME kind (a re-summarize, a retried
// fallback); the fallback a synthesised write supersedes is removed by the
// promoter's DeleteByTurnFallback call.
func (w *NarrativeStoreWriter) WriteNarrative(ctx context.Context, req narrative.NarrativeWriteReq) (string, error) {
	// Idempotent: a retried job re-writing identical content for the turn
	// returns the existing chunk instead of minting (and syncing) a twin.
	hash := HashContent(req.Content)
	if all, err := w.store.List(ctx); err == nil {
		for _, c := range all {
			if c.SessionID == req.SessionID && c.TurnID == req.TurnID && c.Kind == string(req.Kind) && c.ContentHash == hash {
				return c.ID, nil
			}
		}
	}
	id, err := newMemID()
	if err != nil {
		return "", err
	}
	next := Chunk{
		ID: id, SessionID: req.SessionID, ProjectID: req.ProjectID, TurnID: req.TurnID,
		ScopeKind: ScopeKindSession, ScopeID: req.SessionID,
		Content: req.Content, ContentHash: hash,
		Kind: string(req.Kind), RetrievalWeight: req.RetrievalWeight, Source: req.Source,
		CreatedAt: w.now().UTC(),
	}
	if w.embedder != nil {
		if _, noop := w.embedder.(NoopEmbedder); !noop {
			if vecs, err := w.embedder.Embed(ctx, []string{req.Content}); err == nil && len(vecs) > 0 && len(vecs[0]) > 0 {
				next.Embedding = vecs[0]
			}
		}
	}
	if len(next.Embedding) == 0 {
		next.EmbedPending = true
	}
	replaced, err := w.turnChunks(ctx, req.SessionID, req.TurnID, string(req.Kind))
	if err != nil {
		return "", err
	}
	if err := ReplaceForSync(ctx, w.store, w.forgets, next, replaced...); err != nil {
		return "", err
	}
	return id, nil
}

// DeleteByTurnFallback implements narrative.NarrativeWriter.
func (w *NarrativeStoreWriter) DeleteByTurnFallback(ctx context.Context, sessionID, turnID string) error {
	ids, err := w.turnChunks(ctx, sessionID, turnID, string(narrative.ChunkKindNarrativeExtractiveFallback))
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := RemoveForSync(ctx, w.store, w.forgets, id); err != nil {
			return err
		}
	}
	return nil
}

// newMemID mints a chunk id in the harness's shape ("mem-" + 32 hex).
func newMemID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("memory: random id: %w", err)
	}
	return "mem-" + hex.EncodeToString(b), nil
}
