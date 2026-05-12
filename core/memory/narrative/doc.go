// Package narrative implements the memory narrative layer (mission
// memory-narrative-layer-01KQ8TD1). It provides:
//
//   - ChunkKind constants that classify chunks as raw, extractive, or
//     synthesised narratives (WP01).
//   - ExtractiveBuilder: deterministic per-tool and per-turn narrative
//     builders that require no LLM (WP02).
//   - Promoter: an async worker pool that queues and executes
//     per-turn LLM-summarised narrative jobs (WP03).
//   - Retry: exponential backoff schedule for failed synthesis jobs (WP05).
//   - MetricsStore: promotion-score arithmetic and rolling-window
//     retrieval / citation / pin counters (WP06).
//   - CitationDetector: rolling-hash citation detector for the
//     HookPostLLM boundary (WP08).
//   - Prelude loader: system-prompt prelude for long-term chunks (WP09).
//   - NarrativeFirstStrategy: compactor strategy that splices synthesised
//     narratives verbatim (WP10).
//   - Feature flag: env-var + settings-dial gate (WP12).
//
// Invariant (C-001): the greedy raw-text write path in core/memory/store.go
// and core/agentgraph/hooks.go is never modified by this package. All
// narrative writes are ADDITIVE — they produce new chunks alongside the
// existing raw floor.
//
// Invariant (FR-003b): per-tool extractive narratives are synchronous and
// zero-latency (no LLM). Per-turn synthesised narratives are async via
// the Promoter worker pool and never block the next user message.
package narrative
