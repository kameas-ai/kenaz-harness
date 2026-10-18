package memory

import (
	"context"
	"errors"
)

// DrainEmbedPending indexes up to max EmbedPending chunks with embedder
// (memory-sync-01MEMSY01 WP06, H5: each device re-embeds pulled memory —
// vectors never leave a device). It returns how many were indexed.
//
// A NoopEmbedder (or nil) device indexes nothing and returns (0, nil): its
// pulled chunks stay unindexed but visible to List, the long_term prelude
// and lexical search; Query skips them. A store without EmbeddingSetter is
// likewise a no-op. An embed error stops the drain and is returned; the
// remaining chunks stay pending for the next drain.
func DrainEmbedPending(ctx context.Context, store Store, embedder Embedder, max int) (int, error) {
	if store == nil || embedder == nil || max <= 0 {
		return 0, nil
	}
	if _, noop := embedder.(NoopEmbedder); noop {
		return 0, nil
	}
	setter, ok := store.(EmbeddingSetter)
	if !ok {
		return 0, nil
	}
	all, err := store.List(ctx)
	if err != nil {
		return 0, err
	}
	var pending []Chunk
	for _, c := range all {
		if c.EmbedPending {
			pending = append(pending, c)
			if len(pending) == max {
				break
			}
		}
	}
	const batch = 32
	done := 0
	for start := 0; start < len(pending); start += batch {
		end := min(start+batch, len(pending))
		texts := make([]string, 0, end-start)
		for _, c := range pending[start:end] {
			texts = append(texts, c.Content)
		}
		vecs, err := embedder.Embed(ctx, texts)
		if err != nil {
			return done, err
		}
		if len(vecs) != len(texts) {
			return done, errors.New("memory: embedder returned a short batch")
		}
		for i, c := range pending[start:end] {
			if len(vecs[i]) == 0 {
				continue
			}
			if err := setter.SetEmbedding(ctx, c.ID, vecs[i]); err != nil {
				// Deleted while embedding (pull tombstone, forget): skip.
				continue
			}
			done++
		}
	}
	return done, nil
}
