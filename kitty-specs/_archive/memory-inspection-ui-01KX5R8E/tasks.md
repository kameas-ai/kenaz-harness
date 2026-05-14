# tasks.md — Memory inspection UI (capstone scope)

8 work packages. Sequential order due to dependency chain.

---

## WP01 — Scaffold spec artifacts (docs commit)

**Dependencies**: none · **Effort**: S

**Files touched**:
- `kitty-specs/memory-inspection-ui-01KX5R8E/meta.json` (new)
- `kitty-specs/memory-inspection-ui-01KX5R8E/plan.md` (new)
- `kitty-specs/memory-inspection-ui-01KX5R8E/tasks.md` (new)
- `kitty-specs/memory-inspection-ui-01KX5R8E/spec.md` (copy from untracked working tree)

**Acceptance**:
- All four files present and committed.
- spec.md is verbatim from the 16.7K source on disk.
- plan.md documents which features were cherry-picked in v0.5.x vs. what remains for capstone.

---

## WP02 — Retrieval history ring buffer + `Memory_LastRetrieval`

**Dependencies**: WP01 · **Effort**: M

**Files touched**:
- `core/memory/retrieval_history.go` (new)
- `core/memory/retriever.go` — push record after retrieve
- `core/rpc/views/memory/api.go` — `RetrievalReport`, `ScoredChunk` types, `LastRetrieval` on interface
- `core/rpc/views/memory/impl.go` — `LastRetrieval` implementation

**Acceptance**:
- Ring buffer bounded: per-session cap 200, global cap 10k (excess entries drop the oldest per session).
- Concurrent writes from the retriever goroutine + concurrent reads from the RPC surface are race-free.
- `LastRetrieval(unknownSession)` returns empty report, no error.
- `LastRetrieval(knownSession)` returns the most recent record's query + scored chunks.
- `go test -race -short ./core/memory/... ./core/rpc/views/memory/...` green.

---

## WP03 — `Memory_EmbeddingProbe` RPC

**Dependencies**: WP02 · **Effort**: S

**Files touched**:
- `core/rpc/views/memory/api.go` — `EmbeddingProbe` on interface
- `core/rpc/views/memory/impl.go` — implementation
- `core/rpc/views/memory/impl_test.go` — tests

**Acceptance**:
- With no embedder: returns `ErrEmbedderUnavailable`.
- With embedder + non-empty store: returns up to `limit` `ScoredChunk` values sorted desc by similarity.
- `limit` capped at 50 server-side.
- Query of empty string: returns empty slice, no error.

---

## WP04 — `Memory_ResummarizeChunk` RPC

**Dependencies**: WP01 · **Effort**: M

**Files touched**:
- `core/rpc/views/memory/api.go` — `ResummarizeChunk` on interface, `Resummary` in `Config`
- `core/rpc/views/memory/impl.go` — implementation + rate-limit guard
- `core/rpc/views/memory/impl_test.go` — raw-chunk path (extractive fallback), rate-limit path

**Acceptance**:
- Raw chunk (no TurnID): re-runs `ExtractiveBuilder.BuildTurnFallback` → replaces chunk content inline → returns updated `Chunk`.
- Narrative chunk with TurnID: re-enqueues to Promoter (if wired) → returns current `Chunk` unchanged.
- Second call within 60s: returns `ErrResummarizeRateLimited`.
- Unwired store: returns `ErrStoreUnavailable`.

---

## WP05 — `Memory_GetChunkProvenance` RPC + wire types

**Dependencies**: WP01 · **Effort**: M

**Files touched**:
- `core/rpc/views/memory/api.go` — `ChunkProvenance` struct + `GetChunkProvenance` on interface
- `core/rpc/views/memory/impl.go` — implementation
- `core/rpc/views/memory/impl_test.go` — provenance fields populated correctly

**Acceptance**:
- Returns `ChunkProvenance` with: `ChunkID`, `SourceTurn`, `HookBoundary` (from `Chunk.Source`), `Kind`, `ScopePath` (human-readable "session → project → long_term"), `Pinned`, `RetrievalCount` (from `RecallCount`), `CitationCount` (from NarrativeMetrics if wired), `PromotionScore` (from NarrativeMetrics if wired), `EmbedderKind`, `EmbedDimensions`, `CreatedAt`.
- Unknown chunk ID: returns error "memory: chunk not found".
- Nil narrativeMetrics: citation and score fields are zero (not an error).

---

## WP06 — Bindings + frontend types + harnessClient wiring

**Dependencies**: WP02, WP03, WP04, WP05 · **Effort**: M

**Files touched**:
- `core/rpc/bindings.go` — `Memory_LastRetrieval`, `Memory_EmbeddingProbe`, `Memory_ResummarizeChunk`, `Memory_GetChunkProvenance`
- `frontend/src/lib/types.ts` — `RetrievalReport`, `ScoredChunk`, `ChunkProvenance`
- `frontend/src/lib/harnessClient.ts` — `WailsBindingsLike` + `MemoryClient` extensions + fake stubs
- `frontend/wailsjs/go/rpc/Bindings.js` + `Bindings.d.ts` — hand-authored stubs

**Acceptance**:
- `go build ./core/...` clean (no unused imports, no interface drift).
- `vitest run` clean.
- Fake stubs return zero-value safe shapes so existing tests don't break.

---

## WP07 — Frontend: Retrieval Inspector + Embedding Probe

**Dependencies**: WP06 · **Effort**: L

**Files touched**:
- `frontend/src/views/memory/MemoryView.vue` — new `retrieval` main tab, embedding-probe input section
- `frontend/src/views/memory/__tests__/MemoryView.spec.ts` — retrieval tab + probe tests

**Acceptance**:
- `retrieval` tab renders `RetrievalReport`: query string, injected chunks (top K), below-threshold chunks.
- Similarity scores shown as `0.84` (two decimal places).
- Injected-vs-below-threshold visual distinction (e.g. bold border vs. muted).
- Embedding probe: text input + "Probe" button → calls `embeddingProbe(query, 10)` → renders `ScoredChunk[]` list.
- Empty state when no retrieval history: "No retrievals this session yet."
- Loading states present for both retrieval fetch and probe call.
- `vitest run` green.

---

## WP08 — Frontend: Provenance Drawer

**Dependencies**: WP06 · **Effort**: M

**Files touched**:
- `frontend/src/views/memory/ProvenanceDrawer.vue` (new)
- `frontend/src/views/memory/MemoryView.vue` — "Provenance" row action + drawer integration
- `frontend/src/views/memory/__tests__/ProvenanceDrawer.spec.ts` (new)

**Acceptance**:
- Drawer opens on "Provenance" row action, closes on Escape or backdrop click.
- All `ChunkProvenance` fields rendered with labels.
- Source turn link shown when `sourceTurn` is non-empty (formatted as `role:ID`).
- ScopePath displayed as pill chain: e.g. `session → project`.
- ProvenanceDrawer.spec.ts exercises open/close and field rendering.
- `vitest run` green.
