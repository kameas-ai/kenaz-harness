// Package prune is the background memory-prune subsystem (Bundle E
// WP15 of the agent-kernel-graph mission).
//
// Why a separate package: the prune sweep is the *only* selectivity
// layer in the greedy-memory architecture. Write-time stays
// unfiltered — every kernel boundary fires a hook, and every fire
// lands in the store. The Pruner runs out-of-band (default once per
// day per active project) and applies a multiplicatively-combined
// score to decide what to drop.
//
// The package imports core/memory but core/memory does NOT import
// this package (DIRECTIVE_001 — no cyclic imports). This is the
// standard "consumer reaches up" pattern.
//
// Pruning signals (combined multiplicatively into a "keep score"):
//
//   - Staleness: time since LastAccessed, scope-weighted (sessions
//     decay fastest, global scope decays slowest).
//   - Age: time since CreatedAt — a hard "older than M" rule.
//   - RecallFrequency: how often this entry was retrieved. Bottom
//     percentile is dropped first.
//   - EmbeddingClusterCollapse: chunks whose embeddings are within
//     CosineThreshold of a representative are collapsed onto one row;
//     the survivor inherits the recall sum + the latest LastAccessed.
//   - SizeCap: after the four signal-driven cuts, if the store still
//     exceeds MaxEntries the pruner drops oldest-LastAccessed-first
//     until it fits.
//
// Pinned chunks (Chunk.Pinned == true) are immune to every signal —
// they bypass the score calculation entirely.
//
// Determinism: the pruner is a pure function of (chunks, rules, now).
// Tests build a fixture chunk set, instantiate a Pruner with a fixed
// clock, and assert the verdict.
package prune
